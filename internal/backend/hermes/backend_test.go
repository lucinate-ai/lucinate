package hermes

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

func newBackend(t *testing.T, srv *httptest.Server) *Backend {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	b, err := New(Options{
		ConnectionID: "test",
		BaseURL:      srv.URL,
		APIKey:       "k",
		HTTPClient:   srv.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

func TestNew_DefaultsBaseURL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	b, err := New(Options{ConnectionID: "test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer b.Close()
}

func TestNew_RequiresConnectionID(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected error for missing ConnectionID")
	}
}

func TestConnect_ListsModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"hermes-agent"}]}`))
	}))
	defer srv.Close()
	b := newBackend(t, srv)
	if err := b.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if b.profileModel != "hermes-agent" {
		t.Errorf("profileModel = %q want hermes-agent", b.profileModel)
	}
}

func TestConnect_AuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	b := newBackend(t, srv)
	err := b.Connect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "api key required") {
		t.Errorf("expected api-key-required error, got %v", err)
	}
}

func TestListAgents_ReturnsSyntheticEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"hermes-agent"}]}`))
	}))
	defer srv.Close()
	b := newBackend(t, srv)
	_ = b.Connect(context.Background())
	res, err := b.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(res.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(res.Agents))
	}
	if res.Agents[0].ID != syntheticAgentID {
		t.Errorf("ID = %q", res.Agents[0].ID)
	}
}

func TestCreateAgent_Rejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	b := newBackend(t, srv)
	err := b.CreateAgent(context.Background(), backend.CreateAgentParams{Name: "x"})
	if err == nil || !strings.Contains(err.Error(), "not user-creatable") {
		t.Errorf("expected rejection, got %v", err)
	}
}

func TestDeleteAgent_Rejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	b := newBackend(t, srv)
	err := b.DeleteAgent(context.Background(), backend.DeleteAgentParams{AgentID: "x", DeleteFiles: true})
	if err == nil || !strings.Contains(err.Error(), "not user-deletable") {
		t.Errorf("expected rejection, got %v", err)
	}
}

func TestCapabilities_AgentManagementOff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	b := newBackend(t, srv)
	caps := b.Capabilities()
	if caps.AgentManagement {
		t.Error("AgentManagement should be false on Hermes")
	}
	if caps.AuthRecovery != backend.AuthRecoveryAPIKey {
		t.Errorf("AuthRecovery = %v", caps.AuthRecovery)
	}
}

// streamingResponsesHandler returns the typed SSE event sequence
// Hermes emits on /v1/responses for a successful completion. The
// response id and the output text are configurable.
func streamingResponsesHandler(responseID, replyText string) http.Handler {
	frames := []string{
		fmt.Sprintf(`{"type":"response.created","response":{"id":%q,"status":"in_progress","created_at":1,"model":"qwen2.5:0.5b"}}`, responseID),
		fmt.Sprintf(`{"type":"response.output_text.delta","delta":%q}`, replyText[:len(replyText)/2]),
		fmt.Sprintf(`{"type":"response.output_text.delta","delta":%q}`, replyText[len(replyText)/2:]),
		fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"status":"completed","created_at":1,"model":"qwen2.5:0.5b","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}]}}`, responseID, replyText),
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
}

func TestChatSend_StreamsAndPersistsLastResponseID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			_, _ = w.Write([]byte(`{"data":[{"id":"hermes-agent"}]}`))
		case strings.HasSuffix(r.URL.Path, "/responses"):
			streamingResponsesHandler("resp_xyz", "hello there").ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	b := newBackend(t, srv)
	if err := b.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	res, err := b.ChatSend(context.Background(), syntheticAgentID, backend.ChatSendParams{Message: "say hi", IdempotencyKey: "i"})
	if err != nil {
		t.Fatalf("ChatSend: %v", err)
	}
	if res.RunID == "" {
		t.Fatal("empty RunID")
	}

	deadline := time.After(2 * time.Second)
	sawDelta := false
	for {
		select {
		case ev := <-b.Events():
			ce := decodeChat(t, ev)
			switch ce.State {
			case "delta":
				sawDelta = true
			case "final":
				if !sawDelta {
					t.Error("final without delta")
				}
				if got := b.lastResponseID; got != "resp_xyz" {
					t.Errorf("lastResponseID = %q", got)
				}
				return
			case "error":
				t.Fatalf("error event: %s", ce.ErrorMessage)
			}
		case <-deadline:
			t.Fatal("timed out")
		}
	}
}

