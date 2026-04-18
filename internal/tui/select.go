package tui

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/a3tai/openclaw-go/protocol"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/outofcoffee/repclaw/internal/client"
)

// agentItem is a list item for the agent picker.
type agentItem struct {
	agent      protocol.AgentSummary
	sessionKey string
}

func (i agentItem) FilterValue() string {
	if i.agent.Name != "" {
		return i.agent.Name
	}
	return i.agent.ID
}

// agentDelegate renders each agent in the list.
type agentDelegate struct{}

func (d agentDelegate) Height() int                             { return 1 }
func (d agentDelegate) Spacing() int                            { return 0 }
func (d agentDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d agentDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	i, ok := item.(agentItem)
	if !ok {
		return
	}

	name := i.agent.ID
	if i.agent.Name != "" {
		name = i.agent.Name
	}

	var str string
	if index == m.Index() {
		str = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#7D56F4", Dark: "#AD8CFF"}).
			Bold(true).
			Render(fmt.Sprintf("> %s", name))
	} else {
		str = fmt.Sprintf("  %s", name)
	}
	fmt.Fprint(w, str)
}

// selectModel is the agent selection view.
type selectModel struct {
	list     list.Model
	client   *client.Client
	loading  bool
	err      error
	mainKey  string
	selected bool
}

// agentsLoadedMsg is sent when agents are fetched from the gateway.
type agentsLoadedMsg struct {
	result *protocol.AgentsListResult
	err    error
}

func newSelectModel(c *client.Client) selectModel {
	l := list.New(nil, agentDelegate{}, 0, 0)
	l.Title = "Select an agent"
	l.SetShowStatusBar(false)
	l.SetShowHelp(true)
	l.Styles.Title = headerStyle
	l.SetFilteringEnabled(false)

	return selectModel{
		list:    l,
		client:  c,
		loading: true,
	}
}

func (m selectModel) loadAgents() tea.Cmd {
	return func() tea.Msg {
		result, err := m.client.ListAgents(context.Background())
		return agentsLoadedMsg{result: result, err: err}
	}
}

func (m selectModel) Init() tea.Cmd {
	return m.loadAgents()
}

func (m selectModel) Update(msg tea.Msg) (selectModel, tea.Cmd) {
	switch msg := msg.(type) {
	case agentsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.mainKey = msg.result.MainKey
		items := make([]list.Item, len(msg.result.Agents))
		for i, a := range msg.result.Agents {
			sessionKey := m.mainKey
			if a.ID != msg.result.DefaultID {
				sessionKey = a.ID
			}
			items[i] = agentItem{agent: a, sessionKey: sessionKey}
		}
		m.list.SetItems(items)

		// Auto-select if there's only one agent.
		if len(msg.result.Agents) == 1 {
			m.selected = true
		}
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "enter" && !m.loading && m.err == nil {
			if item, ok := m.list.SelectedItem().(agentItem); ok {
				m.selected = true
				_ = item // used by parent
				return m, nil
			}
		}
		if msg.String() == "r" && m.err != nil {
			m.loading = true
			m.err = nil
			return m, m.loadAgents()
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m selectModel) View() string {
	if m.loading {
		return "\n  Connecting to gateway...\n"
	}
	if m.err != nil {
		var b strings.Builder
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("  Error: %v", m.err)))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  Press 'r' to retry, 'q' to quit"))
		b.WriteString("\n")
		return b.String()
	}
	return m.list.View()
}

func (m selectModel) selectedAgent() (agentItem, bool) {
	item, ok := m.list.SelectedItem().(agentItem)
	return item, ok
}

func (m *selectModel) setSize(w, h int) {
	m.list.SetSize(w, h-2)
}
