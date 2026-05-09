package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestBackend_DeleteAgent_DestroyRemovesDirectory(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	b := newBackend(t, srv)
	if err := b.CreateAgent(context.Background(), backend.CreateAgentParams{
		Name: "tonuke", Identity: "id", Soul: "soul", Model: "m",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if err := b.DeleteAgent(context.Background(), backend.DeleteAgentParams{
		AgentID:     "tonuke",
		DeleteFiles: true,
	}); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}

	if _, err := os.Stat(b.store.AgentDir("tonuke")); !os.IsNotExist(err) {
		t.Errorf("agent dir should be removed: %v", err)
	}
	// And nothing should have been written to the archive — the
	// destructive path bypasses Archive entirely.
	if entries, _ := os.ReadDir(filepath.Join(b.store.Root(), ".archive")); len(entries) != 0 {
		t.Errorf("destructive delete should not archive, got %d entries", len(entries))
	}

	result, _ := b.ListAgents(context.Background())
	if len(result.Agents) != 0 {
		t.Errorf("agent should be gone from list, got %d", len(result.Agents))
	}
}

func TestBackend_DeleteAgent_KeepFilesArchives(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	b := newBackend(t, srv)
	if err := b.CreateAgent(context.Background(), backend.CreateAgentParams{
		Name: "toarchive", Identity: "id", Soul: "soul", Model: "m",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if err := b.DeleteAgent(context.Background(), backend.DeleteAgentParams{
		AgentID:     "toarchive",
		DeleteFiles: false,
	}); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}

	if _, err := os.Stat(b.store.AgentDir("toarchive")); !os.IsNotExist(err) {
		t.Errorf("original dir should be moved: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(b.store.Root(), ".archive"))
	if err != nil {
		t.Fatalf("read .archive: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 archived agent, got %d", len(entries))
	}

	result, _ := b.ListAgents(context.Background())
	if len(result.Agents) != 0 {
		t.Errorf("archived agent should not appear in list, got %d", len(result.Agents))
	}
}

func TestBackend_DeleteAgent_MissingAgent(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	b := newBackend(t, srv)
	err := b.DeleteAgent(context.Background(), backend.DeleteAgentParams{
		AgentID:     "ghost",
		DeleteFiles: true,
	})
	if err == nil {
		t.Error("expected error for missing agent")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got %v", err)
	}
}

func TestBackend_CreateAgentUsesDefaultsWhenBlank(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	b := newBackend(t, srv)
	if err := b.CreateAgent(context.Background(), backend.CreateAgentParams{Name: "blank"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if got := b.store.LoadIdentity("blank"); got != DefaultIdentity("blank") {
		t.Errorf("identity not defaulted with name: %q", got)
	}
	if !strings.Contains(b.store.LoadIdentity("blank"), "Name: blank") {
		t.Errorf("identity Name header should use the agent name, got:\n%s", b.store.LoadIdentity("blank"))
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

	if _, err := b.ChatSend(context.Background(), "a", backend.ChatSendParams{Message: "hi", IdempotencyKey: "idem"}); err != nil {
		t.Fatalf("ChatSend: %v", err)
	}

	// Three deltas + one final.
	for i := 0; i < 3; i++ {
		ev := parseChat(t, drainEvent(t, b.Events()))
		if ev.State != "delta" {
			t.Fatalf("expected delta, got %s", ev.State)
		}
	}
	final := parseChat(t, drainEvent(t, b.Events()))
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

// TestBackend_ChatSendSkipsMalformedSSEChunks pushes the streaming
// parser past three "weird" lines that an OpenAI-compatible server
// might emit:
//
//   - a non-`data: ` prefix line (e.g. an SSE comment or `event: …`)
//   - a `data: ` line with non-JSON body (truncated chunk)
//   - a `data: ` line with valid JSON but no `choices` (provider
//     keep-alives sometimes send `{"id":"..."}` without choices)
//
// All three should be skipped silently — the run completes with the
// content from the well-formed chunks intact, and the final event
// fires.
func TestBackend_ChatSendSkipsMalformedSSEChunks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		// Comment / event line — no `data: ` prefix.
		fmt.Fprint(w, ": keep-alive\n\n")
		fmt.Fprint(w, "event: ping\n\n")
		flusher.Flush()
		// Valid first chunk.
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"Hel"}}]}`+"\n\n")
		flusher.Flush()
		// Truncated JSON.
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"`+"\n\n")
		flusher.Flush()
		// Valid JSON but no choices.
		fmt.Fprint(w, `data: {"id":"keepalive-1"}`+"\n\n")
		flusher.Flush()
		// Valid second chunk.
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"lo"}}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	b := newBackend(t, srv)
	if err := b.CreateAgent(context.Background(), backend.CreateAgentParams{Name: "a", Model: "m"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := b.ChatSend(context.Background(), "a", backend.ChatSendParams{Message: "hi", IdempotencyKey: "idem"}); err != nil {
		t.Fatalf("ChatSend: %v", err)
	}

	// emitDelta encodes the full assistant text as a JSON string in
	// ChatEvent.Message. Decode and compare.
	deltaText := func(ev protocol.ChatEvent) string {
		var s string
		_ = json.Unmarshal(ev.Message, &s)
		return s
	}

	first := parseChat(t, drainEvent(t, b.Events()))
	if first.State != "delta" || deltaText(first) != "Hel" {
		t.Fatalf("first delta: state=%s body=%q", first.State, deltaText(first))
	}
	second := parseChat(t, drainEvent(t, b.Events()))
	if second.State != "delta" || deltaText(second) != "Hello" {
		t.Fatalf("second delta: state=%s body=%q", second.State, deltaText(second))
	}
	final := parseChat(t, drainEvent(t, b.Events()))
	if final.State != "final" {
		t.Fatalf("expected final, got state=%s err=%q", final.State, final.ErrorMessage)
	}
	var finalMsg struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(final.Message, &finalMsg); err != nil {
		t.Fatalf("decode final message: %v", err)
	}
	if len(finalMsg.Content) == 0 || finalMsg.Content[0].Text != "Hello" {
		t.Errorf("final body = %+v, want Hello", finalMsg.Content)
	}

	msgs, _ := b.store.LoadHistory("a", 0)
	if len(msgs) != 2 {
		t.Fatalf("expected user+assistant in history, got %d", len(msgs))
	}
	if msgs[1].Content != "Hello" {
		t.Errorf("persisted assistant content = %q", msgs[1].Content)
	}
}

func TestBackend_ChatSendErrorEmitsErrorEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	b := newBackend(t, srv)
	_ = b.CreateAgent(context.Background(), backend.CreateAgentParams{Name: "a", Model: "m"})
	if _, err := b.ChatSend(context.Background(), "a", backend.ChatSendParams{Message: "hi", IdempotencyKey: "idem"}); err != nil {
		t.Fatal(err)
	}
	ev := parseChat(t, drainEvent(t, b.Events()))
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
	_, _ = b.ChatSend(context.Background(), "a", backend.ChatSendParams{Message: "hi", IdempotencyKey: "idem"})
	ev := parseChat(t, drainEvent(t, b.Events()))
	if !strings.Contains(ev.ErrorMessage, "api key required") {
		t.Errorf("expected api-key error message, got %q", ev.ErrorMessage)
	}
}

func TestBackend_ChatSendRequiresModel(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	b := newBackend(t, srv)
	_ = b.CreateAgent(context.Background(), backend.CreateAgentParams{Name: "a"})
	if _, err := b.ChatSend(context.Background(), "a", backend.ChatSendParams{Message: "hi", IdempotencyKey: "idem"}); err == nil {
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
	if caps.GatewayStatus || caps.RemoteExec || caps.Thinking || caps.SessionUsage {
		t.Errorf("expected no gateway-only caps, got %+v", caps)
	}
	if !caps.SessionCompact {
		t.Errorf("expected SessionCompact=true (local summarisation pass), got %+v", caps)
	}
	if !caps.AgentManagement {
		t.Errorf("expected AgentManagement=true, got %+v", caps)
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

func TestSkillCatalogSystemMessage(t *testing.T) {
	t.Run("renders entries", func(t *testing.T) {
		got := skillCatalogSystemMessage([]backend.SkillCatalogEntry{
			{Name: "review", Description: "Code review"},
			{Name: "commit", Description: "Write a commit message"},
		})
		if got == "" {
			t.Fatal("expected a non-empty system message")
		}
		if !strings.Contains(got, "Available agent skills") {
			t.Errorf("missing header: %q", got)
		}
		if !strings.Contains(got, "- review: Code review") {
			t.Errorf("missing review entry: %q", got)
		}
		if strings.Contains(got, "System:") {
			// Crucial: OpenAI uses a real role:system message body,
			// not the OpenClaw System:-prefix kludge.
			t.Errorf("OpenAI catalog should not contain System: prefix: %q", got)
		}
	})

	t.Run("nil and empty return empty string", func(t *testing.T) {
		if got := skillCatalogSystemMessage(nil); got != "" {
			t.Errorf("nil → %q", got)
		}
		if got := skillCatalogSystemMessage([]backend.SkillCatalogEntry{{Name: ""}}); got != "" {
			t.Errorf("blank entry → %q", got)
		}
	})
}

func TestBackend_ChatSend_PassesSkillsAsRealSystemMessage(t *testing.T) {
	type capturedRequest struct {
		Messages []chatRequestMessage `json:"messages"`
	}
	var captured capturedRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"data":[]}`)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	b := newBackend(t, srv)
	if err := b.CreateAgent(context.Background(), backend.CreateAgentParams{Name: "a", Model: "m"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := b.ChatSend(context.Background(), "a", backend.ChatSendParams{
		Message: "hello",
		Skills: []backend.SkillCatalogEntry{
			{Name: "review", Description: "Code review"},
		},
	}); err != nil {
		t.Fatalf("ChatSend: %v", err)
	}
	// Drain the synthetic final/aborted event so the run goroutine
	// finishes before the test ends.
	select {
	case <-b.Events():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream completion")
	}

	var systemBodies []string
	for _, m := range captured.Messages {
		if m.Role == "system" {
			systemBodies = append(systemBodies, m.Content)
		}
	}
	foundCatalog := false
	for _, body := range systemBodies {
		if strings.Contains(body, "Available agent skills") && strings.Contains(body, "- review:") {
			foundCatalog = true
			break
		}
	}
	if !foundCatalog {
		t.Errorf("expected a role:system message containing the skill catalog, got system bodies: %+v", systemBodies)
	}
}

// TestBackend_SessionCompact_NoOpWhenHistoryShort verifies the local
// summarisation pass leaves the transcript untouched when there's
// nothing meaningful to compact (fewer than compactMinHistory messages).
// The HTTP server fails the test if it gets called, so we also assert
// no request was issued.
func TestBackend_SessionCompact_NoOpWhenHistoryShort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s — short history should be a no-op", r.URL.Path)
		http.Error(w, "should not call", http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := newBackend(t, srv)
	if err := b.CreateAgent(context.Background(), backend.CreateAgentParams{Name: "a", Model: "m"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	_ = b.store.AppendMessage("a", Message{Role: "user", Content: "hi"})
	_ = b.store.AppendMessage("a", Message{Role: "assistant", Content: "hello"})

	if err := b.SessionCompact(context.Background(), "a"); err != nil {
		t.Fatalf("SessionCompact: %v", err)
	}
	msgs, _ := b.store.LoadHistory("a", 0)
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages preserved, got %d", len(msgs))
	}
}

// TestBackend_SessionCompact_SummarisesAndPreservesTail covers the
// happy path: with enough history the backend streams a completion,
// swaps the older messages for a single Summary system entry, and
// keeps the most recent compactKeepTail messages verbatim. The test
// also asserts the persisted summary has Summary=true so runStream
// forwards it on the next turn.
func TestBackend_SessionCompact_SummarisesAndPreservesTail(t *testing.T) {
	var captured chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		writeChunk := func(content string) {
			payload := fmt.Sprintf(`{"choices":[{"delta":{"content":%q}}]}`, content)
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}
		writeChunk("User asked about colours; ")
		writeChunk("assistant explained the rainbow.")
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	b := newBackend(t, srv)
	if err := b.CreateAgent(context.Background(), backend.CreateAgentParams{Name: "a", Model: "m"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	// Six messages: oldest pair gets summarised, last four (compactKeepTail) preserved.
	_ = b.store.AppendMessage("a", Message{Role: "user", Content: "what colours are in a rainbow?"})
	_ = b.store.AppendMessage("a", Message{Role: "assistant", Content: "ROYGBIV: red, orange, yellow, green, blue, indigo, violet"})
	_ = b.store.AppendMessage("a", Message{Role: "user", Content: "is indigo really distinct?"})
	_ = b.store.AppendMessage("a", Message{Role: "assistant", Content: "Newton included it; modern usage often skips it."})
	_ = b.store.AppendMessage("a", Message{Role: "user", Content: "thanks"})
	_ = b.store.AppendMessage("a", Message{Role: "assistant", Content: "you're welcome"})

	if err := b.SessionCompact(context.Background(), "a"); err != nil {
		t.Fatalf("SessionCompact: %v", err)
	}

	if !captured.Stream {
		t.Errorf("compact request should stream — non-streaming returns empty content on some Ollama setups")
	}
	if captured.Model != "m" {
		t.Errorf("compact used wrong model: %q", captured.Model)
	}
	// Wire shape: exactly two messages — the system prompt and a
	// user-role transcript dump. Forwarding the older slice as a
	// raw user/assistant sequence ends the request on role: assistant
	// and OpenAI-compat servers (Ollama, vLLM) respond with empty
	// content because they think the conversation is complete.
	if len(captured.Messages) != 2 {
		t.Fatalf("expected exactly 2 messages (system + user transcript), got %d: %+v", len(captured.Messages), captured.Messages)
	}
	if captured.Messages[0].Role != "system" || !strings.Contains(captured.Messages[0].Content, "compacting") {
		t.Errorf("first message should be the compaction system prompt, got %+v", captured.Messages[0])
	}
	if captured.Messages[1].Role != "user" {
		t.Errorf("transcript dump must be role: user so the request ends on a user turn, got %q", captured.Messages[1].Role)
	}
	transcript := captured.Messages[1].Content
	if !strings.Contains(transcript, "user: what colours are in a rainbow?") {
		t.Errorf("transcript missing earliest user turn:\n%s", transcript)
	}
	if !strings.Contains(transcript, "assistant: ROYGBIV") {
		t.Errorf("transcript missing earliest assistant turn:\n%s", transcript)
	}
	if strings.Contains(transcript, "thanks") || strings.Contains(transcript, "you're welcome") {
		t.Errorf("tail message leaked into compaction request: %s", transcript)
	}

	rewritten, err := b.store.LoadHistory("a", 0)
	if err != nil {
		t.Fatal(err)
	}
	// 1 summary + compactKeepTail preserved messages.
	wantLen := 1 + 4
	if len(rewritten) != wantLen {
		t.Fatalf("expected %d messages after compact, got %d: %+v", wantLen, len(rewritten), rewritten)
	}
	head := rewritten[0]
	if head.Role != "system" {
		t.Errorf("summary role should be system, got %q", head.Role)
	}
	if !head.Summary {
		t.Errorf("summary message must have Summary=true so runStream forwards it next turn")
	}
	if !strings.Contains(head.Content, "rainbow") {
		t.Errorf("summary content missing model output: %q", head.Content)
	}
	// Tail preserved verbatim.
	if rewritten[len(rewritten)-1].Content != "you're welcome" {
		t.Errorf("last message not preserved verbatim: %+v", rewritten[len(rewritten)-1])
	}
	if rewritten[len(rewritten)-2].Content != "thanks" {
		t.Errorf("penultimate not preserved verbatim: %+v", rewritten[len(rewritten)-2])
	}
}

// TestBackend_SessionCompact_ForwardsSummaryOnNextTurn proves the
// rewritten transcript actually shrinks subsequent requests: after
// /compact, a follow-up ChatSend should forward the summary system
// message and the preserved tail, but none of the older raw turns.
func TestBackend_SessionCompact_ForwardsSummaryOnNextTurn(t *testing.T) {
	var compactBody, chatBody chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		var body chatRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		// Compaction request is the one carrying the compaction system
		// prompt; the regular chat send doesn't.
		isCompact := len(body.Messages) > 0 && strings.Contains(body.Messages[0].Content, "compacting")
		if isCompact {
			compactBody = body
		} else {
			chatBody = body
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		if isCompact {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"earlier-summary-text"}}]}`+"\n\n")
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	b := newBackend(t, srv)
	_ = b.CreateAgent(context.Background(), backend.CreateAgentParams{Name: "a", Model: "m"})
	_ = b.store.AppendMessage("a", Message{Role: "user", Content: "an-old-question"})
	_ = b.store.AppendMessage("a", Message{Role: "assistant", Content: "an-old-answer"})
	for i := 0; i < 4; i++ {
		_ = b.store.AppendMessage("a", Message{Role: "user", Content: fmt.Sprintf("tail-u-%d", i)})
	}

	if err := b.SessionCompact(context.Background(), "a"); err != nil {
		t.Fatalf("SessionCompact: %v", err)
	}

	if _, err := b.ChatSend(context.Background(), "a", backend.ChatSendParams{Message: "next"}); err != nil {
		t.Fatalf("ChatSend: %v", err)
	}
	// Drain the synthetic [DONE] so the goroutine finishes.
	select {
	case <-b.Events():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream completion")
	}

	if len(compactBody.Messages) == 0 {
		t.Fatal("compaction request never observed")
	}
	if len(chatBody.Messages) == 0 {
		t.Fatal("chat-send request never observed")
	}
	var sawSummary, leakedOld bool
	for _, m := range chatBody.Messages {
		if m.Role == "system" && strings.Contains(m.Content, "earlier-summary-text") {
			sawSummary = true
		}
		if strings.Contains(m.Content, "an-old-question") || strings.Contains(m.Content, "an-old-answer") {
			leakedOld = true
		}
	}
	if !sawSummary {
		t.Errorf("expected the summary system message to be forwarded on the next turn, got: %+v", chatBody.Messages)
	}
	if leakedOld {
		t.Errorf("older raw turns leaked into the next request — compact did not actually shrink history: %+v", chatBody.Messages)
	}
}

// TestBackend_SessionCompact_MissingAgent surfaces a clear error rather
// than a silent success when the session key doesn't resolve to an agent.
func TestBackend_SessionCompact_MissingAgent(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	b := newBackend(t, srv)
	err := b.SessionCompact(context.Background(), "ghost")
	if err == nil || !strings.Contains(err.Error(), "agent not found") {
		t.Errorf("expected agent-not-found error, got %v", err)
	}
}

// TestBackend_SessionCompact_RequiresModel mirrors ChatSend's behaviour:
// without a configured model there's nothing to summarise with, so
// /compact returns a clear error instead of crashing on the wire.
func TestBackend_SessionCompact_RequiresModel(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	b := newBackend(t, srv)
	_ = b.CreateAgent(context.Background(), backend.CreateAgentParams{Name: "a"})
	for i := 0; i < compactMinHistory; i++ {
		_ = b.store.AppendMessage("a", Message{Role: "user", Content: "x"})
	}
	err := b.SessionCompact(context.Background(), "a")
	if err == nil || !strings.Contains(err.Error(), "no model configured") {
		t.Errorf("expected no-model error, got %v", err)
	}
}

// TestBackend_SessionCompact_EmptySummaryRejected ensures a model that
// streams only whitespace doesn't silently wipe history. The original
// transcript is left untouched and the error surfaces back to the TUI.
func TestBackend_SessionCompact_EmptySummaryRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"   "}}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	b := newBackend(t, srv)
	_ = b.CreateAgent(context.Background(), backend.CreateAgentParams{Name: "a", Model: "m"})
	for i := 0; i < compactMinHistory; i++ {
		_ = b.store.AppendMessage("a", Message{Role: "user", Content: fmt.Sprintf("msg-%d", i)})
	}
	err := b.SessionCompact(context.Background(), "a")
	if err == nil || !strings.Contains(err.Error(), "empty summary") {
		t.Errorf("expected empty-summary error, got %v", err)
	}
	// History must remain intact — a failed compact should be safe to retry.
	msgs, _ := b.store.LoadHistory("a", 0)
	if len(msgs) != compactMinHistory {
		t.Errorf("expected history untouched after failed compact, got %d messages", len(msgs))
	}
}

