package hermes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a3tai/openclaw-go/protocol"
	"github.com/gorilla/websocket"

	"github.com/lucinate-ai/lucinate/internal/backend"
)

// fakeGateway is an in-process tui_gateway: it enforces the ?token=
// query at the upgrade (403 on mismatch, like the real dashboard),
// pushes gateway.ready on accept, answers registered RPC methods, and
// records every observed call. Unregistered methods get an empty
// result so Connect's best-effort metadata calls never block.
type fakeGateway struct {
	t     *testing.T
	token string
	srv   *httptest.Server

	writeMu sync.Mutex
	conn    *websocket.Conn

	handlersMu sync.Mutex
	handlers   map[string]func(id uint64, params json.RawMessage)

	calls chan gatewayCall
}

type gatewayCall struct {
	Method string
	Params json.RawMessage
}

func newFakeGateway(t *testing.T, token string) *fakeGateway {
	t.Helper()
	g := &fakeGateway{
		t:        t,
		token:    token,
		handlers: map[string]func(uint64, json.RawMessage){},
		calls:    make(chan gatewayCall, 64),
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.token != "" && r.URL.Query().Get("token") != g.token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		g.writeMu.Lock()
		g.conn = conn
		g.writeMu.Unlock()
		g.write(map[string]any{"jsonrpc": "2.0", "method": "event",
			"params": map[string]any{"type": "gateway.ready", "payload": map[string]any{}}})
		g.serve(conn)
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGateway) serve(conn *websocket.Conn) {
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var req struct {
			ID     uint64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(data, &req) != nil || req.Method == "" {
			continue
		}
		select {
		case g.calls <- gatewayCall{Method: req.Method, Params: req.Params}:
		default:
		}
		g.handlersMu.Lock()
		h := g.handlers[req.Method]
		g.handlersMu.Unlock()
		if h != nil {
			h(req.ID, req.Params)
			continue
		}
		g.respond(req.ID, map[string]any{})
	}
}

func (g *fakeGateway) on(method string, h func(id uint64, params json.RawMessage)) {
	g.handlersMu.Lock()
	g.handlers[method] = h
	g.handlersMu.Unlock()
}

func (g *fakeGateway) write(v any) {
	g.writeMu.Lock()
	defer g.writeMu.Unlock()
	if g.conn != nil {
		_ = g.conn.WriteJSON(v)
	}
}

func (g *fakeGateway) respond(id uint64, result any) {
	g.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (g *fakeGateway) push(params any) {
	g.write(map[string]any{"jsonrpc": "2.0", "method": "event", "params": params})
}

// waitCall blocks until the gateway has observed a call to method.
func (g *fakeGateway) waitCall(method string) gatewayCall {
	g.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case c := <-g.calls:
			if c.Method == method {
				return c
			}
		case <-deadline:
			g.t.Fatalf("timed out waiting for call %s", method)
			return gatewayCall{}
		}
	}
}

