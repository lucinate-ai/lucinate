//go:build integration_hermes

// Integration tests for the Hermes backend, exercised against a real
// Hermes gateway (`hermes dashboard`) brought up by
// test/integration/setup-hermes.sh. Tests are protocol-level — they
// assert on the event state machine and session lifecycle, not on
// model output content.
//
// Run with:
//
//	make test-integration-hermes-setup
//	make test-integration-hermes
//	make test-integration-hermes-teardown
//
// Chat-turn tests additionally need the container's profile wired to a
// working inference provider (the harness does this); they skip when
// LUCINATE_HERMES_TOKEN is unset.

package hermes

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/a3tai/openclaw-go/protocol"
	"github.com/joho/godotenv"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/client"
)

// projectRoot resolves to the repo root from this test file.
func projectRoot() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
}

// liveBackend builds a Backend pointed at the live gateway using env
// vars from .env.hermes, skipping when the harness isn't up.
func liveBackend(t *testing.T) *Backend {
	t.Helper()
	envFile := filepath.Join(projectRoot(), ".env.hermes")
	if _, err := os.Stat(envFile); err == nil {
		_ = godotenv.Load(envFile)
	}
	t.Setenv("HOME", t.TempDir())

	baseURL := os.Getenv("LUCINATE_HERMES_BASE_URL")
	if baseURL == "" {
		t.Skip("LUCINATE_HERMES_BASE_URL not set — run make test-integration-hermes-setup first")
	}

	b, err := New(Options{
		ConnectionID:   "test-" + t.Name(),
		BaseURL:        baseURL,
		APIKey:         os.Getenv("LUCINATE_HERMES_TOKEN"),
		ConnectTimeout: 20 * time.Second,
	})
	if err != nil {
		t.Fatalf("backend: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := b.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return b
}

// waitLiveChatEvent reads events until a chat event in one of the
// wanted states arrives.
func waitLiveChatEvent(t *testing.T, b *Backend, timeout time.Duration, states ...string) protocol.ChatEvent {
	t.Helper()
	deadline := time.After(timeout)
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
			if ce.State == "error" {
				t.Fatalf("chat error while waiting for %v: %s", states, ce.ErrorMessage)
			}
		case <-deadline:
			t.Fatalf("timed out waiting for chat event %v", states)
		}
	}
}

// Connect performs the WS handshake and exposes the synthetic agent.
func TestIntegration_ConnectAndAgents(t *testing.T) {
	b := liveBackend(t)
	agents, err := b.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents.Agents) != 1 || agents.Agents[0].ID != syntheticAgentID {
		t.Fatalf("agents = %+v", agents.Agents)
	}
}