func TestChatHistory_WalksPreviousResponseChain(t *testing.T) {
	// Two responses: resp_b → previous resp_a. The walk should
	// surface both turns in chronological order.
	chain := map[string]string{
		"resp_a": `{"id":"resp_a","status":"completed","created_at":10,"model":"m","input":"first user message","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"first reply"}]}]}`,
		"resp_b": `{"id":"resp_b","status":"completed","created_at":20,"model":"m","previous_response_id":"resp_a","input":"second user message","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"second reply"}]}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for id, body := range chain {
			if strings.HasSuffix(r.URL.Path, "/responses/"+id) {
				_, _ = w.Write([]byte(body))
				return
			}
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	b := newBackend(t, srv)
	b.lastResponseID = "resp_b"

	raw, err := b.ChatHistory(context.Background(), syntheticAgentID, 0)
	if err != nil {
		t.Fatalf("ChatHistory: %v", err)
	}
	var hist struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &hist); err != nil {
		t.Fatalf("decode: %v\nraw: %s", err, raw)
	}
	want := []struct{ role, text string }{
		{"user", "first user message"},
		{"assistant", "first reply"},
		{"user", "second user message"},
		{"assistant", "second reply"},
	}
	if len(hist.Messages) != len(want) {
		t.Fatalf("got %d messages, want %d (raw: %s)", len(hist.Messages), len(want), raw)
	}
	for i, w := range want {
		if hist.Messages[i].Role != w.role {
			t.Errorf("msg[%d].Role = %q want %q", i, hist.Messages[i].Role, w.role)
		}
		if hist.Messages[i].Content[0].Text != w.text {
			t.Errorf("msg[%d].Text = %q want %q", i, hist.Messages[i].Content[0].Text, w.text)
		}
	}
}

func TestChatHistory_WalkBoundedByLimit(t *testing.T) {
	// Build a chain longer than historyWalkLimit. The walk should
	// stop after historyWalkLimit hops.
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// Each response chains to the prior one ad infinitum.
		body := fmt.Sprintf(`{"id":"resp_%d","status":"completed","created_at":%d,"model":"m","previous_response_id":"resp_%d","input":"u%d","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a%d"}]}]}`,
			calls, calls, calls+1, calls, calls)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	b := newBackend(t, srv)
	b.lastResponseID = "resp_start"

	if _, err := b.ChatHistory(context.Background(), syntheticAgentID, 0); err != nil {
		t.Fatalf("ChatHistory: %v", err)
	}
	if calls != historyWalkLimit {
		t.Errorf("walk made %d HTTP calls, want %d", calls, historyWalkLimit)
	}
}

func TestSessionDelete_ClearsLastResponseID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	b := newBackend(t, srv)
	b.lastResponseID = "resp_foo"
	_ = writeLastResponseID(b.opts.ConnectionID, "resp_foo")

	if err := b.SessionDelete(context.Background(), syntheticAgentID); err != nil {
		t.Fatalf("SessionDelete: %v", err)
	}
	if b.lastResponseID != "" {
		t.Errorf("lastResponseID not cleared")
	}
	if got, _ := readLastResponseID(b.opts.ConnectionID); got != "" {
		t.Errorf("file not cleared: %q", got)
	}
}

// TestSessionDelete_NextChatStartsFreshChain is the regression test
// for the /reset bug. Before the fix ChatSend pinned the
// `conversation` field to the connection ID, so the server kept the
// thread alive across resets even though the local pointer was
// cleared. Now ChatSend chains via previous_response_id, so a cleared
// pointer must produce a request with NO previous_response_id field.
func TestSessionDelete_NextChatStartsFreshChain(t *testing.T) {
	type capturedRequest struct {
		PreviousResponseID string `json:"previous_response_id"`
		Conversation       string `json:"conversation"`
	}
	var captured []capturedRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			_, _ = w.Write([]byte(`{"data":[{"id":"hermes-agent"}]}`))
		case strings.HasSuffix(r.URL.Path, "/responses"):
			body, _ := io.ReadAll(r.Body)
			var req capturedRequest
			_ = json.Unmarshal(body, &req)
			captured = append(captured, req)
			id := fmt.Sprintf("resp_%d", len(captured))
			streamingResponsesHandler(id, "ok").ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	b := newBackend(t, srv)
	if err := b.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// First turn: empty chain.
	sendAndDrain(t, b, "first")
	if got := captured[0].PreviousResponseID; got != "" {
		t.Errorf("first turn previous_response_id = %q, want empty", got)
	}

	// Second turn: chains off resp_1.
	sendAndDrain(t, b, "second")
	if got := captured[1].PreviousResponseID; got != "resp_1" {
		t.Errorf("second turn previous_response_id = %q, want resp_1", got)
	}

	// /reset clears the pointer.
	if err := b.SessionDelete(context.Background(), syntheticAgentID); err != nil {
		t.Fatalf("SessionDelete: %v", err)
	}

	// Third turn after reset: must send no previous_response_id.
	sendAndDrain(t, b, "third")
	if got := captured[2].PreviousResponseID; got != "" {
		t.Errorf("post-reset previous_response_id = %q, want empty", got)
	}
	// And no named conversation either — the bug used to pin this to
	// the connection ID which kept the server-side thread alive.
	if got := captured[2].Conversation; got != "" {
		t.Errorf("post-reset conversation = %q, want empty", got)
	}
}

// sendAndDrain helper: send a chat message and consume events until
// the final event arrives. Used by the regression test above to step
// through multiple turns.
func sendAndDrain(t *testing.T, b *Backend, msg string) {
	t.Helper()
	if _, err := b.ChatSend(context.Background(), syntheticAgentID, backend.ChatSendParams{Message: msg, IdempotencyKey: msg}); err != nil {
		t.Fatalf("ChatSend(%q): %v", msg, err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-b.Events():
			ce := decodeChat(t, ev)
			if ce.State == "final" {
				return
			}
			if ce.State == "error" {
				t.Fatalf("error: %s", ce.ErrorMessage)
			}
		case <-deadline:
			t.Fatalf("timeout waiting for final on %q", msg)
		}
	}
}

func TestSessionPatchModel_Rejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	b := newBackend(t, srv)
	if err := b.SessionPatchModel(context.Background(), syntheticAgentID, "anything"); err == nil {
		t.Error("expected error from SessionPatchModel")
	}
}

func decodeChat(t *testing.T, ev protocol.Event) protocol.ChatEvent {
	t.Helper()
	if ev.EventName != protocol.EventChat {
		t.Fatalf("unexpected event: %s", ev.EventName)
	}
	var ce protocol.ChatEvent
	if err := json.Unmarshal(ev.Payload, &ce); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return ce
}

// TestErrorMessage_PrefersErrorObject covers the typed `error.message`
// branch — Hermes' SSE stream is the source of truth for failure
// strings, so the ChatError event the TUI surfaces must be the
// upstream message verbatim, not the bare "stream error" fallback.
func TestErrorMessage_PrefersErrorObject(t *testing.T) {
	cases := []struct {
		name string
		ev   responsesEvent
		want string
	}{
		{
			name: "prefers error object",
			ev:   responsesEvent{Error: &responseErrorObj{Message: "rate limited"}, Message: "ignored"},
			want: "rate limited",
		},
		{
			name: "falls back to top-level message",
			ev:   responsesEvent{Message: "top-level boom"},
			want: "top-level boom",
		},
		{
			name: "fallback when both empty",
			ev:   responsesEvent{},
			want: "stream error",
		},
		{
			name: "empty error object falls through",
			ev:   responsesEvent{Error: &responseErrorObj{}, Message: "from message"},
			want: "from message",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ev.errorMessage(); got != tc.want {
				t.Errorf("errorMessage() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUserInput_ParsesBothShapes is the contract for ChatHistory's
// reverse-walk: the upstream Responses API stores `input` as either
// a raw string or a structured array, and ChatHistory must surface
// the user's message either way so the rendered transcript is
// symmetrical regardless of which wire shape Hermes used.
func TestUserInput_ParsesBothShapes(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", ``, ""},
		{"raw string", `"hello there"`, "hello there"},
		{"array with .text", `[{"type":"message","role":"user","text":"plain text"}]`, "plain text"},
		{"array with content blocks", `[{"role":"user","content":[{"type":"input_text","text":"a "},{"type":"text","text":"b"}]}]`, "a b"},
		{"unrecognised shape", `42`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &responseRecord{}
			if tc.input != "" {
				r.Input = json.RawMessage(tc.input)
			}
			if got := r.userInput(); got != tc.want {
				t.Errorf("userInput() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestProfileDisplayName_FallsBackToHermes pins the agent picker
// label that surfaces before Connect has discovered the profile
// model: the synthetic agent must still have a non-empty name so the
// list isn't blank on first render.
func TestProfileDisplayName_FallsBackToHermes(t *testing.T) {
	if got := profileDisplayName(""); got != "Hermes" {
		t.Errorf("profileDisplayName(\"\") = %q, want Hermes", got)
	}
	if got := profileDisplayName("qwen2.5"); got != "qwen2.5" {
		t.Errorf("profileDisplayName(\"qwen2.5\") = %q, want qwen2.5", got)
	}
}

// TestModelsList_ReturnsProtocolShape covers the model-picker entry
// point: the upstream /v1/models payload is reshaped into the
// protocol.ModelsListResult the TUI consumes.
func TestModelsList_ReturnsProtocolShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"hermes-agent"},{"id":"another"}]}`))
	}))
	defer srv.Close()
	b := newBackend(t, srv)

	res, err := b.ModelsList(context.Background())
	if err != nil {
		t.Fatalf("ModelsList: %v", err)
	}
	if len(res.Models) != 2 {
		t.Fatalf("got %d models, want 2", len(res.Models))
	}
	want := []string{"hermes-agent", "another"}
	for i, m := range res.Models {
		if m.ID != want[i] || m.Name != want[i] {
			t.Errorf("models[%d] = (%q, %q), want (%q, %q)", i, m.ID, m.Name, want[i], want[i])
		}
	}
}

