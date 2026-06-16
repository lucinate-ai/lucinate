//go:build integration

package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
	openclawBackend "github.com/lucinate-ai/lucinate/internal/backend/openclaw"
)

// TestConnectionSmoke_Integration is the version-matrix smoke test. Connecting
// at all proves protocol negotiation and pairing work against the gateway
// version under test (this is the regression that the v3→v4 protocol bump
// broke); the chat round-trip then proves an end-to-end turn against the
// configured model. With the echomodel provider it runs with no external model
// and no API charge.
func TestConnectionSmoke_Integration(t *testing.T) {
	c := connectTestClient(t)
	defer c.Close()
	// connectTestClient fatals on connect failure, so reaching here means the
	// handshake (protocol negotiation + device auth) succeeded.

	agents, err := c.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if agents.MainKey == "" {
		t.Fatal("gateway returned no main session key")
	}

	b := openclawBackend.New(c)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := b.ChatSend(ctx, agents.MainKey, backend.ChatSendParams{
		Message:        "ping",
		IdempotencyKey: "smoke-1",
	})
	if err != nil {
		t.Fatalf("chat send: %v", err)
	}
	t.Logf("chat send accepted: runId=%s status=%s", res.RunID, res.Status)

	// Wait for the turn to finish: chat.final (success) or chat.error/aborted.
	// The first chat on a freshly started gateway can be slow: older gateways
	// lazily install agent-runtime dependencies on the first agent run, which
	// blocks the event loop for tens of seconds before the model is invoked.
	deadline := time.After(240 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for chat.final")
		case ev := <-c.Events():
			if ev.EventName != protocol.EventChat {
				continue
			}
			var chatEv protocol.ChatEvent
			if json.Unmarshal(ev.Payload, &chatEv) != nil {
				continue
			}
			if res.RunID != "" && chatEv.RunID != "" && chatEv.RunID != res.RunID {
				continue
			}
			switch chatEv.State {
			case "final":
				text := strings.TrimSpace(backend.ExtractChatText(chatEv.Message))
				if text == "" {
					t.Fatal("chat.final had empty content")
				}
				t.Logf("chat.final: %s", text)
				return
			case "error", "aborted":
				t.Fatalf("chat %s: %s", chatEv.State, chatEv.ErrorMessage)
			}
		}
	}
}
