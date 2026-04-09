package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/canopybmc/osfci-cli/internal/auth"
)

// tokenResponse matches the JSON from /user/{username}/get_token.
type tokenResponse struct {
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with the OSFCI platform",
	Long: `Log in to osfci.tech using your HPE SSO or native OSFCI credentials.
The session is saved to ~/.config/osfci-cli/session.json.

Both HPE SSO and native OSFCI accounts are supported — the server
handles the distinction transparently.`,
	Args: cobra.NoArgs,
	Run:  runLogin,
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Clear the saved OSFCI session",
	Args:  cobra.NoArgs,
	Run:   runLogout,
}

func init() {
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
}

func runLogin(cmd *cobra.Command, args []string) {
	// Prompt for username.
	fmt.Print("Username or email: ")
	var username string
	if _, err := fmt.Scanln(&username); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading username: %v\n", err)
		os.Exit(1)
	}
	username = strings.TrimSpace(username)
	if username == "" {
		fmt.Fprintf(os.Stderr, "Username cannot be empty.\n")
		os.Exit(1)
	}

	// Prompt for password (hidden).
	fmt.Print("Password: ")
	passwordBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println() // newline after hidden input
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading password: %v\n", err)
		os.Exit(1)
	}
	password := string(passwordBytes)
	if password == "" {
		fmt.Fprintf(os.Stderr, "Password cannot be empty.\n")
		os.Exit(1)
	}

	// Perform login.
	client := newUnauthClient()
	fmt.Printf("Logging in to %s...\n", gatewayHost)

	cookie, body, err := client.Login(username, password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
		os.Exit(1)
	}

	// Parse the token response.
	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing login response: %v\n", err)
		os.Exit(1)
	}

	if tok.AccessKey == "" || tok.SecretKey == "" {
		fmt.Fprintf(os.Stderr, "Login response missing access/secret keys.\n")
		os.Exit(1)
	}

	// Save session.
	session := &auth.Session{
		Username:  username,
		AccessKey: tok.AccessKey,
		SecretKey: tok.SecretKey,
		Cookie:    cookie,
		Server:    gatewayHost,
		LoginTime: time.Now().UTC().Format(time.RFC3339),
	}
	if err := auth.SaveSession(session); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving session: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Logged in as %s.\n", username)
	fmt.Printf("Session saved to ~/.config/osfci-cli/session.json\n")
}

func runLogout(cmd *cobra.Command, args []string) {
	if err := auth.ClearSession(); err != nil {
		fmt.Fprintf(os.Stderr, "Error clearing session: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Logged out. Session cleared.")
}
