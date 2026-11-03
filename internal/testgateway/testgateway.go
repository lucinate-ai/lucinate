// Package testgateway is the shared fake hosted-room gateway the rooms and
// tui unit tests dial over a real WebSocket. It speaks the same JSON-RPC
// framing the production rooms.Client expects — one object per frame,
// {id, method, params} in, {jsonrpc, id, result} (or {jsonrpc, id, error})
// out — so a switch that "succeeds" in a test has really crossed the wire.
//
// The package deliberately does NOT import internal/rooms: several rooms
// tests live in package rooms itself, and importing rooms here would be an
// import cycle in test. The Member type below mirrors the roster row fields
// the tests inspect.
package testgateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Options tunes a Server. The zero value is a two-member room; use
// DefaultOptions unless a test needs a knob that does not exist yet.
type Options struct{}

// DefaultOptions returns the default server options.
func DefaultOptions() Options { return Options{} }

// Member is one roster row the fake gateway stores.
type Member struct {
	MemberID    string         `json:"member_id"`
	Profile     string         `json:"profile"`
	Handle      string         `json:"handle"`
	DisplayName string         `json:"display_name,omitempty"`
	ModelConfig map[string]any `json:"model_config,omitempty"`
}

// roomState is one hosted room.
type roomState struct {
	roomID    string
	name      string
	members   []Member
	disbanded bool
	revision  int
}

// SendScript overrides what the next groups.send triggers: instead of the
// default single delta, the server appends turn.started plus the first
// deliver fragments, then drops every socket — the "connection cut mid
// answer" path. It is one-shot.
type SendScript struct {
	handle    string
	deliver   int
	fragments []string
}

// ReplyWithDeltas scripts the next send to deliver deliver of the fragments
// for handle and then drop the connection.
func ReplyWithDeltas(handle string, deliver int, fragments ...string) SendScript {
	return SendScript{handle: handle, deliver: deliver, fragments: fragments}
}

// Server is the fake gateway.
type Server struct {
	mu     sync.Mutex
	t      *testing.T
	srv    *httptest.Server
	conns  []*websocket.Conn
	closed chan struct{}

	upgradeStatus   int
	silent          map[string]bool
	rejectSendCode  int
	rejectSendArmed bool

	memberModelSupported bool
	memberRejectCode     int
	memberRejectMsg      string
	memberRejectArmed    bool

	catalogueModel   string
	catalogueOptions []string
	catalogueSet     bool

	rooms   map[string]*roomState
	order   []string
	events  []map[string]any
	latest  int
	working bool

	sent       []string
	calls      []string
	createdIDs []string

	conflicts   map[string]string
	conflictAll string

	script   *SendScript
	sessions map[string]string

	memberModelCalls []string

	// v2 live state: who is answering, and what the room is waiting on.
	active       map[string]bool
	pending      map[string]map[string]any
	approveCalls []string
	retryCalls   []string
}

// New starts a fake gateway with the default two-member room ("sztab":
// matt + kowal) and registers its shutdown with t.
func New(t *testing.T, _ Options) *Server {
	t.Helper()
	s := &Server{
		t:                t,
		closed:           make(chan struct{}),
		silent:           make(map[string]bool),
		rooms:            make(map[string]*roomState),
		sessions:         make(map[string]string),
		conflicts:        make(map[string]string),
		active:           make(map[string]bool),
		pending:          make(map[string]map[string]any),
		catalogueModel:   "gpt-5",
		catalogueOptions: []string{"gpt-5", "gpt-5-mini", "claude-sonnet-4"},
	}
	s.rooms["sztab"] = &roomState{
		roomID: "sztab",
		name:   "Sztab",
		members: []Member{
			{MemberID: "m1", Profile: "matt", Handle: "matt", DisplayName: "Matt"},
			{MemberID: "m2", Profile: "kowal", Handle: "kowal", DisplayName: "Kowal"},
		},
		revision: 2,
	}
	s.order = append(s.order, "sztab")
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		status := s.upgradeStatus
		s.mu.Unlock()
		if status != 0 {
			http.Error(w, http.StatusText(status), status)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, conn)
		s.mu.Unlock()
		defer conn.Close()
		s.serve(conn)
	}))
	t.Cleanup(func() {
		close(s.closed)
		s.srv.Close()
	})
	return s
}

