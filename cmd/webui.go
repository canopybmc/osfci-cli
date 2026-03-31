package cmd

import (
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/spf13/cobra"
)

var (
	webuiPort int
)

var webuiCmd = &cobra.Command{
	Use:   "webui",
	Short: "Open the BMC WebUI in your browser via a local proxy",
	Long: `Starts a local HTTPS reverse proxy that injects the CLI's session cookie
into every request, then opens your browser pointing at it. This gives
you full access to the OpenBMC WebUI (and its built-in serial console,
KVM, etc.) without needing to log in to OSFCI separately in your browser.

The proxy runs on https://localhost:8443 by default (change with --port).

IMPORTANT: You must accept the self-signed certificate in your browser.
If KVM or SOL console don't connect, the browser is silently rejecting
the cert for WebSocket connections. Fix by typing 'thisisunsafe' on
the Chrome certificate warning page (no input field — just type it).

Press Ctrl-C to stop the proxy.

Example:
  osfci webui              # start proxy on :8443 and open browser
  osfci webui --port 9443  # use a different port`,
	Args: cobra.NoArgs,
	Run:  runWebUI,
}

func init() {
	webuiCmd.Flags().IntVar(&webuiPort, "port", 8443,
		"Local proxy port")
	rootCmd.AddCommand(webuiCmd)
}

func runWebUI(cmd *cobra.Command, args []string) {
	session := requireSession()
	if session.AllocatedServer == "" {
		fmt.Fprintf(os.Stderr, "No server allocated. Run: osfci server claim <type>\n")
		os.Exit(1)
	}

	// Check if BMC is up.
	client := newClient(session)
	body, err := client.BMCUp()
	if err == nil {
		s := string(body)
		if s != `"1"` && s != "1" {
			fmt.Println("Warning: BMC appears to be offline. The WebUI may not load.")
		}
	}

	// Build the reverse proxy targeting the OSFCI gateway.
	target, _ := url.Parse(client.BaseURL)
	proxy := httputil.NewSingleHostReverseProxy(target)

	// Use TLS with InsecureSkipVerify for the upstream connection to
	// osfci.tech (their cert chain is incomplete).
	proxy.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec
		},
	}

	// Inject the osfci_cookie into every proxied request.
	cookie := client.CookieValue()
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.AddCookie(&http.Cookie{
			Name:  "osfci_cookie",
			Value: cookie,
		})
		// Set the Host header to the gateway so it routes correctly.
		req.Host = target.Host
	}

	// Generate a self-signed TLS cert for the local proxy.
	cert, err := generateSelfSignedCert()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating TLS cert: %v\n", err)
		os.Exit(1)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", webuiPort)
	localURL := fmt.Sprintf("https://localhost:%d/", webuiPort)

	// Suppress TLS handshake error logs from browsers that reject the
	// self-signed cert on preflight/favicon requests. These are harmless.
	quietLog := log.New(os.Stderr, "", 0)
	quietLog.SetOutput(devNull{})

	server := &http.Server{
		Addr:    addr,
		Handler: proxy,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
		},
		ErrorLog: quietLog,
	}

	// Handle Ctrl-C gracefully.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nStopping proxy...")
		server.Close()
	}()

	fmt.Printf("Starting local proxy: %s -> %s\n", localURL, client.BaseURL)
	fmt.Printf("Server: %s (%s)\n", session.AllocatedServer, session.AllocatedType)
	fmt.Println()
	fmt.Println("Accept the self-signed certificate in your browser.")
	fmt.Println("If KVM or SOL console don't connect, the browser may be")
	fmt.Println("silently rejecting the cert for WebSocket connections.")
	fmt.Println("Fix: type 'thisisunsafe' on the Chrome certificate warning page.")
	fmt.Println()
	fmt.Println("Press Ctrl-C to stop the proxy.")

	// Open browser.
	go func() {
		if err := openBrowser(localURL); err != nil {
			fmt.Printf("Could not open browser: %v\n", err)
			fmt.Printf("Open this URL manually: %s\n", localURL)
		}
	}()

	// Start the HTTPS server (blocks until closed).
	if err := server.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
		log.Fatalf("Proxy server error: %v", err)
	}
}

// devNull discards all writes (used to suppress TLS handshake error logs).
type devNull struct{}

func (devNull) Write(p []byte) (int, error) { return len(p), nil }

// openBrowser opens a URL in the default system browser.
func openBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", rawURL)
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", rawURL)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	return cmd.Start()
}