// TestModelsList_PropagatesAuthError ensures a 401/403 from
// /v1/models surfaces as the canonical "api key required" string —
// the connecting view matches on this prefix to route into the
// API-key recovery modal.
func TestModelsList_PropagatesAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	b := newBackend(t, srv)

	_, err := b.ModelsList(context.Background())
	if err == nil || !strings.Contains(err.Error(), "api key required") {
		t.Errorf("ModelsList error = %v, want api-key-required prefix", err)
	}
}

// TestChatAbort_UnknownRunIsNoop covers the lookup-miss branch — an
// abort for a run that already completed (or was never registered)
// should silently return rather than emitting a spurious aborted
// event the chat view would then have to filter out.
func TestChatAbort_UnknownRunIsNoop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	b := newBackend(t, srv)

	if err := b.ChatAbort(context.Background(), syntheticAgentID, "run-does-not-exist"); err != nil {
		t.Errorf("ChatAbort on unknown run should not error, got %v", err)
	}

	// And no event should have been emitted.
	select {
	case ev := <-b.Events():
		t.Errorf("unexpected event emitted: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestChatAbort_StopsRunningStreamAndEmitsAborted is the user-
// observable contract for /abort: the streaming goroutine is
// cancelled and the chat view receives an aborted event so it can
// flip the input back from "thinking" to ready.
//
// We register a fake run directly to avoid relying on a long-lived
// streaming connection in the test.
func TestChatAbort_StopsRunningStreamAndEmitsAborted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	b := newBackend(t, srv)

	cancelled := make(chan struct{})
	_, cancel := context.WithCancel(context.Background())
	b.mu.Lock()
	b.runs["run-1"] = func() {
		cancel()
		close(cancelled)
	}
	b.mu.Unlock()

	if err := b.ChatAbort(context.Background(), syntheticAgentID, "run-1"); err != nil {
		t.Fatalf("ChatAbort: %v", err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("registered cancel was not invoked by ChatAbort")
	}

	// And the registered run is removed so a re-abort is a no-op.
	b.mu.Lock()
	_, stillThere := b.runs["run-1"]
	b.mu.Unlock()
	if stillThere {
		t.Error("expected run to be removed from the map after abort")
	}

	// Aborted event surfaces to the events channel.
	select {
	case ev := <-b.Events():
		ce := decodeChat(t, ev)
		if ce.State != "aborted" {
			t.Errorf("event state = %q, want aborted", ce.State)
		}
	case <-time.After(time.Second):
		t.Fatal("no aborted event emitted")
	}
}

// TestSessionsList_UnknownAgentReturnsEmpty pins the contract that
// callers passing the wrong agent ID see an empty session list rather
// than an error: the TUI's session browser would treat an error as a
// transient backend failure, which is the wrong UX for a misrouted
// query.
func TestSessionsList_UnknownAgentReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	b := newBackend(t, srv)

	raw, err := b.SessionsList(context.Background(), "not-the-synthetic-id")
	if err != nil {
		t.Fatalf("SessionsList: %v", err)
	}
	if got := string(raw); got != `{"sessions":[]}` {
		t.Errorf("SessionsList = %s, want empty list", got)
	}
}

// TestCreateSession_RejectsUnknownAgent covers the symmetric guard on
// CreateSession: the Hermes synthetic agent ID is the only acceptable
// argument; anything else is a programmer error.
func TestCreateSession_RejectsUnknownAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	b := newBackend(t, srv)

	if _, err := b.CreateSession(context.Background(), "wrong-id", "k"); err == nil {
		t.Error("expected error for unknown agent ID")
	}
	got, err := b.CreateSession(context.Background(), syntheticAgentID, "ignored-key")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got != syntheticAgentID {
		t.Errorf("returned key = %q, want %q", got, syntheticAgentID)
	}
}

