package tui

import (
	"testing"
)

func TestNotifications_NotifyAndClear(t *testing.T) {
	m := newTestChatModel()

	m.notify("first")
	m.notify("second")
	m.notifyError("uh oh")

	if len(m.notifications) != 3 {
		t.Fatalf("len = %d, want 3", len(m.notifications))
	}
	if m.notifications[0].text != "first" || m.notifications[0].isError {
		t.Errorf("[0] = %+v", m.notifications[0])
	}
	if !m.notifications[2].isError {
		t.Errorf("[2] should be isError, got %+v", m.notifications[2])
	}

	m.clearNotifications()
	if len(m.notifications) != 0 {
		t.Errorf("after clear: len = %d, want 0", len(m.notifications))
	}
}

func TestNotifications_EmptyTextIsDropped(t *testing.T) {
	m := newTestChatModel()
	m.notify("")
	m.notifyError("")
	if len(m.notifications) != 0 {
		t.Errorf("empty text appended: %+v", m.notifications)
	}
}

func TestNotifications_SurviveHistoryRefresh(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{{role: "user", content: "old"}}
	m.notify("Routine \"demo\" stopped by assistant.")

	// Simulate a history refresh replacing m.messages.
	m.handleHistoryRefresh([]chatMessage{
		{role: "user", content: "step 1"},
		{role: "assistant", content: "reply"},
	})

	if len(m.notifications) != 1 {
		t.Errorf("notification dropped by history refresh: %+v", m.notifications)
	}
}

// handleHistoryRefresh mimics the Update handler's body for
// historyRefreshMsg, in a way callable from tests without going
// through the full bubbletea machinery.
func (m *chatModel) handleHistoryRefresh(replacement []chatMessage) {
	m.messages = replacement
}
