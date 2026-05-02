package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// DefaultHistoryLimit is the number of messages loaded when restoring a session.
const DefaultHistoryLimit = 50

// DefaultConnectTimeoutSeconds is the per-attempt deadline for the
// initial connect and each reconnect attempt. Long enough for a slow
// network handshake but short enough that a wedged DNS resolution gives
// up within a single attention span. Slow backends (local LLMs warming
// up, distant gateways) can override this from the config screen.
const DefaultConnectTimeoutSeconds = 15

// Preferences holds user-configurable settings persisted to disk.
//
// CheckForUpdates is a *bool, not a bool, so an older config.json
// that predates the field unmarshals to nil and is treated as
// "enabled" — anything else would silently disable update checks
// for every existing user. Read it through UpdateChecksEnabled.
type Preferences struct {
	CompletionBell        bool   `json:"completionBell"`
	HistoryLimit          int    `json:"historyLimit"`
	ConnectTimeoutSeconds int    `json:"connectTimeoutSeconds"`
	CheckForUpdates       *bool  `json:"checkForUpdates,omitempty"`
	LastUpdateCheck       int64  `json:"lastUpdateCheck,omitempty"`
	LatestSeenVersion     string `json:"latestSeenVersion,omitempty"`
}

// UpdateChecksEnabled reports whether the user has the startup
// update check turned on. An unset field (older config files)
// counts as enabled.
func (p Preferences) UpdateChecksEnabled() bool {
	if p.CheckForUpdates == nil {
		return true
	}
	return *p.CheckForUpdates
}

// DefaultPreferences returns the default preference values.
func DefaultPreferences() Preferences {
	enabled := true
	return Preferences{
		CompletionBell:        true,
		HistoryLimit:          DefaultHistoryLimit,
		ConnectTimeoutSeconds: DefaultConnectTimeoutSeconds,
		CheckForUpdates:       &enabled,
	}
}

// PreferencesPath returns the path to the preferences file,
// creating the parent directory if necessary.
func PreferencesPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// LoadPreferences reads preferences from disk.
// Returns defaults if the file is missing or unreadable.
func LoadPreferences() Preferences {
	path, err := PreferencesPath()
	if err != nil {
		return DefaultPreferences()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultPreferences()
	}
	var p Preferences
	if err := json.Unmarshal(data, &p); err != nil {
		return DefaultPreferences()
	}
	if p.HistoryLimit <= 0 {
		p.HistoryLimit = DefaultHistoryLimit
	}
	if p.ConnectTimeoutSeconds <= 0 {
		p.ConnectTimeoutSeconds = DefaultConnectTimeoutSeconds
	}
	// Defensive against laptop clock changes — a future timestamp
	// would otherwise wedge the daily rate-limit until time caught up.
	if p.LastUpdateCheck > time.Now().Unix() {
		p.LastUpdateCheck = 0
	}
	return p
}

// SavePreferences writes preferences to disk.
func SavePreferences(p Preferences) error {
	path, err := PreferencesPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
