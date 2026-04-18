package tui

import (
	"fmt"
	"testing"

	"github.com/a3tai/openclaw-go/protocol"
)

func TestSelectModel_AutoSelectSingleAgent(t *testing.T) {
	m := newSelectModel(nil)

	msg := agentsLoadedMsg{
		result: &protocol.AgentsListResult{
			DefaultID: "main",
			MainKey:   "main",
			Agents: []protocol.AgentSummary{
				{ID: "main", Name: "My Agent"},
			},
		},
	}

	m, _ = m.Update(msg)

	if !m.selected {
		t.Error("expected auto-select when only one agent exists")
	}
	item, ok := m.selectedAgent()
	if !ok {
		t.Fatal("expected selected agent item")
	}
	if item.agent.ID != "main" {
		t.Errorf("selected agent ID = %q, want %q", item.agent.ID, "main")
	}
	if item.sessionKey != "main" {
		t.Errorf("session key = %q, want %q", item.sessionKey, "main")
	}
}

func TestSelectModel_NoAutoSelectMultipleAgents(t *testing.T) {
	m := newSelectModel(nil)

	msg := agentsLoadedMsg{
		result: &protocol.AgentsListResult{
			DefaultID: "main",
			MainKey:   "main",
			Agents: []protocol.AgentSummary{
				{ID: "main", Name: "Agent One"},
				{ID: "secondary", Name: "Agent Two"},
			},
		},
	}

	m, _ = m.Update(msg)

	if m.selected {
		t.Error("should not auto-select when multiple agents exist")
	}
}

func TestSelectModel_NoAutoSelectOnError(t *testing.T) {
	m := newSelectModel(nil)

	msg := agentsLoadedMsg{
		err: fmt.Errorf("connection failed"),
	}

	m, _ = m.Update(msg)

	if m.selected {
		t.Error("should not auto-select on error")
	}
	if m.err == nil {
		t.Error("expected error to be set")
	}
}
