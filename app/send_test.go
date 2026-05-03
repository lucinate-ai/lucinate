package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/client"
)

// sendFakeBackend is a minimal backend.Backend used to drive the
// app.Send unit tests. It records the wire-level calls Send performs
// (CreateSession's session key, ChatSend's message + idempotency key)
// and lets each test push the chat event sequence the assistant turn
// should "respond" with through the events channel.
type sendFakeBackend struct {
	events chan protocol.Event

	listResult *protocol.AgentsListResult
	listErr    error
	connectErr error

	createKey     string
	createAgentID string
	createReturn  string
	createErr     error
	createCalls   int

	sentParams       backend.ChatSendParams
	sentSessionKey   string
	sentRunID        string
	chatSendErr      error
	chatSendDispatch func(ev <-chan protocol.Event, sessionKey, runID string)

	closed bool
	mu     sync.Mutex
}

func newSendFakeBackend() *sendFakeBackend {
	return &sendFakeBackend{
		events:       make(chan protocol.Event, 8),
		listResult:   &protocol.AgentsListResult{},
		createReturn: "",
		sentRunID:    "run-1",
	}
}

func (f *sendFakeBackend) Connect(ctx context.Context) error { return f.connectErr }
func (f *sendFakeBackend) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}
func (f *sendFakeBackend) wasClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}
func (f *sendFakeBackend) Events() <-chan protocol.Event { return f.events }
func (f *sendFakeBackend) Supervise(ctx context.Context, notify func(client.ConnState)) {
	<-ctx.Done()
}
func (f *sendFakeBackend) ListAgents(ctx context.Context) (*protocol.AgentsListResult, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listResult, nil
}
func (f *sendFakeBackend) CreateAgent(ctx context.Context, params backend.CreateAgentParams) error {
	return nil
}
func (f *sendFakeBackend) DeleteAgent(ctx context.Context, params backend.DeleteAgentParams) error {
	return nil
}
func (f *sendFakeBackend) SessionsList(ctx context.Context, agentID string) (json.RawMessage, error) {
	return json.RawMessage(`{"sessions":[]}`), nil
}
func (f *sendFakeBackend) CreateSession(ctx context.Context, agentID, key string) (string, error) {
	f.createCalls++
	f.createAgentID = agentID
	f.createKey = key
	if f.createErr != nil {
		return "", f.createErr
	}
	if f.createReturn != "" {
		return f.createReturn, nil
	}
	return key, nil
}
func (f *sendFakeBackend) SessionDelete(ctx context.Context, sessionKey string) error { return nil }
func (f *sendFakeBackend) ChatSend(ctx context.Context, sessionKey string, params backend.ChatSendParams) (*protocol.ChatSendResult, error) {
	f.sentParams = params
	f.sentSessionKey = sessionKey
	if f.chatSendErr != nil {
		return nil, f.chatSendErr
	}
	if f.chatSendDispatch != nil {
		go f.chatSendDispatch(f.events, sessionKey, f.sentRunID)
	}
	return &protocol.ChatSendResult{RunID: f.sentRunID}, nil
}
func (f *sendFakeBackend) ChatAbort(ctx context.Context, sessionKey, runID string) error { return nil }
func (f *sendFakeBackend) ChatHistory(ctx context.Context, sessionKey string, limit int) (json.RawMessage, error) {
	return json.RawMessage(`{"messages":[]}`), nil
}
func (f *sendFakeBackend) ModelsList(ctx context.Context) (*protocol.ModelsListResult, error) {
	return &protocol.ModelsListResult{}, nil
}
func (f *sendFakeBackend) SessionPatchModel(ctx context.Context, sessionKey, modelID string) error {
	return nil
}
func (f *sendFakeBackend) Capabilities() backend.Capabilities { return backend.Capabilities{} }

var _ backend.Backend = (*sendFakeBackend)(nil)

// emitChatEvent pushes a chat event onto the fake's events channel.
// Tests use this either inline or from a chatSendDispatch hook to
// simulate the assistant streaming.
func emitChatEvent(ch chan<- protocol.Event, sessionKey, runID, state string, message any, errMsg string) {
	chatEv := protocol.ChatEvent{
		RunID:        runID,
		SessionKey:   sessionKey,
		State:        state,
		ErrorMessage: errMsg,
	}
	if message != nil {
		raw, _ := json.Marshal(message)
		chatEv.Message = raw
	}
	payload, _ := json.Marshal(chatEv)
	ch <- protocol.Event{EventName: protocol.EventChat, Payload: payload}
}

