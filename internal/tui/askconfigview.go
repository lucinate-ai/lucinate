package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// askConfigModel is the "/config ▸ Ask command defaults" sub-screen. It
// edits the preconfigured values the `lucinate ask` subcommand uses —
// the same fields `lucinate send` accepts (connection, agent, session,
// detach). KEEP IN SYNC with config.AskDefaults and the send/ask flag
// sets (internal/cli/send.go, internal/cli/ask.go): a new send field
// should gain a row here so it stays customisable.
type askConfigModel struct {
	connInput    textinput.Model
	agentInput   textinput.Model
	sessionInput textinput.Model
	detach       bool

	focusIdx  int // one of the askField* constants below
	prefs     config.Preferences
	hideHints bool
	width     int
	height    int
}

const (
	askFieldConnection = iota
	askFieldAgent
	askFieldSession
	askFieldDetach
	askFieldCount
)

func newAskConfigModel(prefs config.Preferences, hideHints bool) askConfigModel {
	conn := textinput.New()
	conn.CharLimit = 128
	conn.Placeholder = "connection name or ID"
	conn.SetValue(prefs.Ask.Connection)

	agent := textinput.New()
	agent.CharLimit = 128
	agent.Placeholder = "agent name or ID"
	agent.SetValue(prefs.Ask.Agent)

	session := textinput.New()
	session.CharLimit = 128
	session.Placeholder = "leave blank for the agent's main session"
	session.SetValue(prefs.Ask.Session)

	return askConfigModel{
		connInput:    conn,
		agentInput:   agent,
		sessionInput: session,
		detach:       prefs.Ask.Detach,
		prefs:        prefs,
		hideHints:    hideHints,
	}
}

func (m askConfigModel) Init() tea.Cmd { return nil }

// focusInitial lands focus on the first field and returns its blink
// command. Called by the AppModel when entering the sub-screen, where a
// pointer receiver can mutate the stored model (Init's value receiver
// can't).
func (m *askConfigModel) focusInitial() tea.Cmd {
	m.focusIdx = askFieldConnection
	return m.syncFocus()
}

func (m askConfigModel) Update(msg tea.Msg) (askConfigModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		return m.handleKey(key)
	}
	return m.updateInput(msg)
}

func (m askConfigModel) handleKey(msg tea.KeyPressMsg) (askConfigModel, tea.Cmd) {
	switch msg.String() {
	case "tab", "down":
		m.focusIdx = (m.focusIdx + 1) % askFieldCount
		return m, m.syncFocus()
	case "shift+tab", "up":
		m.focusIdx = (m.focusIdx - 1 + askFieldCount) % askFieldCount
		return m, m.syncFocus()
	case "esc", "enter":
		// Both keys persist the edits and return to the config view —
		// there's no separate "cancel"; this mirrors the config screen,
		// which also saves on every change.
		return m.save()
	case "space", "left", "right":
		// Only meaningful on the detach toggle; on a text field these
		// (notably space) are literal input and must reach the textinput.
		if m.focusIdx == askFieldDetach {
			m.detach = !m.detach
			return m, nil
		}
	}
	return m.updateInput(msg)
}

// syncFocus blurs every text input and focuses the one matching the
// current field, returning that input's focus command. The detach row
// has no text input, so focus there returns nil.
func (m *askConfigModel) syncFocus() tea.Cmd {
	m.connInput.Blur()
	m.agentInput.Blur()
	m.sessionInput.Blur()
	switch m.focusIdx {
	case askFieldConnection:
		return m.connInput.Focus()
	case askFieldAgent:
		return m.agentInput.Focus()
	case askFieldSession:
		return m.sessionInput.Focus()
	}
	return nil
}

func (m askConfigModel) updateInput(msg tea.Msg) (askConfigModel, tea.Cmd) {
	var cmd tea.Cmd
	switch m.focusIdx {
	case askFieldConnection:
		m.connInput, cmd = m.connInput.Update(msg)
	case askFieldAgent:
		m.agentInput, cmd = m.agentInput.Update(msg)
	case askFieldSession:
		m.sessionInput, cmd = m.sessionInput.Update(msg)
	}
	return m, cmd
}

// applyToPrefs returns a copy of the model's preferences with the Ask
// defaults set from the current form values.
func (m askConfigModel) applyToPrefs() config.Preferences {
	p := m.prefs
	p.Ask = config.AskDefaults{
		Connection: strings.TrimSpace(m.connInput.Value()),
		Agent:      strings.TrimSpace(m.agentInput.Value()),
		Session:    strings.TrimSpace(m.sessionInput.Value()),
		Detach:     m.detach,
	}
	return p
}

func (m askConfigModel) save() (askConfigModel, tea.Cmd) {
	prefs := m.applyToPrefs()
	return m, func() tea.Msg {
		_ = config.SavePreferences(prefs)
		return askConfigClosedMsg{prefs: prefs}
	}
}

// Actions returns the discoverable view-level commands. Toggle is only
// present on the detach row; field navigation and text entry stay inline
// as intrinsic form controls (as in the connections form).
func (m askConfigModel) Actions() []Action {
	actions := make([]Action, 0, 2)
	if m.focusIdx == askFieldDetach {
		actions = append(actions, Action{ID: "toggle", Label: "Toggle", Key: "space"})
	}
	actions = append(actions, Action{ID: "back", Label: "Save & back", Key: "esc"})
	return actions
}

func (m askConfigModel) TriggerAction(id string) (askConfigModel, tea.Cmd) {
	switch id {
	case "toggle":
		m.detach = !m.detach
		return m, nil
	case "back":
		return m.save()
	}
	return m, nil
}

// wantsInput reports whether the focused field accepts free-form typing,
// so embedders driving an external keyboard show it for the text rows
// but not the detach toggle.
func (m askConfigModel) wantsInput() bool {
	return m.focusIdx != askFieldDetach
}

func (m askConfigModel) View() string {
	var b strings.Builder

	b.WriteString(headerStyle.Render(" Ask command defaults "))
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("  Pre-filled values for `lucinate ask`. Leave a field blank to require it\n  on the command line."))
	b.WriteString("\n\n")

	b.WriteString(m.renderField("Connection:", m.connInput.View(), askFieldConnection))
	b.WriteString(m.renderField("Agent:", m.agentInput.View(), askFieldAgent))
	b.WriteString(m.renderField("Session (optional):", m.sessionInput.View(), askFieldSession))

	check := "[ ]"
	if m.detach {
		check = "[x]"
	}
	detachLabel := "Detach (don't wait for a reply):"
	detachLine := "  " + detachLabel
	if m.focusIdx == askFieldDetach {
		detachLine = lipgloss.NewStyle().Foreground(accent).Bold(true).Render(detachLine)
	}
	b.WriteString(detachLine)
	b.WriteString("\n  ")
	b.WriteString(check)
	b.WriteString("\n")

	if !m.hideHints {
		b.WriteString("\n")
		b.WriteString(helpStyle.Render(renderActionHints(m.Actions()) + " · tab: next field"))
	}

	return b.String()
}

// renderField renders a labelled text input, highlighting the label when
// the field is focused.
func (m askConfigModel) renderField(label, inputView string, field int) string {
	labelLine := "  " + label
	if m.focusIdx == field {
		labelLine = lipgloss.NewStyle().Foreground(accent).Bold(true).Render(labelLine)
	}
	return labelLine + "\n  " + inputView + "\n\n"
}

func (m *askConfigModel) setSize(w, h int) {
	m.width = w
	m.height = h
}
