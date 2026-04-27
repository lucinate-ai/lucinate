package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lucinate-ai/lucinate/internal/config"
)

type configItemKind int

const (
	configItemBool configItemKind = iota
	configItemInt
)

type configItem struct {
	label   string
	key     string
	kind    configItemKind
	checked bool // for bool items
	value   int  // for int items
	min     int
	max     int
	step    int
}

type configModel struct {
	items     []configItem
	cursor    int
	prefs     config.Preferences
	width     int
	height    int
	hideHints bool
}

func newConfigModel(prefs config.Preferences, hideHints bool) configModel {
	return configModel{
		prefs:     prefs,
		hideHints: hideHints,
		items: []configItem{
			{label: "Completion notification (terminal bell)", key: "completionBell", kind: configItemBool, checked: prefs.CompletionBell},
			{label: "History limit (messages loaded per session)", key: "historyLimit", kind: configItemInt, value: prefs.HistoryLimit, min: 10, max: 500, step: 10},
		},
	}
}

func (m configModel) Init() tea.Cmd { return nil }

func (m configModel) Update(msg tea.Msg) (configModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "left", "h":
			item := &m.items[m.cursor]
			if item.kind == configItemInt && item.value-item.step >= item.min {
				item.value -= item.step
				m.applyToPrefs()
				prefs := m.prefs
				return m, func() tea.Msg {
					_ = config.SavePreferences(prefs)
					return prefsUpdatedMsg{prefs: prefs}
				}
			}
		case "right", "l":
			item := &m.items[m.cursor]
			if item.kind == configItemInt && item.value+item.step <= item.max {
				item.value += item.step
				m.applyToPrefs()
				prefs := m.prefs
				return m, func() tea.Msg {
					_ = config.SavePreferences(prefs)
					return prefsUpdatedMsg{prefs: prefs}
				}
			}
		default:
			// Discoverable shortcuts route through TriggerAction so the
			// help line and the keystroke share a single source of truth.
			for _, a := range m.Actions() {
				if a.Key == msg.String() {
					return m.TriggerAction(a.ID)
				}
			}
		}
	}
	return m, nil
}

// Actions returns the discoverable, view-level commands the config
// view exposes. `toggle` is only present when the focused row is a bool
// item — flipping a checkbox is the only configurable operation that
// translates cleanly into a single named button on embedders with a
// native action surface. The ←/→ adjust controls remain inline (per-row
// form controls, not screen commands), so they don't appear here.
func (m configModel) Actions() []Action {
	actions := make([]Action, 0, 2)
	if m.cursor >= 0 && m.cursor < len(m.items) && m.items[m.cursor].kind == configItemBool {
		actions = append(actions, Action{ID: "toggle", Label: "Toggle", Key: "space"})
	}
	actions = append(actions, Action{ID: "back", Label: "Back", Key: "esc"})
	return actions
}

// TriggerAction invokes the named action.
func (m configModel) TriggerAction(id string) (configModel, tea.Cmd) {
	switch id {
	case "toggle":
		if m.cursor < 0 || m.cursor >= len(m.items) {
			return m, nil
		}
		item := &m.items[m.cursor]
		if item.kind != configItemBool {
			return m, nil
		}
		item.checked = !item.checked
		m.applyToPrefs()
		prefs := m.prefs
		return m, func() tea.Msg {
			_ = config.SavePreferences(prefs)
			return prefsUpdatedMsg{prefs: prefs}
		}
	case "back":
		return m, func() tea.Msg { return goBackFromConfigMsg{} }
	}
	return m, nil
}

func (m *configModel) applyToPrefs() {
	for _, item := range m.items {
		switch item.key {
		case "completionBell":
			m.prefs.CompletionBell = item.checked
		case "historyLimit":
			m.prefs.HistoryLimit = item.value
		}
	}
}

func (m configModel) View() string {
	var b strings.Builder

	header := headerStyle.Render(" Config ")
	b.WriteString(header)
	b.WriteString("\n\n")

	for i, item := range m.items {
		var line string
		switch item.kind {
		case configItemBool:
			check := "[ ]"
			if item.checked {
				check = "[x]"
			}
			line = fmt.Sprintf("  %s %s", check, item.label)
		case configItemInt:
			line = fmt.Sprintf("  ◀ %d ▶  %s", item.value, item.label)
		}
		if i == m.cursor {
			line = lipgloss.NewStyle().Foreground(accent).Bold(true).Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	if !m.hideHints {
		b.WriteString("\n")
		// `toggle` and `back` come out of Actions(); ←/→ adjust stays
		// hand-rendered as a per-row form control (it operates on
		// whichever int item the cursor is on, not on the screen as a
		// whole).
		hint := renderActionHints(m.Actions())
		if hint == "" {
			hint = "  ←/→: adjust"
		} else {
			hint += " · ←/→: adjust"
		}
		b.WriteString(helpStyle.Render(hint))
	}

	return b.String()
}

func (m *configModel) setSize(w, h int) {
	m.width = w
	m.height = h
}
