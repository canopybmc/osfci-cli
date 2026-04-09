// Package cmd implements the CLI commands for osfci-cli.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/canopybmc/osfci-cli/internal/api"
	"github.com/canopybmc/osfci-cli/internal/auth"
)

var (
	// gatewayHost is the OSFCI gateway hostname.
	gatewayHost string
)

var rootCmd = &cobra.Command{
	Use:   "osfci",
	Short: "CLI client for the OSFCI remote BMC development platform",
	Long: `osfci-cli provides command-line access to osfci.tech, HPE's remote
OpenBMC development platform. Allocate servers, flash firmware, access
serial consoles, and control power — all from the terminal.`,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&gatewayHost, "server", "osfci.tech",
		"OSFCI gateway hostname")
}

// requireSession loads the saved session or exits with an error.
func requireSession() *auth.Session {
	session, err := auth.LoadSession()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading session: %v\n", err)
		os.Exit(1)
	}
	if session == nil {
		fmt.Fprintf(os.Stderr, "Not logged in. Run: osfci login\n")
		os.Exit(1)
	}
	if session.IsExpired() {
		fmt.Fprintf(os.Stderr, "Session expired. Run: osfci login\n")
		os.Exit(1)
	}
	return session
}

// newClient creates an authenticated API client from the saved session.
func newClient(session *auth.Session) *api.Client {
	host := gatewayHost
	if session.Server != "" {
		host = session.Server
	}
	return api.NewClient(host, session)
}

// newUnauthClient creates an unauthenticated API client.
func newUnauthClient() *api.Client {
	return api.NewClient(gatewayHost, nil)
}
