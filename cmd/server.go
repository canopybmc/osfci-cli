package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/canopybmc/osfci-cli/internal/auth"
)

// serverModel matches the JSON from /ci/get_server_models.
type serverModel struct {
	Product string `json:"Product"`
	Brand   string `json:"Brand"`
	Active  int    `json:"Active"`
}

// serverAllocation matches the JSON from /ci/get_server/{type}.
type serverAllocation struct {
	Servername    string `json:"Servername"`
	Waittime      string `json:"Waittime"`
	Queue         string `json:"Queue"`
	RemainingTime string `json:"RemainingTime"`
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage remote server allocation",
}

var serverListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available server models",
	Args:  cobra.NoArgs,
	Run:   runServerList,
}

var serverClaimCmd = &cobra.Command{
	Use:   "claim <server-type>",
	Short: "Allocate a remote server (e.g. DL385_GEN11)",
	Long: `Claim a physical server from the OSFCI pool. Available types can be
found with 'osfci server list'. The server is allocated for ~60 minutes.

Example:
  osfci server claim DL385_GEN11`,
	Args: cobra.ExactArgs(1),
	Run:  runServerClaim,
}

var serverStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current server allocation",
	Args:  cobra.NoArgs,
	Run:   runServerStatus,
}

var serverReleaseCmd = &cobra.Command{
	Use:   "release",
	Short: "Release the allocated server",
	Args:  cobra.NoArgs,
	Run:   runServerRelease,
}

func init() {
	serverCmd.AddCommand(serverListCmd)
	serverCmd.AddCommand(serverClaimCmd)
	serverCmd.AddCommand(serverStatusCmd)
	serverCmd.AddCommand(serverReleaseCmd)
	rootCmd.AddCommand(serverCmd)
}

func runServerList(cmd *cobra.Command, args []string) {
	session := requireSession()
	client := newClient(session)

	body, err := client.GetServerModels()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing servers: %v\n", err)
		os.Exit(1)
	}

	var models []serverModel
	if err := json.Unmarshal(body, &models); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		fmt.Fprintf(os.Stderr, "Raw response: %s\n", string(body))
		os.Exit(1)
	}

	if len(models) == 0 {
		fmt.Println("No server models available.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TYPE\tBRAND\tSTATUS")
	fmt.Fprintln(w, "----\t-----\t------")
	for _, m := range models {
		status := "available"
		if m.Active != 1 {
			status = "inactive"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", m.Product, m.Brand, status)
	}
	w.Flush()
}

func runServerClaim(cmd *cobra.Command, args []string) {
	session := requireSession()
	client := newClient(session)
	serverType := args[0]

	fmt.Printf("Requesting %s server...\n", serverType)

	body, err := client.ClaimServer(serverType)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error claiming server: %v\n", err)
		os.Exit(1)
	}

	var alloc serverAllocation
	if err := json.Unmarshal(body, &alloc); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		fmt.Fprintf(os.Stderr, "Raw response: %s\n", string(body))
		os.Exit(1)
	}

	if alloc.Servername == "" {
		fmt.Printf("No server available right now.\n")
		if alloc.Waittime != "" && alloc.Waittime != "0" {
			fmt.Printf("Estimated wait: %s seconds\n", alloc.Waittime)
		}
		if alloc.Queue != "" && alloc.Queue != "0" {
			fmt.Printf("Queue position: %s\n", alloc.Queue)
		}
		return
	}

	// Update session with allocation info.
	session.AllocatedServer = alloc.Servername
	session.AllocatedType = serverType
	session.ClaimTime = time.Now().UTC().Format(time.RFC3339)
	if err := auth.SaveSession(session); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save allocation to session: %v\n", err)
	}

	fmt.Printf("Server allocated: %s\n", alloc.Servername)
	if alloc.RemainingTime != "" && alloc.RemainingTime != "0" {
		fmt.Printf("Time remaining: %s seconds\n", alloc.RemainingTime)
	}
}

func runServerStatus(cmd *cobra.Command, args []string) {
	session := requireSession()

	if session.AllocatedServer == "" {
		fmt.Println("No server currently allocated.")
		fmt.Println("Use: osfci server claim <type>")
		return
	}

	fmt.Printf("Server:    %s\n", session.AllocatedServer)
	fmt.Printf("Type:      %s\n", session.AllocatedType)

	if session.ClaimTime != "" {
		t, err := time.Parse(time.RFC3339, session.ClaimTime)
		if err == nil {
			elapsed := time.Since(t).Round(time.Second)
			remaining := 60*time.Minute - elapsed
			if remaining < 0 {
				remaining = 0
			}
			fmt.Printf("Claimed:   %s ago\n", elapsed)
			fmt.Printf("Remaining: ~%s (server-side may differ)\n", remaining.Round(time.Second))
		}
	}

	// Check BMC status.
	client := newClient(session)
	body, err := client.BMCUp()
	if err != nil {
		fmt.Printf("BMC:       unknown (error: %v)\n", err)
	} else {
		s := string(body)
		if s == `"1"` || s == "1" {
			fmt.Printf("BMC:       online\n")
		} else {
			fmt.Printf("BMC:       offline\n")
		}
	}
}

func runServerRelease(cmd *cobra.Command, args []string) {
	session := requireSession()

	if session.AllocatedServer == "" {
		fmt.Println("No server currently allocated.")
		return
	}

	client := newClient(session)
	serverName := session.AllocatedServer

	fmt.Printf("Releasing server %s...\n", serverName)

	_, err := client.ReleaseServer(serverName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error releasing server: %v\n", err)
		os.Exit(1)
	}

	// Clear allocation from session.
	session.AllocatedServer = ""
	session.AllocatedType = ""
	session.ClaimTime = ""
	if err := auth.SaveSession(session); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not update session: %v\n", err)
	}

	fmt.Printf("Server %s released.\n", serverName)
}
