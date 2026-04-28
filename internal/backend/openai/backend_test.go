package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
)

// newBackend wires a Backend at a temp HOME pointing at the given test
// server.
func newBackend(t *testing.T, srv *httptest.Server) *Backend {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	b, err := New(Options{
		ConnectionID: "conn-test",
		BaseURL:      srv.URL + "/v1",
		HTTPClient:   srv.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

// drainEvent reads a single event off the channel with a timeout so a
// stuck stream surfaces as a test failure rather than hanging.
func drainEvent(t *testing.T, ch <-chan protocol.Event) protocol.Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
		return protocol.Event{}
	}
}

func parseChat(t *testing.T, ev protocol.Event) protocol.ChatEvent {
	t.Helper()
	if ev.EventName != protocol.EventChat {
		t.Fatalf("unexpected event name: %s", ev.EventName)
	}
	var ce protocol.ChatEvent
	if err := json.Unmarshal(ev.Payload, &ce); err != nil {
		t.Fatalf("decode chat event: %v", err)
	}
	return ce
}

func TestBackend_ConnectAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no key", http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := newBackend(t, srv)
	err := b.Connect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "api key required") {
		t.Fatalf("expected api key required error, got %v", err)
	}
}

func TestBackend_ConnectSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-test"}]}`))
	}))
	defer srv.Close()

	b := newBackend(t, srv)
	if err := b.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
}

func TestBackend_ListAgents_EmptyStore(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	b := newBackend(t, srv)
	result, err := b.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(result.Agents) != 0 {
		t.Errorf("expected empty store, got %d agents", len(result.Agents))
	}
}

