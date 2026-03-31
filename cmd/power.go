package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var powerCmd = &cobra.Command{
	Use:   "power",
	Short: "Control server power state",
}

var powerOnCmd = &cobra.Command{
	Use:   "on",
	Short: "Power on the allocated server",
	Args:  cobra.NoArgs,
	Run:   runPowerOn,
}

var powerOffCmd = &cobra.Command{
	Use:   "off",
	Short: "Power off the allocated server",
	Args:  cobra.NoArgs,
	Run:   runPowerOff,
}

func init() {
	powerCmd.AddCommand(powerOnCmd)
	powerCmd.AddCommand(powerOffCmd)
	rootCmd.AddCommand(powerCmd)
}

func runPowerOn(cmd *cobra.Command, args []string) {
	session := requireSession()
	if session.AllocatedServer == "" {
		fmt.Fprintf(os.Stderr, "No server allocated. Run: osfci server claim <type>\n")
		os.Exit(1)
	}

	client := newClient(session)
	fmt.Printf("Powering on %s...\n", session.AllocatedServer)

	_, err := client.PowerOn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Power on command sent.")
}

func runPowerOff(cmd *cobra.Command, args []string) {
	session := requireSession()
	if session.AllocatedServer == "" {
		fmt.Fprintf(os.Stderr, "No server allocated. Run: osfci server claim <type>\n")
		os.Exit(1)
	}

	client := newClient(session)
	fmt.Printf("Powering off %s...\n", session.AllocatedServer)

	_, err := client.PowerOff()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Power off command sent.")
}
