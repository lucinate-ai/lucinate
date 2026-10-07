// Package rooms is lucinate's client for Hermes hosted rooms — the
// "Bot Mode" group chats the desktop app, the dashboard and `hermes
// --tui` all drive over the same gateway WebSocket.
//
// A room is a durable, append-only transcript hosted by one gateway
// (`groups.*` RPC). Its roster is 2–6 Hermes profiles local to that
// gateway; each is a "bot" that answers when the room receives a user
// message. Mention routing is server-side and matches group-chat
// intuition: `@handle` addresses one member, `@all`/`@everyone` or no
// mention at all addresses the whole roster.
//
// The package is deliberately transport-thin: it reuses
// internal/backend/hermes/rpc for the JSON-RPC-over-WebSocket plumbing
// and adds only the room vocabulary, roster rules, preset store and
// local-profile discovery the TUI needs.
package rooms

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Roster bounds, mirroring the gateway's own validation
// (gateway/hosted_room_discussion.py: MIN/MAX_DISCUSSION_MEMBERS).
const (
	MinMembers = 2
	MaxMembers = 6
)

// reservedHandles are mention handles the gateway refuses to hand to a
// member, because they already mean "the whole roster".
var reservedHandles = map[string]struct{}{"all": {}, "everyone": {}}

// Member is one roster row: a Hermes profile on the serving gateway
// plus the handle other members use to mention it.
type Member struct {
	MemberID    string `json:"member_id"`
	Profile     string `json:"profile"`
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name,omitempty"`
	// ModelConfig is the per-member model configuration the gateway stores on
	// the roster row. It is read-only from a client's point of view: a room's
	// roster (this JSON included) is frozen once the room exists, which is why
	// ModelLabel reports "profile default" when it is absent.
	ModelConfig map[string]any `json:"model_config,omitempty"`
}

// Room is one hosted room row (gateway/hosted_rooms.py::_room_from_row).
type Room struct {
	RoomID             string   `json:"room_id"`
	Name               string   `json:"name"`
	Members            []Member `json:"members"`
	AuthorityGatewayID string   `json:"authority_gateway_id"`
	AuthorityEpoch     int      `json:"authority_epoch"`
	Revision           int      `json:"revision"`
	CreatedAt          float64  `json:"created_at"`
	UpdatedAt          float64  `json:"updated_at"`
	DisbandedAt        *float64 `json:"disbanded_at,omitempty"`
	LatestSeq          *int     `json:"latest_seq,omitempty"`
}

// Actor identifies who appended an event. The gateway owns it; clients
// never supply it.
type Actor struct {
	Kind         string `json:"kind"`
	ID           string `json:"id"`
	DisplayName  string `json:"display_name,omitempty"`
	Profile      string `json:"profile,omitempty"`
	ConnectionID string `json:"connection_id,omitempty"`
}

// Event is one transcript row. Kind is one of message.user,
// message.member, room.activity, turn.* or a control kind such as
// room.created / room.members_changed.
type Event struct {
	RoomID    string         `json:"room_id"`
	Seq       int            `json:"seq"`
	EventID   string         `json:"event_id"`
	Kind      string         `json:"kind"`
	Actor     Actor          `json:"actor"`
	Payload   map[string]any `json:"payload"`
	CreatedAt float64        `json:"created_at"`
}

// Authority is the fenced-ownership stamp on a room log page.
type Authority struct {
	GatewayID string `json:"gateway_id"`
	Epoch     int    `json:"epoch"`
}

// LogPage is a bounded delta of the room log after a cursor.
type LogPage struct {
	Events    []Event   `json:"events"`
	Cursor    int       `json:"cursor"`
	LatestSeq int       `json:"latest_seq"`
	HasMore   bool      `json:"has_more"`
	Authority Authority `json:"authority"`
}

// Text returns an event's human text, empty for events without one.
func (e Event) Text() string {
	s, _ := e.Payload["text"].(string)
	return s
}

// Speaker returns the label to render an event under.
func (e Event) Speaker() string {
	if e.Actor.DisplayName != "" {
		return e.Actor.DisplayName
	}
	if e.Actor.Profile != "" {
		return e.Actor.Profile
	}
	if e.Actor.ID != "" {
		return e.Actor.ID
	}
	return e.Actor.Kind
}

