// Package auth handles OSFCI authentication, credential persistence, and request signing.
package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Session holds the persisted authentication state for an OSFCI session.
type Session struct {
	Username  string `json:"username"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	Cookie    string `json:"cookie"`     // osfci_cookie value
	Server    string `json:"server"`     // gateway hostname (e.g. osfci.tech)
	LoginTime string `json:"login_time"` // RFC3339 timestamp

	// Allocated server info (populated after claim)
	AllocatedServer string `json:"allocated_server,omitempty"`
	AllocatedType   string `json:"allocated_type,omitempty"`
	ClaimTime       string `json:"claim_time,omitempty"`
}

// configDir returns ~/.config/osfci-cli, creating it if needed.
func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	dir := filepath.Join(home, ".config", "osfci-cli")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("cannot create config directory: %w", err)
	}
	return dir, nil
}

// sessionPath returns the path to the session file.
func sessionPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "session.json"), nil
}

// SaveSession writes the session to disk.
func SaveSession(s *Session) error {
	path, err := sessionPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot marshal session: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("cannot write session file: %w", err)
	}
	return nil
}

// LoadSession reads the session from disk. Returns nil if no session exists.
func LoadSession() (*Session, error) {
	path, err := sessionPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read session file: %w", err)
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("cannot parse session file: %w", err)
	}
	return &s, nil
}

// ClearSession removes the session file.
func ClearSession() error {
	path, err := sessionPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot remove session file: %w", err)
	}
	return nil
}

// IsExpired checks if the session is likely expired (>24h since login).
func (s *Session) IsExpired() bool {
	t, err := time.Parse(time.RFC3339, s.LoginTime)
	if err != nil {
		return true
	}
	return time.Since(t) > 24*time.Hour
}
