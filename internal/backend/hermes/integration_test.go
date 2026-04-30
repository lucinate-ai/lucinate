//go:build integration_hermes

// Integration tests for the Hermes backend, exercised against a real
// Hermes API server brought up by test/integration/setup-hermes.sh.
// Tests are protocol-level — they assert on the event state machine
// and on chain walking, not on response content.
//
// Run with:
//
//	make test-integration-hermes-setup
//	make test-integration-hermes
//	make test-integration-hermes-teardown

package hermes

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/a3tai/openclaw-go/protocol"
	"github.com/joho/godotenv"

	"github.com/lucinate-ai/lucinate/internal/backend"
)

// projectRoot resolves to the repo root from this test file.
func projectRoot() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
}

// connectTestBackend builds a Backend pointed at the live Hermes API
// server using env vars from .env.hermes. HOME is overridden to a
// tempdir so the per-connection last_response_id pointer is isolated
// from the developer's real ~/.lucinate/.
func connectTestBackend(t *testing.T) *Backend {
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
	model := os.Getenv("LUCINATE_HERMES_DEFAULT_MODEL")
	if model == "" {
		model = "qwen2.5:0.5b"
	}

	b, err := New(Options{
		ConnectionID: "test-" + t.Name(),
		BaseURL:      baseURL,
		APIKey:       os.Getenv("LUCINATE_HERMES_API_KEY"),
		DefaultModel: model,
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

func TestIntegration_Connect(t *testing.T) {
	connectTestBackend(t)
}

func TestIntegration_ListAgents_ReturnsSyntheticEntry(t *testing.T) {
	b := connectTestBackend(t)
	res, err := b.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(res.Agents) != 1 {
		t.Fatalf("expected exactly 1 synthetic agent, got %d", len(res.Agents))
	}
	if res.Agents[0].ID != syntheticAgentID {
		t.Errorf("agent ID = %q want %q", res.Agents[0].ID, syntheticAgentID)
	}
}

func TestIntegration_CreateAgent_Rejected(t *testing.T) {
	b := connectTestBackend(t)
	err := b.CreateAgent(context.Background(), backend.CreateAgentParams{Name: "anything"})
	if err == nil {
		t.Error("expected CreateAgent to reject on a Hermes connection")
	}
}

func TestIntegration_ChatSendStreams(t *testing.T) {
	b := connectTestBackend(t)

	if _, err := b.ChatSend(context.Background(), syntheticAgentID, backend.ChatSendParams{
		Message:        "say hi briefly",
		IdempotencyKey: "idem-1",
	}); err != nil {
		t.Fatalf("ChatSend: %v", err)
	}

	timeout := time.After(120 * time.Second)
	sawDelta := false
	for {
		select {
		case ev := <-b.Events():
			ce := decodeChatEvent(t, ev)
			switch ce.State {
			case "delta":
				sawDelta = true
			case "final":
				if !sawDelta {
					t.Error("final arrived before any delta")
				}
				if b.lastResponseID == "" {
					t.Error("expected lastResponseID to be persisted")
				}
				return
			case "error":
				t.Fatalf("chat returned error: %s", ce.ErrorMessage)
			}
		case <-timeout:
			t.Fatal("timed out waiting for final event")
		}
	}
}

func TestIntegration_ChatHistory_RoundTripsThroughServer(t *testing.T) {
	b := connectTestBackend(t)

	// First turn establishes a response id we can chain off.
	if _, err := b.ChatSend(context.Background(), syntheticAgentID, backend.ChatSendParams{
		Message:        "what is 2+2? answer in one word",
		IdempotencyKey: "h1",
	}); err != nil {
		t.Fatalf("ChatSend: %v", err)
	}
	drainToFinal(t, b, 120*time.Second)

	raw, err := b.ChatHistory(context.Background(), syntheticAgentID, 0)
	if err != nil {
		t.Fatalf("ChatHistory: %v", err)
	}
	var hist struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &hist); err != nil {
		t.Fatalf("decode history: %v\nraw: %s", err, raw)
	}
	if len(hist.Messages) < 2 {
		t.Fatalf("expected ≥2 messages walked from server, got %d (raw: %s)", len(hist.Messages), raw)
	}
	if hist.Messages[0].Role != "user" {
		t.Errorf("first walked role = %q want user", hist.Messages[0].Role)
	}
	if hist.Messages[1].Role != "assistant" {
		t.Errorf("second walked role = %q want assistant", hist.Messages[1].Role)
	}
}

func TestIntegration_ChatAbort(t *testing.T) {
	b := connectTestBackend(t)

	// Ask for something long so the abort window is wide enough.
	res, err := b.ChatSend(context.Background(), syntheticAgentID, backend.ChatSendParams{
		Message:        "count from 1 to 50, one number per line",
		IdempotencyKey: "abort",
	})
	if err != nil {
		t.Fatalf("ChatSend: %v", err)
	}

	// Wait for the first delta then abort.
	select {
	case ev := <-b.Events():
		_ = decodeChatEvent(t, ev)
	case <-time.After(60 * time.Second):
		t.Fatal("timed out waiting for first delta before abort")
	}

	if err := b.ChatAbort(context.Background(), syntheticAgentID, res.RunID); err != nil {
		t.Fatalf("ChatAbort: %v", err)
	}

	timeout := time.After(15 * time.Second)
	for {
		select {
		case ev := <-b.Events():
			ce := decodeChatEvent(t, ev)
			if ce.State == "aborted" {
				return
			}
		case <-timeout:
			t.Fatal("timed out waiting for aborted event")
		}
	}
}

// drainToFinal blocks until a chat-final event arrives or the timeout
// fires. Used by tests that need to roll past a streaming turn before
// asserting on follow-up state (history, lastResponseID).
func drainToFinal(t *testing.T, b *Backend, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-b.Events():
			ce := decodeChatEvent(t, ev)
			switch ce.State {
			case "final":
				return
			case "error":
				t.Fatalf("error event: %s", ce.ErrorMessage)
			}
		case <-deadline:
			t.Fatal("timed out draining to final")
		}
	}
}

func decodeChatEvent(t *testing.T, ev protocol.Event) protocol.ChatEvent {
	t.Helper()
	if ev.EventName != protocol.EventChat {
		t.Fatalf("unexpected event: %s", ev.EventName)
	}
	var ce protocol.ChatEvent
	if err := json.Unmarshal(ev.Payload, &ce); err != nil {
		t.Fatalf("decode chat event: %v", err)
	}
	return ce
}
