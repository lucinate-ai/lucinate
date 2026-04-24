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
	items  []configItem
	cursor int
	prefs  config.Preferences
	width  int
	height int
}

func newConfigModel(prefs config.Preferences) configModel {
	return configModel{
		prefs: prefs,
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
		case "space":
			item := &m.items[m.cursor]
			if item.kind == configItemBool {
				item.checked = !item.checked
				m.applyToPrefs()
				prefs := m.prefs
				return m, func() tea.Msg {
					_ = config.SavePreferences(prefs)
					return prefsUpdatedMsg{prefs: prefs}
				}
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
		case "esc":
			return m, func() tea.Msg { return goBackFromConfigMsg{} }
		}
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

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  space: toggle · ←/→: adjust · esc: back"))

	return b.String()
}

func (m *configModel) setSize(w, h int) {
	m.width = w
	m.height = h
}