// TestChatSend_RejectsUnknownSession covers the guard at the top of
// ChatSend — only the synthetic agent ID is accepted, matching the
// CreateSession contract above.
func TestChatSend_RejectsUnknownSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	b := newBackend(t, srv)

	_, err := b.ChatSend(context.Background(), "wrong-key", backend.ChatSendParams{Message: "hi"})
	if err == nil || !strings.Contains(err.Error(), "session not found") {
		t.Errorf("expected session-not-found error, got %v", err)
	}
}

// TestChatSend_ErrorsWithoutModel pins the helpful failure when the
// connection has no model configured and Connect hasn't yet
// discovered one — without this guard the request would post a
// malformed body and the user would see whatever 400 the upstream
// chose to surface.
func TestChatSend_ErrorsWithoutModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	b := newBackend(t, srv)

	_, err := b.ChatSend(context.Background(), syntheticAgentID, backend.ChatSendParams{Message: "hi"})
	if err == nil || !strings.Contains(err.Error(), "no model configured") {
		t.Errorf("expected no-model-configured error, got %v", err)
	}
}

// TestLastResponseIDPersistence is the round-trip for the small
// pointer file that backs the Hermes history walk: a write must be
// readable, and an absent file must surface as an error rather than
// an empty success (so callers can distinguish "fresh connection"
// from "read failed").
func TestLastResponseIDPersistence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if _, err := readLastResponseID("conn"); err == nil {
		t.Error("expected error reading absent last-response file")
	}

	if err := writeLastResponseID("conn", "resp_42"); err != nil {
		t.Fatalf("writeLastResponseID: %v", err)
	}
	got, err := readLastResponseID("conn")
	if err != nil {
		t.Fatalf("readLastResponseID: %v", err)
	}
	if got != "resp_42" {
		t.Errorf("got %q, want resp_42", got)
	}

	if err := clearLastResponseID("conn"); err != nil {
		t.Fatalf("clearLastResponseID: %v", err)
	}
	if _, err := readLastResponseID("conn"); err == nil {
		t.Error("expected error after clear")
	}
}
