package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
)

type viewState int

const (
	viewSelect viewState = iota
	viewChat
	viewSessions
	viewConfig
)

// AppOptions configures an AppModel. Embedders that drive the program from
// a platform-native input surface (so the in-TUI textarea would be
// duplicate UI) set HideInputArea; see app.RunOptions for the full
// rationale.
type AppOptions struct {
	HideInputArea   bool
	HideActionHints bool
	DisableMouse    bool

	// OnInputFocusChanged, if non-nil, is invoked whenever the active
	// view's preferred input mode changes. See app.RunOptions for the
	// full rationale; this is the unexported plumbing.
	OnInputFocusChanged func(wantsInput bool)

	// OnActionsChanged, if non-nil, is invoked whenever the active
	// view's set of exposed Actions changes. See app.RunOptions for
	// the full rationale; this is the unexported plumbing.
	OnActionsChanged func(actions []Action)
}

// AppModel is the root bubbletea model.
type AppModel struct {
	state           viewState
	selectModel     selectModel
	chatModel       chatModel
	sessionsModel   sessionsModel
	configModel     configModel
	client          *client.Client
	prefs           config.Preferences
	width           int
	height          int
	hideInput       bool
	hideActionHints bool
	disableMouse    bool

	onInputFocusChanged func(bool)
	lastWantsInput      bool
	inputFocusReported  bool

	onActionsChanged func([]Action)
	lastActions      []Action
	actionsReported  bool
}

// NewApp creates the root application model.
func NewApp(c *client.Client, opts AppOptions) AppModel {
	return AppModel{
		state:               viewSelect,
		selectModel:         newSelectModel(c, opts.HideActionHints),
		client:              c,
		prefs:               config.LoadPreferences(),
		hideInput:           opts.HideInputArea,
		hideActionHints:     opts.HideActionHints,
		disableMouse:        opts.DisableMouse,
		onInputFocusChanged: opts.OnInputFocusChanged,
		onActionsChanged:    opts.OnActionsChanged,
	}
}

func (m AppModel) Init() tea.Cmd {
	return m.selectModel.Init()
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	prevState := m.state
	next, cmd := m.update(msg)
	if next.state != prevState {
		// View transitions in an embedded terminal can leave stale cells
		// from the previous view when an on-screen keyboard
		// simultaneously toggles and resizes the embedder's grid
		// mid-transition. Bubble Tea's incremental renderer assumes
		// the host terminal preserves its grid across resizes; some
		// embedded terminals don't after a rapid show/hide/show
		// keyboard sequence (e.g. picker → create-form → picker →
		// chat). Forcing a clear-screen on every viewState transition
		// guarantees the next render starts from a known-empty grid.
		// The CLI hardly notices — terminal emulators repaint a single
		// CSI 2J in microseconds.
		cmd = tea.Batch(cmd, tea.ClearScreen)
	}
	nextModel, cmd := next.maybeNotifyInputFocus(cmd)
	next = nextModel.(AppModel)
	return next.maybeNotifyActions(cmd)
}

// Actions returns the discoverable, view-level commands the active
// view currently exposes. AppModel re-publishes the slice on every
// transition through OnActionsChanged so embedders can rebuild their
// action UI without polling.
func (m AppModel) Actions() []Action {
	switch m.state {
	case viewSelect:
		return m.selectModel.Actions()
	case viewSessions:
		return m.sessionsModel.Actions()
	case viewConfig:
		return m.configModel.Actions()
	}
	return nil
}

// TriggerAction routes an embedder-issued action invocation to the
// active view. Both keystrokes (handled inside each view's KeyPressMsg
// switch) and external triggers (Program.TriggerAction) go through the
// view's TriggerAction so the work lives in one place.
func (m AppModel) TriggerAction(id string) (AppModel, tea.Cmd) {
	switch m.state {
	case viewSelect:
		var cmd tea.Cmd
		m.selectModel, cmd = m.selectModel.TriggerAction(id)
		return m, cmd
	case viewSessions:
		var cmd tea.Cmd
		m.sessionsModel, cmd = m.sessionsModel.TriggerAction(id)
		return m, cmd
	case viewConfig:
		var cmd tea.Cmd
		m.configModel, cmd = m.configModel.TriggerAction(id)
		return m, cmd
	}
	return m, nil
}

// maybeNotifyActions invokes OnActionsChanged whenever the computed
// actions slice differs from the last value reported. Mirrors
// maybeNotifyInputFocus: the first call after Init always fires so
// embedders see the startup list, and the callback runs from a tea.Cmd
// so the bubbletea event loop is not blocked by embedder work.
func (m AppModel) maybeNotifyActions(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	if m.onActionsChanged == nil {
		return m, cmd
	}
	actions := m.Actions()
	if m.actionsReported && actionsEqual(actions, m.lastActions) {
		return m, cmd
	}
	// Snapshot the slice so a downstream mutation by the next Update
	// can't be observed retroactively through lastActions.
	snapshot := append([]Action(nil), actions...)
	m.lastActions = snapshot
	m.actionsReported = true
	cb := m.onActionsChanged
	notify := func() tea.Msg {
		cb(snapshot)
		return nil
	}
	if cmd == nil {
		return m, notify
	}
	return m, tea.Batch(cmd, notify)
}

