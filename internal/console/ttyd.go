// Package console implements a ttyd WebSocket client for terminal access.
//
// ttyd protocol (binary WebSocket frames):
//
//	Client -> Server:
//	  '{' + JSON        Initial handshake: {"AuthToken":"","columns":N,"rows":N}
//	  '0' + data        Keyboard input (raw bytes)
//	  '1' + JSON        Resize: {"columns":N,"rows":N}
//	  '2'               Pause PTY reads (flow control)
//	  '3'               Resume PTY reads (flow control)
//
//	Server -> Client:
//	  '0' + data        Terminal output (raw bytes)
//	  '1' + string      Window title
//	  '2' + JSON        Terminal preferences
package console

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/gorilla/websocket"
	"golang.org/x/term"
)

// ttyd command bytes (same numeric value, different meaning per direction).
const (
	cmdInput  = '0' // C->S: keyboard input, S->C: terminal output
	cmdResize = '1' // C->S: resize terminal, S->C: window title
	cmdPause  = '2' // C->S: pause, S->C: preferences
	cmdResume = '3' // C->S: resume
)

// handshake is the initial JSON message sent to ttyd.
type handshake struct {
	AuthToken string `json:"AuthToken"`
	Columns   int    `json:"columns"`
	Rows      int    `json:"rows"`
}

// resize is the JSON payload for terminal resize messages.
type resize struct {
	Columns int `json:"columns"`
	Rows    int `json:"rows"`
}

// Session represents an active ttyd console connection.
type Session struct {
	conn        *websocket.Conn
	done        chan struct{}
	closeOnce   sync.Once
	oldState    *term.State
	escapeNext  bool // true if previous byte was '~' at start of line
	atLineStart bool
}

// Connect establishes a WebSocket connection to a ttyd instance proxied
// through the OSFCI gateway. consolePath is the gateway path, e.g.
// "/ci/console" for the host serial console.
func Connect(host, cookie, consolePath string) (*Session, error) {
	// ttyd WebSocket endpoint is at {consolePath}/ws under the gateway.
	wsURL := fmt.Sprintf("wss://%s%s/ws", host, consolePath)

	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec
		},
		Subprotocols: []string{"tty"},
	}

	headers := http.Header{}
	headers.Set("Cookie", fmt.Sprintf("osfci_cookie=%s", cookie))

	conn, _, err := dialer.Dial(wsURL, headers)
	if err != nil {
		return nil, fmt.Errorf("WebSocket connect to %s failed: %w", wsURL, err)
	}

	s := &Session{
		conn:        conn,
		done:        make(chan struct{}),
		atLineStart: true,
	}

	// Send initial handshake with terminal size.
	cols, rows := 80, 24
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		cols, rows = w, h
	}

	hs := handshake{
		AuthToken: "", // no auth token needed when going through the gateway proxy
		Columns:   cols,
		Rows:      rows,
	}
	hsJSON, err := json.Marshal(hs)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("marshaling handshake: %w", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, hsJSON); err != nil {
		conn.Close()
		return nil, fmt.Errorf("sending handshake: %w", err)
	}

	return s, nil
}

// Run enters interactive terminal mode. It:
//   - Puts stdin into raw mode
//   - Reads from the WebSocket and writes to stdout (terminal output)
//   - Reads from stdin and writes to the WebSocket (keyboard input)
//   - Handles terminal resize (SIGWINCH)
//   - Supports ~. escape sequence to detach
//
// Blocks until the connection closes or the user detaches with ~.
func (s *Session) Run() error {
	// Put terminal into raw mode.
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("entering raw mode: %w", err)
	}
	s.oldState = oldState
	defer s.restore()

	// Handle SIGWINCH for terminal resize.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	// Handle SIGINT/SIGTERM gracefully.
	intCh := make(chan os.Signal, 1)
	signal.Notify(intCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(intCh)

	var wg sync.WaitGroup

	// Goroutine: read from WebSocket, write to stdout.
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.readLoop()
	}()

	// Goroutine: read from stdin, write to WebSocket.
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.writeLoop()
	}()

	// Goroutine: handle signals.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case sig := <-sigCh:
				if sig == syscall.SIGWINCH {
					s.sendResize()
				}
			case <-intCh:
				s.Close()
				return
			case <-s.done:
				return
			}
		}
	}()

	wg.Wait()
	return nil
}

