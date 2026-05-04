package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/lucinate-ai/lucinate/internal/config"
)

func TestNewChatModel_DeleteWordBackwardBinding(t *testing.T) {
	m := newChatModel(nil, "main", "", "test", "", config.DefaultPreferences(), false, "", "")

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
	m := newChatModel(nil, "main", "", "test", "", config.DefaultPreferences(), false, "", "")

	// Plain enter should NOT match InsertNewline.
	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	if key.Matches(enter, m.textarea.KeyMap.InsertNewline) {
		t.Error("plain enter should not match InsertNewline")
	}

	// Alt+enter SHOULD match InsertNewline.
	altEnter := tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}
	if !key.Matches(altEnter, m.textarea.KeyMap.InsertNewline) {
		t.Errorf("alt+enter should match InsertNewline, got string=%q", altEnter.String())
	}
}

func TestUpKey_RecallsLastQueuedMessage(t *testing.T) {
	m := newChatModel(nil, "main", "", "test", "", config.DefaultPreferences(), false, "", "")
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
	m := newChatModel(nil, "main", "", "test", "", config.DefaultPreferences(), false, "", "")
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
	m := newChatModel(nil, "main", "", "test", "", config.DefaultPreferences(), false, "", "")
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

func TestUpKey_RecallingOnlyMessageEmptiesQueue(t *testing.T) {
	m := newChatModel(nil, "main", "", "test", "", config.DefaultPreferences(), false, "", "")
	m.viewport = viewport.New()
	m.width = 80
	m.height = 30
	m.pendingMessages = []string{"solo"}

	up := tea.KeyPressMsg{Code: tea.KeyUp}
	m, _ = m.Update(up)

	if got, want := m.textarea.Value(), "solo"; got != want {
		t.Errorf("textarea value: got %q, want %q", got, want)
	}
	if len(m.pendingMessages) != 0 {
		t.Errorf("pendingMessages should be empty, got %v", m.pendingMessages)
	}
}

func TestUpKey_SuccessiveRecallsPopInLIFOOrder(t *testing.T) {
	m := newChatModel(nil, "main", "", "test", "", config.DefaultPreferences(), false, "", "")
	m.viewport = viewport.New()
	m.width = 80
	m.height = 30
	m.pendingMessages = []string{"a", "b", "c"}

	up := tea.KeyPressMsg{Code: tea.KeyUp}

	m, _ = m.Update(up)
	if got, want := m.textarea.Value(), "c"; got != want {
		t.Fatalf("first recall: got %q, want %q", got, want)
	}

	// Clearing the textarea lets the next up press recall again.
	m.textarea.Reset()
	m, _ = m.Update(up)
	if got, want := m.textarea.Value(), "b"; got != want {
		t.Fatalf("second recall: got %q, want %q", got, want)
	}

	m.textarea.Reset()
	m, _ = m.Update(up)
	if got, want := m.textarea.Value(), "a"; got != want {
		t.Fatalf("third recall: got %q, want %q", got, want)
	}

	if len(m.pendingMessages) != 0 {
		t.Errorf("expected empty queue after three recalls, got %v", m.pendingMessages)
	}
}

func TestUpKey_RecallThenClearDiscardsMessage(t *testing.T) {
	m := newChatModel(nil, "main", "", "test", "", config.DefaultPreferences(), false, "", "")
	m.viewport = viewport.New()
	m.width = 80
	m.height = 30
	m.pendingMessages = []string{"keep", "discard"}

	up := tea.KeyPressMsg{Code: tea.KeyUp}
	m, _ = m.Update(up)

	// User decides to delete the recalled message by clearing the input and
	// not pressing enter. The queued message should stay gone.
	m.textarea.Reset()

	if len(m.pendingMessages) != 1 || m.pendingMessages[0] != "keep" {
		t.Errorf("pendingMessages: got %v, want [keep]", m.pendingMessages)
	}
}

func TestUpKey_RecallEditAndRequeueWhileSending(t *testing.T) {
	m := newChatModel(nil, "main", "", "test", "", config.DefaultPreferences(), false, "", "")
	m.viewport = viewport.New()
	m.width = 80
	m.height = 30
	m.sending = true
	m.pendingMessages = []string{"original"}

	up := tea.KeyPressMsg{Code: tea.KeyUp}
	m, _ = m.Update(up)
	if got, want := m.textarea.Value(), "original"; got != want {
		t.Fatalf("recall: got %q, want %q", got, want)
	}

	// Edit the recalled text and press enter while the agent is still
	// responding — it should re-queue with the new contents.
	m.textarea.SetValue("edited")
	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	m, _ = m.Update(enter)

	if len(m.pendingMessages) != 1 || m.pendingMessages[0] != "edited" {
		t.Errorf("pendingMessages: got %v, want [edited]", m.pendingMessages)
	}
	if got := m.textarea.Value(); got != "" {
		t.Errorf("textarea should be reset after enter, got %q", got)
	}
}

func TestView_HelpShowsUpHintWhenQueued(t *testing.T) {
	m := newChatModel(nil, "main", "", "test", "", config.DefaultPreferences(), false, "", "")
	m.viewport = viewport.New()
	m.setSize(80, 30)
	m.pendingMessages = []string{"one", "two"}

	view := m.View()
	if !strings.Contains(view, "2 queued (up: edit last)") {
		t.Errorf("expected help text to advertise up-arrow recall, got:\n%s", view)
	}
}