// TestRenderTranscriptForCompact pins the transcript-dump shape that
// drives /compact: a labelled "role: content" block, summaries
// rendered under a "summary" label so prior digests fold in, and
// non-summary system messages dropped defensively. The shape is
// load-bearing because it lives inside a single role: user message —
// see summarise() for why ending the request on role: assistant
// breaks Ollama and friends.
func TestRenderTranscriptForCompact(t *testing.T) {
	got := renderTranscriptForCompact([]Message{
		{Role: "system", Content: "stale system prompt"},
		{Role: "system", Content: "earlier digest", Summary: true},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: ""}, // dropped (empty content)
	})
	want := "summary: earlier digest\n\nuser: hello\n\nassistant: hi there"
	if got != want {
		t.Errorf("renderTranscriptForCompact mismatch:\n got %q\nwant %q", got, want)
	}
	if got := renderTranscriptForCompact(nil); got != "" {
		t.Errorf("nil older slice should render as empty, got %q", got)
	}
	if got := renderTranscriptForCompact([]Message{{Role: "system", Content: "drop me"}}); got != "" {
		t.Errorf("only non-summary system messages should render as empty, got %q", got)
	}
}

// TestBackend_ChatHistory_RendersSummary asserts that summary messages
// produced by /compact appear in the protocol.ChatHistoryResult shape
// — without this the chat view would silently drop the digest on
// history refresh and the user would see a gap.
func TestBackend_ChatHistory_RendersSummary(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	b := newBackend(t, srv)
	_ = b.CreateAgent(context.Background(), backend.CreateAgentParams{Name: "a", Model: "m"})
	_ = b.store.AppendMessage("a", Message{Role: "system", Content: "Summary of earlier conversation:\nfoo", Summary: true})
	_ = b.store.AppendMessage("a", Message{Role: "user", Content: "next"})

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
		t.Fatalf("expected summary + user message, got %d: %+v", len(got.Messages), got.Messages)
	}
	if got.Messages[0].Role != "system" || got.Messages[0].Content[0].Text != "Summary of earlier conversation:\nfoo" {
		t.Errorf("summary not rendered in history: %+v", got.Messages[0])
	}
}