// RunPassive reads terminal output from the WebSocket and writes it to
// stdout without entering raw terminal mode. No keyboard input is sent.
// Useful for non-interactive environments (CI, scripting, piping) or
// when stdin is not a TTY.
//
// Blocks until the connection closes or the context is interrupted.
func (s *Session) RunPassive() error {
	// Handle SIGINT/SIGTERM gracefully.
	intCh := make(chan os.Signal, 1)
	signal.Notify(intCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(intCh)

	var wg sync.WaitGroup

	// Goroutine: read from WebSocket, write to stdout.
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.readLoop()
	}()

	// Goroutine: handle signals.
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-intCh:
			s.Close()
		case <-s.done:
		}
	}()

	wg.Wait()
	return nil
}

// readLoop reads WebSocket messages and writes terminal output to stdout.
func (s *Session) readLoop() {
	defer s.Close()
	for {
		_, msg, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				select {
				case <-s.done:
					// Expected close, don't print error.
				default:
					fmt.Fprintf(os.Stderr, "\r\nConnection closed: %v\r\n", err)
				}
			}
			return
		}
		if len(msg) < 1 {
			continue
		}

		cmd := msg[0]
		payload := msg[1:]

		switch cmd {
		case cmdInput: // '0' from server = terminal output
			os.Stdout.Write(payload)
		case cmdResize: // '1' from server = window title (ignore)
		case cmdPause: // '2' from server = preferences (ignore)
		}
	}
}

// writeLoop reads stdin and sends keyboard input over the WebSocket.
// Handles the ~. escape sequence: if the user types ~ at the start of
// a line followed by '.', the session is closed.
func (s *Session) writeLoop() {
	defer s.Close()
	buf := make([]byte, 4096)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			if err != io.EOF {
				select {
				case <-s.done:
				default:
					fmt.Fprintf(os.Stderr, "\r\nStdin error: %v\r\n", err)
				}
			}
			return
		}
		if n == 0 {
			continue
		}

		data := buf[:n]

		// Check for ~. escape sequence.
		if s.checkEscape(data) {
			fmt.Fprintf(os.Stderr, "\r\nDetached.\r\n")
			return
		}

		// Send input: '0' + data
		msg := make([]byte, 1+n)
		msg[0] = cmdInput
		copy(msg[1:], data)

		if err := s.conn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
			select {
			case <-s.done:
			default:
				fmt.Fprintf(os.Stderr, "\r\nWrite error: %v\r\n", err)
			}
			return
		}
	}
}

// checkEscape processes input bytes looking for the ~. escape sequence.
// Returns true if the user wants to detach.
func (s *Session) checkEscape(data []byte) bool {
	for _, b := range data {
		switch {
		case b == '\r' || b == '\n':
			s.atLineStart = true
			s.escapeNext = false
		case s.atLineStart && b == '~':
			s.escapeNext = true
			s.atLineStart = false
		case s.escapeNext && b == '.':
			return true
		default:
			s.atLineStart = false
			s.escapeNext = false
		}
	}
	return false
}

// sendResize sends a terminal resize message to ttyd.
func (s *Session) sendResize() {
	cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return
	}
	r := resize{Columns: cols, Rows: rows}
	payload, err := json.Marshal(r)
	if err != nil {
		return
	}
	msg := make([]byte, 1+len(payload))
	msg[0] = cmdResize
	copy(msg[1:], payload)
	s.conn.WriteMessage(websocket.BinaryMessage, msg)
}

// Close shuts down the session.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.conn.Close()
	})
}

// restore returns the terminal to its original state.
func (s *Session) restore() {
	if s.oldState != nil {
		term.Restore(int(os.Stdin.Fd()), s.oldState)
	}
}
