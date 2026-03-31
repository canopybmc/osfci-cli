package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	logsOutput string
	logsBios   bool
)

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Download serial-over-LAN logs",
	Long: `Download the captured SOL (Serial Over LAN) logs from the allocated server.
By default downloads BMC logs. Use --bios for BIOS logs.

The logs are written to a local file (default: sol.log, or specify with -o).

Examples:
  osfci logs                    # BMC SOL logs -> sol.log
  osfci logs -o boot.log        # BMC SOL logs -> boot.log
  osfci logs --bios             # BIOS SOL logs -> sol.log`,
	Args: cobra.NoArgs,
	Run:  runLogs,
}

func init() {
	logsCmd.Flags().StringVarP(&logsOutput, "output", "o", "sol.log",
		"Output file path")
	logsCmd.Flags().BoolVar(&logsBios, "bios", false,
		"Download BIOS SOL logs instead of BMC logs")
	rootCmd.AddCommand(logsCmd)
}

func runLogs(cmd *cobra.Command, args []string) {
	session := requireSession()
	if session.AllocatedServer == "" {
		fmt.Fprintf(os.Stderr, "No server allocated. Run: osfci server claim <type>\n")
		os.Exit(1)
	}

	client := newClient(session)

	var body []byte
	var err error
	var logType string

	if logsBios {
		logType = "BIOS"
		body, err = client.GetBIOSSOLLogs()
	} else {
		logType = "BMC"
		body, err = client.GetBMCSOLLogs(session.Username)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading %s logs: %v\n", logType, err)
		os.Exit(1)
	}

	if len(body) == 0 {
		fmt.Printf("No %s SOL logs available (empty response).\n", logType)
		return
	}

	if err := os.WriteFile(logsOutput, body, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing to %s: %v\n", logsOutput, err)
		os.Exit(1)
	}

	fmt.Printf("%s SOL logs written to %s (%d bytes)\n", logType, logsOutput, len(body))
}