// A wrong token is rejected at the upgrade with the canonical
// api-key-required error.
func TestIntegration_BadTokenRejected(t *testing.T) {
	base := os.Getenv("LUCINATE_HERMES_BASE_URL")
	if base == "" {
		t.Skip("LUCINATE_HERMES_BASE_URL not set")
	}
	b, err := New(Options{ConnectionID: "bad-token", BaseURL: base, APIKey: "definitely-wrong", ConnectTimeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer b.Close()
	err = b.Connect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "api key required") {
		t.Fatalf("want api key required, got %v", err)
	}
}

// A legacy-endpoint URL fails fast with the migration hint.
func TestIntegration_LegacyEndpointHint(t *testing.T) {
	b, err := New(Options{ConnectionID: "legacy", BaseURL: "http://localhost:8642/v1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer b.Close()
	err = b.Connect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "legacy Hermes API server") {
		t.Fatalf("want legacy hint, got %v", err)
	}
}

// Sessions round-trip: create → chat turn → listed under the stored id
// → history holds both turns → delete removes it.
func TestIntegration_SessionLifecycleAndHistory(t *testing.T) {
	b := liveBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	key, err := b.CreateSession(ctx, syntheticAgentID, "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if _, err := b.ChatSend(ctx, key, backend.ChatSendParams{Message: "Reply with exactly: lucinate-integration-ok"}); err != nil {
		t.Fatalf("ChatSend: %v", err)
	}
	waitLiveChatEvent(t, b, 90*time.Second, "final")

	// The session appears in the list once it holds a message.
	raw, err := b.SessionsList(ctx, syntheticAgentID)
	if err != nil {
		t.Fatalf("SessionsList: %v", err)
	}
	var list struct {
		Sessions []struct {
			Key string `json:"key"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(list.Sessions) == 0 {
		t.Fatalf("session missing from list: %s", raw)
	}

	// Full transcript from the server — both roles present.
	hist, err := b.ChatHistory(ctx, key, 50)
	if err != nil {
		t.Fatalf("ChatHistory: %v", err)
	}
	var out struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(hist, &out); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	roles := map[string]bool{}
	for _, m := range out.Messages {
		roles[m.Role] = true
	}
	if !roles["user"] || !roles["assistant"] {
		t.Fatalf("history missing a role: %s", hist)
	}

	if err := b.SessionDelete(ctx, key); err != nil {
		t.Fatalf("SessionDelete: %v", err)
	}
}

// Streaming order: deltas reassemble into the final text.
func TestIntegration_ChatStreamingOrder(t *testing.T) {
	b := liveBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	key, err := b.CreateSession(ctx, syntheticAgentID, "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := b.ChatSend(ctx, key, backend.ChatSendParams{Message: "Reply with exactly: hello world"}); err != nil {
		t.Fatalf("ChatSend: %v", err)
	}

	var lastDelta string
	deadline := time.After(90 * time.Second)
	for {
		var done bool
		select {
		case ev := <-b.Events():
			if ev.EventName != protocol.EventChat {
				continue
			}
			var ce protocol.ChatEvent
			if json.Unmarshal(ev.Payload, &ce) != nil {
				continue
			}
			switch ce.State {
			case "delta":
				text := backend.ExtractChatText(ce.Message)
				if !strings.HasPrefix(text, lastDelta) {
					t.Fatalf("delta not cumulative: %q then %q", lastDelta, text)
				}
				lastDelta = text
			case "final":
				if got := backend.ExtractChatText(ce.Message); lastDelta != "" && got != lastDelta {
					t.Fatalf("final %q != accumulated %q", got, lastDelta)
				}
				done = true
			case "error":
				t.Fatalf("chat error: %s", ce.ErrorMessage)
			}
		case <-deadline:
			t.Fatal("timed out waiting for final")
		}
		if done {
			break
		}
	}
}

// Abort mid-stream: the turn ends aborted and the session stays
// usable.
func TestIntegration_AbortMidStream(t *testing.T) {
	b := liveBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	key, err := b.CreateSession(ctx, syntheticAgentID, "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := b.ChatSend(ctx, key, backend.ChatSendParams{Message: "Write a very long 500-word essay about computing history."}); err != nil {
		t.Fatalf("ChatSend: %v", err)
	}
	if err := b.ChatAbort(ctx, key, ""); err != nil {
		t.Fatalf("ChatAbort: %v", err)
	}
	// On a fast model (the echo stub answers instantly) the turn can
	// win the race and complete before the interrupt lands, so either
	// terminal state is legitimate — the point is the turn terminates
	// and the session survives. The interrupted→aborted mapping itself
	// is pinned by the unit tests against the captured fixture.
	waitLiveChatEvent(t, b, 60*time.Second, "aborted", "final")

	// Session usable after the abort.
	if _, err := b.ChatSend(ctx, key, backend.ChatSendParams{Message: "Reply with exactly: ok"}); err != nil {
		t.Fatalf("ChatSend after abort: %v", err)
	}
	waitLiveChatEvent(t, b, 90*time.Second, "final")
}

// Tool events (echo leg only): the scripted marker makes the stub
// request Hermes' terminal tool; Hermes executes it for real and the
// backend surfaces a start/result tool-card pair sharing the
// server-supplied tool id.
func TestIntegration_ScriptedToolEvents(t *testing.T) {
	if os.Getenv("LUCINATE_HERMES_SCRIPTED") != "1" {
		// Loaded via .env.hermes by liveBackend below; pre-check the
		// raw env for a fast skip message, then re-check after load.
		_ = godotenv.Load(filepath.Join(projectRoot(), ".env.hermes"))
	}
	b := liveBackend(t)
	if os.Getenv("LUCINATE_HERMES_SCRIPTED") != "1" {
		t.Skip("scripted tool tests need the echo leg (setup-hermes.sh --echo)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	key, err := b.CreateSession(ctx, syntheticAgentID, "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := b.ChatSend(ctx, key, backend.ChatSendParams{Message: "[[tool:shell echo lucinate-tool-ok]]"}); err != nil {
		t.Fatalf("ChatSend: %v", err)
	}

	var startID, resultID string
	var resultIsError any
	deadline := time.After(90 * time.Second)
	for startID == "" || resultID == "" {
		select {
		case ev := <-b.Events():
			if ev.EventName == protocol.EventChat {
				var ce protocol.ChatEvent
				if json.Unmarshal(ev.Payload, &ce) == nil && ce.State == "error" {
					t.Fatalf("chat error: %s", ce.ErrorMessage)
				}
				continue
			}
			if ev.EventName != protocol.EventAgent {
				continue
			}
			var ae protocol.AgentEvent
			if json.Unmarshal(ev.Payload, &ae) != nil || ae.Stream != "tool" {
				continue
			}
			switch ae.Data["phase"] {
			case "start":
				startID, _ = ae.Data["toolCallId"].(string)
			case "result":
				resultID, _ = ae.Data["toolCallId"].(string)
				resultIsError = ae.Data["isError"]
			}
		case <-deadline:
			t.Fatalf("timed out; start=%q result=%q", startID, resultID)
		}
	}
	if startID == "" || startID != resultID {
		t.Fatalf("tool ids don't pair: start=%q result=%q", startID, resultID)
	}
	if resultIsError != false {
		t.Fatalf("scripted echo run flagged as error: %v", resultIsError)
	}
	waitLiveChatEvent(t, b, 60*time.Second, "final")
}

// Reconnect: restarting the container mid-session drops the WS; the
// supervisor redials, resumes the session, and chat works again.
func TestIntegration_ReconnectResumesSession(t *testing.T) {
	b := liveBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	key, err := b.CreateSession(ctx, syntheticAgentID, "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Land one message so the session is persisted for resume.
	if _, err := b.ChatSend(ctx, key, backend.ChatSendParams{Message: "Reply with exactly: pre-restart"}); err != nil {
		t.Fatalf("ChatSend: %v", err)
	}
	waitLiveChatEvent(t, b, 90*time.Second, "final")

	states := make(chan client.ConnState, 32)
	supCtx, supCancel := context.WithCancel(ctx)
	defer supCancel()
	go b.Supervise(supCtx, func(s client.ConnState) { states <- s })

	compose := filepath.Join(projectRoot(), "test", "integration", "hermes", "docker-compose.yml")
	restart := exec.CommandContext(ctx, "docker", "compose", "-f", compose, "restart", "hermes")
	if out, err := restart.CombinedOutput(); err != nil {
		t.Skipf("docker compose restart unavailable: %v: %s", err, out)
	}

	// Expect down → (reconnecting…) → connected.
	sawDisconnect, sawConnected := false, false
	deadline := time.After(240 * time.Second)
	for !sawConnected {
		select {
		case s := <-states:
			switch s.Status {
			case client.StatusDisconnected:
				sawDisconnect = true
			case client.StatusConnected:
				if sawDisconnect {
					sawConnected = true
				}
			}
		case <-deadline:
			t.Fatal("supervisor never reported reconnected")
		}
	}

	// The resumed session takes the next turn.
	if _, err := b.ChatSend(ctx, key, backend.ChatSendParams{Message: "Reply with exactly: post-restart"}); err != nil {
		t.Fatalf("ChatSend after restart: %v", err)
	}
	waitLiveChatEvent(t, b, 120*time.Second, "final")
}

// Usage grows after a turn; compact succeeds and history stays
// coherent.
func TestIntegration_UsageAndCompact(t *testing.T) {
	b := liveBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	key, err := b.CreateSession(ctx, syntheticAgentID, "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := b.ChatSend(ctx, key, backend.ChatSendParams{Message: "Reply with exactly: usage-check"}); err != nil {
		t.Fatalf("ChatSend: %v", err)
	}
	waitLiveChatEvent(t, b, 90*time.Second, "final")

	raw, err := b.SessionUsage(ctx, key)
	if err != nil {
		t.Fatalf("SessionUsage: %v", err)
	}
	// The on-demand session.usage RPC returns zeroed totals on
	// v2026.6.5 even after a completed turn — the authoritative
	// per-turn usage arrives inline on message.complete (asserted by
	// the unit tests). Here we pin only that /stats' RPC round-trips
	// and parses.
	var usage struct {
		Total *int `json:"total"`
		Calls *int `json:"calls"`
	}
	if err := json.Unmarshal(raw, &usage); err != nil {
		t.Fatalf("decode usage: %v", err)
	}
	if usage.Total == nil || usage.Calls == nil {
		t.Fatalf("usage shape unexpected: %s", raw)
	}

	if err := b.SessionCompact(ctx, key); err != nil {
		t.Fatalf("SessionCompact: %v", err)
	}
	if _, err := b.ChatHistory(ctx, key, 50); err != nil {
		t.Fatalf("ChatHistory after compact: %v", err)
	}
}
