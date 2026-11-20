package rooms

import (
	"context"
	"fmt"
	"strings"
)

// Per-member model selection in a hosted room.
//
// ── What the protocol allows (verified against the gateway, not assumed) ─────
//
// A room member does not answer from the room's connection: the driver runs
// each turn on a hidden `room_plumbing` session living in the MEMBER's own
// profile (`source=bot_room`, `hidden=1`, title `Group: <room_id>`). The
// gateway ships `groups.member_model` for exactly one job — seat that member's
// turns on another model for THAT session:
//
//	{"room_id": …, "handle": … , "model": … , "provider": …?}
//	→ {room_id, handle, profile, session_id, model, provider, session_created}
//
// What the gateway writes is session state (`model_config.room_member_model` on
// the member's room session) and nothing else — the member profile's config is
// never touched, which is the whole point: `server._stored_session_runtime_
// overrides` deliberately drops stored models for room sessions (a stale
// provider pin once left room bots out of credit), so the seat is only honoured
// because it is marked as a deliberate per-room pick. It survives reconnects
// (the pick is on the session, not on this client's memory) and dies with the
// session, exactly like the dashboard's own "change model" on a chat.
//
// Older gateways do not have the method at all. The client therefore asks
// `groups.capabilities` first (SupportsMemberModel) and says so plainly rather
// than showing a change that never reached anyone.
const (
	// ModelCatalogueMethod is the catalogue call the chat backend already uses.
	ModelCatalogueMethod = "model.options"

	// MemberModelMethod is the room method that seats one member's model for
	// the room's session. It is not called blindly: the client asks
	// groups.capabilities first and reports the gateway's refusal when the
	// method is missing.
	MemberModelMethod = "groups.member_model"
)

// MemberModelSeat is the gateway's answer to a successful switch: which session
// the member's turns now run on, and with which model. `SessionCreated` is true
// when the member had not spoken in this room yet and the session was minted to
// carry the pick — the seat then applies to that member's very first turn.
type MemberModelSeat struct {
	RoomID         string `json:"room_id"`
	Handle         string `json:"handle"`
	Profile        string `json:"profile"`
	SessionID      string `json:"session_id"`
	Model          string `json:"model"`
	Provider       string `json:"provider"`
	SessionCreated bool   `json:"session_created"`
}

// SeatedLabel describes where the pick landed, for a status line.
func (s MemberModelSeat) SeatedLabel() string {
	where := "session " + shortSessionID(s.SessionID)
	if s.SessionCreated {
		where = "new " + where
	}
	if s.Provider != "" {
		return fmt.Sprintf("%s on %s (via %s)", s.Model, where, s.Provider)
	}
	return fmt.Sprintf("%s on %s", s.Model, where)
}

// shortSessionID keeps the readable head of a session id (`20260926_001507_1856e4`).
func shortSessionID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 24 {
		return id
	}
	return id[:24]
}

// ModelCatalogue is the gateway's model list plus the model currently in use.
type ModelCatalogue struct {
	Model   string   `json:"model"`
	Options []string `json:"options"`
}

// ModelCatalogue asks the gateway which models are selectable. A gateway that
// does not answer (or answers with an empty list) is not an error the caller
// must treat as fatal: the catalogue exists to *offer* choices, and an empty
// one simply means the user types the model name instead.
func (c *Client) ModelCatalogue(ctx context.Context) (ModelCatalogue, error) {
	var out ModelCatalogue
	if err := c.call(ctx, ModelCatalogueMethod, struct{}{}, &out); err != nil {
		return ModelCatalogue{}, err
	}
	return out, nil
}

// SupportsMemberModel reports whether this gateway can switch a member's model
// for the room's session, by looking the method up in its advertised
// capabilities. The bool is false (with a nil error) when the gateway answered
// and simply does not offer it — that is a fact about the gateway, not a
// failure of the call.
func (c *Client) SupportsMemberModel(ctx context.Context) (bool, error) {
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return false, err
	}
	for _, m := range caps.Methods {
		if m == MemberModelMethod {
			return true, nil
		}
	}
	return false, nil
}

