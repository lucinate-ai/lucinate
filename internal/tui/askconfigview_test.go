package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lucinate-ai/lucinate/internal/config"
)

func TestAskConfigModel_PrefillsFromPrefs(t *testing.T) {
	prefs := config.DefaultPreferences()
	prefs.Ask = config.AskDefaults{Connection: "home", Agent: "bot", Session: "s1", Detach: true}
	m := newAskConfigModel(prefs, false)
	if m.connInput.Value() != "home" {
		t.Errorf("connection = %q, want home", m.connInput.Value())
	}
	if m.agentInput.Value() != "bot" {
		t.Errorf("agent = %q, want bot", m.agentInput.Value())
	}
	if m.sessionInput.Value() != "s1" {
		t.Errorf("session = %q, want s1", m.sessionInput.Value())
	}
	if !m.detach {
		t.Error("detach should be true")
	}
}

func TestAskConfigModel_FocusInitialAndNavigate(t *testing.T) {
	m := newAskConfigModel(config.DefaultPreferences(), false)
	m.focusInitial()
	if m.focusIdx != askFieldConnection {
		t.Fatalf("focusInitial should land on connection, got %d", m.focusIdx)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.focusIdx != askFieldAgent {
		t.Fatalf("tab should advance to agent, got %d", m.focusIdx)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.focusIdx != askFieldDetach {
		t.Fatalf("expected detach focus after three tabs, got %d", m.focusIdx)
	}
	// Wrap around back to connection.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.focusIdx != askFieldConnection {
		t.Fatalf("tab should wrap to connection, got %d", m.focusIdx)
	}
}

func TestAskConfigModel_SpaceTogglesDetachOnlyOnDetachField(t *testing.T) {
	m := newAskConfigModel(config.DefaultPreferences(), false)
	m.focusInitial() // connection field

	// Space on a text field is literal input, not a toggle.
	m, _ = m.Update(tea.KeyPressMsg{Code: ' '})
	if m.detach {
		t.Error("space on text field should not toggle detach")
	}

	// Move to detach, then space toggles.
	for i := 0; i < 3; i++ {
		m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: ' '})
	if !m.detach {
		t.Error("space on detach field should toggle it on")
	}
}

func TestAskConfigModel_Actions(t *testing.T) {
	m := newAskConfigModel(config.DefaultPreferences(), false)
	m.focusInitial() // connection field
	if got := actionIDs(m.Actions()); !equalStrings(got, []string{"back"}) {
		t.Errorf("text field actions = %v, want [back]", got)
	}
	m.focusIdx = askFieldDetach
	if got := actionIDs(m.Actions()); !equalStrings(got, []string{"toggle", "back"}) {
		t.Errorf("detach actions = %v, want [toggle back]", got)
	}
}

func TestAskConfigModel_SaveOnEscEmitsClosedMsg(t *testing.T) {
	t.Setenv("LUCINATE_DATA_DIR", t.TempDir())
	m := newAskConfigModel(config.DefaultPreferences(), false)
	m.focusInitial()
	m.connInput.SetValue("home")
	m.agentInput.SetValue("bot")
	m.sessionInput.SetValue("sess")
	m.detach = true

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("expected a save cmd from esc")
	}
	msg := cmd()
	closed, ok := msg.(askConfigClosedMsg)
	if !ok {
		t.Fatalf("expected askConfigClosedMsg, got %T", msg)
	}
	if closed.prefs.Ask.Connection != "home" || closed.prefs.Ask.Agent != "bot" ||
		closed.prefs.Ask.Session != "sess" || !closed.prefs.Ask.Detach {
		t.Errorf("saved Ask defaults wrong: %+v", closed.prefs.Ask)
	}
}

func TestAskConfigModel_ViewContainsLabels(t *testing.T) {
	m := newAskConfigModel(config.DefaultPreferences(), false)
	m.setSize(80, 30)
	view := m.View()
	for _, want := range []string{"Ask command defaults", "Connection:", "Agent:", "Session", "Detach"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}
}