func newTestBackend(t *testing.T, g *fakeGateway, token string) *Backend {
	t.Helper()
	b, err := New(Options{
		ConnectionID:   "test-conn",
		BaseURL:        g.srv.URL,
		APIKey:         token,
		ConnectTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

func connectBackend(t *testing.T, b *Backend) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
}

// waitChatEvent reads events until a chat event in one of the wanted
// states arrives.
func waitChatEvent(t *testing.T, b *Backend, states ...string) protocol.ChatEvent {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-b.Events():
			if ev.EventName != protocol.EventChat {
				continue
			}
			var ce protocol.ChatEvent
			if json.Unmarshal(ev.Payload, &ce) != nil {
				continue
			}
			for _, s := range states {
				if ce.State == s {
					return ce
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for chat event %v", states)
		}
	}
}

const testSessionResult = `{"session_id": "live1", "stored_session_id": "20260719_000000_stored", "message_count": 0, "messages": [], "info": {"model": "openai/gpt-4o-mini", "profile_name": "default"}}`

func onCreateSession(g *fakeGateway) {
	g.on("session.create", func(id uint64, _ json.RawMessage) {
		g.respond(id, json.RawMessage(testSessionResult))
	})
}

// Connect performs the handshake, loads metadata, and the synthetic
// agent carries the gateway's active model.
func TestConnect_HandshakeAndAgentSnapshot(t *testing.T) {
	g := newFakeGateway(t, "tok")
	g.on("model.options", func(id uint64, _ json.RawMessage) {
		g.respond(id, map[string]any{"model": "openai/gpt-4o-mini", "provider": "auto"})
	})
	b := newTestBackend(t, g, "tok")
	connectBackend(t, b)

	agents, err := b.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents.Agents) != 1 || agents.Agents[0].ID != syntheticAgentID {
		t.Fatalf("agents = %+v", agents.Agents)
	}
	if agents.Agents[0].Model == nil || agents.Agents[0].Model.Primary != "openai/gpt-4o-mini" {
		t.Fatalf("model not populated: %+v", agents.Agents[0])
	}
}

// A rejected upgrade (bad token → HTTP 403) maps to the canonical
// api-key-required error so the connecting view opens the token modal.
func TestConnect_BadTokenRoutesToAuthModal(t *testing.T) {
	g := newFakeGateway(t, "correct")
	b := newTestBackend(t, g, "wrong")
	err := b.Connect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "api key required") {
		t.Fatalf("want api key required error, got %v", err)
	}
}

// StoreAPIKey updates the token used by the next dial — the
// auth-modal recovery path.
func TestStoreAPIKey_NextConnectUsesNewToken(t *testing.T) {
	g := newFakeGateway(t, "correct")
	b := newTestBackend(t, g, "wrong")
	if err := b.Connect(context.Background()); err == nil {
		t.Fatal("precondition: bad token should fail")
	}
	if err := b.StoreAPIKey("correct"); err != nil {
		t.Fatalf("StoreAPIKey: %v", err)
	}
	connectBackend(t, b)
}

// Legacy URLs (port 8642 / a /v1 path) fail fast with the migration
// hint — no network round-trip needed.
func TestConnect_LegacyEndpointHint(t *testing.T) {
	for _, base := range []string{"http://127.0.0.1:8642", "http://localhost:9119/v1"} {
		b, err := New(Options{ConnectionID: "c", BaseURL: base, ConnectTimeout: time.Second})
		if err != nil {
			t.Fatalf("New(%s): %v", base, err)
		}
		err = b.Connect(context.Background())
		if err == nil || !strings.Contains(err.Error(), "legacy Hermes API server") {
			t.Fatalf("Connect(%s): want legacy hint, got %v", base, err)
		}
		_ = b.Close()
	}
}

