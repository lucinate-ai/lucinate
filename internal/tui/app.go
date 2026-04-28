package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/config"
)

type viewState int

const (
	viewConnections viewState = iota
	viewConnecting
	viewSelect
	viewChat
	viewSessions
	viewConfig
)

// BackendFactory builds an unconnected backend.Backend for a stored
// connection. The TUI owns Connect (so it can route auth errors into
// modal recovery flows). Embedders pass a factory whose
// implementation chooses the right concrete backend (OpenClaw,
// OpenAI-compat, ...) based on Connection.Type.
type BackendFactory func(*config.Connection) (backend.Backend, error)

// AppOptions configures an AppModel. Embedders that drive the program
// from a platform-native input surface set HideInputArea; see
// app.RunOptions for the full rationale.
type AppOptions struct {
	HideInputArea   bool
	HideActionHints bool
	DisableMouse    bool

	// Store, when non-nil, enables managed mode: the TUI owns the
	// connection lifecycle, runs the connections picker as the entry
	// view (or auto-picks via the same decision tree the resolver
	// uses), and surfaces the /connections command. Mutually
	// exclusive with passing a pre-connected backend to NewApp.
	Store *config.Connections

	// Initial is the connection the TUI will attempt first when in
	// managed mode. A nil Initial drops the user into the picker.
	Initial *config.Connection

	// BackendFactory is required in managed mode; it constructs an
	// unconnected backend for a chosen connection.
	BackendFactory BackendFactory

	// OnBackendChanged is invoked after a successful connect — the
	// app-layer driver uses this to rewire the events pump and
	// supervisor onto the new backend. Nil in legacy mode.
	OnBackendChanged func(backend.Backend)

	// OnConnectionsChanged is invoked after any CRUD operation
	// (add/edit/delete/MarkUsed) so embedders can persist the store.
	// Nil in legacy mode.
	OnConnectionsChanged func(config.Connections)

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
	state             viewState
	connectionsModel  connectionsModel
	connectingModel   connectingModel
	selectModel       selectModel
	chatModel         chatModel
	sessionsModel     sessionsModel
	configModel       configModel
	backend           backend.Backend
	activeConn        *config.Connection // the connection backend belongs to; rendered in status bars
	store             *config.Connections
	backendFactory    BackendFactory
	onBackendChanged  func(backend.Backend)
	onConnsChanged    func(config.Connections)
	prefs             config.Preferences
	width             int
	height            int
	hideInput         bool
	hideActionHints   bool
	disableMouse      bool
	managed           bool

	onInputFocusChanged func(bool)
	lastWantsInput      bool
	inputFocusReported  bool

	onActionsChanged func([]Action)
	lastActions      []Action
	actionsReported  bool
}

// NewApp creates the root application model.
//
// In legacy mode the caller passes a pre-connected backend and
// leaves opts.Store nil. In managed mode the caller passes a nil
// backend and supplies opts.Store + opts.BackendFactory; the TUI
// handles connect/auth/switch internally.
func NewApp(b backend.Backend, opts AppOptions) AppModel {
	managed := opts.Store != nil

	m := AppModel{
		backend:              b,
		prefs:                config.LoadPreferences(),
		hideInput:            opts.HideInputArea,
		hideActionHints:      opts.HideActionHints,
		disableMouse:         opts.DisableMouse,
		store:                opts.Store,
		backendFactory:       opts.BackendFactory,
		onBackendChanged:     opts.OnBackendChanged,
		onConnsChanged:       opts.OnConnectionsChanged,
		managed:              managed,
		onInputFocusChanged:  opts.OnInputFocusChanged,
		onActionsChanged:     opts.OnActionsChanged,
	}

	switch {
	case !managed:
		// Legacy: jump straight into the agent picker. No active
		// connection — embedders manage that elsewhere.
		m.state = viewSelect
		m.selectModel = newSelectModel(b, opts.HideActionHints, managed, nil)
	case opts.Initial != nil:
		// Managed with an initial pick: try to connect first, surface
		// errors / auth modals from there.
		m.state = viewConnecting
		m.connectingModel = newConnectingModel(opts.Initial, opts.HideActionHints)
	default:
		// Managed without an initial pick: open the connections picker.
		m.state = viewConnections
		m.connectionsModel = newConnectionsModel(opts.Store, opts.HideActionHints)
	}

	return m
}

