package rooms

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// Preset is a reusable room definition — a "predefined room". It is
// what the rooms menu offers as one-keystroke setups: a name, the
// profiles to invite, and the thread to run the conversation on.
//
// A preset is local to lucinate; the room it describes lives on the
// gateway. Creating a room from a preset is idempotent on RoomID, so
// re-running a preset adopts the existing room instead of forking a
// second one.
type Preset struct {
	Name        string   `json:"name"`
	RoomID      string   `json:"room_id"`
	ThreadID    string   `json:"thread_id,omitempty"`
	Members     []Member `json:"members"`
	Description string   `json:"description,omitempty"`

	// Worktree is the Orca worktree selector this room is seated for. It
	// is passed to the members as project context on the room's first
	// message, so a predefined room carries its project with it.
	Worktree string `json:"worktree,omitempty"`
}

// PresetStore is the on-disk collection of predefined rooms.
type PresetStore struct {
	Presets []Preset `json:"presets"`
}

// DefaultPresetsPath is <lucinate data dir>/rooms.json.
func DefaultPresetsPath() (string, error) {
	dir, err := config.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "rooms.json"), nil
}

// LoadPresets reads the store. A missing file is an empty store, and a
// malformed one is too: a corrupt preset list must never stop the TUI
// from starting. The error is reserved for unreadable-but-present files.
func LoadPresets(path string) (PresetStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PresetStore{}, nil
		}
		return PresetStore{}, err
	}
	var store PresetStore
	if err := json.Unmarshal(data, &store); err != nil {
		return PresetStore{}, nil
	}
	return store, nil
}

// SavePresets writes the store with the same 0700/0600 permissions the
// rest of lucinate's state uses.
func SavePresets(path string, store PresetStore) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// Add validates and appends a preset. The roster must satisfy the
// gateway's rules and the name must be new (case-insensitive); RoomID is
// derived from the name when the caller left it blank.
func (s *PresetStore) Add(p Preset) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return fmt.Errorf("preset name is required")
	}
	if err := ValidateRoster(p.Members); err != nil {
		return fmt.Errorf("preset %q: %w", p.Name, err)
	}
	if s.Find(p.Name) != nil {
		return fmt.Errorf("a preset named %q already exists", p.Name)
	}
	if strings.TrimSpace(p.RoomID) == "" {
		p.RoomID = Slug(p.Name)
	}
	if p.ThreadID == "" {
		p.ThreadID = DefaultThreadID
	}
	s.Presets = append(s.Presets, p)
	return nil
}

// Remove deletes a preset by name or room id (case-insensitive). It
// reports whether anything was removed.
func (s *PresetStore) Remove(name string) bool {
	want := strings.ToLower(strings.TrimSpace(name))
	for i, p := range s.Presets {
		if strings.ToLower(p.Name) == want || strings.ToLower(p.RoomID) == want {
			s.Presets = append(s.Presets[:i], s.Presets[i+1:]...)
			return true
		}
	}
	return false
}

// Find returns the preset matching a name or room id
// (case-insensitive), or nil.
func (s *PresetStore) Find(name string) *Preset {
	want := strings.ToLower(strings.TrimSpace(name))
	for i := range s.Presets {
		if strings.ToLower(s.Presets[i].Name) == want || strings.ToLower(s.Presets[i].RoomID) == want {
			return &s.Presets[i]
		}
	}
	return nil
}

// UpdatePresetRoomID points a stored preset at the room id it now starts.
//
// A preset whose id was disbanded — or is taken by a live room with a
// different roster — starts a NEW room instead of the one it names. The
// preset follows that new room, otherwise every start mints another one and
// the list of predefined rooms drifts away from the rooms that exist. A
// missing store, an unknown preset and an already-current id are all
// nothing-to-do, reported as (false, nil).
func UpdatePresetRoomID(path, key, roomID string) (bool, error) {
	if strings.TrimSpace(roomID) == "" {
		return false, fmt.Errorf("room id is required")
	}
	store, err := LoadPresets(path)
	if err != nil {
		return false, err
	}
	preset := store.Find(key)
	if preset == nil || preset.RoomID == roomID {
		return false, nil
	}
	preset.RoomID = roomID
	if err := SavePresets(path, store); err != nil {
		return false, err
	}
	return true, nil
}

// Names lists the preset names, sorted, for a menu.
func (s *PresetStore) Names() []string {
	out := make([]string, 0, len(s.Presets))
	for _, p := range s.Presets {
		out = append(out, p.Name)
	}
	sort.Strings(out)
	return out
}

// maxSlugChars bounds a derived room id. The gateway allows 128, but the
// id is what a human types in `rooms send <id>` — a 90-character slug
// built from six profile names is unusable, so it is cut at a separator.
const maxSlugChars = 48

// Slug turns a display name into a gateway-safe room id.
func Slug(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > maxSlugChars {
		out = out[:maxSlugChars]
		if i := strings.LastIndexByte(out, '-'); i > 0 {
			out = out[:i]
		}
		out = strings.Trim(out, "-")
	}
	if out == "" {
		out = "room"
	}
	return out
}
