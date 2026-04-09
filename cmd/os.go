package cmd

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/canopybmc/osfci-cli/internal/console"
)

var osCmd = &cobra.Command{
	Use:   "os",
	Short: "OS image management via USB boot",
	Long: `Manage operating system images on the OSFCI platform. OS images are
written to a physical USB storage device attached to the target server.
The host boots from this USB device.

Note: You can only use pre-configured images from HPE's library.
Custom image upload is not supported.`,
}

var osListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available OS images",
	Args:  cobra.NoArgs,
	Run:   runOSList,
}

var osLoadCmd = &cobra.Command{
	Use:   "load <image-name>",
	Short: "Write an OS image to the USB device",
	Long: `Downloads the specified OS image and writes it to the physical USB
device attached to the target server. Shows real-time progress of the
dd operation.

After the write completes, power on or reset the server to boot from USB.
The host BIOS must have USB boot enabled and prioritized.

Examples:
  osfci os list                        # see available images
  osfci os load ubuntu-22.04-server.img
  osfci power on                       # boot from USB`,
	Args: cobra.ExactArgs(1),
	Run:  runOSLoad,
}

var osConsoleCmd = &cobra.Command{
	Use:   "console",
	Short: "Attach to the OS loader console",
	Long: `Connect to the OS loader ttyd console to monitor the progress of
a USB write operation (dd + pv output).

Use ~. to detach in interactive mode, or -f for follow mode.`,
	Args: cobra.NoArgs,
	Run:  runOSConsole,
}

var osConsoleFollow bool

func init() {
	osConsoleCmd.Flags().BoolVarP(&osConsoleFollow, "follow", "f", false,
		"Read-only follow mode (no keyboard input)")
	osCmd.AddCommand(osListCmd)
	osCmd.AddCommand(osLoadCmd)
	osCmd.AddCommand(osConsoleCmd)
	rootCmd.AddCommand(osCmd)
}

// osImageList matches the JSON response from /ci/get_os_installers/
type osImageList struct {
	Files []string `json:"files"`
}

func runOSList(cmd *cobra.Command, args []string) {
	session := requireSession()
	if session.AllocatedServer == "" {
		fmt.Fprintf(os.Stderr, "No server allocated. Run: osfci server claim <type>\n")
		os.Exit(1)
	}

	client := newClient(session)
	body, err := client.ListOSImages()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing OS images: %v\n", err)
		os.Exit(1)
	}

	var list osImageList
	if err := json.Unmarshal(body, &list); err != nil {
		// Might be a different format — show raw response.
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		fmt.Fprintf(os.Stderr, "Raw response: %s\n", string(body))
		os.Exit(1)
	}

	if len(list.Files) == 0 {
		fmt.Println("No OS images available.")
		return
	}

	fmt.Printf("Available OS images (%d):\n", len(list.Files))
	for _, f := range list.Files {
		fmt.Printf("  %s\n", f)
	}
}

func runOSLoad(cmd *cobra.Command, args []string) {
	session := requireSession()
	if session.AllocatedServer == "" {
		fmt.Fprintf(os.Stderr, "No server allocated. Run: osfci server claim <type>\n")
		os.Exit(1)
	}

	client := newClient(session)
	imageName := args[0]

	fmt.Printf("Loading %s onto USB device on %s...\n", imageName, session.AllocatedServer)
	fmt.Println("This downloads the image and writes it via dd. May take several minutes.")
	fmt.Println()

	// Trigger the load (fire-and-forget — the controller starts async).
	_, err := client.LoadOSImage(imageName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error triggering OS load: %v\n", err)
		os.Exit(1)
	}

	// Give the controller a moment to start ttyd on port 7684.
	time.Sleep(2 * time.Second)

	// Attach to the OS loader console to show dd progress.
	fmt.Println("Attached to OS loader console (dd progress):")
	fmt.Println("---")

	host := client.Host()
	cookie := client.CookieValue()

	watchOSLoaderConsole(host, cookie)

	fmt.Println("---")
	fmt.Println("USB write complete. Power on or reset the server to boot from USB.")
}

// watchOSLoaderConsole connects to the OS loader ttyd and streams output
// until the dd completes or the connection closes.
func watchOSLoaderConsole(host, cookie string) {
	wsURL := fmt.Sprintf("wss://%s/ci/os_loader_console/ws", host)
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
		fmt.Fprintf(os.Stderr, "Warning: could not connect to OS loader console: %v\n", err)
		fmt.Fprintf(os.Stderr, "The dd may still be running. Use 'osfci os console' to reconnect.\n")
		return
	}
	defer conn.Close()

	// Send ttyd handshake.
	handshake := `{"AuthToken":"","columns":80,"rows":24}`
	conn.WriteMessage(websocket.BinaryMessage, []byte(handshake))

	// Read output until connection closes or we see dd completion.
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if len(msg) < 2 {
			continue
		}
		text := string(msg[1:])
		// Print progress output (pv and dd output).
		fmt.Print(text)
		// Detect dd completion — pv outputs a final line with total bytes.
		// dd outputs "records in/out" lines when done.
		if strings.Contains(text, "records out") {
			// Give a moment for any trailing output.
			time.Sleep(1 * time.Second)
			return
		}
	}
}

func runOSConsole(cmd *cobra.Command, args []string) {
	session := requireSession()
	if session.AllocatedServer == "" {
		fmt.Fprintf(os.Stderr, "No server allocated. Run: osfci server claim <type>\n")
		os.Exit(1)
	}

	client := newClient(session)
	host := client.Host()
	cookie := client.CookieValue()
	consolePath := "/ci/os_loader_console"

	interactive := !osConsoleFollow && term.IsTerminal(int(os.Stdin.Fd()))

	if interactive {
		fmt.Printf("Connecting to OS loader console on %s...\n", session.AllocatedServer)
		fmt.Printf("Escape sequence: ~. (tilde-dot at start of line) to detach\n\n")
	} else {
		fmt.Fprintf(os.Stderr, "Connecting to OS loader console on %s (follow mode, Ctrl-C to stop)...\n",
			session.AllocatedServer)
	}

	sess, err := console.Connect(host, cookie, consolePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Connection failed: %v\n", err)
		os.Exit(1)
	}
	defer sess.Close()

	if interactive {
		if err := sess.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Console error: %v\n", err)
			os.Exit(1)
		}
	} else {
		if err := sess.RunPassive(); err != nil {
			fmt.Fprintf(os.Stderr, "Console error: %v\n", err)
			os.Exit(1)
		}
	}
}