func (m AppModel) Init() tea.Cmd {
	switch m.state {
	case viewSelect:
		return m.selectModel.Init()
	case viewConnecting:
		return m.startConnect(m.connectingModel.connection)
	case viewConnections:
		return m.connectionsModel.Init()
	}
	return nil
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
	case viewConnections:
		return m.connectionsModel.Actions()
	case viewConnecting:
		return m.connectingModel.Actions()
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
	case viewConnections:
		var cmd tea.Cmd
		m.connectionsModel, cmd = m.connectionsModel.TriggerAction(id)
		return m, cmd
	case viewConnecting:
		var cmd tea.Cmd
		m.connectingModel, cmd = m.connectingModel.TriggerAction(id)
		return m, cmd
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
// list, sessions, config, connections list) return false: they only need
// the platform's existing navigation affordances.
func (m AppModel) computeWantsInput() bool {
	switch m.state {
	case viewChat:
		return true
	case viewSelect:
		return m.selectModel.subState == subStateCreate
	case viewConnections:
		return m.connectionsModel.wantsInput()
	case viewConnecting:
		return m.connectingModel.wantsInput()
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
		// Only forward to the active view's model — the others
		// hold zero-value bubbles widgets whose internal state
		// panics on SetSize before they're constructed via the
		// transition path that owns them.
		switch m.state {
		case viewConnections:
			m.connectionsModel.setSize(msg.Width, msg.Height)
		case viewConnecting:
			m.connectingModel.setSize(msg.Width, msg.Height)
		case viewSelect:
			m.selectModel.setSize(msg.Width, msg.Height)
		case viewChat:
			m.chatModel.setSize(msg.Width, msg.Height)
		case viewSessions:
			m.sessionsModel.setSize(msg.Width, msg.Height)
		case viewConfig:
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

	case showConnectionsMsg:
		if !m.managed {
			return m, nil
		}
		// Tear down the active client so the next pick gets a fresh
		// pump+supervisor. Publishing nil to the driver causes it to
		// close the current client. Run on a goroutine so the
		// blocking send into the driver channel never stalls the
		// bubbletea event loop.
		if m.onBackendChanged != nil && m.backend != nil {
			cb := m.onBackendChanged
			go cb(nil)
		}
		m.backend = nil
		m.activeConn = nil
		m.connectionsModel = newConnectionsModel(m.store, m.hideActionHints)
		m.connectionsModel.setSize(m.width, m.height)
		m.state = viewConnections
		return m, m.connectionsModel.Init()

	case connectionPickedMsg:
		return m.beginConnect(msg.connection)

	case connectAttemptMsg:
		return m.beginConnect(msg.connection)

	case connectResultMsg:
		return m.handleConnectResult(msg)

	case authResolvedMsg:
		if msg.cancelled {
			// User backed out of the auth modal — return to the picker.
			m.connectionsModel = newConnectionsModel(m.store, m.hideActionHints)
			m.connectionsModel.setSize(m.width, m.height)
			m.state = viewConnections
			if msg.backend != nil {
				_ = msg.backend.Close()
			}
			return m, m.connectionsModel.Init()
		}
		// Retry connect with the same backend (token has been
		// stored/cleared as appropriate by the modal).
		return m, m.retryConnect(msg.connection, msg.backend)

	case connectionsChangedMsg:
		if m.onConnsChanged != nil && m.store != nil {
			cb := m.onConnsChanged
			snapshot := *m.store
			notify := func() tea.Msg {
				cb(snapshot)
				return nil
			}
			return m, notify
		}
		return m, nil

	case showSessionsMsg:
		m.sessionsModel = newSessionsModel(m.backend, msg.agentID, msg.agentName, msg.modelID, msg.mainKey, m.hideActionHints, m.activeConn)
		m.sessionsModel.setSize(m.width, m.height)
		m.state = viewSessions
		return m, m.sessionsModel.Init()

	case sessionSelectedMsg:
		m.chatModel = newChatModel(m.backend, msg.sessionKey, m.sessionsModel.agentID, msg.agentName, msg.modelID, m.prefs, m.hideInput, connectionLabel(m.activeConn))
		m.chatModel.setSize(m.width, m.height)
		m.state = viewChat
		return m, m.chatModel.Init()

	case newSessionCreatedMsg:
		if msg.err != nil {
			m.sessionsModel.err = msg.err
			m.sessionsModel.loading = false
			return m, nil
		}
		m.chatModel = newChatModel(m.backend, msg.sessionKey, m.sessionsModel.agentID, msg.agentName, msg.modelID, m.prefs, m.hideInput, connectionLabel(m.activeConn))
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
		m.chatModel = newChatModel(m.backend, msg.sessionKey, msg.agentID, msg.agentName, msg.modelID, m.prefs, m.hideInput, connectionLabel(m.activeConn))
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
	case viewConnections:
		var cmd tea.Cmd
		m.connectionsModel, cmd = m.connectionsModel.Update(msg)
		return m, cmd

	case viewConnecting:
		var cmd tea.Cmd
		m.connectingModel, cmd = m.connectingModel.Update(msg)
		return m, cmd

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
				b := m.backend
				agentID := item.agent.ID
				createKey := "main"
				if item.sessionKey == m.selectModel.mainKey {
					createKey = item.sessionKey
				}
				return m, func() tea.Msg {
					key, err := b.CreateSession(context.Background(), agentID, createKey)
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

// connectTimeout matches the legacy CLI bound. Long enough for a slow
// network handshake but short enough that a wedged DNS resolution gives
// up within a single attention span.
const connectTimeout = 15 * time.Second

// startConnect kicks off a Connect attempt for the given connection
// without changing view state. The caller (Init) has already set the
// state to viewConnecting.
func (m AppModel) startConnect(conn *config.Connection) tea.Cmd {
	if conn == nil || m.backendFactory == nil {
		return nil
	}
	factory := m.backendFactory
	return func() tea.Msg {
		b, err := factory(conn)
		if err != nil {
			return connectResultMsg{connection: conn, err: err}
		}
		return runConnect(conn, b)
	}
}

// beginConnect transitions to viewConnecting and starts a connect
// attempt. Used both for picker-driven picks and direct
// connectAttemptMsg dispatches.
func (m AppModel) beginConnect(conn *config.Connection) (AppModel, tea.Cmd) {
	if conn == nil {
		return m, nil
	}
	m.connectingModel = newConnectingModel(conn, m.hideActionHints)
	m.connectingModel.setSize(m.width, m.height)
	m.state = viewConnecting
	return m, m.startConnect(conn)
}

// retryConnect reuses an existing backend (whose stored token may
// have been mutated by an auth modal) and re-runs Connect.
func (m AppModel) retryConnect(conn *config.Connection, b backend.Backend) tea.Cmd {
	return func() tea.Msg {
		return runConnect(conn, b)
	}
}

// runConnect performs a single Connect attempt and classifies the
// result so handleConnectResult can decide between transitioning to
// viewSelect (success), opening an auth modal (recoverable), or
// returning to the picker (unrecoverable).
func runConnect(conn *config.Connection, b backend.Backend) connectResultMsg {
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	if err := b.Connect(ctx); err != nil {
		switch {
		case isTokenMismatchErr(err):
			return connectResultMsg{connection: conn, backend: b, authNeed: authRecoveryTokenMismatch, err: err}
		case isTokenMissingErr(err):
			return connectResultMsg{connection: conn, backend: b, authNeed: authRecoveryTokenMissing, err: err}
		case isAPIKeyErr(err) && conn != nil:
			return connectResultMsg{connection: conn, backend: b, authNeed: authRecoveryAPIKey, err: err}
		default:
			_ = b.Close()
			return connectResultMsg{connection: conn, err: err}
		}
	}
	return connectResultMsg{connection: conn, backend: b}
}

func isTokenMismatchErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "gateway token mismatch")
}

func isTokenMissingErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "gateway token missing")
}

// isAPIKeyErr matches the error string the OpenAI-compat backend emits
// for HTTP 401 / 403 responses. The backend formats the message
// uniformly so the TUI can route it into the API-key auth modal
// without parsing the upstream provider's body.
func isAPIKeyErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "api key required")
}

