package tui

import (
	"context"
	"encoding/json"
	"testing"

	"charm.land/bubbles/v2/list"
	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// TestAppModel_OpenAgent_PrefersMessagingSession: opening an agent that
// has a Telegram DM must resume that conversation, not a blank "main"
// session. This drives the picker selection path end to end so it also
// covers the sessions.list lookup wired into the create command.
func TestAppModel_OpenAgent_PrefersMessagingSession(t *testing.T) {
	fake := newFakeBackend()
	fake.sessionsListHook = func(ctx context.Context, agentID string) (json.RawMessage, error) {
		return json.RawMessage(`{"sessions":[
			{"key":"agent:main:main","kind":"global","updatedAt":9000},
			{"key":"agent:main:telegram:direct:123456789","kind":"direct",
			 "deliveryContext":{"channel":"telegram","to":"123456789"},"updatedAt":1000}
		]}`), nil
	}
	// Default CreateSession echoes the key back, so the resolved session
	// key in the message is exactly what the open path chose.

	m := AppModel{
		state:   viewSelect,
		backend: fake,
		prefs:   config.Preferences{ConnectTimeoutSeconds: 1},
	}
	m.selectModel = newSelectModel(fake, true, false, nil, false, "")
	m.selectModel.list.SetItems([]list.Item{
		agentItem{agent: protocol.AgentSummary{ID: "main", Name: "main"}, sessionKey: "main"},
	})
	m.selectModel.loading = false
	m.selectModel.selected = true

	_, cmd := m.update(agentsLoadedMsg{result: &protocol.AgentsListResult{
		Agents: []protocol.AgentSummary{{ID: "main", Name: "main"}},
	}})
	if cmd == nil {
		t.Fatal("expected a session-create command after agent selection")
	}

	msg, ok := cmd().(sessionCreatedMsg)
	if !ok {
		t.Fatalf("expected sessionCreatedMsg, got %T", cmd())
	}
	if msg.err != nil {
		t.Fatalf("unexpected error: %v", msg.err)
	}
	if want := "agent:main:telegram:direct:123456789"; msg.sessionKey != want {
		t.Errorf("opened session %q, want the Telegram DM %q", msg.sessionKey, want)
	}
}