// makeSendStore returns a one-connection store with the supplied
// connection added under the given name. Send tests don't exercise
// persistence so the unsaved store is fine.
func makeSendStore(name string) (*Connections, *Connection) {
	conn := Connection{
		ID:   "conn-id",
		Name: name,
		Type: ConnTypeOpenAI,
		URL:  "http://localhost:1234/v1",
	}
	return &Connections{Connections: []Connection{conn}}, &conn
}

// fixedFactory returns a BackendFactory that ignores its argument and
// always hands back the supplied backend. Lets tests inject the fake
// backend without going through DefaultBackendFactory's auth wiring.
func fixedFactory(b backend.Backend) BackendFactory {
	return func(*Connection) (backend.Backend, error) { return b, nil }
}

func TestSend_WaitsForFinalAndWritesReply(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fb := newSendFakeBackend()
	fb.listResult = &protocol.AgentsListResult{
		DefaultID: "main",
		MainKey:   "main",
		Agents:    []protocol.AgentSummary{{ID: "main", Name: "Primary"}},
	}
	fb.chatSendDispatch = func(ch <-chan protocol.Event, sessionKey, runID string) {
		// Re-cast — emitChatEvent expects send-only.
		out := fb.events
		emitChatEvent(out, sessionKey, runID, "delta", "Hello", "")
		emitChatEvent(out, sessionKey, runID, "final", map[string]any{
			"role": "assistant",
			"content": []map[string]any{
				{"type": "text", "text": "Hello, world!"},
			},
		}, "")
	}

	store, _ := makeSendStore("openai-local")
	var buf bytes.Buffer
	err := Send(context.Background(), SendOptions{
		Connection:       "openai-local",
		Agent:            "main",
		Message:          "hi",
		Out:              &buf,
		BackendFactory:   fixedFactory(fb),
		ConnectionsStore: store,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := buf.String(); got != "Hello, world!\n" {
		t.Errorf("output = %q, want %q", got, "Hello, world!\n")
	}
	if fb.sentParams.Message != "hi" {
		t.Errorf("ChatSend message = %q, want %q", fb.sentParams.Message, "hi")
	}
	if !strings.HasPrefix(fb.sentParams.IdempotencyKey, "lucinate-send-") {
		t.Errorf("IdempotencyKey = %q, want lucinate-send- prefix", fb.sentParams.IdempotencyKey)
	}
	if !fb.wasClosed() {
		t.Error("backend was not closed after Send returned")
	}
}

func TestSend_DefaultsToMainKeyForDefaultAgent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fb := newSendFakeBackend()
	fb.listResult = &protocol.AgentsListResult{
		DefaultID: "default-agent",
		MainKey:   "main-session-canonical",
		Agents: []protocol.AgentSummary{
			{ID: "default-agent", Name: "Default"},
			{ID: "other-agent", Name: "Other"},
		},
	}
	fb.chatSendDispatch = func(ch <-chan protocol.Event, sessionKey, runID string) {
		emitChatEvent(fb.events, sessionKey, runID, "final", map[string]any{
			"content": []map[string]any{{"type": "text", "text": "ok"}},
		}, "")
	}

	store, _ := makeSendStore("c")
	if err := Send(context.Background(), SendOptions{
		Connection: "c", Agent: "default-agent", Message: "hi",
		Out: &bytes.Buffer{}, BackendFactory: fixedFactory(fb), ConnectionsStore: store,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if fb.createKey != "main-session-canonical" {
		t.Errorf("CreateSession key = %q, want %q (MainKey for default agent)", fb.createKey, "main-session-canonical")
	}
	if fb.createAgentID != "default-agent" {
		t.Errorf("CreateSession agent = %q, want default-agent", fb.createAgentID)
	}
}

func TestSend_DefaultsToLiteralMainForOtherAgents(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fb := newSendFakeBackend()
	fb.listResult = &protocol.AgentsListResult{
		DefaultID: "default-agent",
		MainKey:   "main-session-canonical",
		Agents: []protocol.AgentSummary{
			{ID: "default-agent", Name: "Default"},
			{ID: "other-agent", Name: "Other"},
		},
	}
	fb.chatSendDispatch = func(ch <-chan protocol.Event, sessionKey, runID string) {
		emitChatEvent(fb.events, sessionKey, runID, "final", map[string]any{
			"content": []map[string]any{{"type": "text", "text": "ok"}},
		}, "")
	}

	store, _ := makeSendStore("c")
	if err := Send(context.Background(), SendOptions{
		Connection: "c", Agent: "other-agent", Message: "hi",
		Out: &bytes.Buffer{}, BackendFactory: fixedFactory(fb), ConnectionsStore: store,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if fb.createKey != "main" {
		t.Errorf("CreateSession key = %q, want %q (literal main for non-default agent)", fb.createKey, "main")
	}
}

func TestSend_HonoursExplicitSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fb := newSendFakeBackend()
	fb.listResult = &protocol.AgentsListResult{
		DefaultID: "main",
		MainKey:   "should-be-ignored",
		Agents:    []protocol.AgentSummary{{ID: "main", Name: "Primary"}},
	}
	fb.chatSendDispatch = func(ch <-chan protocol.Event, sessionKey, runID string) {
		emitChatEvent(fb.events, sessionKey, runID, "final", map[string]any{
			"content": []map[string]any{{"type": "text", "text": "ok"}},
		}, "")
	}

	store, _ := makeSendStore("c")
	if err := Send(context.Background(), SendOptions{
		Connection: "c", Agent: "main", Session: "scratch-2030", Message: "hi",
		Out: &bytes.Buffer{}, BackendFactory: fixedFactory(fb), ConnectionsStore: store,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if fb.createKey != "scratch-2030" {
		t.Errorf("CreateSession key = %q, want %q", fb.createKey, "scratch-2030")
	}
}

func TestSend_DetachReturnsAfterAck(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fb := newSendFakeBackend()
	fb.listResult = &protocol.AgentsListResult{
		Agents: []protocol.AgentSummary{{ID: "main", Name: "Primary"}},
	}
	// Crucially, do NOT push any final event — Send must not wait for one.

	store, _ := makeSendStore("c")
	done := make(chan error, 1)
	go func() {
		done <- Send(context.Background(), SendOptions{
			Connection: "c", Agent: "main", Message: "kick off",
			Detach: true, Out: &bytes.Buffer{}, BackendFactory: fixedFactory(fb), ConnectionsStore: store,
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Send (detach): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Send (detach) did not return — it appears to be waiting for a reply")
	}
	if fb.sentParams.Message != "kick off" {
		t.Errorf("ChatSend message = %q, want %q", fb.sentParams.Message, "kick off")
	}
}

func TestSend_ReturnsErrorOnChatErrorEvent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fb := newSendFakeBackend()
	fb.listResult = &protocol.AgentsListResult{
		Agents: []protocol.AgentSummary{{ID: "main", Name: "Primary"}},
	}
	fb.chatSendDispatch = func(ch <-chan protocol.Event, sessionKey, runID string) {
		emitChatEvent(fb.events, sessionKey, runID, "error", nil, "model is on fire")
	}

	store, _ := makeSendStore("c")
	err := Send(context.Background(), SendOptions{
		Connection: "c", Agent: "main", Message: "hi",
		Out: &bytes.Buffer{}, BackendFactory: fixedFactory(fb), ConnectionsStore: store,
	})
	if err == nil || !strings.Contains(err.Error(), "model is on fire") {
		t.Fatalf("expected chat-error to surface, got %v", err)
	}
}

func TestSend_ReturnsErrorOnChatAborted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fb := newSendFakeBackend()
	fb.listResult = &protocol.AgentsListResult{
		Agents: []protocol.AgentSummary{{ID: "main", Name: "Primary"}},
	}
	fb.chatSendDispatch = func(ch <-chan protocol.Event, sessionKey, runID string) {
		emitChatEvent(fb.events, sessionKey, runID, "aborted", nil, "")
	}

	store, _ := makeSendStore("c")
	err := Send(context.Background(), SendOptions{
		Connection: "c", Agent: "main", Message: "hi",
		Out: &bytes.Buffer{}, BackendFactory: fixedFactory(fb), ConnectionsStore: store,
	})
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("expected aborted error, got %v", err)
	}
}

func TestSend_MatchesConnectionByNameCaseInsensitive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fb := newSendFakeBackend()
	fb.listResult = &protocol.AgentsListResult{
		Agents: []protocol.AgentSummary{{ID: "main", Name: "Primary"}},
	}
	fb.chatSendDispatch = func(ch <-chan protocol.Event, sessionKey, runID string) {
		emitChatEvent(fb.events, sessionKey, runID, "final", map[string]any{
			"content": []map[string]any{{"type": "text", "text": "ok"}},
		}, "")
	}

	store, _ := makeSendStore("My-Connection")
	if err := Send(context.Background(), SendOptions{
		Connection: "MY-CONNECTION", Agent: "primary", Message: "hi",
		Out: &bytes.Buffer{}, BackendFactory: fixedFactory(fb), ConnectionsStore: store,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestSend_MatchesAgentByNameCaseInsensitive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fb := newSendFakeBackend()
	fb.listResult = &protocol.AgentsListResult{
		Agents: []protocol.AgentSummary{{ID: "agent-xyz", Name: "Primary"}},
	}
	fb.chatSendDispatch = func(ch <-chan protocol.Event, sessionKey, runID string) {
		emitChatEvent(fb.events, sessionKey, runID, "final", map[string]any{
			"content": []map[string]any{{"type": "text", "text": "ok"}},
		}, "")
	}

	store, _ := makeSendStore("c")
	if err := Send(context.Background(), SendOptions{
		Connection: "c", Agent: "primary", Message: "hi",
		Out: &bytes.Buffer{}, BackendFactory: fixedFactory(fb), ConnectionsStore: store,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if fb.createAgentID != "agent-xyz" {
		t.Errorf("CreateSession agentID = %q, want %q (resolved by name)", fb.createAgentID, "agent-xyz")
	}
}

func TestSend_ConnectionNotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	store, _ := makeSendStore("real")
	err := Send(context.Background(), SendOptions{
		Connection: "ghost", Agent: "main", Message: "hi",
		Out: &bytes.Buffer{}, BackendFactory: fixedFactory(newSendFakeBackend()), ConnectionsStore: store,
	})
	if err == nil || !strings.Contains(err.Error(), "connection") {
		t.Fatalf("expected connection-not-found error, got %v", err)
	}
}

func TestSend_AgentNotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fb := newSendFakeBackend()
	fb.listResult = &protocol.AgentsListResult{
		Agents: []protocol.AgentSummary{{ID: "real", Name: "Real"}},
	}

	store, _ := makeSendStore("c")
	err := Send(context.Background(), SendOptions{
		Connection: "c", Agent: "ghost", Message: "hi",
		Out: &bytes.Buffer{}, BackendFactory: fixedFactory(fb), ConnectionsStore: store,
	})
	if err == nil || !strings.Contains(err.Error(), "agent") {
		t.Fatalf("expected agent-not-found error, got %v", err)
	}
}

func TestSend_MissingArgumentsRejected(t *testing.T) {
	cases := []struct {
		name string
		opts SendOptions
		want string
	}{
		{"connection", SendOptions{Agent: "a", Message: "m"}, "connection"},
		{"agent", SendOptions{Connection: "c", Message: "m"}, "agent"},
		{"message", SendOptions{Connection: "c", Agent: "a"}, "message"},
		{"whitespace-message", SendOptions{Connection: "c", Agent: "a", Message: "   \n"}, "message"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Send(context.Background(), tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestSend_BackendErrorsBubbleUp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	t.Run("connect", func(t *testing.T) {
		fb := newSendFakeBackend()
		fb.connectErr = errors.New("dial timeout")
		store, _ := makeSendStore("c")
		err := Send(context.Background(), SendOptions{
			Connection: "c", Agent: "main", Message: "hi",
			Out: &bytes.Buffer{}, BackendFactory: fixedFactory(fb), ConnectionsStore: store,
		})
		if err == nil || !strings.Contains(err.Error(), "dial timeout") {
			t.Fatalf("expected connect error, got %v", err)
		}
		if !fb.wasClosed() {
			t.Error("backend was not closed after Connect failed")
		}
	})

	t.Run("list-agents", func(t *testing.T) {
		fb := newSendFakeBackend()
		fb.listErr = errors.New("rpc broken")
		store, _ := makeSendStore("c")
		err := Send(context.Background(), SendOptions{
			Connection: "c", Agent: "main", Message: "hi",
			Out: &bytes.Buffer{}, BackendFactory: fixedFactory(fb), ConnectionsStore: store,
		})
		if err == nil || !strings.Contains(err.Error(), "rpc broken") {
			t.Fatalf("expected list-agents error, got %v", err)
		}
	})

	t.Run("chat-send", func(t *testing.T) {
		fb := newSendFakeBackend()
		fb.listResult = &protocol.AgentsListResult{
			Agents: []protocol.AgentSummary{{ID: "main", Name: "Primary"}},
		}
		fb.chatSendErr = errors.New("rate limited")
		store, _ := makeSendStore("c")
		err := Send(context.Background(), SendOptions{
			Connection: "c", Agent: "main", Message: "hi",
			Out: &bytes.Buffer{}, BackendFactory: fixedFactory(fb), ConnectionsStore: store,
		})
		if err == nil || !strings.Contains(err.Error(), "rate limited") {
			t.Fatalf("expected chat-send error, got %v", err)
		}
	})
}

func TestSend_FallsBackToDeltaTextWhenFinalEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fb := newSendFakeBackend()
	fb.listResult = &protocol.AgentsListResult{
		Agents: []protocol.AgentSummary{{ID: "main", Name: "Primary"}},
	}
	fb.chatSendDispatch = func(ch <-chan protocol.Event, sessionKey, runID string) {
		// Stream cumulative deltas, then close with an ack-shaped final
		// (no content) — same shape some backends emit on completion.
		emitChatEvent(fb.events, sessionKey, runID, "delta", "Streaming", "")
		emitChatEvent(fb.events, sessionKey, runID, "delta", "Streaming reply body", "")
		emitChatEvent(fb.events, sessionKey, runID, "final", map[string]any{
			"role":    "assistant",
			"content": []map[string]any{},
		}, "")
	}

	store, _ := makeSendStore("c")
	var buf bytes.Buffer
	if err := Send(context.Background(), SendOptions{
		Connection: "c", Agent: "main", Message: "hi",
		Out: &buf, BackendFactory: fixedFactory(fb), ConnectionsStore: store,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := buf.String(); got != "Streaming reply body\n" {
		t.Errorf("output = %q, want %q", got, "Streaming reply body\n")
	}
}

func TestSend_IgnoresEventsForOtherSessions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fb := newSendFakeBackend()
	fb.listResult = &protocol.AgentsListResult{
		DefaultID: "main",
		MainKey:   "main",
		Agents:    []protocol.AgentSummary{{ID: "main", Name: "Primary"}},
	}
	fb.createReturn = "session-A"
	fb.chatSendDispatch = func(ch <-chan protocol.Event, sessionKey, runID string) {
		// Inject a final event for a different session before the
		// real one — Send must filter it out.
		emitChatEvent(fb.events, "session-B", runID, "final", map[string]any{
			"content": []map[string]any{{"type": "text", "text": "wrong session"}},
		}, "")
		emitChatEvent(fb.events, sessionKey, runID, "final", map[string]any{
			"content": []map[string]any{{"type": "text", "text": "right session"}},
		}, "")
	}

	store, _ := makeSendStore("c")
	var buf bytes.Buffer
	if err := Send(context.Background(), SendOptions{
		Connection: "c", Agent: "main", Message: "hi",
		Out: &buf, BackendFactory: fixedFactory(fb), ConnectionsStore: store,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "right session" {
		t.Errorf("output = %q, want %q", got, "right session")
	}
}

func TestSend_ContextCancellationReturns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fb := newSendFakeBackend()
	fb.listResult = &protocol.AgentsListResult{
		Agents: []protocol.AgentSummary{{ID: "main", Name: "Primary"}},
	}
	// chatSendDispatch never emits a final, so the only way out is ctx.

	store, _ := makeSendStore("c")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Send(ctx, SendOptions{
			Connection: "c", Agent: "main", Message: "hi",
			Out: &bytes.Buffer{}, BackendFactory: fixedFactory(fb), ConnectionsStore: store,
		})
	}()
	// Give Send a moment to reach the wait, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Send did not return after context cancellation")
	}
}
