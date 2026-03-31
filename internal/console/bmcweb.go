package console

import (
	"crypto/tls"
	"encoding/base64"
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

// BMCWebSession represents a console connection via bmcweb's /console0 WebSocket.
// Unlike ttyd, bmcweb uses raw binary frames with no command prefix byte.
// Authentication is via HTTP Basic Auth in the WebSocket upgrade request.
type BMCWebSession struct {
	conn        *websocket.Conn
	done        chan struct{}
	closeOnce   sync.Once
	oldState    *term.State
	escapeNext  bool
	atLineStart bool
}

// ConnectBMCWeb establishes a WebSocket connection to the BMC's /console0
// endpoint through the OSFCI gateway's reverse proxy (which proxies / to
// the BMC's port 443).
//
// The gateway injects the osfci_cookie for routing, and we send HTTP Basic
// Auth credentials for bmcweb authentication.
func ConnectBMCWeb(host, cookie, bmcUser, bmcPass string) (*BMCWebSession, error) {
	wsURL := fmt.Sprintf("wss://%s/console0", host)

	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec
		},
	}

	// bmcweb authenticates via HTTP Basic Auth or session token.
	basicAuth := base64.StdEncoding.EncodeToString([]byte(bmcUser + ":" + bmcPass))

	headers := http.Header{}
	headers.Set("Cookie", fmt.Sprintf("osfci_cookie=%s", cookie))
	headers.Set("Authorization", fmt.Sprintf("Basic %s", basicAuth))

	conn, _, err := dialer.Dial(wsURL, headers)
	if err != nil {
		return nil, fmt.Errorf("WebSocket connect to %s failed: %w", wsURL, err)
	}

	s := &BMCWebSession{
		conn:        conn,
		done:        make(chan struct{}),
		atLineStart: true,
	}

	return s, nil
}

// Run enters interactive terminal mode for the bmcweb console.
func (s *BMCWebSession) Run() error {
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("entering raw mode: %w", err)
	}
	s.oldState = oldState
	defer s.restore()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	intCh := make(chan os.Signal, 1)
	signal.Notify(intCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(intCh)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.readLoop()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.writeLoop()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
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

// RunPassive reads output without entering raw terminal mode.
func (s *BMCWebSession) RunPassive() error {
	intCh := make(chan os.Signal, 1)
	signal.Notify(intCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(intCh)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.readLoop()
	}()

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

// readLoop reads raw binary WebSocket frames and writes to stdout.
// bmcweb /console0 sends raw terminal data with no framing — just bytes.
func (s *BMCWebSession) readLoop() {
	defer s.Close()
	for {
		_, msg, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				select {
				case <-s.done:
				default:
					fmt.Fprintf(os.Stderr, "\r\nConnection closed: %v\r\n", err)
				}
			}
			return
		}
		// bmcweb sends raw bytes — no command prefix to strip.
		os.Stdout.Write(msg)
	}
}

// writeLoop reads stdin and sends raw bytes over the WebSocket.
func (s *BMCWebSession) writeLoop() {
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

		// bmcweb expects raw bytes — no command prefix.
		if err := s.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
			select {
			case <-s.done:
			default:
				fmt.Fprintf(os.Stderr, "\r\nWrite error: %v\r\n", err)
			}
			return
		}
	}
}

func (s *BMCWebSession) checkEscape(data []byte) bool {
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

func (s *BMCWebSession) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.conn.Close()
	})
}

func (s *BMCWebSession) restore() {
	if s.oldState != nil {
		term.Restore(int(os.Stdin.Fd()), s.oldState)
	}
}