// computeWantsInput reports whether the active view currently has a focused
// text-input widget that expects free-form typing (the chat textarea, the
// new-agent form fields). Embedders use this signal to decide whether to
// surface their on-screen keyboard. List- and viewport-only views (agent
// list, sessions, config) return false: they only need the platform's
// existing navigation affordances.
func (m AppModel) computeWantsInput() bool {
	switch m.state {
	case viewChat:
		return true
	case viewSelect:
		return m.selectModel.subState == subStateCreate
	}
	return false
}

// maybeNotifyInputFocus invokes the OnInputFocusChanged callback whenever
// the computed input-focus state differs from the last value reported. The
// first call after Init always fires so embedders see the startup state
// without having to assume a default. The callback runs from a tea.Cmd so
// the bubbletea event loop is not blocked by embedder work.
func (m AppModel) maybeNotifyInputFocus(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	if m.onInputFocusChanged == nil {
		return m, cmd
	}
	wants := m.computeWantsInput()
	if m.inputFocusReported && wants == m.lastWantsInput {
		return m, cmd
	}
	m.lastWantsInput = wants
	m.inputFocusReported = true
	cb := m.onInputFocusChanged
	notify := func() tea.Msg {
		cb(wants)
		return nil
	}
	if cmd == nil {
		return m, notify
	}
	return m, tea.Batch(cmd, notify)
}

func (m AppModel) update(msg tea.Msg) (AppModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.selectModel.setSize(msg.Width, msg.Height)
		if m.state == viewChat {
			m.chatModel.setSize(msg.Width, msg.Height)
		}
		if m.state == viewSessions {
			m.sessionsModel.setSize(msg.Width, msg.Height)
		}
		if m.state == viewConfig {
			m.configModel.setSize(msg.Width, msg.Height)
		}
		return m, nil

	case goBackMsg:
		m.state = viewSelect
		return m, nil

	case showConfigMsg:
		m.configModel = newConfigModel(m.prefs, m.hideActionHints)
		m.configModel.setSize(m.width, m.height)
		m.state = viewConfig
		return m, nil

	case goBackFromConfigMsg:
		m.state = viewChat
		return m, nil

	case prefsUpdatedMsg:
		m.prefs = msg.prefs
		m.chatModel.prefs = msg.prefs
		return m, nil

	case ConnStateMsg:
		m.chatModel.applyConnState(msg)
		return m, nil

	case showSessionsMsg:
		m.sessionsModel = newSessionsModel(m.client, msg.agentID, msg.agentName, msg.modelID, msg.mainKey, m.hideActionHints)
		m.sessionsModel.setSize(m.width, m.height)
		m.state = viewSessions
		return m, m.sessionsModel.Init()

	case sessionSelectedMsg:
		m.chatModel = newChatModel(m.client, msg.sessionKey, m.sessionsModel.agentID, msg.agentName, msg.modelID, m.prefs, m.hideInput)
		m.chatModel.setSize(m.width, m.height)
		m.state = viewChat
		return m, m.chatModel.Init()

	case newSessionCreatedMsg:
		if msg.err != nil {
			m.sessionsModel.err = msg.err
			m.sessionsModel.loading = false
			return m, nil
		}
		m.chatModel = newChatModel(m.client, msg.sessionKey, m.sessionsModel.agentID, msg.agentName, msg.modelID, m.prefs, m.hideInput)
		m.chatModel.setSize(m.width, m.height)
		m.state = viewChat
		return m, m.chatModel.Init()

	case goBackFromSessionsMsg:
		m.state = viewChat
		return m, nil

	case sessionCreatedMsg:
		if msg.err != nil {
			m.selectModel.err = msg.err
			m.state = viewSelect
			return m, nil
		}
		m.chatModel = newChatModel(m.client, msg.sessionKey, msg.agentID, msg.agentName, msg.modelID, m.prefs, m.hideInput)
		m.chatModel.setSize(m.width, m.height)
		m.state = viewChat
		return m, m.chatModel.Init()

	case TriggerActionMsg:
		return m.TriggerAction(msg.ID)

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			if m.state == viewConfig {
				m.state = viewChat
				return m, nil
			}
			if m.state == viewSessions {
				m.state = viewChat
				return m, nil
			}
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
				// Create/resume a session so the gateway returns
				// the resolved session key for event filtering.
				cl := m.client
				agentID := item.agent.ID
				createKey := "main"
				if item.sessionKey == m.selectModel.mainKey {
					createKey = item.sessionKey
				}
				return m, func() tea.Msg {
					key, err := cl.CreateSession(context.Background(), agentID, createKey)
					return sessionCreatedMsg{
						sessionKey: key,
						agentID:    agentID,
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

	case viewSessions:
		var cmd tea.Cmd
		m.sessionsModel, cmd = m.sessionsModel.Update(msg)
		return m, cmd

	case viewConfig:
		var cmd tea.Cmd
		m.configModel, cmd = m.configModel.Update(msg)
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
	case viewSessions:
		v = tea.NewView(m.sessionsModel.View())
	case viewConfig:
		v = tea.NewView(m.configModel.View())
	default:
		v = tea.NewView("")
	}
	v.AltScreen = true
	if !m.disableMouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	v.KeyboardEnhancements.ReportEventTypes = true
	return v
}
