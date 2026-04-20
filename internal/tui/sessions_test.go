package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func newTestSessionsModel() sessionsModel {
	m := newSessionsModel(nil, "agent-1", "Scout", "model-1", "main-key")
	m.setSize(120, 40)
	return m
}

func TestSessionsLoadedMsg_PopulatesList(t *testing.T) {
	m := newTestSessionsModel()
	m, _ = m.Update(sessionsLoadedMsg{
		sessions: []sessionItem{
			{key: "s1", title: "First chat"},
			{key: "s2", title: "Second chat"},
		},
	})
	if m.loading {
		t.Error("expected loading to be false after sessionsLoadedMsg")
	}
	if len(m.list.Items()) != 2 {
		t.Errorf("expected 2 items, got %d", len(m.list.Items()))
	}
}

func TestSessionsLoadedMsg_Error(t *testing.T) {
	m := newTestSessionsModel()
	m, _ = m.Update(sessionsLoadedMsg{err: errString("gateway error")})
	if m.err == nil {
		t.Error("expected error to be set")
	}
	if m.loading {
		t.Error("expected loading to be false")
	}
}

func TestSessionsKey_Enter_SelectsSession(t *testing.T) {
	m := newTestSessionsModel()
	m, _ = m.Update(sessionsLoadedMsg{
		sessions: []sessionItem{
			{key: "s1", title: "First chat"},
		},
	})
	m, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a cmd from enter")
	}
	msg := cmd()
	sel, ok := msg.(sessionSelectedMsg)
	if !ok {
		t.Fatalf("expected sessionSelectedMsg, got %T", msg)
	}
	if sel.sessionKey != "s1" {
		t.Errorf("expected session key %q, got %q", "s1", sel.sessionKey)
	}
}

func TestSessionsKey_Esc_GoesBack(t *testing.T) {
	m := newTestSessionsModel()
	m, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	_ = m
	if cmd == nil {
		t.Fatal("expected a cmd from esc")
	}
	msg := cmd()
	if _, ok := msg.(goBackFromSessionsMsg); !ok {
		t.Errorf("expected goBackFromSessionsMsg, got %T", msg)
	}
}

func TestSessionsKey_N_WhenLoading_Ignored(t *testing.T) {
	m := newTestSessionsModel()
	// loading is true by default
	m, cmd := m.handleKey(tea.KeyPressMsg{Code: 'n'})
	_ = m
	if cmd != nil {
		t.Error("expected nil cmd when loading")
	}
}

func TestSessionsKey_R_RetriesOnError(t *testing.T) {
	m := newTestSessionsModel()
	m.loading = false
	m.err = errString("some error")
	m, cmd := m.handleKey(tea.KeyPressMsg{Code: 'r'})
	if !m.loading {
		t.Error("expected loading to be true after retry")
	}
	if m.err != nil {
		t.Error("expected err to be cleared after retry")
	}
	if cmd == nil {
		t.Error("expected a loadSessions cmd")
	}
}

func TestSessionsView_Loading(t *testing.T) {
	m := newTestSessionsModel()
	view := m.View()
	if view == "" {
		t.Error("expected non-empty view")
	}
}

func TestSessionsView_Empty(t *testing.T) {
	m := newTestSessionsModel()
	m.loading = false
	m, _ = m.Update(sessionsLoadedMsg{sessions: nil})
	view := m.View()
	if view == "" {
		t.Error("expected non-empty view")
	}
}