// SetMemberModel asks the gateway to run that member's turns in the room's
// session with the given model. It returns the gateway's seat: the session the
// member will answer from and the model now seated on it.
//
// `provider` is optional and only needed when the same model name exists under
// several providers; the gateway resolves it from its own catalogue otherwise.
//
// A gateway without the method answers -32601; callers should check
// SupportsMemberModel first so the user gets "this gateway cannot do that"
// rather than a protocol error. A gateway that has the method but cannot serve
// the model refuses the call and the caller must roll its state back — the seat
// never reached the session.
func (c *Client) SetMemberModel(ctx context.Context, roomID, handle, model, provider string) (MemberModelSeat, error) {
	if strings.TrimSpace(roomID) == "" {
		return MemberModelSeat{}, fmt.Errorf("room id is required")
	}
	if strings.TrimSpace(handle) == "" {
		return MemberModelSeat{}, fmt.Errorf("member handle is required")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return MemberModelSeat{}, fmt.Errorf("model name is required")
	}
	params := map[string]any{"room_id": roomID, "handle": strings.TrimPrefix(handle, "@"), "model": model}
	if p := strings.TrimSpace(provider); p != "" {
		params["provider"] = p
	}
	var out MemberModelSeat
	if err := c.call(ctx, MemberModelMethod, params, &out); err != nil {
		return MemberModelSeat{}, err
	}
	if strings.TrimSpace(out.Model) == "" {
		// A gateway that answers without a seat is not a gateway whose answer the
		// UI may show as applied.
		return MemberModelSeat{}, fmt.Errorf("gateway accepted the switch but reported no model")
	}
	return out, nil
}

// MemberModel is the model a roster row reports, empty when the member runs the
// profile's own default.
func MemberModel(m Member) string {
	if m.ModelConfig == nil {
		return ""
	}
	if v, ok := m.ModelConfig["model"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// ModelLabel is what a roster row shows for that member.
func ModelLabel(m Member) string {
	if model := MemberModel(m); model != "" {
		return model
	}
	return "profile default"
}

// WithMemberModel returns a copy of the member carrying the given model, so a
// caller updating local state never mutates a roster it shares with the view.
func WithMemberModel(m Member, model string) Member {
	cfg := make(map[string]any, len(m.ModelConfig)+1)
	for k, v := range m.ModelConfig {
		cfg[k] = v
	}
	cfg["model"] = strings.TrimSpace(model)
	m.ModelConfig = cfg
	return m
}

// ── the /model command ───────────────────────────────────────────────────────

// ModelCommand is a parsed `/model` invocation.
type ModelCommand struct {
	// Member is the member the command addresses, nil when the user asked for
	// the member picker.
	Member *Member
	// Model is the requested model, empty when the user wants the picker.
	Model string
}

// ParseModelCommand reads the four accepted shapes:
//
//	/model                    → pick a member, then a model
//	/model @handle             → pick a model for that member
//	/model @handle <name>      → switch directly
//	/model @handle             → also reports the member's current model, which
//	                             is why the picker opens on it
//
// Anything else is a user error, and says which part was wrong: a bare model
// name without a handle, a handle nobody in the roster has, or an empty model
// name.
func ParseModelCommand(args []string, members []Member) (ModelCommand, error) {
	if len(args) == 0 {
		return ModelCommand{}, nil
	}
	raw := strings.TrimSpace(args[0])
	if raw == "" {
		return ModelCommand{}, nil
	}
	if !strings.HasPrefix(raw, "@") {
		return ModelCommand{}, fmt.Errorf(
			"/model needs a member: /model @handle [name] — %q is not a handle", raw)
	}
	handle := strings.TrimPrefix(raw, "@")
	member := FindMember(members, handle)
	if member == nil {
		return ModelCommand{}, fmt.Errorf("no member %q in this room — the roster is %s", raw, rosterHandles(members))
	}
	cmd := ModelCommand{Member: member}
	if len(args) == 1 {
		return cmd, nil
	}
	model := strings.TrimSpace(strings.Join(args[1:], " "))
	if model == "" {
		return ModelCommand{}, fmt.Errorf("the model name is empty — /model %s <name> or /model %s to pick from the list", raw, raw)
	}
	cmd.Model = model
	return cmd, nil
}

// rosterHandles renders the roster for an error message.
func rosterHandles(members []Member) string {
	if len(members) == 0 {
		return "empty"
	}
	handles := make([]string, 0, len(members))
	for _, m := range members {
		handles = append(handles, "@"+memberHandle(m))
	}
	return strings.Join(handles, " ")
}

// FilterModels narrows a catalogue by a case-insensitive subsequence match, the
// same "type a few letters anywhere in the name" behaviour the chat's model
// picker uses (its fuzzy filter). An empty query keeps everything.
func FilterModels(options []string, query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		out := make([]string, len(options))
		copy(out, options)
		return out
	}
	out := make([]string, 0, len(options))
	for _, opt := range options {
		if fuzzyMatch(strings.ToLower(opt), query) {
			out = append(out, opt)
		}
	}
	return out
}

// fuzzyMatch reports whether every rune of query appears in s, in order.
func fuzzyMatch(s, query string) bool {
	if query == "" {
		return true
	}
	q := []rune(query)
	i := 0
	for _, r := range s {
		if r == q[i] {
			i++
			if i == len(q) {
				return true
			}
		}
	}
	return false
}
