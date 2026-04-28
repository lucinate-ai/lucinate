package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Secrets is the persistence shape for per-connection sensitive
// data (today: OpenAI-compat API keys). The file lives at
// ~/.lucinate/secrets/secrets.json with mode 0600. Values are
// keyed by connection ID so deleting a Connection naturally
// invalidates its secret on next save.
//
// A future enhancement is to back this with the OS keychain
// (Keychain on macOS, libsecret on Linux, Credential Manager on
// Windows) and fall back to the JSON file when no keychain is
// available. The current implementation keeps everything on disk
// so first-run UX doesn't depend on platform-specific libraries.
type Secrets struct {
	APIKeys map[string]string `json:"apiKeys,omitempty"`
}

// SecretsPath returns the on-disk location, creating the parent
// directory if needed.
func SecretsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".lucinate", "secrets")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "secrets.json"), nil
}

// LoadSecrets reads the secrets file from disk, returning an empty
// Secrets value if missing or unreadable.
func LoadSecrets() Secrets {
	path, err := SecretsPath()
	if err != nil {
		return Secrets{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Secrets{}
	}
	var s Secrets
	if err := json.Unmarshal(data, &s); err != nil {
		return Secrets{}
	}
	if s.APIKeys == nil {
		s.APIKeys = map[string]string{}
	}
	return s
}

// SaveSecrets writes the secrets file atomically, with mode 0600.
func SaveSecrets(s Secrets) error {
	path, err := SecretsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".secrets-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0600); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

// GetAPIKey returns the API key for a connection, or "" if none.
func GetAPIKey(connID string) string {
	if connID == "" {
		return ""
	}
	return LoadSecrets().APIKeys[connID]
}

// SetAPIKey stores or removes a connection's API key. An empty key
// clears the entry. The function loads-mutates-saves on each call;
// concurrent writers from the same process aren't a concern because
// the TUI only stores keys from a single goroutine (the auth-modal
// resolution path).
func SetAPIKey(connID, key string) error {
	if connID == "" {
		return fmt.Errorf("connection id is required")
	}
	s := LoadSecrets()
	if s.APIKeys == nil {
		s.APIKeys = map[string]string{}
	}
	if key == "" {
		delete(s.APIKeys, connID)
	} else {
		s.APIKeys[connID] = key
	}
	return SaveSecrets(s)
}
