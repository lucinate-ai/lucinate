//go:build integration_openai

// Integration tests for the OpenAI-compatible backend, exercised
// against a real /v1 endpoint (typically Ollama, set up by
// test/integration/setup-openai.sh). These tests are protocol-level —
// they assert on the event state machine and on-disk persistence, not
// on response content (model output is non-deterministic).
//
// Run with:
//
//	make test-integration-openai-setup
//	make test-integration-openai
//	make test-integration-openai-teardown

package openai

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

// projectRoot is two levels up from this file (internal/backend/openai
// → repo root). Used to locate the .env.openai file written by setup.
func projectRoot() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
}

// connectTestBackend builds a Backend pointed at the live /v1 endpoint
// using env vars from .env.openai. HOME is overridden to a tempdir so
// the agent store is isolated from the developer's real
// ~/.lucinate/agents/.
func connectTestBackend(t *testing.T) *Backend {
	t.Helper()
	envFile := filepath.Join(projectRoot(), ".env.openai")
	if _, err := os.Stat(envFile); err == nil {
		_ = godotenv.Load(envFile)
	}
	t.Setenv("HOME", t.TempDir())

	baseURL := os.Getenv("LUCINATE_OPENAI_BASE_URL")
	if baseURL == "" {
		t.Skip("LUCINATE_OPENAI_BASE_URL not set — run make test-integration-openai-setup first")
	}
	model := os.Getenv("LUCINATE_OPENAI_DEFAULT_MODEL")
	if model == "" {
		model = "qwen2.5:0.5b"
	}

	b, err := New(Options{
		ConnectionID: "test",
		BaseURL:      baseURL,
		APIKey:       os.Getenv("LUCINATE_OPENAI_API_KEY"),
		DefaultModel: model,
	})
	if err != nil {
		t.Fatalf("backend: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := b.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return b
}

func TestIntegration_Connect(t *testing.T) {
	connectTestBackend(t)
}

func TestIntegration_ModelsList(t *testing.T) {
	b := connectTestBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := b.ModelsList(ctx)
	if err != nil {
		t.Fatalf("ModelsList: %v", err)
	}
	if len(result.Models) == 0 {
		t.Fatal("expected at least one model from the live endpoint")
	}
	wantModel := os.Getenv("LUCINATE_OPENAI_DEFAULT_MODEL")
	if wantModel == "" {
		return
	}
	for _, m := range result.Models {
		if m.ID == wantModel {
			return
		}
	}
	t.Errorf("default model %q not in models list (got %d models)", wantModel, len(result.Models))
}

// TestIntegration_ChatSendStreams drives a full chat turn through the
// backend: agent creation, ChatSend, stream drain to a final event,
// and on-disk history persistence. Asserts on the protocol shape
// (delta → final transition, history.jsonl present) — never on
// response content, which is non-deterministic.
func TestIntegration_ChatSendStreams(t *testing.T) {
	b := connectTestBackend(t)

	if err := b.CreateAgent(context.Background(), backend.CreateAgentParams{
		Name:     "integration-test",
		Identity: "You are a terse assistant.",
		Soul:     "Reply with a single short sentence.",
		Model:    os.Getenv("LUCINATE_OPENAI_DEFAULT_MODEL"),
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if _, err := b.ChatSend(context.Background(), "integration-test", "say hi", "idem-1"); err != nil {
		t.Fatalf("ChatSend: %v", err)
	}

	timeout := time.After(60 * time.Second)
	sawDelta := false
	for {
		select {
		case ev := <-b.events:
			ce := decodeChat(t, ev)
			switch ce.State {
			case "delta":
				sawDelta = true
			case "final":
				if !sawDelta {
					t.Error("final arrived before any delta")
				}
				assertHistoryPersisted(t, b, "integration-test")
				return
			case "error":
				t.Fatalf("chat returned error: %s", ce.ErrorMessage)
			}
		case <-timeout:
			t.Fatal("timed out waiting for final event")
		}
	}
}

// TestIntegration_ChatAbort starts a stream and aborts it. The backend
// should emit an aborted event and stop streaming further deltas.
func TestIntegration_ChatAbort(t *testing.T) {
	b := connectTestBackend(t)

	if err := b.CreateAgent(context.Background(), backend.CreateAgentParams{
		Name:  "abort-test",
		Model: os.Getenv("LUCINATE_OPENAI_DEFAULT_MODEL"),
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// Ask for something long so the abort window is wide enough that
	// the test isn't racing the "final" event of a one-token reply.
	res, err := b.ChatSend(context.Background(), "abort-test", "count from 1 to 50, one per line", "idem-abort")
	if err != nil {
		t.Fatalf("ChatSend: %v", err)
	}
	runID := res.RunID

	// Wait for the first delta so we know the stream has started, then
	// fire the abort.
	select {
	case ev := <-b.events:
		_ = decodeChat(t, ev)
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for first delta before abort")
	}

	if err := b.ChatAbort(context.Background(), "abort-test", runID); err != nil {
		t.Fatalf("ChatAbort: %v", err)
	}

	timeout := time.After(15 * time.Second)
	for {
		select {
		case ev := <-b.events:
			ce := decodeChat(t, ev)
			if ce.State == "aborted" {
				return
			}
			// Drain any pre-cancellation deltas.
		case <-timeout:
			t.Fatal("timed out waiting for aborted event")
		}
	}
}

func TestIntegration_SessionPatchModelPersists(t *testing.T) {
	b := connectTestBackend(t)
	if err := b.CreateAgent(context.Background(), backend.CreateAgentParams{
		Name:  "patch-test",
		Model: "old-model",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := b.SessionPatchModel(context.Background(), "patch-test", "new-model"); err != nil {
		t.Fatalf("SessionPatchModel: %v", err)
	}
	meta, err := b.store.LoadMeta("patch-test")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.Model != "new-model" {
		t.Errorf("Model = %q want %q", meta.Model, "new-model")
	}
}

// decodeChat unwraps a protocol.Event into its ChatEvent payload, or
// fatals the test if the event isn't a chat event.
func decodeChat(t *testing.T, ev protocol.Event) protocol.ChatEvent {
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

// assertHistoryPersisted checks the agent's transcript made it to
// disk: a user line and an assistant line, in that order.
func assertHistoryPersisted(t *testing.T, b *Backend, agentID string) {
	t.Helper()
	msgs, err := b.store.LoadHistory(agentID, 0)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages persisted, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("first message role = %q want user", msgs[0].Role)
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("second message role = %q want assistant", msgs[1].Role)
	}
	if msgs[1].Content == "" {
		t.Error("assistant message has empty content")
	}
}
