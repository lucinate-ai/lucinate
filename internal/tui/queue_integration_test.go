//go:build integration

package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/a3tai/openclaw-go/protocol"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/glamour"
	"github.com/joho/godotenv"

	"github.com/outofcoffee/repclaw/internal/client"
	"github.com/outofcoffee/repclaw/internal/config"
)

// projectRoot returns the repo root (two levels up from this file).
func projectRoot() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

// connectTestClient loads .env, creates a client, and connects to the gateway.
func connectTestClient(t *testing.T) *client.Client {
	t.Helper()
	// Load .env from project root since go test runs in the package directory.
	envFile := filepath.Join(projectRoot(), ".env")
	if _, err := os.Stat(envFile); err == nil {
		_ = godotenv.Load(envFile)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	c, err := client.New(cfg)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return c
}

// newIntegrationChatModel creates a chatModel wired to a real client and session.
func newIntegrationChatModel(t *testing.T, c *client.Client) *chatModel {
	t.Helper()
	agents, err := c.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if len(agents.Agents) == 0 {
		t.Fatal("no agents available on gateway")
	}
	sessionKey := agents.MainKey
	agentName := agents.Agents[0].Name
	if agentName == "" {
		agentName = agents.Agents[0].ID
	}
	t.Logf("agent=%s session=%s", agentName, sessionKey)

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(80),
	)
	m := chatModel{
		viewport:   viewport.New(80, 20),
		client:     c,
		sessionKey: sessionKey,
		agentName:  agentName,
		renderer:   renderer,
		width:      80,
		height:     30,
	}
	return &m
}

// execCmd runs a tea.Cmd and sends results to the channel. Handles tea.BatchMsg
// by fanning out each sub-cmd sequentially (to avoid concurrent WebSocket writes).
func execCmd(cmd tea.Cmd, results chan<- tea.Msg) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if msg == nil {
			return
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			// Run batch cmds sequentially to avoid concurrent WebSocket writes.
			for _, sub := range batch {
				if sub == nil {
					continue
				}
				if subMsg := sub(); subMsg != nil {
					results <- subMsg
				}
			}
			return
		}
		results <- msg
	}()
}

// TestQueueOrdering_Integration verifies that queued messages are delivered
// to the gateway in FIFO order. It connects to the real gateway using .env.
//
// Run with:
//
//	go test -tags integration -run TestQueueOrdering ./internal/tui/ -v -count=1
func TestQueueOrdering_Integration(t *testing.T) {
	c := connectTestClient(t)
	defer c.Close()
	m := newIntegrationChatModel(t, c)

	// Send the first message — this sets m.sending = true.
	firstMsg := "Reply to every message with ONLY the single word 'ack'. No punctuation, no explanation — just 'ack'."
	m.messages = append(m.messages, chatMessage{role: "user", content: firstMsg})
	m.sending = true
	t.Log(">>> sending initial instruction")

	cmdCh := make(chan tea.Msg, 32)
	execCmd(m.sendMessage(firstMsg), cmdCh)

	// Queue messages while the first is in-flight.
	m.pendingMessages = []string{"1", "2", "3"}
	t.Log(">>> queued: [1, 2, 3]")

	// Event loop — process gateway events and cmd results until the queue is
	// fully drained and no response is pending.
	timeout := time.After(90 * time.Second)
	drainCount := 0
	for {
		if !m.sending && len(m.pendingMessages) == 0 {
			t.Log("--- queue fully drained, sending=false")
			break
		}

		select {
		case ev := <-c.Events():
			logTestEvent(t, ev)
			cmd := m.handleEvent(ev)
			execCmd(cmd, cmdCh)

		case msg := <-cmdCh:
			switch msg := msg.(type) {
			case chatSentMsg:
				if msg.err != nil {
					t.Fatalf("send error: %v", msg.err)
				}
				drainCount++
				t.Logf("    chatSentMsg OK (drain #%d)", drainCount)
			case historyRefreshMsg:
				// Do NOT apply — this replaces m.messages with server history
				// and would destroy the ordering we're verifying.
				t.Log("    historyRefreshMsg (skipped)")
			case statsLoadedMsg:
				// ignore
			default:
				t.Logf("    unhandled msg: %T", msg)
			}

		case <-timeout:
			t.Fatalf("TIMEOUT — sending=%v pending=%d messages=%d",
				m.sending, len(m.pendingMessages), len(m.messages))
		}
	}

	// Print the full conversation.
	t.Log("")
	t.Log("=== Conversation ===")
	var userContents []string
	for _, msg := range m.messages {
		prefix := msg.role
		content := msg.content
		if msg.errMsg != "" {
			content = "[error] " + msg.errMsg
		}
		if len(content) > 120 {
			content = content[:120] + "..."
		}
		t.Logf("  %-10s %s", prefix+":", content)
		if msg.role == "user" {
			userContents = append(userContents, msg.content)
		}
	}

	// Verify FIFO order of user messages.
	expected := []string{firstMsg, "1", "2", "3"}
	if len(userContents) != len(expected) {
		t.Fatalf("expected %d user messages, got %d: %v", len(expected), len(userContents), userContents)
	}
	for i, want := range expected {
		if userContents[i] != want {
			t.Errorf("user message[%d]: got %q, want %q", i, userContents[i], want)
		}
	}
	t.Log("")
	t.Log("PASS: queue ordering verified — messages delivered in FIFO order")
}

// logTestEvent logs a gateway event for observability.
func logTestEvent(t *testing.T, ev protocol.Event) {
	t.Helper()
	switch ev.EventName {
	case protocol.EventChat:
		var chatEv protocol.ChatEvent
		if err := json.Unmarshal(ev.Payload, &chatEv); err == nil {
			content := string(chatEv.Message)
			if len(content) > 80 {
				content = content[:80] + "..."
			}
			t.Logf("  event: chat.%s seq=%d msg=%s", chatEv.State, chatEv.Seq, content)
			return
		}
	case protocol.EventExecFinished:
		t.Log("  event: exec.finished")
		return
	case "exec.approval.resolved":
		t.Log("  event: exec.approval.resolved")
		return
	case protocol.EventExecDenied:
		t.Log("  event: exec.denied")
		return
	}
	name := ev.EventName
	if name == "" {
		name = "(empty)"
	}
	t.Logf("  event: %s", name)
}

