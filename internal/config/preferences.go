package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	// Agents holds per-agent overrides, keyed by agent ID. Nested
	// under one sub-object (rather than a parallel top-level map per
	// setting) so future per-agent prefs can sit alongside HeaderColor
	// without growing config.json horizontally.
	Agents map[string]AgentPreferences `json:"agents,omitempty"`
}

// AgentPreferences holds settings that are scoped to a single agent.
// Add new per-agent fields here rather than introducing more top-level
// maps on Preferences.
type AgentPreferences struct {
	// HeaderColor is the chat header background for this agent,
	// normalised to "#RRGGBB". Empty means use the built-in default.
	HeaderColor string `json:"headerColor,omitempty"`
}

// HeaderColorFor returns the per-agent header colour override, or "" when
// the agent has no entry. Safe to call on a zero-value Preferences.
func (p Preferences) HeaderColorFor(agentID string) string {
	if agentID == "" {
		return ""
	}
	return p.Agents[agentID].HeaderColor
}

// SetHeaderColor stores hex as the header colour for agentID. An empty
// hex clears the entry, restoring the default. The nested map is
// allocated on demand so callers don't need to initialise it.
func (p *Preferences) SetHeaderColor(agentID, hex string) {
	if agentID == "" {
		return
	}
	ap := p.Agents[agentID]
	ap.HeaderColor = hex
	if ap == (AgentPreferences{}) {
		delete(p.Agents, agentID)
		if len(p.Agents) == 0 {
			p.Agents = nil
		}
		return
	}
	if p.Agents == nil {
		p.Agents = map[string]AgentPreferences{}
	}
	p.Agents[agentID] = ap
}

// NormalizeHexColor validates a hex colour string and returns it in
// canonical "#RRGGBB" form. Accepts inputs with or without a leading
// "#" and the 3-digit "#RGB" shorthand. Returns an error when the
// input is not a valid hex colour.
func NormalizeHexColor(s string) (string, error) {
	raw := strings.TrimSpace(s)
	raw = strings.TrimPrefix(raw, "#")
	if len(raw) != 3 && len(raw) != 6 {
		return "", fmt.Errorf("invalid hex colour %q: expected 3 or 6 hex digits", s)
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return "", fmt.Errorf("invalid hex colour %q: non-hex digit %q", s, string(c))
		}
	}
	if len(raw) == 3 {
		raw = string([]byte{raw[0], raw[0], raw[1], raw[1], raw[2], raw[2]})
	}
	return "#" + strings.ToUpper(raw), nil
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
