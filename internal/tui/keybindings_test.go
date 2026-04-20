package tui

import (
	"testing"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

func TestNewChatModel_DeleteWordBackwardBinding(t *testing.T) {
	m := newChatModel(nil, "main", "test", "")

	// ctrl+w should match DeleteWordBackward.
	ctrlW := tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl}
	if !key.Matches(ctrlW, m.textarea.KeyMap.DeleteWordBackward) {
		t.Errorf("ctrl+w should match DeleteWordBackward, got string=%q", ctrlW.String())
	}

	// alt+backspace should also match DeleteWordBackward.
	altBS := tea.KeyPressMsg{Code: tea.KeyBackspace, Mod: tea.ModAlt}
	if !key.Matches(altBS, m.textarea.KeyMap.DeleteWordBackward) {
		t.Errorf("alt+backspace should match DeleteWordBackward, got string=%q", altBS.String())
	}

	// Plain backspace should NOT match DeleteWordBackward.
	plainBS := tea.KeyPressMsg{Code: tea.KeyBackspace}
	if key.Matches(plainBS, m.textarea.KeyMap.DeleteWordBackward) {
		t.Error("plain backspace should not match DeleteWordBackward")
	}
}

func TestNewChatModel_InsertNewlineBinding(t *testing.T) {
	m := newChatModel(nil, "main", "test", "")

	// Plain enter should NOT match InsertNewline.
	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	if key.Matches(enter, m.textarea.KeyMap.InsertNewline) {
		t.Error("plain enter should not match InsertNewline")
	}

	// Shift+enter SHOULD match InsertNewline.
	shiftEnter := tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}
	if !key.Matches(shiftEnter, m.textarea.KeyMap.InsertNewline) {
		t.Errorf("shift+enter should match InsertNewline, got string=%q", shiftEnter.String())
	}
}

func TestUpKey_RecallsLastQueuedMessage(t *testing.T) {
	m := newChatModel(nil, "main", "test", "")
	m.viewport = viewport.New()
	m.width = 80
	m.height = 30
	m.pendingMessages = []string{"first", "second", "third"}

	up := tea.KeyPressMsg{Code: tea.KeyUp}
	m, _ = m.Update(up)

	if got, want := m.textarea.Value(), "third"; got != want {
		t.Errorf("textarea value: got %q, want %q", got, want)
	}
	if got, want := len(m.pendingMessages), 2; got != want {
		t.Fatalf("pendingMessages length: got %d, want %d", got, want)
	}
	if m.pendingMessages[0] != "first" || m.pendingMessages[1] != "second" {
		t.Errorf("remaining pending: got %v, want [first second]", m.pendingMessages)
	}
}

func TestUpKey_NoQueuedMessagesLeavesInputEmpty(t *testing.T) {
	m := newChatModel(nil, "main", "test", "")
	m.viewport = viewport.New()
	m.width = 80
	m.height = 30

	up := tea.KeyPressMsg{Code: tea.KeyUp}
	m, _ = m.Update(up)

	if got := m.textarea.Value(); got != "" {
		t.Errorf("textarea should remain empty, got %q", got)
	}
}

func TestUpKey_NonEmptyInputDoesNotRecall(t *testing.T) {
	m := newChatModel(nil, "main", "test", "")
	m.viewport = viewport.New()
	m.width = 80
	m.height = 30
	m.textarea.SetValue("in progress")
	m.pendingMessages = []string{"queued"}

	up := tea.KeyPressMsg{Code: tea.KeyUp}
	m, _ = m.Update(up)

	if got, want := m.textarea.Value(), "in progress"; got != want {
		t.Errorf("textarea value: got %q, want %q", got, want)
	}
	if len(m.pendingMessages) != 1 || m.pendingMessages[0] != "queued" {
		t.Errorf("pendingMessages should be untouched, got %v", m.pendingMessages)
	}
}
