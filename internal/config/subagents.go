// Package config — subagent spawn defaults.
//
// SubagentDefaults seeds the manual /subagents spawn verb (and the
// future Ollama orchestrator) with the model / label the user
// typically wants. The values are advisory: backends ignore fields they
// don't understand, and most real safety knobs (max-depth,
// max-concurrent, timeout) live server-side on the gateway for
// OpenClaw / Hermes. Stored at ~/.lucinate/subagents.json.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// SubagentDefaults bundles the client-side defaults applied to a
// /subagents spawn invocation when the user doesn't override them
// inline. All fields are optional.
type SubagentDefaults struct {
	// Model overrides the model the spawned subagent uses. Empty
	// lets the backend pick (parent's model for OpenClaw).
	Model string `json:"model,omitempty"`

	// Label is a human-readable handle prefilled on the spawn form.
	Label string `json:"label,omitempty"`

	// AgentID overrides the target agent for the spawned subagent.
	// Empty falls back to the parent session's agent.
	AgentID string `json:"agentId,omitempty"`

	// TimeoutSeconds caps the spawn's wall-clock runtime. 0 leaves
	// the limit to the backend. Currently advisory — only the future
	// Ollama orchestrator enforces it client-side; OpenClaw uses its
	// own server-side timeout.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
}

// DefaultSubagentDefaults returns the zero value. Kept as a constructor
// so future fields can grow defaults without changing call sites.
func DefaultSubagentDefaults() SubagentDefaults { return SubagentDefaults{} }

// SubagentDefaultsPath returns the path to the subagent defaults file,
// creating the parent directory if necessary.
func SubagentDefaultsPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "subagents.json"), nil
}

// LoadSubagentDefaults reads the defaults from disk. A missing or
// unreadable file returns the zero value — that's the same shape the
// rest of the TUI treats as "user has no preferences", which keeps the
// /subagents spawn flow free of "file not found" noise on first run.
func LoadSubagentDefaults() SubagentDefaults {
	path, err := SubagentDefaultsPath()
	if err != nil {
		return DefaultSubagentDefaults()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultSubagentDefaults()
	}
	var d SubagentDefaults
	if err := json.Unmarshal(data, &d); err != nil {
		return DefaultSubagentDefaults()
	}
	return d
}

// SaveSubagentDefaults writes the defaults to disk.
func SaveSubagentDefaults(d SubagentDefaults) error {
	path, err := SubagentDefaultsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