// URL returns the HTTP base URL clients dial via DialBaseURL.
func (s *Server) URL() string { return s.srv.URL }

// Configure arms the scripted mid-stream drop for the next groups.send.
func (s *Server) Configure(sc SendScript) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := sc
	s.script = &cp
}

// SetWorking sets the driver working flag reported by groups.state.
func (s *Server) SetWorking(w bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.working = w
}

// SetSilent makes the named method never answer, so the caller's timeout
// fires. ClearSilent re-enables it.
func (s *Server) SetSilent(method string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.silent[method] = true
}

// ClearSilent re-enables a silenced method.
func (s *Server) ClearSilent(method string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.silent, method)
}

// SetRejectSend arms the next groups.send to fail with the given JSON-RPC
// code and a "member turn rejected" message. One-shot.
func (s *Server) SetRejectSend(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejectSendCode = code
	s.rejectSendArmed = true
}

// SetUpgradeStatus makes the HTTP upgrade fail with the given status.
func (s *Server) SetUpgradeStatus(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upgradeStatus = code
}

// SetModelCatalogue sets the model.options answer: the current model plus
// the selectable options.
func (s *Server) SetModelCatalogue(current string, options ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.catalogueModel = current
	s.catalogueOptions = append([]string(nil), options...)
	s.catalogueSet = true
}

// SetMemberModelSupported controls whether groups.capabilities advertises
// groups.member_model.
func (s *Server) SetMemberModelSupported(supported bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memberModelSupported = supported
}

// SetMemberModelReject arms the next groups.member_model to fail with the
// given code and message. One-shot; a rejected seat changes nothing.
func (s *Server) SetMemberModelReject(code int, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memberRejectCode = code
	s.memberRejectMsg = msg
	s.memberRejectArmed = true
}

// SetRoomIDConflict makes groups.create fail with 4110 and msg for the
// listed room ids.
func (s *Server) SetRoomIDConflict(msg string, ids ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		s.conflicts[id] = msg
	}
}

// SetRoomIDConflictAll makes every groups.create fail with 4110 and msg.
func (s *Server) SetRoomIDConflictAll(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conflictAll = msg
}

// Sent returns every groups.send text in order.
func (s *Server) Sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sent...)
}

// Calls returns every method name received, in order.
func (s *Server) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// CreateCalls counts groups.create calls.
func (s *Server) CreateCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.calls {
		if c == "groups.create" {
			n++
		}
	}
	return n
}

// CreatedIDs returns every room id a groups.create asked for, in order.
func (s *Server) CreatedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.createdIDs...)
}

// MemberRow finds a roster row by handle across all rooms.
func (s *Server) MemberRow(handle string) (Member, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.order {
		for _, m := range s.rooms[id].members {
			if m.Handle == handle {
				cp := m
				return cp, true
			}
		}
	}
	return Member{}, false
}

// MemberModelCalls returns every applied seat as "handle=model".
func (s *Server) MemberModelCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.memberModelCalls...)
}

// SetMemberActivity marks the named handles as answering right now. The
// durable room log never carries text deltas on the real gateway, so this is
// the only live signal a client renders.
func (s *Server) SetMemberActivity(handles ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = make(map[string]bool, len(handles))
	for _, h := range handles {
		s.active[h] = true
	}
	s.working = len(handles) > 0
}

// SetPendingApproval stages one approval request for a handle, with the exact
// coordinates groups.approve fences on.
func (s *Server) SetPendingApproval(handle, requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[handle] = map[string]any{
		"kind": "approval", "member_id": s.memberIDLocked(handle),
		"task_id": "task-" + handle, "execution_generation": 1,
		"request_id": requestID, "approval": map[string]any{"choices": []string{"once", "deny"}},
	}
}

// SetPendingRetry stages one indeterminate task the user may retry.
func (s *Server) SetPendingRetry(handle string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[handle] = map[string]any{
		"kind": "retry", "member_id": s.memberIDLocked(handle),
		"task_id": "task-" + handle, "execution_generation": 1,
	}
}

// ApproveCalls returns every applied approval as "handle=choice".
func (s *Server) ApproveCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.approveCalls...)
}

// RetryCalls returns every retried task as "handle".
func (s *Server) RetryCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.retryCalls...)
}