// handleConnectResult routes a connect outcome: success transitions to
// the agent picker and publishes the new client to the app-layer
// driver; recoverable auth errors open a modal; everything else lands
// the user back on the connections picker with an error banner.
func (m AppModel) handleConnectResult(msg connectResultMsg) (AppModel, tea.Cmd) {
	if msg.err == nil && msg.backend != nil {
		// Success — promote to default and publish the backend.
		if m.store != nil && msg.connection != nil {
			m.store.MarkUsed(msg.connection.ID)
		}
		m.backend = msg.backend
		m.activeConn = msg.connection
		if m.onBackendChanged != nil {
			cb := m.onBackendChanged
			b := msg.backend
			go cb(b) // blocking send; do off the event loop
		}
		m.selectModel = newSelectModel(msg.backend, m.hideActionHints, m.managed, m.activeConn)
		m.selectModel.setSize(m.width, m.height)
		m.state = viewSelect
		var cmd tea.Cmd = m.selectModel.Init()
		if m.onConnsChanged != nil && m.store != nil {
			cb := m.onConnsChanged
			snapshot := *m.store
			notify := func() tea.Msg {
				cb(snapshot)
				return nil
			}
			cmd = tea.Batch(cmd, notify)
		}
		return m, cmd
	}

	if msg.authNeed != authRecoveryNone && msg.backend != nil {
		m.connectingModel.enterAuthModal(msg.connection, msg.backend, msg.authNeed, msg.err)
		return m, nil
	}

	// Unrecoverable: bounce back to the picker with the error.
	m.connectionsModel = newConnectionsModel(m.store, m.hideActionHints)
	m.connectionsModel.setSize(m.width, m.height)
	if msg.err != nil {
		m.connectionsModel.lastErr = msg.err
	} else {
		m.connectionsModel.lastErr = errors.New("connect failed")
	}
	m.state = viewConnections
	return m, m.connectionsModel.Init()
}

func (m AppModel) View() tea.View {
	var v tea.View
	switch m.state {
	case viewConnections:
		v = tea.NewView(m.connectionsModel.View())
	case viewConnecting:
		v = tea.NewView(m.connectingModel.View())
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
