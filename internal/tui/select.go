package tui

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/a3tai/openclaw-go/protocol"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
			Foreground(accent).
			Bold(true).
			Render(fmt.Sprintf("> %s", name))
	} else {
		str = fmt.Sprintf("  %s", name)
	}
	fmt.Fprint(w, str)
}

type selectSubState int

const (
	subStateList   selectSubState = iota
	subStateCreate
)

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// selectModel is the agent selection view.
type selectModel struct {
	list     list.Model
	client   *client.Client
	loading  bool
	err      error
	mainKey  string
	selected bool

	// Create-agent form state.
	subState       selectSubState
	nameInput      textinput.Model
	workInput      textinput.Model
	focusedField   int // 0 = name, 1 = workspace
	creating       bool
	createErr      error
	newAgentID     string
	workspaceEdited bool
	nameValidMsg   string
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

func (m selectModel) createAgent(name, workspace string) tea.Cmd {
	cl := m.client
	return func() tea.Msg {
		err := cl.CreateAgent(context.Background(), name, workspace)
		return agentCreatedMsg{name: name, err: err}
	}
}

func (m selectModel) Init() tea.Cmd {
	return m.loadAgents()
}

// normaliseName converts input to kebab-case: lowercase, spaces to hyphens.
func normaliseName(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	return s
}

// validateName returns an error message if the name is invalid, or empty if valid.
func validateName(s string) string {
	if s == "" {
		return "Required"
	}
	if s[0] < 'a' || s[0] > 'z' {
		return "Must start with a letter"
	}
	if !namePattern.MatchString(s) {
		return "Use lowercase letters, numbers, hyphens only"
	}
	return ""
}

func (m *selectModel) initCreateForm() {
	m.subState = subStateCreate
	m.focusedField = 0
	m.creating = false
	m.createErr = nil
	m.workspaceEdited = false
	m.nameValidMsg = "Required"

	m.nameInput = textinput.New()
	m.nameInput.Placeholder = "my-agent"
	m.nameInput.CharLimit = 64
	m.nameInput.Focus()

	m.workInput = textinput.New()
	m.workInput.Placeholder = "~/.openclaw/workspaces/my-agent"
	m.workInput.CharLimit = 256
}

func (m *selectModel) switchFocus() {
	if m.focusedField == 0 {
		m.focusedField = 1
		m.nameInput.Blur()
		m.workInput.Focus()
	} else {
		m.focusedField = 0
		m.workInput.Blur()
		m.nameInput.Focus()
	}
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

		// If we just created an agent, auto-select it.
		if m.newAgentID != "" {
			for i, item := range items {
				if ai, ok := item.(agentItem); ok && ai.agent.ID == m.newAgentID {
					m.list.Select(i)
					m.selected = true
					break
				}
			}
			m.newAgentID = ""
			return m, nil
		}

		// Auto-select if there's only one agent.
		if len(msg.result.Agents) == 1 {
			m.selected = true
		}
		return m, nil

	case agentCreatedMsg:
		m.creating = false
		if msg.err != nil {
			m.createErr = msg.err
			return m, nil
		}
		m.newAgentID = msg.name
		m.subState = subStateList
		m.loading = true
		return m, m.loadAgents()

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	if m.subState == subStateCreate {
		return m.updateCreateForm(msg)
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m selectModel) handleKey(msg tea.KeyPressMsg) (selectModel, tea.Cmd) {
	if m.subState == subStateCreate {
		return m.handleCreateKey(msg)
	}

	// List sub-state keys.
	switch msg.String() {
	case "enter":
		if !m.loading && m.err == nil {
			if item, ok := m.list.SelectedItem().(agentItem); ok {
				m.selected = true
				_ = item
				return m, nil
			}
		}
	case "n":
		if !m.loading && m.err == nil {
			m.initCreateForm()
			return m, nil
		}
	case "r":
		if m.err != nil {
			m.loading = true
			m.err = nil
			return m, m.loadAgents()
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m selectModel) handleCreateKey(msg tea.KeyPressMsg) (selectModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.subState = subStateList
		return m, nil

	case "tab", "shift+tab":
		m.switchFocus()
		return m, nil

	case "enter":
		if m.creating {
			return m, nil
		}
		name := m.nameInput.Value()
		workspace := m.workInput.Value()
		m.nameValidMsg = validateName(name)
		if m.nameValidMsg != "" {
			return m, nil
		}
		if workspace == "" {
			workspace = "~/.openclaw/workspaces/" + name
		}
		m.creating = true
		m.createErr = nil
		return m, m.createAgent(name, workspace)
	}

	return m.updateCreateForm(msg)
}

func (m selectModel) updateCreateForm(msg tea.Msg) (selectModel, tea.Cmd) {
	prevName := m.nameInput.Value()

	var cmd tea.Cmd
	if m.focusedField == 0 {
		m.nameInput, cmd = m.nameInput.Update(msg)

		// Auto-normalise as the user types.
		raw := m.nameInput.Value()
		normalised := normaliseName(raw)
		if normalised != raw {
			m.nameInput.SetValue(normalised)
		}

		// Update validation.
		m.nameValidMsg = validateName(m.nameInput.Value())

		// Auto-suggest workspace when name changes and user hasn't edited workspace.
		if m.nameInput.Value() != prevName && !m.workspaceEdited {
			name := m.nameInput.Value()
			if name != "" {
				m.workInput.SetValue("~/.openclaw/workspaces/" + name)
			} else {
				m.workInput.SetValue("")
			}
		}
	} else {
		prevWork := m.workInput.Value()
		m.workInput, cmd = m.workInput.Update(msg)
		if m.workInput.Value() != prevWork {
			m.workspaceEdited = true
		}
	}
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
	if m.subState == subStateCreate {
		return m.viewCreateForm()
	}
	return m.list.View() + "\n" + helpStyle.Render("  n: new agent")
}

func (m selectModel) viewCreateForm() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(headerStyle.Render(" Create new agent "))
	b.WriteString("\n\n")

	b.WriteString("  Name:\n")
	b.WriteString("  " + m.nameInput.View() + "\n")
	if m.nameValidMsg != "" && m.nameInput.Value() != "" {
		b.WriteString("  " + errorStyle.Render(m.nameValidMsg) + "\n")
	}
	b.WriteString("\n")

	b.WriteString("  Workspace:\n")
	b.WriteString("  " + m.workInput.View() + "\n")
	b.WriteString("\n")

	if m.creating {
		b.WriteString(statusStyle.Render("  Creating agent..."))
	} else if m.createErr != nil {
		b.WriteString(errorStyle.Render(fmt.Sprintf("  Error: %v", m.createErr)))
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("  Enter: retry | Esc: cancel"))
	} else {
		b.WriteString(helpStyle.Render("  Tab: switch fields | Enter: create | Esc: cancel"))
	}
	b.WriteString("\n")
	return b.String()
}

func (m selectModel) selectedAgent() (agentItem, bool) {
	item, ok := m.list.SelectedItem().(agentItem)
	return item, ok
}

func (m *selectModel) setSize(w, h int) {
	m.list.SetSize(w, h-2)
}
