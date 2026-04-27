package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/lucinate-ai/lucinate/internal/client"
)

func TestApplyConnState_DropsStreamingPlaceholderOnDisconnect(t *testing.T) {
	m := newTestChatModel()
	m.connState = ConnStateMsg{Status: client.StatusConnected}
	m.sending = true
	m.runID = "run-123"
	m.messages = []chatMessage{
		{role: "user", content: "hi"},
		{role: "assistant", streaming: true, awaitingDelta: true},
	}

	m.applyConnState(ConnStateMsg{Status: client.StatusDisconnected})

	if m.sending {
		t.Errorf("sending should be cleared on disconnect during in-flight stream")
	}
	if m.runID != "" {
		t.Errorf("runID should be cleared on disconnect: got %q", m.runID)
	}
	if len(m.messages) != 2 {
		t.Fatalf("want 2 messages (user + system note), got %d: %+v", len(m.messages), m.messages)
	}
	last := m.messages[len(m.messages)-1]
	if last.role != "system" || !strings.Contains(last.content, "Lost gateway connection") {
		t.Errorf("expected system disconnect note, got role=%s content=%q", last.role, last.content)
	}
}

func TestApplyConnState_NoSystemNoteOnInitialDisconnect(t *testing.T) {
	// If the supervisor sends a Disconnected state before we ever observed
	// Connected (e.g. a startup race), don't pollute the chat with a stale
	// "lost connection" note for a connection we never had.
	m := newTestChatModel()
	// Note: m.connState defaults to zero value, which is StatusConnected (iota 0).
	// To exercise the "never connected" path we set it explicitly to a non-Connected sentinel.
	m.connState = ConnStateMsg{Status: client.StatusReconnecting}

	m.applyConnState(ConnStateMsg{Status: client.StatusDisconnected})

	for _, msg := range m.messages {
		if strings.Contains(msg.content, "Lost gateway connection") {
			t.Fatalf("should not have added disconnect note when prev was not Connected; messages=%+v", m.messages)
		}
	}
}

func TestApplyConnState_AddsReconnectedNoteOnRecovery(t *testing.T) {
	m := newTestChatModel()
	m.connState = ConnStateMsg{Status: client.StatusReconnecting, Attempt: 3}

	m.applyConnState(ConnStateMsg{Status: client.StatusConnected})

	if len(m.messages) != 1 {
		t.Fatalf("want 1 message, got %d", len(m.messages))
	}
	if !strings.Contains(m.messages[0].content, "Reconnected") {
		t.Errorf("expected reconnected note, got %q", m.messages[0].content)
	}
}

func TestApplyConnState_AuthFailedAddsErrorMessage(t *testing.T) {
	m := newTestChatModel()
	m.applyConnState(ConnStateMsg{
		Status: client.StatusAuthFailed,
		Err:    errors.New("connect: gateway token mismatch"),
	})

	if len(m.messages) != 1 {
		t.Fatalf("want 1 message, got %d", len(m.messages))
	}
	if m.messages[0].errMsg == "" {
		t.Errorf("expected errMsg on auth-failed system message")
	}
	if !strings.Contains(m.messages[0].errMsg, "restart") {
		t.Errorf("auth-failed message should instruct user to restart: %q", m.messages[0].errMsg)
	}
}

func TestConnectionBadge(t *testing.T) {
	cases := []struct {
		name     string
		state    ConnStateMsg
		wantSubs string // substring expected in the rendered (styled) badge
	}{
		{"connected is empty", ConnStateMsg{Status: client.StatusConnected}, ""},
		{"disconnected", ConnStateMsg{Status: client.StatusDisconnected}, "disconnected"},
		{"reconnecting first attempt", ConnStateMsg{Status: client.StatusReconnecting, Attempt: 1}, "reconnecting"},
		{"reconnecting later attempt", ConnStateMsg{Status: client.StatusReconnecting, Attempt: 4}, "attempt 4"},
		{"auth failed", ConnStateMsg{Status: client.StatusAuthFailed}, "auth failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := connectionBadge(tc.state)
			if tc.wantSubs == "" {
				if got != "" {
					t.Errorf("badge: want empty, got %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.wantSubs) {
				t.Errorf("badge: want substring %q, got %q", tc.wantSubs, got)
			}
		})
	}
}
