package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/canopybmc/osfci-cli/internal/auth"
	"github.com/canopybmc/osfci-cli/internal/console"
)

var (
	consoleType   string
	consoleFollow bool
)

var consoleCmd = &cobra.Command{
	Use:   "console",
	Short: "Attach to a serial console on the allocated server",
	Long: `Connect to a serial console via the OSFCI gateway.

Console types:
  host    Host serial console / SOL (default) — via ttyd on controller
  bmc     BMC EM100 emulator console — via ttyd on controller
  bios    BIOS EM100 emulator console — via ttyd on controller
  web     BMC shell via bmcweb /console0 WebSocket — goes through the
          gateway's reverse proxy to the BMC's HTTPS port. Does NOT
          depend on the physical FTDI serial adapter. Requires the BMC
          to be booted and bmcweb running.

In interactive mode (default when stdin is a TTY), use ~. to detach.
In follow mode (-f) or when stdin is not a TTY, output is printed
read-only with no keyboard input — useful for scripting and CI.

Examples:
  osfci console                # host serial console (default)
  osfci console --type bmc     # BMC emulator console via ttyd
  osfci console --type web     # BMC shell via bmcweb (no FTDI needed)
  osfci console -f             # read-only, follow output (Ctrl-C to stop)`,
	Args: cobra.NoArgs,
	Run:  runConsole,
}

func init() {
	consoleCmd.Flags().StringVarP(&consoleType, "type", "t", "host",
		"Console type: host, bmc, bios, web")
	consoleCmd.Flags().BoolVarP(&consoleFollow, "follow", "f", false,
		"Read-only follow mode (no keyboard input, works without a TTY)")
	rootCmd.AddCommand(consoleCmd)
}

func runConsole(cmd *cobra.Command, args []string) {
	session := requireSession()
	if session.AllocatedServer == "" {
		fmt.Fprintf(os.Stderr, "No server allocated. Run: osfci server claim <type>\n")
		os.Exit(1)
	}

	client := newClient(session)
	host := client.Host()
	cookie := client.CookieValue()

	interactive := !consoleFollow && term.IsTerminal(int(os.Stdin.Fd()))

	// The "web" type uses a different protocol (bmcweb /console0 WebSocket)
	// instead of ttyd.
	if consoleType == "web" {
		runBMCWebConsole(session, host, cookie, interactive)
		return
	}

	// Map ttyd-based console types to gateway paths.
	var consolePath string
	switch consoleType {
	case "host":
		consolePath = "/ci/console"
	case "bmc":
		consolePath = "/ci/bmc_console"
	case "bios":
		consolePath = "/ci/smbios_console"
	default:
		fmt.Fprintf(os.Stderr, "Unknown console type: %s (use: host, bmc, bios, web)\n", consoleType)
		os.Exit(1)
	}

	if interactive {
		fmt.Printf("Connecting to %s console on %s...\n", consoleType, session.AllocatedServer)
		fmt.Printf("Escape sequence: ~. (tilde-dot at start of line) to detach\n\n")
	} else {
		fmt.Fprintf(os.Stderr, "Connecting to %s console on %s (follow mode, Ctrl-C to stop)...\n",
			consoleType, session.AllocatedServer)
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

// runBMCWebConsole connects to the BMC shell via bmcweb's /console0 WebSocket,
// proxied through the OSFCI gateway's HTTPS reverse proxy. This path does NOT
// depend on the physical FTDI serial adapter — it goes through the BMC's
// network stack.
func runBMCWebConsole(session *auth.Session, host, cookie string, interactive bool) {
	// Check BMC is up first.
	client := newClient(session)
	body, err := client.BMCUp()
	if err == nil {
		s := string(body)
		if s != `"1"` && s != "1" {
			fmt.Fprintf(os.Stderr, "BMC appears to be offline. The web console requires a booted BMC.\n")
			os.Exit(1)
		}
	}

	bmcUser := "root"
	bmcPass := "0penBmc"

	if interactive {
		fmt.Printf("Connecting to BMC web console on %s via bmcweb /console0...\n", session.AllocatedServer)
		fmt.Printf("Escape sequence: ~. (tilde-dot at start of line) to detach\n\n")
	} else {
		fmt.Fprintf(os.Stderr, "Connecting to BMC web console on %s (follow mode, Ctrl-C to stop)...\n",
			session.AllocatedServer)
	}

	sess, err := console.ConnectBMCWeb(host, cookie, bmcUser, bmcPass)
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
