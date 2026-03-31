package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var bmcCmd = &cobra.Command{
	Use:   "bmc",
	Short: "BMC status and diagnostics",
}

var bmcStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check if the BMC is reachable",
	Long: `Checks whether the BMC on the allocated server is responding on
port 443 (HTTPS). This is a TCP-level probe performed by the gateway.`,
	Args: cobra.NoArgs,
	Run:  runBMCStatus,
}

func init() {
	bmcCmd.AddCommand(bmcStatusCmd)
	rootCmd.AddCommand(bmcCmd)
}

func runBMCStatus(cmd *cobra.Command, args []string) {
	session := requireSession()
	if session.AllocatedServer == "" {
		fmt.Fprintf(os.Stderr, "No server allocated. Run: osfci server claim <type>\n")
		os.Exit(1)
	}

	client := newClient(session)
	body, err := client.BMCUp()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking BMC status: %v\n", err)
		os.Exit(1)
	}

	s := strings.TrimSpace(string(body))
	// The response is JSON string "1" or "0".
	if s == `"1"` || s == "1" {
		fmt.Println("BMC is online (port 443 reachable).")
	} else {
		fmt.Println("BMC is offline (port 443 not reachable).")
	}
}
