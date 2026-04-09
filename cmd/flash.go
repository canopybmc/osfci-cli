package cmd

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"

	"github.com/canopybmc/osfci-cli/internal/api"
)

var (
	flashOriginal bool
	flashNoWait   bool
	flashBios     bool
)

var flashCmd = &cobra.Command{
	Use:   "flash [firmware-file]",
	Short: "Upload and flash firmware via EM100 emulator",
	Long: `Upload a firmware binary to the EM100 SPI flash emulator on the
allocated server. By default flashes the BMC. Use --bios to flash the
host BIOS/UEFI instead.

After uploading, waits for the EM100 to finish programming and verify
the image (typically 30-60 seconds).

Use --original to load HPE's stock firmware instead of uploading a file.
Use --no-wait to skip waiting for EM100 verification.

The OSFCI platform has TWO separate EM100 emulators:
  - BMC flash (default) — the OpenBMC image
  - BIOS flash (--bios) — the host UEFI/BIOS image
Both must be loaded for the host to boot.

Examples:
  osfci flash firmware.static.mtd           # flash BMC
  osfci flash --bios uefi.rom               # flash host BIOS
  osfci flash --original                    # load stock BMC
  osfci flash --original --bios             # load stock BIOS
  osfci flash --original --bios --original  # load both stock images`,
	Args: cobra.MaximumNArgs(1),
	Run:  runFlash,
}

var emulatorCmd = &cobra.Command{
	Use:   "emulator",
	Short: "EM100 flash emulator management",
}

var emulatorResetCmd = &cobra.Command{
	Use:   "reset [bmc|bios]",
	Short: "Reset an EM100 emulator",
	Long: `Reset the EM100 flash emulator hardware. Use 'bmc' (default) or 'bios'.

Examples:
  osfci emulator reset       # reset BMC EM100
  osfci emulator reset bios  # reset BIOS EM100`,
	Args: cobra.MaximumNArgs(1),
	Run:  runEmulatorReset,
}

var emulatorPoolCmd = &cobra.Command{
	Use:   "pool",
	Short: "Check emulator pool availability",
	Args:  cobra.NoArgs,
	Run:   runEmulatorPool,
}

func init() {
	flashCmd.Flags().BoolVar(&flashOriginal, "original", false,
		"Load HPE's stock firmware instead of uploading a file")
	flashCmd.Flags().BoolVar(&flashBios, "bios", false,
		"Flash the host BIOS/UEFI EM100 instead of the BMC EM100")
	flashCmd.Flags().BoolVar(&flashNoWait, "no-wait", false,
		"Skip waiting for EM100 verification (advanced)")
	rootCmd.AddCommand(flashCmd)

	emulatorCmd.AddCommand(emulatorResetCmd)
	emulatorCmd.AddCommand(emulatorPoolCmd)
	rootCmd.AddCommand(emulatorCmd)
}