// memberIDLocked resolves a handle to the roster member id.
func (s *Server) memberIDLocked(handle string) string {
	for _, id := range s.order {
		for _, m := range s.rooms[id].members {
			if m.Handle == handle {
				return m.MemberID
			}
		}
	}
	return ""
}

// CloseConns drops every live socket, which is what a gateway restart looks
// like from the client side. It returns how many sockets were dropped.
func (s *Server) CloseConns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.conns)
	for _, c := range s.conns {
		_ = c.Close()
	}
	s.conns = nil
	return n
}

func (s *Server) serve(conn *websocket.Conn) {
	for {
		var req struct {
			ID     uint64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := conn.ReadJSON(&req); err != nil {
			return
		}
		result, rpcErr, respond := s.handle(req.Method, req.Params)
		if !respond {
			return // silenced: hang up on this socket; the caller times out
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if rpcErr != nil {
			resp["error"] = rpcErr
		} else {
			resp["result"] = result
		}
		if err := conn.WriteJSON(resp); err != nil {
			return
		}
	}
}

type rpcErrJSON struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handle(method string, params json.RawMessage) (any, *rpcErrJSON, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, method)
	if s.silent[method] {
		s.mu.Unlock()
		<-s.closed
		s.mu.Lock()
		return nil, nil, false
	}
	switch method {
	case "groups.capabilities":
		methods := []string{"groups.log", "groups.send", "groups.state", "groups.create",
			"groups.list", "groups.rename", "groups.stop", "groups.disband", "groups.approve",
			"groups.retry", "groups.replicate", "groups.replica_state", "groups.promote",
			"groups.demote", "groups.peer.invite", "groups.peer.revoke", "groups.peer.register",
			"groups.capabilities", "model.options"}
		if s.memberModelSupported {
			methods = append(methods, "groups.member_model")
		}
		return map[string]any{
			"protocol_version": 2, "driver": true, "persistent_process": true,
			"authority_gateway_id": "fake",
			"room_link":            map[string]any{"enabled": true, "profile": "default"},
			"features": []string{
				"authority_epoch", "coordinator_fencing", "room_identity", "monotonic_log",
				"idempotent_send", "replayable_disband", "typed_events", "actor_identity",
				"log_replication", "authority_takeover",
			},
			"methods":       methods,
			"max_log_limit": 500,
		}, nil, true
	case "groups.list":
		rooms := []any{}
		for _, id := range s.order {
			r := s.rooms[id]
			if r.disbanded {
				continue
			}
			rooms = append(rooms, s.roomJSON(r))
		}
		return map[string]any{"rooms": rooms, "next_offset": nil}, nil, true
	case "groups.create":
		var p struct {
			RoomID  string   `json:"room_id"`
			Name    string   `json:"name"`
			Members []Member `json:"members"`
		}
		_ = json.Unmarshal(params, &p)
		s.createdIDs = append(s.createdIDs, p.RoomID)
		if msg, ok := s.conflicts[p.RoomID]; ok {
			return nil, &rpcErrJSON{Code: 4110, Message: msg}, true
		}
		if s.conflictAll != "" {
			return nil, &rpcErrJSON{Code: 4110, Message: s.conflictAll}, true
		}
		if r, ok := s.rooms[p.RoomID]; ok {
			return map[string]any{"room": s.roomJSON(r)}, nil, true
		}
		r := &roomState{roomID: p.RoomID, name: p.Name, members: append([]Member(nil), p.Members...), revision: 1}
		s.rooms[p.RoomID] = r
		s.order = append(s.order, p.RoomID)
		return map[string]any{"room": s.roomJSON(r)}, nil, true
	case "groups.state":
		var p struct {
			RoomID string `json:"room_id"`
		}
		_ = json.Unmarshal(params, &p)
		r := s.rooms[p.RoomID]
		if r == nil {
			r = s.rooms["sztab"]
		}
		activity := []any{}
		for _, id := range s.order {
			for _, m := range s.rooms[id].members {
				if s.active[m.Handle] {
					activity = append(activity, map[string]any{"member_id": m.MemberID, "status": "running"})
				}
			}
		}
		actions := []any{}
		for _, a := range s.pending {
			actions = append(actions, a)
		}
		return map[string]any{"room": s.roomJSON(r), "driver_status": map[string]any{
			"running": true, "working": s.working || len(activity) > 0, "blocked": len(actions) > 0,
			"counts": map[string]any{}, "member_activity": activity,
			"pending_actions": actions, "peer_routes": []any{},
		}}, nil, true
	case "groups.send":
		var p struct {
			RoomID  string         `json:"room_id"`
			EventID string         `json:"event_id"`
			Payload map[string]any `json:"payload"`
		}
		_ = json.Unmarshal(params, &p)
		if s.rejectSendArmed {
			s.rejectSendArmed = false
			return nil, &rpcErrJSON{Code: s.rejectSendCode, Message: "member turn rejected"}, true
		}
		text, _ := p.Payload["text"].(string)
		s.sent = append(s.sent, text)
		ev := s.appendLocked(p.RoomID, "message.user", "", text)
		if s.script != nil {
			sc := s.script
			s.script = nil
			handle := sc.handle
			s.appendLocked(p.RoomID, "turn.started", handle, "")
			n := sc.deliver
			if n > len(sc.fragments) {
				n = len(sc.fragments)
			}
			for _, f := range sc.fragments[:n] {
				s.appendLocked(p.RoomID, "message.member.delta", handle, f)
			}
			go func() {
				time.Sleep(100 * time.Millisecond)
				s.CloseConns()
			}()
		} else {
			s.appendLocked(p.RoomID, "turn.started", "matt", "")
			s.appendLocked(p.RoomID, "message.member.delta", "matt", "Pracuje nad tym")
			s.working = true
		}
		return map[string]any{"event": ev, "accepted": true, "driver_started": true}, nil, true
	case "groups.log":
		var p struct {
			RoomID   string `json:"room_id"`
			SinceSeq int    `json:"since_seq"`
			Limit    int    `json:"limit"`
		}
		_ = json.Unmarshal(params, &p)
		out := []any{}
		for _, ev := range s.events {
			if roomID, _ := ev["room_id"].(string); roomID != p.RoomID {
				continue
			}
			if seq, _ := ev["seq"].(int); seq > p.SinceSeq {
				out = append(out, ev)
			}
		}
		if out == nil {
			out = []any{}
		}
		return map[string]any{
			"events": out, "cursor": s.latest, "latest_seq": s.latest, "has_more": false,
			"authority": map[string]any{"gateway_id": "fake", "epoch": 1},
		}, nil, true
	case "groups.rename":
		var p struct {
			RoomID  string `json:"room_id"`
			EventID string `json:"event_id"`
			Name    string `json:"name"`
		}
		_ = json.Unmarshal(params, &p)
		if r := s.rooms[p.RoomID]; r != nil {
			r.name = p.Name
			return map[string]any{"room": s.roomJSON(r)}, nil, true
		}
		return map[string]any{"room": s.roomJSON(s.rooms["sztab"])}, nil, true
	case "groups.stop":
		return map[string]any{"cancelled": 1}, nil, true
	case "groups.disband":
		var p struct {
			RoomID string `json:"room_id"`
		}
		_ = json.Unmarshal(params, &p)
		if r := s.rooms[p.RoomID]; r != nil {
			r.disbanded = true
		}
		return map[string]any{"tombstone": map[string]any{"room_id": p.RoomID}}, nil, true
	case "groups.approve":
		var p struct {
			RoomID              string `json:"room_id"`
			MemberID            string `json:"member_id"`
			TaskID              string `json:"task_id"`
			ExecutionGeneration int    `json:"execution_generation"`
			RequestID           string `json:"request_id"`
			Choice              string `json:"choice"`
		}
		_ = json.Unmarshal(params, &p)
		var handle string
		var action map[string]any
		for h, a := range s.pending {
			if a["member_id"] == p.MemberID {
				handle, action = h, a
				break
			}
		}
		if action == nil || action["kind"] != "approval" {
			return nil, &rpcErrJSON{Code: 5119, Message: "room approval is no longer pending"}, true
		}
		if action["task_id"] != p.TaskID || action["request_id"] != p.RequestID ||
			int(action["execution_generation"].(int)) != p.ExecutionGeneration {
			return nil, &rpcErrJSON{Code: 5119, Message: "room approval is no longer pending"}, true
		}
		if p.Choice != "once" && p.Choice != "deny" {
			return nil, &rpcErrJSON{Code: 5119, Message: "room approval choice must be once or deny"}, true
		}
		delete(s.pending, handle)
		s.approveCalls = append(s.approveCalls, handle+"="+p.Choice)
		return map[string]any{"approved": true, "result": map[string]any{"choice": p.Choice}}, nil, true
	case "groups.retry":
		var p struct {
			RoomID              string `json:"room_id"`
			MemberID            string `json:"member_id"`
			TaskID              string `json:"task_id"`
			ExecutionGeneration int    `json:"execution_generation"`
		}
		_ = json.Unmarshal(params, &p)
		var handle string
		for h, a := range s.pending {
			if a["task_id"] == p.TaskID {
				handle, _ = h, a
				break
			}
		}
		if handle == "" {
			return nil, &rpcErrJSON{Code: 5118, Message: "room task is not retryable"}, true
		}
		delete(s.pending, handle)
		s.retryCalls = append(s.retryCalls, handle)
		return map[string]any{"retried": true, "task": map[string]any{
			"room_id": p.RoomID, "task_id": p.TaskID, "status": "retried",
			"execution_generation": p.ExecutionGeneration + 1,
		}}, nil, true
	case "model.options":
		opts := s.catalogueOptions
		if opts == nil {
			opts = []string{}
		}
		return map[string]any{"model": s.catalogueModel, "options": opts}, nil, true
	case "groups.member_model":
		var p struct {
			RoomID   string `json:"room_id"`
			Handle   string `json:"handle"`
			Model    string `json:"model"`
			Provider string `json:"provider"`
		}
		_ = json.Unmarshal(params, &p)
		if !s.memberModelSupported {
			return nil, &rpcErrJSON{Code: -32601, Message: "method not found: groups.member_model"}, true
		}
		if s.memberRejectArmed {
			s.memberRejectArmed = false
			return nil, &rpcErrJSON{Code: s.memberRejectCode, Message: s.memberRejectMsg}, true
		}
		var target *roomState
		var idx int
		found := false
		for _, id := range s.order {
			r := s.rooms[id]
			for i := range r.members {
				if r.members[i].Handle == p.Handle {
					target, idx, found = r, i, true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return nil, &rpcErrJSON{Code: 4004, Message: fmt.Sprintf("unknown member %q", p.Handle)}, true
		}
		m := &target.members[idx]
		if m.ModelConfig == nil {
			m.ModelConfig = map[string]any{}
		}
		m.ModelConfig["model"] = p.Model
		s.memberModelCalls = append(s.memberModelCalls, p.Handle+"="+p.Model)
		sess, ok := s.sessions[p.Handle]
		created := false
		if !ok {
			sess = fmt.Sprintf("sess-%s-01", p.Handle)
			s.sessions[p.Handle] = sess
			created = true
		}
		return map[string]any{
			"room_id": p.RoomID, "handle": p.Handle, "profile": m.Profile,
			"session_id": sess, "model": p.Model, "provider": p.Provider,
			"session_created": created,
		}, nil, true
	}
	return map[string]any{}, nil, true
}

func (s *Server) roomJSON(r *roomState) map[string]any {
	members := []any{}
	for _, m := range r.members {
		members = append(members, map[string]any{
			"member_id": m.MemberID, "profile": m.Profile, "handle": m.Handle,
			"display_name": m.DisplayName, "model_config": m.ModelConfig,
		})
	}
	return map[string]any{
		"room_id": r.roomID, "name": r.name, "revision": 2,
		"authority_gateway_id": "fake", "authority_epoch": 1,
		"created_at": 1.0, "updated_at": 2.0,
		"members": members, "latest_seq": s.latest,
	}
}

func (s *Server) appendLocked(roomID, kind, profile, text string) map[string]any {
	s.latest++
	payload := map[string]any{}
	if text != "" {
		payload["text"] = text
	}
	actor := map[string]any{"kind": "member", "id": "m1"}
	if profile != "" {
		actor["profile"] = profile
		actor["display_name"] = profile
	} else if kind == "message.user" {
		actor = map[string]any{"kind": "user"}
	}
	ev := map[string]any{
		"room_id": roomID, "seq": s.latest, "event_id": fmt.Sprintf("e%d", s.latest),
		"kind": kind, "actor": actor, "payload": payload,
		"created_at": float64(1700000000 + s.latest),
	}
	s.events = append(s.events, ev)
	if kind == "turn.settled" || kind == "message.member" {
		s.working = false
	}
	return ev
}
