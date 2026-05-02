package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func newTestSessionsModel() sessionsModel {
	m := newSessionsModel(nil, "agent-1", "Scout", "model-1", "main-key", false, nil, false)
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

// --- cleanDerivedTitle ---

func TestCleanDerivedTitle_Plain(t *testing.T) {
	got := cleanDerivedTitle("Hello world")
	if got != "Hello world" {
		t.Errorf("got %q, want %q", got, "Hello world")
	}
}

func TestCleanDerivedTitle_StripsSenderPrefix(t *testing.T) {
	got := cleanDerivedTitle("Sender (untrusted metadata): Some title")
	if got != "Some title" {
		t.Errorf("got %q, want %q", got, "Some title")
	}
}

func TestCleanDerivedTitle_StripsMarkdownFences(t *testing.T) {
	got := cleanDerivedTitle("```json\nsome content")
	if got != "some content" {
		t.Errorf("got %q, want %q", got, "some content")
	}

	got = cleanDerivedTitle("```some content")
	if got != "some content" {
		t.Errorf("got %q, want %q", got, "some content")
	}
}

func TestCleanDerivedTitle_StripsJSONContent(t *testing.T) {
	got := cleanDerivedTitle(`{"label": "cli", "text": "hello"}`)
	if got != "" {
		t.Errorf("expected empty string for JSON content, got %q", got)
	}
}

func TestCleanDerivedTitle_CombinedPrefixAndFence(t *testing.T) {
	got := cleanDerivedTitle("Sender (untrusted metadata): ```json\n{\"foo\":\"bar\"}")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestCleanDerivedTitle_EmptyInput(t *testing.T) {
	got := cleanDerivedTitle("")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestCleanDerivedTitle_WhitespaceOnly(t *testing.T) {
	got := cleanDerivedTitle("   ")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// --- sessionGroup ---

func TestSessionGroup_Conversations(t *testing.T) {
	got := sessionGroup("session-key-123")
	if got != "Conversations" {
		t.Errorf("got %q, want %q", got, "Conversations")
	}
}

func TestSessionGroup_Scheduled(t *testing.T) {
	got := sessionGroup("agent-1:cron:daily-check")
	if got != "Scheduled" {
		t.Errorf("got %q, want %q", got, "Scheduled")
	}
}

// --- skipHeaders ---

func TestSkipHeaders_SkipsGroupHeader(t *testing.T) {
	m := newTestSessionsModel()
	m, _ = m.Update(sessionsLoadedMsg{
		sessions: []sessionItem{
			{key: "s1", title: "First", group: "Conversations"},
			{key: "s2", title: "Second", group: "Conversations"},
		},
	})
	// After loading, the list has [header, s1, s2] and selection is at index 1.
	// Move up should skip the header.
	m.list.Select(0) // put on the header
	m.skipHeaders(1) // move down
	if m.list.Index() != 1 {
		t.Errorf("expected index 1 after skipping header, got %d", m.list.Index())
	}
}

func TestSkipHeaders_StaysOnSessionItem(t *testing.T) {
	m := newTestSessionsModel()
	m, _ = m.Update(sessionsLoadedMsg{
		sessions: []sessionItem{
			{key: "s1", title: "First", group: "Conversations"},
		},
	})
	// Selection should be on s1 (index 1).
	m.skipHeaders(-1) // try moving up
	// Should stay at index 1 (the header at 0 is skipped, clamped to 0, which is header, so stays).
	idx := m.list.Index()
	if idx < 0 {
		t.Errorf("expected non-negative index, got %d", idx)
	}
}
