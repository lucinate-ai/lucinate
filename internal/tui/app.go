package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/outofcoffee/repclaw/internal/client"
)

type viewState int

const (
	viewSelect viewState = iota
	viewChat
)

// AppModel is the root bubbletea model.
type AppModel struct {
	state       viewState
	selectModel selectModel
	chatModel   chatModel
	client      *client.Client
	width       int
	height      int
}

// NewApp creates the root application model.
func NewApp(c *client.Client) AppModel {
	return AppModel{
		state:       viewSelect,
		selectModel: newSelectModel(c),
		client:      c,
	}
}

func (m AppModel) Init() tea.Cmd {
	return m.selectModel.Init()
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.selectModel.setSize(msg.Width, msg.Height)
		if m.state == viewChat {
			m.chatModel.setSize(msg.Width, msg.Height)
		}
		return m, nil

	case goBackMsg:
		m.state = viewSelect
		return m, nil

	case sessionCreatedMsg:
		if msg.err != nil {
			m.selectModel.err = msg.err
			m.state = viewSelect
			return m, nil
		}
		m.chatModel = newChatModel(m.client, msg.sessionKey, msg.agentName, msg.modelID)
		m.chatModel.setSize(m.width, m.height)
		m.state = viewChat
		return m, m.chatModel.Init()

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			if m.state == viewChat {
				m.state = viewSelect
				return m, nil
			}
			return m, tea.Quit
		}
	}

	switch m.state {
	case viewSelect:
		var cmd tea.Cmd
		m.selectModel, cmd = m.selectModel.Update(msg)
		if m.selectModel.selected {
			m.selectModel.selected = false
			if item, ok := m.selectModel.selectedAgent(); ok {
				name := item.agent.Name
				if name == "" {
					name = item.agent.ID
				}
				modelID := ""
				if item.agent.Model != nil {
					modelID = item.agent.Model.Primary
				}
				if item.sessionKey == m.selectModel.mainKey {
					// Default agent: use the main session key directly.
					m.chatModel = newChatModel(m.client, item.sessionKey, name, modelID)
					m.chatModel.setSize(m.width, m.height)
					m.state = viewChat
					return m, m.chatModel.Init()
				}
				// Non-default agent: create a session so the gateway
				// routes to the correct agent and workspace.
				cl := m.client
				agentID := item.agent.ID
				return m, func() tea.Msg {
					key, err := cl.CreateSession(context.Background(), agentID)
					return sessionCreatedMsg{
						sessionKey: key,
						agentName:  name,
						modelID:    modelID,
						err:        err,
					}
				}
			}
		}
		return m, cmd

	case viewChat:
		var cmd tea.Cmd
		m.chatModel, cmd = m.chatModel.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m AppModel) View() tea.View {
	var v tea.View
	switch m.state {
	case viewSelect:
		v = tea.NewView(m.selectModel.View())
	case viewChat:
		v = tea.NewView(m.chatModel.View())
	default:
		v = tea.NewView("")
	}
	v.AltScreen = true
	v.KeyboardEnhancements.ReportEventTypes = true
	v.KeyboardEnhancements.ReportAllKeysAsEscapeCodes = true
	return v
}