// CreateSession returns the live handle; SessionsList maps stored-id
// entries into the session-browser shape.
func TestSessions_CreateAndList(t *testing.T) {
	g := newFakeGateway(t, "tok")
	onCreateSession(g)
	g.on("session.list", func(id uint64, _ json.RawMessage) {
		g.respond(id, json.RawMessage(`{"sessions":[
			{"id":"20260719_000000_stored","title":"Greetings","preview":"hello","started_at":1784425825.3,"message_count":2,"source":"tui"}]}`))
	})
	b := newTestBackend(t, g, "tok")
	connectBackend(t, b)

	key, err := b.CreateSession(context.Background(), syntheticAgentID, "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if key != "live1" {
		t.Fatalf("session key = %q, want live1", key)
	}

	raw, err := b.SessionsList(context.Background(), syntheticAgentID)
	if err != nil {
		t.Fatalf("SessionsList: %v", err)
	}
	var list struct {
		Sessions []struct {
			Key         string `json:"key"`
			Title       string `json:"title"`
			LastMessage string `json:"lastMessage"`
			UpdatedAt   int64  `json:"updatedAt"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list.Sessions) != 1 {
		t.Fatalf("sessions = %+v", list.Sessions)
	}
	s := list.Sessions[0]
	if s.Key != "20260719_000000_stored" || s.Title != "Greetings" || s.LastMessage != "hello" || s.UpdatedAt == 0 {
		t.Fatalf("mapped entry = %+v", s)
	}
}

// SessionDelete addresses the stored id, not the live handle.
func TestSessionDelete_UsesStoredID(t *testing.T) {
	g := newFakeGateway(t, "tok")
	onCreateSession(g)
	b := newTestBackend(t, g, "tok")
	connectBackend(t, b)

	key, _ := b.CreateSession(context.Background(), syntheticAgentID, "")
	if err := b.SessionDelete(context.Background(), key); err != nil {
		t.Fatalf("SessionDelete: %v", err)
	}
	call := g.waitCall("session.delete")
	var p struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(call.Params, &p)
	if p.SessionID != "20260719_000000_stored" {
		t.Fatalf("session.delete id = %q, want stored id", p.SessionID)
	}
}

// ChatSend streams: deltas accumulate, the final carries the inline
// usage, and the first turn gets the skills preamble exactly once.
func TestChatSend_StreamsWithSkillsPreamble(t *testing.T) {
	g := newFakeGateway(t, "tok")
	onCreateSession(g)
	var submittedMu sync.Mutex
	var submitted []string
	g.on("prompt.submit", func(id uint64, params json.RawMessage) {
		var p struct {
			SessionID string `json:"session_id"`
			Text      string `json:"text"`
		}
		_ = json.Unmarshal(params, &p)
		submittedMu.Lock()
		submitted = append(submitted, p.Text)
		submittedMu.Unlock()
		g.respond(id, map[string]any{"status": "streaming"})
		g.push(map[string]any{"type": "message.start", "session_id": p.SessionID})
		g.push(map[string]any{"type": "message.delta", "session_id": p.SessionID, "payload": map[string]any{"text": "hel"}})
		g.push(map[string]any{"type": "message.delta", "session_id": p.SessionID, "payload": map[string]any{"text": "lo"}})
		g.push(map[string]any{"type": "message.complete", "session_id": p.SessionID,
			"payload": map[string]any{"text": "hello", "status": "complete",
				"usage": map[string]any{"total": 42, "context_max": 65536}}})
	})
	b := newTestBackend(t, g, "tok")
	connectBackend(t, b)
	key, _ := b.CreateSession(context.Background(), syntheticAgentID, "")

	skills := []backend.SkillCatalogEntry{{Name: "commit-message", Description: "conventional commits"}}
	res, err := b.ChatSend(context.Background(), key, backend.ChatSendParams{Message: "hi", Skills: skills})
	if err != nil {
		t.Fatalf("ChatSend: %v", err)
	}
	if res.RunID == "" {
		t.Fatal("empty run id")
	}

	delta := waitChatEvent(t, b, "delta")
	if delta.RunID != res.RunID {
		t.Fatalf("delta run id = %q, want %q", delta.RunID, res.RunID)
	}
	final := waitChatEvent(t, b, "final")
	if got := backend.ExtractChatText(final.Message); got != "hello" {
		t.Fatalf("final text = %q", got)
	}
	if !strings.Contains(string(final.Usage), `"total":42`) &&
		!strings.Contains(string(final.Usage), `"total": 42`) {
		t.Fatalf("usage not attached: %s", final.Usage)
	}

	// Second turn: no preamble again.
	if _, err := b.ChatSend(context.Background(), key, backend.ChatSendParams{Message: "again", Skills: skills}); err != nil {
		t.Fatalf("second ChatSend: %v", err)
	}
	waitChatEvent(t, b, "final")

	submittedMu.Lock()
	defer submittedMu.Unlock()
	if len(submitted) != 2 {
		t.Fatalf("submitted %d prompts", len(submitted))
	}
	if !strings.HasPrefix(submitted[0], "System: ") || !strings.Contains(submitted[0], "commit-message") || !strings.HasSuffix(submitted[0], "hi") {
		t.Fatalf("turn 1 preamble wrong: %q", submitted[0])
	}
	if submitted[1] != "again" {
		t.Fatalf("turn 2 should carry no preamble: %q", submitted[1])
	}
}

// ChatAbort calls session.interrupt; the interrupted message.complete
// surfaces as the aborted event.
func TestChatAbort_SurfacesAborted(t *testing.T) {
	g := newFakeGateway(t, "tok")
	onCreateSession(g)
	g.on("session.interrupt", func(id uint64, params json.RawMessage) {
		var p struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(params, &p)
		g.respond(id, map[string]any{"status": "interrupted"})
		g.push(map[string]any{"type": "message.complete", "session_id": p.SessionID,
			"payload": map[string]any{"text": "Operation interrupted", "status": "interrupted"}})
	})
	b := newTestBackend(t, g, "tok")
	connectBackend(t, b)
	key, _ := b.CreateSession(context.Background(), syntheticAgentID, "")

	if err := b.ChatAbort(context.Background(), key, "run-x"); err != nil {
		t.Fatalf("ChatAbort: %v", err)
	}
	waitChatEvent(t, b, "aborted")
}

// An interactive ask is auto-declined via the paired respond RPC and a
// visible System: notice rides the streaming text.
func TestInteractiveAsk_AutoDeclined(t *testing.T) {
	g := newFakeGateway(t, "tok")
	onCreateSession(g)
	g.on("prompt.submit", func(id uint64, params json.RawMessage) {
		var p struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(params, &p)
		g.respond(id, map[string]any{"status": "streaming"})
		g.push(map[string]any{"type": "message.start", "session_id": p.SessionID})
		g.push(map[string]any{"type": "clarify.request", "session_id": p.SessionID,
			"payload": map[string]any{"question": "Which file?", "request_id": "req9"}})
	})
	b := newTestBackend(t, g, "tok")
	connectBackend(t, b)
	key, _ := b.CreateSession(context.Background(), syntheticAgentID, "")
	if _, err := b.ChatSend(context.Background(), key, backend.ChatSendParams{Message: "go"}); err != nil {
		t.Fatalf("ChatSend: %v", err)
	}

	delta := waitChatEvent(t, b, "delta")
	if got := backend.ExtractChatText(delta.Message); !strings.Contains(got, "System: ") || !strings.Contains(got, "clarify") {
		t.Fatalf("decline notice missing: %q", got)
	}

	call := g.waitCall("clarify.respond")
	var p struct {
		SessionID string `json:"session_id"`
		RequestID string `json:"request_id"`
	}
	_ = json.Unmarshal(call.Params, &p)
	if p.RequestID != "req9" || p.SessionID != "live1" {
		t.Fatalf("clarify.respond params = %s", call.Params)
	}
}

// ChatHistory maps the gateway transcript into the TUI shape,
// tolerating string and block content.
func TestChatHistory_MapsTranscript(t *testing.T) {
	g := newFakeGateway(t, "tok")
	onCreateSession(g)
	g.on("session.history", func(id uint64, _ json.RawMessage) {
		g.respond(id, json.RawMessage(`{"count":2,"messages":[
			{"role":"user","content":"hi there"},
			{"role":"assistant","content":[{"type":"text","text":"hello!"}]},
			{"role":"system","content":"ignored"}]}`))
	})
	b := newTestBackend(t, g, "tok")
	connectBackend(t, b)
	key, _ := b.CreateSession(context.Background(), syntheticAgentID, "")

	raw, err := b.ChatHistory(context.Background(), key, 50)
	if err != nil {
		t.Fatalf("ChatHistory: %v", err)
	}
	var out struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("messages = %+v", out.Messages)
	}
	if out.Messages[0].Role != "user" || out.Messages[0].Content[0].Text != "hi there" {
		t.Fatalf("user turn = %+v", out.Messages[0])
	}
	if out.Messages[1].Role != "assistant" || out.Messages[1].Content[0].Text != "hello!" {
		t.Fatalf("assistant turn = %+v", out.Messages[1])
	}
}

// Usage and compact resolve the live session and hit the gateway.
func TestUsageAndCompact(t *testing.T) {
	g := newFakeGateway(t, "tok")
	onCreateSession(g)
	g.on("session.usage", func(id uint64, _ json.RawMessage) {
		g.respond(id, json.RawMessage(`{"calls":1,"input":10,"output":5,"total":15}`))
	})
	b := newTestBackend(t, g, "tok")
	connectBackend(t, b)
	key, _ := b.CreateSession(context.Background(), syntheticAgentID, "")

	raw, err := b.SessionUsage(context.Background(), key)
	if err != nil {
		t.Fatalf("SessionUsage: %v", err)
	}
	if !strings.Contains(string(raw), `"total":15`) {
		t.Fatalf("usage = %s", raw)
	}
	if err := b.SessionCompact(context.Background(), key); err != nil {
		t.Fatalf("SessionCompact: %v", err)
	}
	g.waitCall("session.compress")
}

// Capabilities pin the phase-1 surface.
func TestCapabilities_PhaseOne(t *testing.T) {
	b, _ := New(Options{ConnectionID: "c"})
	caps := b.Capabilities()
	if !caps.GatewayStatus || !caps.SessionUsage || !caps.SessionCompact {
		t.Fatalf("status/usage/compact must be on: %+v", caps)
	}
	if caps.RemoteExec || caps.Thinking || caps.Cron || caps.AgentManagement {
		t.Fatalf("exec/thinking/cron/agent-mgmt must be off: %+v", caps)
	}
	if caps.AuthRecovery != backend.AuthRecoveryAPIKey {
		t.Fatalf("auth recovery = %v", caps.AuthRecovery)
	}
}

// Status reports the gateway summary and the active stored session.
func TestStatus_ReportsActiveSession(t *testing.T) {
	g := newFakeGateway(t, "tok")
	onCreateSession(g)
	g.on("model.options", func(id uint64, _ json.RawMessage) {
		g.respond(id, map[string]any{"model": "openai/gpt-4o-mini"})
	})
	b := newTestBackend(t, g, "tok")
	connectBackend(t, b)
	if _, err := b.CreateSession(context.Background(), syntheticAgentID, ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	status, err := b.Status(context.Background(), syntheticAgentID, "live1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Type != "hermes" || status.Auth != "gateway token" || status.DefaultModel != "openai/gpt-4o-mini" {
		t.Fatalf("status = %+v", status)
	}
	if status.Thread == nil || status.Thread.LastResponseID != "20260719_000000_stored" {
		t.Fatalf("active session missing: %+v", status.Thread)
	}
}

// CreateAgent / DeleteAgent stay rejected.
func TestAgentManagementRejected(t *testing.T) {
	b, _ := New(Options{ConnectionID: "c"})
	if err := b.CreateAgent(context.Background(), backend.CreateAgentParams{}); err == nil {
		t.Fatal("CreateAgent should reject")
	}
	if err := b.DeleteAgent(context.Background(), backend.DeleteAgentParams{}); err == nil {
		t.Fatal("DeleteAgent should reject")
	}
}

// New applies defaults and derives the /api/ws URL.
func TestNew_Defaults(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("ConnectionID must be required")
	}
	b, err := New(Options{ConnectionID: "c"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if b.opts.BaseURL != DefaultBaseURL {
		t.Fatalf("BaseURL default = %q", b.opts.BaseURL)
	}
	wsURL, err := b.wsURL()
	if err != nil {
		t.Fatalf("wsURL: %v", err)
	}
	if wsURL != "ws://localhost:9119/api/ws" {
		t.Fatalf("wsURL = %q", wsURL)
	}
}