func TestBackend_CreateAndListAgent(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	b := newBackend(t, srv)
	err := b.CreateAgent(context.Background(), backend.CreateAgentParams{
		Name:     "researcher",
		Identity: "I research things.",
		Soul:     "Methodical and thorough.",
		Model:    "gpt-test",
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	result, err := b.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(result.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(result.Agents))
	}
	if result.Agents[0].ID != "researcher" {
		t.Errorf("agent ID = %q", result.Agents[0].ID)
	}
	if result.Agents[0].Model == nil || result.Agents[0].Model.Primary != "gpt-test" {
		t.Errorf("agent model: %+v", result.Agents[0].Model)
	}
	if result.DefaultID != "researcher" {
		t.Errorf("DefaultID = %q", result.DefaultID)
	}
}

func TestBackend_CreateAgentUsesDefaultsWhenBlank(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	b := newBackend(t, srv)
	if err := b.CreateAgent(context.Background(), backend.CreateAgentParams{Name: "blank"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if got := b.store.LoadIdentity("blank"); got != DefaultIdentity {
		t.Errorf("identity not defaulted: %q", got)
	}
	if got := b.store.LoadSoul("blank"); got != DefaultSoul {
		t.Errorf("soul not defaulted: %q", got)
	}
}

func TestBackend_CreateSessionRequiresAgent(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	b := newBackend(t, srv)
	if _, err := b.CreateSession(context.Background(), "ghost", ""); err == nil {
		t.Error("expected error for missing agent")
	}
}

func TestBackend_ChatSendStreamsDeltasAndFinal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		writeChunk := func(content string) {
			payload := fmt.Sprintf(`{"choices":[{"delta":{"content":%q}}]}`, content)
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}
		writeChunk("Hel")
		writeChunk("lo")
		writeChunk(", world!")
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	b := newBackend(t, srv)
	if err := b.CreateAgent(context.Background(), backend.CreateAgentParams{Name: "a", Model: "m"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if _, err := b.ChatSend(context.Background(), "a", "hi", "idem"); err != nil {
		t.Fatalf("ChatSend: %v", err)
	}

	// Three deltas + one final.
	for i := 0; i < 3; i++ {
		ev := parseChat(t, drainEvent(t, b.events))
		if ev.State != "delta" {
			t.Fatalf("expected delta, got %s", ev.State)
		}
	}
	final := parseChat(t, drainEvent(t, b.events))
	if final.State != "final" {
		t.Fatalf("expected final, got %s", final.State)
	}

	// History persisted: user message + assistant response.
	msgs, err := b.store.LoadHistory("a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "hi" {
		t.Errorf("user message: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "Hello, world!" {
		t.Errorf("assistant message: %+v", msgs[1])
	}
}

func TestBackend_ChatSendErrorEmitsErrorEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	b := newBackend(t, srv)
	_ = b.CreateAgent(context.Background(), backend.CreateAgentParams{Name: "a", Model: "m"})
	if _, err := b.ChatSend(context.Background(), "a", "hi", "idem"); err != nil {
		t.Fatal(err)
	}
	ev := parseChat(t, drainEvent(t, b.events))
	if ev.State != "error" {
		t.Fatalf("expected error event, got %s", ev.State)
	}
	if !strings.Contains(ev.ErrorMessage, "429") {
		t.Errorf("expected status code in message, got %q", ev.ErrorMessage)
	}
}

func TestBackend_ChatSendUnauthorisedEmitsAPIKeyMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no key", http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := newBackend(t, srv)
	_ = b.CreateAgent(context.Background(), backend.CreateAgentParams{Name: "a", Model: "m"})
	_, _ = b.ChatSend(context.Background(), "a", "hi", "idem")
	ev := parseChat(t, drainEvent(t, b.events))
	if !strings.Contains(ev.ErrorMessage, "api key required") {
		t.Errorf("expected api-key error message, got %q", ev.ErrorMessage)
	}
}

func TestBackend_ChatSendRequiresModel(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	b := newBackend(t, srv)
	_ = b.CreateAgent(context.Background(), backend.CreateAgentParams{Name: "a"})
	if _, err := b.ChatSend(context.Background(), "a", "hi", "idem"); err == nil {
		t.Error("expected error for missing model")
	}
}

func TestBackend_ChatHistoryRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	b := newBackend(t, srv)
	_ = b.CreateAgent(context.Background(), backend.CreateAgentParams{Name: "a", Model: "m"})
	_ = b.store.AppendMessage("a", Message{Role: "user", Content: "ping"})
	_ = b.store.AppendMessage("a", Message{Role: "assistant", Content: "pong"})

	raw, err := b.ChatHistory(context.Background(), "a", 0)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got.Messages))
	}
	if got.Messages[1].Role != "assistant" || got.Messages[1].Content[0].Text != "pong" {
		t.Errorf("history shape: %+v", got.Messages)
	}
}

func TestBackend_ModelsList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"alpha"},{"id":"beta"}]}`)
	}))
	defer srv.Close()

	b := newBackend(t, srv)
	result, err := b.ModelsList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Models) != 2 || result.Models[0].ID != "alpha" {
		t.Errorf("models: %+v", result.Models)
	}
}

func TestBackend_SessionPatchModelPersists(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	b := newBackend(t, srv)
	_ = b.CreateAgent(context.Background(), backend.CreateAgentParams{Name: "a", Model: "old"})
	if err := b.SessionPatchModel(context.Background(), "a", "new"); err != nil {
		t.Fatal(err)
	}
	meta, _ := b.store.LoadMeta("a")
	if meta.Model != "new" {
		t.Errorf("model not persisted: %q", meta.Model)
	}
}

func TestBackend_Capabilities(t *testing.T) {
	b := &Backend{}
	caps := b.Capabilities()
	if caps.GatewayStatus || caps.RemoteExec || caps.SessionCompact || caps.Thinking || caps.SessionUsage {
		t.Errorf("expected no optional caps, got %+v", caps)
	}
	if caps.AuthRecovery != backend.AuthRecoveryAPIKey {
		t.Errorf("expected APIKey auth recovery, got %v", caps.AuthRecovery)
	}
}

func TestBackend_StoreAPIKeyUsedInSubsequentRequests(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()

	b := newBackend(t, srv)
	if err := b.StoreAPIKey("secret-123"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ModelsList(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer secret-123" {
		t.Errorf("Authorization = %q", got)
	}
}