func runFlash(cmd *cobra.Command, args []string) {
	session := requireSession()
	if session.AllocatedServer == "" {
		fmt.Fprintf(os.Stderr, "No server allocated. Run: osfci server claim <type>\n")
		os.Exit(1)
	}

	client := newClient(session)
	target := "BMC"
	if flashBios {
		target = "BIOS"
	}

	if flashOriginal {
		fmt.Printf("Loading original HPE %s firmware onto EM100...\n", target)
		var err error
		if flashBios {
			_, err = client.StartOriginalBIOS()
		} else {
			_, err = client.StartOriginalBMC(session.Username)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		consolePath := "/ci/console"
		if flashBios {
			consolePath = "/ci/smbios_console"
		}
		if !flashNoWait {
			waitForEM100(client, consolePath)
		}
		fmt.Printf("Original %s firmware loaded. Ready to power on.\n", target)
		return
	}

	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Specify a firmware file or use --original.\n")
		fmt.Fprintf(os.Stderr, "Usage: osfci flash [--bios] <firmware-file>\n")
		os.Exit(1)
	}

	firmwarePath := args[0]
	f, err := os.Open(firmwarePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot open %s: %v\n", firmwarePath, err)
		os.Exit(1)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot stat %s: %v\n", firmwarePath, err)
		os.Exit(1)
	}

	filename := filepath.Base(firmwarePath)
	sizeMB := float64(stat.Size()) / (1024 * 1024)
	fmt.Printf("Uploading %s (%.1f MiB) to %s EM100...\n", filename, sizeMB, target)

	if flashBios {
		_, err = client.UploadBIOSFirmware(session.Username, filename, f, stat.Size())
	} else {
		_, err = client.UploadBMCFirmware(session.Username, filename, f, stat.Size())
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Upload failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Upload complete. Waiting for %s EM100 to program and verify...\n", target)

	consolePath := "/ci/console"
	if flashBios {
		consolePath = "/ci/smbios_console"
	}
	if !flashNoWait {
		waitForEM100(client, consolePath)
	}

	fmt.Printf("%s EM100 ready. You can now power on the server.\n", target)
}

// waitForEM100 connects to the specified console (EM100 output) and watches for
// "Verify: PASS" or "FATAL" to determine if programming succeeded.
func waitForEM100(client *api.Client, consolePath string) {
	host := client.Host()
	cookie := client.CookieValue()

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
		fmt.Fprintf(os.Stderr, "Warning: could not monitor EM100 progress: %v\n", err)
		fmt.Fprintf(os.Stderr, "Wait ~60 seconds before powering on.\n")
		return
	}
	defer conn.Close()

	// Send ttyd handshake.
	handshake := `{"AuthToken":"","columns":80,"rows":24}`
	conn.WriteMessage(websocket.BinaryMessage, []byte(handshake))

	done := make(chan string, 1)
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				done <- "disconnected"
				return
			}
			if len(msg) < 2 {
				continue
			}
			text := string(msg[1:])
			if strings.Contains(text, "Verify: PASS") {
				done <- "pass"
				return
			}
			if strings.Contains(text, "FATAL") {
				done <- "fail"
				return
			}
			if strings.Contains(text, "Sent ") || strings.Contains(text, "Read ") {
				lines := strings.Split(strings.TrimSpace(text), "\n")
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "Sent ") || strings.HasPrefix(line, "Read ") {
						fmt.Printf("\r  %s", line)
					}
				}
			}
			if strings.Contains(text, "Transfer Succeeded") {
				fmt.Printf("\n  Transfer complete, verifying...\n")
			}
		}
	}()

	select {
	case result := <-done:
		fmt.Println()
		switch result {
		case "pass":
			fmt.Println("EM100 verify: PASS")
		case "fail":
			fmt.Fprintf(os.Stderr, "EM100 verify: FAILED (check image size matches chip)\n")
			os.Exit(1)
		case "disconnected":
			fmt.Fprintf(os.Stderr, "Warning: EM100 console disconnected. Wait ~30s before powering on.\n")
		}
	case <-time.After(120 * time.Second):
		fmt.Println()
		fmt.Fprintf(os.Stderr, "Warning: timed out waiting for EM100 (120s). It may still be programming.\n")
	}
}

func runEmulatorReset(cmd *cobra.Command, args []string) {
	session := requireSession()
	if session.AllocatedServer == "" {
		fmt.Fprintf(os.Stderr, "No server allocated. Run: osfci server claim <type>\n")
		os.Exit(1)
	}

	deviceType := "bmc"
	if len(args) > 0 {
		deviceType = args[0]
	}
	if deviceType != "bmc" && deviceType != "bios" && deviceType != "rom" {
		fmt.Fprintf(os.Stderr, "Unknown emulator type: %s (use: bmc, bios)\n", deviceType)
		os.Exit(1)
	}
	// The controller uses "rom" for the BIOS emulator.
	if deviceType == "bios" {
		deviceType = "rom"
	}

	client := newClient(session)
	fmt.Printf("Resetting %s EM100 emulator...\n", args)

	_, err := client.ResetEmulator(deviceType)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("EM100 emulator reset.")
}

func runEmulatorPool(cmd *cobra.Command, args []string) {
	session := requireSession()
	if session.AllocatedServer == "" {
		fmt.Fprintf(os.Stderr, "No server allocated. Run: osfci server claim <type>\n")
		os.Exit(1)
	}

	client := newClient(session)
	body, err := client.EmulatorPool()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Emulator pool status: %s\n", string(body))
}
