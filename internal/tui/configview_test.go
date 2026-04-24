package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lucinate-ai/lucinate/internal/config"
)

func newTestConfigModel() configModel {
	prefs := config.DefaultPreferences()
	m := newConfigModel(prefs)
	m.setSize(80, 30)
	return m
}

func TestConfigModel_Init(t *testing.T) {
	m := newTestConfigModel()
	cmd := m.Init()
	if cmd != nil {
		t.Error("expected nil cmd from Init")
	}
}

func TestConfigModel_View_ContainsLabel(t *testing.T) {
	m := newTestConfigModel()
	view := m.View()
	if !strings.Contains(view, "Completion notification") {
		t.Errorf("expected view to contain item label, got: %s", view)
	}
	if !strings.Contains(view, "space: toggle") {
		t.Errorf("expected view to contain help text, got: %s", view)
	}
}

func TestConfigModel_View_CheckedByDefault(t *testing.T) {
	m := newTestConfigModel()
	view := m.View()
	if !strings.Contains(view, "[x]") {
		t.Error("expected checked checkbox by default (CompletionBell defaults to true)")
	}
}

func TestConfigModel_View_UncheckedWhenDisabled(t *testing.T) {
	prefs := config.Preferences{CompletionBell: false}
	m := newConfigModel(prefs)
	view := m.View()
	if !strings.Contains(view, "[ ]") {
		t.Error("expected unchecked checkbox when CompletionBell is false")
	}
}

func TestConfigModel_SpaceTogglesOff(t *testing.T) {
	m := newTestConfigModel()
	if !m.items[0].checked {
		t.Fatal("expected checked initially (default is true)")
	}

	m, cmd := m.Update(tea.KeyPressMsg{Code: ' '})
	if m.items[0].checked {
		t.Error("expected unchecked after space")
	}
	if cmd == nil {
		t.Error("expected a cmd to save preferences")
	}
	msg := cmd()
	if pMsg, ok := msg.(prefsUpdatedMsg); !ok {
		t.Errorf("expected prefsUpdatedMsg, got %T", msg)
	} else if pMsg.prefs.CompletionBell {
		t.Error("expected CompletionBell to be false after toggling off")
	}
}

func TestConfigModel_SpaceTogglesOn(t *testing.T) {
	prefs := config.Preferences{CompletionBell: false}
	m := newConfigModel(prefs)

	m, cmd := m.Update(tea.KeyPressMsg{Code: ' '})
	if !m.items[0].checked {
		t.Error("expected checked after toggling on")
	}
	if cmd == nil {
		t.Fatal("expected a cmd")
	}
	msg := cmd()
	pMsg := msg.(prefsUpdatedMsg)
	if !pMsg.prefs.CompletionBell {
		t.Error("expected CompletionBell to be true after toggling on")
	}
}

func TestConfigModel_EscGoesBack(t *testing.T) {
	m := newTestConfigModel()
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("expected a cmd from esc")
	}
	msg := cmd()
	if _, ok := msg.(goBackFromConfigMsg); !ok {
		t.Errorf("expected goBackFromConfigMsg, got %T", msg)
	}
}

func TestConfigModel_CursorNavigation(t *testing.T) {
	m := newTestConfigModel()
	// Two items now: cursor starts at 0, can move to 1.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.cursor != 1 {
		t.Errorf("expected cursor 1 after down, got %d", m.cursor)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.cursor != 1 {
		t.Errorf("expected cursor 1 at end, got %d", m.cursor)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.cursor != 0 {
		t.Errorf("expected cursor 0, got %d", m.cursor)
	}
}

func TestConfigModel_View_ContainsHistoryLimit(t *testing.T) {
	m := newTestConfigModel()
	view := m.View()
	if !strings.Contains(view, "History limit") {
		t.Errorf("expected view to contain history limit label, got: %s", view)
	}
	if !strings.Contains(view, "50") {
		t.Errorf("expected view to contain default history limit value, got: %s", view)
	}
}

func TestConfigModel_HistoryLimit_RightIncreases(t *testing.T) {
	m := newTestConfigModel()
	// Move cursor to history limit item (index 1).
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	initial := m.items[1].value

	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.items[1].value != initial+10 {
		t.Errorf("expected %d after right, got %d", initial+10, m.items[1].value)
	}
	if cmd == nil {
		t.Error("expected a cmd to save preferences")
	}
	msg := cmd()
	if pMsg, ok := msg.(prefsUpdatedMsg); !ok {
		t.Errorf("expected prefsUpdatedMsg, got %T", msg)
	} else if pMsg.prefs.HistoryLimit != initial+10 {
		t.Errorf("expected HistoryLimit %d, got %d", initial+10, pMsg.prefs.HistoryLimit)
	}
}

func TestConfigModel_HistoryLimit_LeftDecreases(t *testing.T) {
	m := newTestConfigModel()
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	initial := m.items[1].value

	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.items[1].value != initial-10 {
		t.Errorf("expected %d after left, got %d", initial-10, m.items[1].value)
	}
	if cmd == nil {
		t.Error("expected a cmd to save preferences")
	}
}

func TestConfigModel_HistoryLimit_RespectsMin(t *testing.T) {
	prefs := config.Preferences{CompletionBell: true, HistoryLimit: 10}
	m := newConfigModel(prefs)
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.items[1].value != 10 {
		t.Errorf("expected value to stay at min 10, got %d", m.items[1].value)
	}
	if cmd != nil {
		t.Error("expected nil cmd when at minimum")
	}
}

func TestConfigModel_HistoryLimit_RespectsMax(t *testing.T) {
	prefs := config.Preferences{CompletionBell: true, HistoryLimit: 500}
	m := newConfigModel(prefs)
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.items[1].value != 500 {
		t.Errorf("expected value to stay at max 500, got %d", m.items[1].value)
	}
	if cmd != nil {
		t.Error("expected nil cmd when at maximum")
	}
}

func TestConfigModel_SpaceNoOpOnIntItem(t *testing.T) {
	m := newTestConfigModel()
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	before := m.items[1].value
	m, cmd := m.Update(tea.KeyPressMsg{Code: ' '})
	if m.items[1].value != before {
		t.Error("space should not change int item value")
	}
	if cmd != nil {
		t.Error("expected nil cmd for space on int item")
	}
}

func TestConfigModel_SetSize(t *testing.T) {
	m := newTestConfigModel()
	m.setSize(120, 50)
	if m.width != 120 || m.height != 50 {
		t.Errorf("expected 120x50, got %dx%d", m.width, m.height)
	}
}