// MemberIndex reports the roster position a turn event refers to, or
// -1 when the event carries none.
func (e Event) MemberIndex() int {
	switch v := e.Payload["member_index"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return -1
}

// RoomLinkStatus reports whether this gateway can link a room to a peer
// gateway (cross-machine rooms). Local rooms work with it disabled.
type RoomLinkStatus struct {
	Enabled bool   `json:"enabled"`
	Profile string `json:"profile,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// Capabilities describes the hosted-room protocol this gateway speaks.
type Capabilities struct {
	ProtocolVersion    int            `json:"protocol_version"`
	Driver             bool           `json:"driver"`
	PersistentProcess  bool           `json:"persistent_process"`
	AuthorityGatewayID string         `json:"authority_gateway_id"`
	RoomLink           RoomLinkStatus `json:"room_link"`
	Features           []string       `json:"features"`
	Methods            []string       `json:"methods"`
	MaxLogLimit        int            `json:"max_log_limit"`
}

// DriverStatus is the live driver view for one room.
type DriverStatus struct {
	Running        bool             `json:"running"`
	Working        bool             `json:"working"`
	Blocked        bool             `json:"blocked"`
	Counts         map[string]int   `json:"counts"`
	PendingActions []map[string]any `json:"pending_actions"`
}

// StateResult is one room plus its live driver status.
type StateResult struct {
	Room         Room          `json:"room"`
	DriverStatus *DriverStatus `json:"driver_status,omitempty"`
}

// ValidateRoster applies the gateway's own roster rules before a roster
// ever reaches the wire, so a bad roster fails with a readable local
// error instead of a 4001 from the far side.
func ValidateRoster(members []Member) error {
	if len(members) < MinMembers || len(members) > MaxMembers {
		return fmt.Errorf("a room needs between %d and %d members, got %d",
			MinMembers, MaxMembers, len(members))
	}
	profiles := make(map[string]struct{}, len(members))
	handles := make(map[string]struct{}, len(members))
	ids := make(map[string]struct{}, len(members))
	for i, m := range members {
		switch {
		case strings.TrimSpace(m.MemberID) == "":
			return fmt.Errorf("member %d: member id is required", i)
		case strings.TrimSpace(m.Profile) == "":
			return fmt.Errorf("member %d: profile is required", i)
		case strings.TrimSpace(m.Handle) == "":
			return fmt.Errorf("member %d: handle is required", i)
		}
		profile := strings.ToLower(strings.TrimSpace(m.Profile))
		handle := strings.ToLower(strings.TrimSpace(m.Handle))
		id := strings.ToLower(strings.TrimSpace(m.MemberID))
		if _, ok := reservedHandles[handle]; ok {
			return fmt.Errorf("member %d: handle %q is reserved (all/everyone address the whole roster)", i, m.Handle)
		}
		if _, ok := profiles[profile]; ok {
			return fmt.Errorf("member %d: profiles must be unique (profile %q appears twice)", i, m.Profile)
		}
		if _, ok := handles[handle]; ok {
			return fmt.Errorf("member %d: handles must be unique (handle %q appears twice)", i, m.Handle)
		}
		if _, ok := ids[id]; ok {
			return fmt.Errorf("member %d: member ids must be unique (id %q appears twice)", i, m.MemberID)
		}
		profiles[profile] = struct{}{}
		handles[handle] = struct{}{}
		ids[id] = struct{}{}
	}
	return nil
}

// GatewayWSURL derives the gateway's WebSocket endpoint from the same
// HTTP base URL a Hermes connection stores, matching the derivation the
// Hermes chat backend uses so one connection configures both.
func GatewayWSURL(baseURL, token string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("invalid base URL %q: %w", baseURL, err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "ws", "wss":
		// already a websocket scheme
	case "":
		return "", fmt.Errorf("invalid base URL %q: missing scheme", baseURL)
	default:
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	u.Path = "/api/ws"
	u.RawQuery = ""
	if token != "" {
		q := u.Query()
		q.Set("token", token)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// HandleFor derives the default mention handle for a profile name.
//
// The gateway's mention grammar is @([A-Za-z0-9][A-Za-z0-9._:-]*) — the
// first character must be alphanumeric, so a handle is lowercased,
// invalid characters become '-', and leading/trailing punctuation is
// trimmed (otherwise "__weird__" would produce a handle nothing can
// mention).
func HandleFor(profile string) string {
	h := strings.ToLower(strings.TrimSpace(profile))
	var b strings.Builder
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.', r == ':':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	h = strings.TrimFunc(b.String(), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	if h == "" {
		h = "bot"
	}
	if _, reserved := reservedHandles[h]; reserved {
		h = "bot-" + h
	}
	return h
}

// RosterFor builds a roster from profile names, deriving member ids and
// handles. It does not validate; call ValidateRoster for that.
func RosterFor(profiles []string) []Member {
	members := make([]Member, 0, len(profiles))
	for i, p := range profiles {
		p = strings.TrimSpace(p)
		members = append(members, Member{
			MemberID:    fmt.Sprintf("m%d", i+1),
			Profile:     p,
			Handle:      HandleFor(p),
			DisplayName: displayNameFor(p),
		})
	}
	return members
}

func displayNameFor(profile string) string {
	if profile == "" {
		return ""
	}
	if profile == "default" {
		return "Default"
	}
	return strings.ToUpper(profile[:1]) + profile[1:]
}

// maxNameParts bounds how many profile names a derived room name lists
// before it summarises the rest. Six names concatenated is unreadable.
const maxNameParts = 3

// DisplayNameFor derives a room name from its roster, so seating a room
// needs no typing: ["matt","kowal"] becomes "Matt+Kowal", and a longer
// roster collapses to "A+B+C (+3)".
func DisplayNameFor(profiles []string) string {
	names := make([]string, 0, len(profiles))
	for _, p := range profiles {
		if p = strings.TrimSpace(p); p != "" {
			names = append(names, displayNameFor(p))
		}
	}
	if len(names) == 0 {
		return "New room"
	}
	if len(names) > maxNameParts {
		extra := len(names) - maxNameParts
		names = append(names[:maxNameParts:maxNameParts], fmt.Sprintf("(+%d)", extra))
	}
	return strings.Join(names, "+")
}

// SortedProfiles returns the distinct profile names in a roster,
// sorted, for display.
func SortedProfiles(members []Member) []string {
	out := make([]string, 0, len(members))
	for _, m := range members {
		out = append(out, m.Profile)
	}
	sort.Strings(out)
	return out
}
