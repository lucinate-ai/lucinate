package rooms

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// RoomPrefs is the client-side state lucinate keeps per room. None of it
// lives on the gateway: the hosted-room protocol owns the transcript and the
// roster, but how a client displays that room and where it steers messages
// is a local choice, so it is stored locally (and survives a restart).
type RoomPrefs struct {
	// Mode is the routing mode (see RoutingMode). Empty means broadcast.
	Mode string `json:"mode,omitempty"`
	// Moderator is the handle every un-addressed message goes to while
	// Mode is "moderator".
	Moderator string `json:"moderator,omitempty"`
	// RoundRobinIndex is where the round-robin cursor stands.
	RoundRobinIndex int `json:"round_robin_index,omitempty"`
	// Colours overrides the default header colour per handle.
	Colours map[string]string `json:"colours,omitempty"`
	// CompactKeep is how many trailing messages /compact leaves verbatim.
	// Zero means the default.
	CompactKeep int `json:"compact_keep,omitempty"`
	// Compaction is the room's live compaction, if it has one.
	Compaction *Compaction `json:"compaction,omitempty"`
}

// PrefsStore is the on-disk collection of per-room preferences.
type PrefsStore struct {
	Rooms map[string]RoomPrefs `json:"rooms"`
}

// DefaultPrefsPath is <lucinate data dir>/rooms-prefs.json.
//
// It is deliberately not the presets file: a preset is a room definition the
// user authored, while these are view settings — one bad write must not cost
// the user their predefined rooms.
func DefaultPrefsPath() (string, error) {
	dir, err := config.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "rooms-prefs.json"), nil
}

// LoadPrefs reads the store. As with presets, a missing or malformed file is
// an empty store: bad preferences must never stop the rooms view opening.
func LoadPrefs(path string) (PrefsStore, error) {
	data, err := prefsRead(path)
	if err != nil {
		return PrefsStore{}, err
	}
	if data == nil {
		return PrefsStore{}, nil
	}
	var store PrefsStore
	if err := json.Unmarshal(data, &store); err != nil {
		return PrefsStore{}, nil
	}
	return store, nil
}

// SavePrefs writes the store 0700/0600, like the rest of lucinate's state.
func SavePrefs(path string, store PrefsStore) error {
	return prefsWrite(path, store)
}

// prefsRead reads a state file, reporting (nil, nil) when it is simply not
// there — a first run is not an error.
func prefsRead(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

// prefsWrite writes JSON state with the same 0700 directory / 0600 file
// permissions every other lucinate state file uses.
func prefsWrite(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// Get returns a room's preferences, or the zero value when it has none.
func (s *PrefsStore) Get(roomID string) RoomPrefs {
	if s == nil || s.Rooms == nil {
		return RoomPrefs{}
	}
	return s.Rooms[roomID]
}

// Set stores a room's preferences.
func (s *PrefsStore) Set(roomID string, p RoomPrefs) {
	if s.Rooms == nil {
		s.Rooms = map[string]RoomPrefs{}
	}
	s.Rooms[roomID] = p
}

// Update mutates a room's preferences in place, creating the map and the
// entry on first use so callers never have to nil-check.
func (s *PrefsStore) Update(roomID string, mutate func(*RoomPrefs)) {
	p := s.Get(roomID)
	mutate(&p)
	s.Set(roomID, p)
}

// RoutingMode is the stored mode, with broadcasts for anything unparseable —
// a hand-edited file must not lock the room into a mode the code does not
// know.
func (p RoomPrefs) RoutingMode() RoutingMode {
	mode, err := ParseRoutingMode(p.Mode)
	if err != nil {
		return RouteBroadcast
	}
	return mode
}

// KeepLast is the compaction window, bounded so a typo cannot turn /compact
// into a no-op or into "summarise everything".
func (p RoomPrefs) KeepLast() int { return NormalizeKeepLast(p.CompactKeep) }

// CompactedAt is the compaction's age, zero when the room has none.
func (p RoomPrefs) CompactedAt() time.Time {
	if p.Compaction == nil {
		return time.Time{}
	}
	return time.Unix(int64(p.Compaction.CreatedAt), 0)
}

// summarisePrefs is used by the status line: it answers "what is this room
// doing with my messages?" without the caller re-deriving it.
func (p RoomPrefs) describe() string {
	switch p.RoutingMode() {
	case RouteModerator:
		if p.Moderator == "" {
			return "moderator routing (no moderator set — the first member takes it)"
		}
		return "moderator routing → @" + p.Moderator
	case RouteRoundRobin:
		return fmt.Sprintf("round-robin routing (cursor %d)", p.RoundRobinIndex)
	default:
		return "broadcast routing"
	}
}
