// Package api provides an HTTP client for the OSFCI gateway API.
package api

import (
	"crypto/tls"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"github.com/canopybmc/osfci-cli/internal/auth"
)

// Client wraps an HTTP client with OSFCI authentication.
type Client struct {
	BaseURL    string // e.g. "https://osfci.tech"
	Session    *auth.Session
	httpClient *http.Client
}

// NewClient creates an API client for the given gateway host.
// If session is non-nil, requests are authenticated.
func NewClient(host string, session *auth.Session) *Client {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			// OSFCI uses a certificate that may not be in standard CA bundles.
			InsecureSkipVerify: true, //nolint:gosec
		},
	}
	return &Client{
		BaseURL: "https://" + host,
		Session: session,
		httpClient: &http.Client{
			Transport: transport,
			// Do not follow redirects automatically — we need to capture
			// Set-Cookie headers from the initial response.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// newRequest creates an http.Request with the proper base URL.
func (c *Client) newRequest(method, path string, body io.Reader) (*http.Request, error) {
	u := c.BaseURL + path
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	return req, nil
}

// addCookie adds the osfci_cookie to the request if we have a session.
func (c *Client) addCookie(req *http.Request) {
	if c.Session != nil && c.Session.Cookie != "" {
		req.AddCookie(&http.Cookie{
			Name:  "osfci_cookie",
			Value: c.Session.Cookie,
		})
	}
}

// addSignature adds HMAC-SHA1 signed auth headers to the request.
func (c *Client) addSignature(req *http.Request) {
	if c.Session == nil || c.Session.AccessKey == "" || c.Session.SecretKey == "" {
		return
	}
	date := auth.FormatDate()
	contentType := req.Header.Get("Content-Type")
	sig := auth.Sign(req.Method, contentType, date, req.URL.Path, c.Session.SecretKey)
	req.Header.Set("Authorization", auth.AuthorizationHeader(c.Session.AccessKey, sig))
	req.Header.Set("myDate", date)
}

// Login performs username/password authentication.
// On success, returns the Set-Cookie value and the raw response body (JSON with accessKey/secretKey).
func (c *Client) Login(username, password string) (cookie string, body []byte, err error) {
	form := url.Values{}
	form.Set("username", username)
	form.Set("password", password)

	path := fmt.Sprintf("/user/%s/get_token", username)
	req, err := c.newRequest("POST", path, strings.NewReader(form.Encode()))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("login failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	// Extract osfci_cookie from Set-Cookie header.
	for _, c := range resp.Cookies() {
		if c.Name == "osfci_cookie" {
			cookie = c.Value
			break
		}
	}
	if cookie == "" {
		return "", nil, fmt.Errorf("login succeeded but no osfci_cookie in response")
	}

	return cookie, respBody, nil
}

// VerifyUser checks whether a username exists in OSFCI's native storage.
// Returns (exists bool, oktaRedirect string, err).
func (c *Client) VerifyUser(username string) (bool, string, error) {
	path := fmt.Sprintf("/user/%s/verify_user", username)
	req, err := c.newRequest("GET", path, nil)
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("verify_user request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "", fmt.Errorf("reading response: %w", err)
	}

	// Response is JSON: {"Exists": 1} or {"Exists": 0, "Redirect": "..."}
	// Simple check — avoid importing encoding/json for this small response.
	s := string(body)
	if strings.Contains(s, `"Exists": 1`) || strings.Contains(s, `"Exists":1`) {
		return true, "", nil
	}
	return false, "", nil
}

// GetServerModels returns the raw JSON response listing available server models.
func (c *Client) GetServerModels() ([]byte, error) {
	return c.cookieGet("/ci/get_server_models")
}

// ClaimServer allocates a server of the given type. Returns raw JSON response.
// The frontend URL is /ci/get_server/{serverType} (two segments after /ci/).
func (c *Client) ClaimServer(serverType string) ([]byte, error) {
	path := fmt.Sprintf("/ci/get_server/%s", serverType)
	return c.cookieGet(path)
}

// ReleaseServer releases the named server. Returns raw response body.
// The frontend sends this as PUT /ci/stop_server/{servername}.
func (c *Client) ReleaseServer(serverName string) ([]byte, error) {
	path := fmt.Sprintf("/ci/stop_server/%s", serverName)
	return c.cookiePut(path, nil)
}

// PowerOn powers on the allocated server.
func (c *Client) PowerOn() ([]byte, error) {
	return c.cookieGet("/ci/power_on")
}

// PowerOff powers off the allocated server.
func (c *Client) PowerOff() ([]byte, error) {
	return c.cookieGet("/ci/power_off")
}

// BMCUp checks if the BMC is reachable. Returns the raw response ("1" or "0").
func (c *Client) BMCUp() ([]byte, error) {
	return c.cookieGet("/ci/bmc_up")
}

// cookieGet performs a GET request with the osfci_cookie attached.
func (c *Client) cookieGet(path string) ([]byte, error) {
	req, err := c.newRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}
	c.addCookie(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return body, nil
}

// cookiePut performs a PUT request with the osfci_cookie attached.
func (c *Client) cookiePut(path string, body io.Reader) ([]byte, error) {
	req, err := c.newRequest("PUT", path, body)
	if err != nil {
		return nil, err
	}
	c.addCookie(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	return respBody, nil
}

// SignedGet performs a GET with both cookie and HMAC signature.
func (c *Client) SignedGet(path, contentType string) ([]byte, error) {
	req, err := c.newRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.addCookie(req)
	c.addSignature(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return body, nil
}

// UploadBMCFirmware uploads a BMC firmware file via multipart form.
// The form field name is "fichier" (as the OSFCI controller expects).
func (c *Client) UploadBMCFirmware(username string, filename string, fileReader io.Reader, fileSize int64) ([]byte, error) {
	path := fmt.Sprintf("/ci/bmc_firmware/%s", username)
	return c.uploadMultipart(path, "fichier", filename, fileReader, fileSize)
}

// uploadMultipart performs a multipart file upload with cookie auth.
func (c *Client) uploadMultipart(path, fieldName, filename string, fileReader io.Reader, fileSize int64) ([]byte, error) {
	// Use a pipe to stream the multipart body without buffering the entire
	// firmware image in memory.
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	// Write the multipart body in a goroutine.
	errCh := make(chan error, 1)
	go func() {
		defer pw.Close()
		part, err := writer.CreateFormFile(fieldName, filename)
		if err != nil {
			errCh <- fmt.Errorf("creating form file: %w", err)
			return
		}
		if _, err := io.Copy(part, fileReader); err != nil {
			errCh <- fmt.Errorf("writing file data: %w", err)
			return
		}
		if err := writer.Close(); err != nil {
			errCh <- fmt.Errorf("closing multipart writer: %w", err)
			return
		}
		errCh <- nil
	}()

	req, err := c.newRequest("POST", path, pr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.addCookie(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upload request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check if the multipart writer had an error.
	if writeErr := <-errCh; writeErr != nil {
		return nil, writeErr
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// The controller returns "Error" on failure.
	if strings.TrimSpace(string(body)) == "Error" {
		return nil, fmt.Errorf("controller returned error during firmware upload")
	}

	return body, nil
}

// StartOriginalBMC loads HPE's stock BMC firmware onto the EM100 emulator.
func (c *Client) StartOriginalBMC(username string) ([]byte, error) {
	path := fmt.Sprintf("/ci/start_bmc/%s", username)
	return c.cookieGet(path)
}

// StartOriginalBIOS loads HPE's stock BIOS/UEFI firmware onto the BIOS EM100 emulator.
func (c *Client) StartOriginalBIOS() ([]byte, error) {
	return c.cookieGet("/ci/start_smbios/")
}

// UploadBIOSFirmware uploads a BIOS/UEFI firmware file via multipart form.
func (c *Client) UploadBIOSFirmware(username string, filename string, fileReader io.Reader, fileSize int64) ([]byte, error) {
	path := fmt.Sprintf("/ci/bios_firmware/%s", username)
	return c.uploadMultipart(path, "fichier", filename, fileReader, fileSize)
}

// ResetEmulator resets the EM100 emulator. deviceType is "bmc" or "rom".
func (c *Client) ResetEmulator(deviceType string) ([]byte, error) {
	path := fmt.Sprintf("/ci/reset_emulator/%s", deviceType)
	return c.cookieGet(path)
}

// EmulatorPool checks if the emulator pool is available.
func (c *Client) EmulatorPool() ([]byte, error) {
	return c.cookieGet("/ci/is_emulators_pool")
}

// GetBMCSOLLogs downloads the BMC serial-over-LAN logs.
// Frontend URL: /ci/sol_bmc_logs/{username}/
func (c *Client) GetBMCSOLLogs(username string) ([]byte, error) {
	path := fmt.Sprintf("/ci/sol_bmc_logs/%s/", username)
	return c.cookieGet(path)
}

// GetBIOSSOLLogs downloads the BIOS serial-over-LAN logs.
func (c *Client) GetBIOSSOLLogs() ([]byte, error) {
	return c.cookieGet("/ci/sol_bios_logs")
}

// ListOSImages returns the JSON list of available OS images from the storage backend.
// Response format: {"files": ["ubuntu-22.04.img", "fedora-38.img", ...]}
func (c *Client) ListOSImages() ([]byte, error) {
	return c.cookieGet("/ci/get_os_installers/")
}

// LoadOSImage triggers downloading and writing the named image to the USB device.
// This is a fire-and-forget request — the actual dd happens asynchronously on the
// controller. Monitor progress via the os_loader_console ttyd.
func (c *Client) LoadOSImage(filename string) ([]byte, error) {
	path := fmt.Sprintf("/ci/get_os_installers/%s", filename)
	return c.cookieGet(path)
}

// Host returns the gateway hostname (for WebSocket connections etc).
func (c *Client) Host() string {
	// Strip the "https://" prefix.
	return strings.TrimPrefix(c.BaseURL, "https://")
}

// CookieValue returns the osfci_cookie value for WebSocket auth.
func (c *Client) CookieValue() string {
	if c.Session != nil {
		return c.Session.Cookie
	}
	return ""
}
