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
	viewCrons
	viewModelPicker
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
	DisableExitKeys bool
	DisableMouse    bool

	// BrightCursor pins the chat textarea cursor's on-frame to ANSI 15
	// (bright white) instead of Bubbles' default ANSI 7. See
	// app.RunOptions for the full rationale; this is the unexported
	// plumbing.
	BrightCursor bool

	// Store, when non-nil, enables managed mode: the TUI owns the
	// connection lifecycle, runs the connections picker as the entry
	// view (or auto-picks via the same decision tree the resolver
	// uses), and surfaces the /connections command. Mutually
	// exclusive with passing a pre-connected backend to NewApp.
	Store *config.Connections

	// Initial is the connection the TUI will attempt first when in
	// managed mode. A nil Initial drops the user into the picker.
	Initial *config.Connection

	// InitialAgent / InitialSession / InitialMessage are the
	// pre-resolution overrides used by `lucinate chat`. See
	// app.RunOptions for the full rationale; this is the unexported
	// plumbing. Each is consumed once at the transition that would
	// otherwise have prompted the user, and the AppModel clears them
	// on auth-cancel / connect-failure / `/connections` so a different
	// connection's picker doesn't inherit the original intent.
	InitialAgent   string
	InitialSession string
	InitialMessage string

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

	// OnFocusedFieldChanged, if non-nil, is invoked whenever the
	// active view's focused text-input changes (Tab/Shift-Tab inside a
	// form, or entry into a new view that lands focus on a different
	// field). The string is the new field's current value at the
	// moment of transition, so embedders driving an external input
	// surface (a native-platform host's text field, say) can hydrate
	// it to match. See app.RunOptions for the full rationale.
	OnFocusedFieldChanged func(value string)
}

// AppModel is the root bubbletea model.
type AppModel struct {
	state            viewState
	connectionsModel connectionsModel
	connectingModel  connectingModel
	selectModel      selectModel
	chatModel        chatModel
	sessionsModel    sessionsModel
	configModel      configModel
	cronsModel       cronsModel
	modelPicker      modelPickerModel
	backend          backend.Backend
	activeConn       *config.Connection // the connection backend belongs to; rendered in status bars
	store            *config.Connections
	backendFactory   BackendFactory
	onBackendChanged func(backend.Backend)
	onConnsChanged   func(config.Connections)
	prefs            config.Preferences
	width            int
	height           int
	hideInput        bool
	hideActionHints  bool
	disableExitKeys  bool
	disableMouse     bool
	brightCursor     bool
	managed          bool

	onInputFocusChanged func(bool)
	lastWantsInput      bool
	inputFocusReported  bool

	onActionsChanged func([]Action)
	lastActions      []Action
	actionsReported  bool

	onFocusedFieldChanged func(string)
	lastFocusedFieldKey   string
	focusedFieldReported  bool

	// Update-check state. Populated when the daily startup check
	// finds a release the user hasn't yet seen.
	updateAvailable bool
	updateLatest    string
	updateURL       string

	// One-shot overrides supplied by `lucinate chat`. Consumed at
	// the first transition that would otherwise have prompted the
	// user, and cleared on auth-cancel / unrecoverable connect
	// failure / `/connections` so they don't follow the user across
	// connection scopes.
	initialAgent   string
	initialSession string
	initialMessage string
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
		backend:               b,
		prefs:                 config.LoadPreferences(),
		hideInput:             opts.HideInputArea,
		hideActionHints:       opts.HideActionHints,
		disableExitKeys:       opts.DisableExitKeys,
		disableMouse:          opts.DisableMouse,
		brightCursor:          opts.BrightCursor,
		store:                 opts.Store,
		backendFactory:        opts.BackendFactory,
		onBackendChanged:      opts.OnBackendChanged,
		onConnsChanged:        opts.OnConnectionsChanged,
		managed:               managed,
		onInputFocusChanged:   opts.OnInputFocusChanged,
		onActionsChanged:      opts.OnActionsChanged,
		onFocusedFieldChanged: opts.OnFocusedFieldChanged,
		initialAgent:          opts.InitialAgent,
		initialSession:        opts.InitialSession,
		initialMessage:        opts.InitialMessage,
	}

	switch {
	case !managed:
		// Legacy: jump straight into the agent picker. No active
		// connection — embedders manage that elsewhere.
		m.state = viewSelect
		m.selectModel = newSelectModel(b, opts.HideActionHints, managed, nil, opts.DisableExitKeys, "")
	case opts.Initial != nil:
		// Managed with an initial pick: try to connect first, surface
		// errors / auth modals from there.
		m.state = viewConnecting
		m.connectingModel = newConnectingModel(opts.Initial, opts.HideActionHints)
	default:
		// Managed without an initial pick: open the connections picker.
		m.state = viewConnections
		m.connectionsModel = newConnectionsModel(opts.Store, opts.HideActionHints, opts.DisableExitKeys)
	}

	return m
}

func (m AppModel) Init() tea.Cmd {
	var cmd tea.Cmd
	switch m.state {
	case viewSelect:
		cmd = m.selectModel.Init()
	case viewConnecting:
		cmd = m.startConnect(m.connectingModel.connection)
	case viewConnections:
		cmd = m.connectionsModel.Init()
	}
	if upd := updateCheckCmd(m.prefs); upd != nil {
		cmd = tea.Batch(cmd, upd)
	}
	return cmd
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
	nextModel, cmd = next.maybeNotifyActions(cmd)
	next = nextModel.(AppModel)
	return next.maybeNotifyFocusedField(cmd)
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
	case viewCrons:
		return m.cronsModel.Actions()
	case viewModelPicker:
		return m.modelPicker.Actions()
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
	case viewCrons:
		var cmd tea.Cmd
		m.cronsModel, cmd = m.cronsModel.TriggerAction(id)
		return m, cmd
	case viewModelPicker:
		var cmd tea.Cmd
		m.modelPicker, cmd = m.modelPicker.TriggerAction(id)
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

// computeFocusedField returns a pair (identityKey, currentValue) for
// the active view's focused text input. The identityKey changes only
// when the FIELD changes (not when its value mutates from a user
// keystroke), so dedup against it fires the callback on transitions
// without spamming on every typed character. The currentValue is read
// at notification time so embedders see the field's pre-fill in Edit
// mode and the empty initial state on Add.
func (m AppModel) computeFocusedField() (key, value string) {
	switch m.state {
	case viewConnections:
		return m.connectionsModel.focusedFieldIdentity()
	}
	// Other views (chat, agent picker, sessions, config, connecting)
	// either have no field-level focus or only ever expose one input;
	// embedders that need single-input hydration can still observe
	// OnInputFocusChanged for view-level transitions.
	return "", ""
}

// maybeNotifyFocusedField invokes OnFocusedFieldChanged when the
// focused-field identity changes. Mirrors maybeNotifyInputFocus /
// maybeNotifyActions: the first post-Init call always fires so the
// embedder sees the starting state, and the callback runs from a
// tea.Cmd to keep the event loop responsive.
func (m AppModel) maybeNotifyFocusedField(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	if m.onFocusedFieldChanged == nil {
		return m, cmd
	}
	key, value := m.computeFocusedField()
	if m.focusedFieldReported && key == m.lastFocusedFieldKey {
		return m, cmd
	}
	m.lastFocusedFieldKey = key
	m.focusedFieldReported = true
	cb := m.onFocusedFieldChanged
	notify := func() tea.Msg {
		cb(value)
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
		case viewCrons:
			m.cronsModel.setSize(msg.Width, msg.Height)
		case viewModelPicker:
			m.modelPicker.setSize(msg.Width, msg.Height)
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

	case updateCheckDoneMsg:
		// Mutate prefs on the main loop so this never races the
		// config view's writer. Newer is set only when there is a
		// genuinely new version the user hasn't already seen.
		m.prefs.LastUpdateCheck = msg.At
		if msg.LatestSeen != "" {
			m.prefs.LatestSeenVersion = msg.LatestSeen
		}
		if msg.Newer {
			m.updateAvailable = true
			m.updateLatest = msg.LatestSeen
			m.updateURL = msg.URL
			m.chatModel.updateLatest = msg.LatestSeen
		}
		prefs := m.prefs
		return m, func() tea.Msg {
			_ = config.SavePreferences(prefs)
			return nil
		}

	case ConnStateMsg:
		m.chatModel.applyConnState(msg)
		return m, nil

	case tea.FocusMsg:
		m.chatModel.terminalFocused = true
		return m, nil

	case tea.BlurMsg:
		m.chatModel.terminalFocused = false
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
		// Clear any one-shot `lucinate chat` overrides — agent IDs
		// are not portable across connections, and a reconnect via
		// /connections is exactly the cue that the user has changed
		// their mind about scope.
		m.initialAgent = ""
		m.initialSession = ""
		m.initialMessage = ""
		m.connectionsModel = newConnectionsModel(m.store, m.hideActionHints, m.disableExitKeys)
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
			// Clear `lucinate chat` overrides: the user just signalled
			// they don't want this connection, and a different one
			// won't share the same agent / session keys.
			m.initialAgent = ""
			m.initialSession = ""
			m.initialMessage = ""
			m.connectionsModel = newConnectionsModel(m.store, m.hideActionHints, m.disableExitKeys)
			m.connectionsModel.setSize(m.width, m.height)
			m.state = viewConnections
			if msg.backend != nil {
				_ = msg.backend.Close()
			}
			return m, m.connectionsModel.Init()
		}
		// Retry connect with the same backend (token has been
		// stored/cleared as appropriate by the modal). Overrides
		// stay set: a successful retry should still drive the
		// auto-pick the original invocation requested.
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

	case showCronsMsg:
		cron, ok := m.backend.(backend.CronBackend)
		if !ok {
			// Capability missing — bounce silently. The /crons handler
			// in commands.go renders the user-facing "not available"
			// message before dispatching this msg, so reaching this
			// branch implies a programming error elsewhere.
			return m, nil
		}
		m.cronsModel = newCronsModel(cron, msg.filterAgentID, msg.filterLabel, m.hideActionHints, m.activeConn, m.disableExitKeys)
		m.cronsModel.setSize(m.width, m.height)
		m.state = viewCrons
		return m, m.cronsModel.Init()

	case goBackFromCronsMsg:
		m.state = viewChat
		return m, nil

	case showSessionsMsg:
		m.sessionsModel = newSessionsModel(m.backend, msg.agentID, msg.agentName, msg.modelID, msg.mainKey, m.hideActionHints, m.activeConn, m.disableExitKeys)
		m.sessionsModel.setSize(m.width, m.height)
		m.state = viewSessions
		return m, m.sessionsModel.Init()

	case showModelPickerMsg:
		m.modelPicker = newModelPickerModel(m.backend, msg.sessionKey, msg.currentModelID, m.hideActionHints, m.activeConn, m.disableExitKeys)
		m.modelPicker.setSize(m.width, m.height)
		m.state = viewModelPicker
		return m, m.modelPicker.Init()

	case goBackFromModelPickerMsg:
		m.state = viewChat
		return m, nil

	case sessionSelectedMsg:
		agentID := msg.agentID
		if agentID == "" {
			agentID = m.sessionsModel.agentID
		}
		m.chatModel = newChatModel(m.backend, msg.sessionKey, agentID, msg.agentName, msg.modelID, m.prefs, m.hideInput, connectionLabel(m.activeConn), "", m.brightCursor)
		m.chatModel.setSize(m.width, m.height)
		m.state = viewChat
		return m, m.chatModel.Init()

	case cronTranscriptMsg:
		// Cron-isolated runs don't keep a queryable chat session, so
		// rebuild the transcript from the run log instead — that's the
		// same data the run-history previews on the cron-detail page
		// already use.
		m.chatModel = newChatModel(m.backend, "", msg.job.AgentID, msg.agentName, "", m.prefs, true, connectionLabel(m.activeConn), "", m.brightCursor)
		m.chatModel.setSize(m.width, m.height)
		m.chatModel.messages = buildCronTranscriptMessages(cronPayloadText(msg.job), msg.runs, m.chatModel.renderer)
		m.chatModel.historyLoading = false
		m.chatModel.updateViewport()
		m.state = viewChat
		return m, nil

	case newSessionCreatedMsg:
		if msg.err != nil {
			m.sessionsModel.err = msg.err
			m.sessionsModel.loading = false
			return m, nil
		}
		m.chatModel = newChatModel(m.backend, msg.sessionKey, m.sessionsModel.agentID, msg.agentName, msg.modelID, m.prefs, m.hideInput, connectionLabel(m.activeConn), "", m.brightCursor)
		m.chatModel.setSize(m.width, m.height)
		m.state = viewChat
		return m, m.chatModel.Init()

	case goBackFromSessionsMsg:
		m.state = viewChat
		return m, nil

	case sessionCreatedMsg:
		if msg.err != nil {
			m.selectModel.err = msg.err
			// Drop the selection-loading lock so the user can
			// retry / pick a different agent — without this the
			// picker would stay frozen on the loading line.
			m.selectModel.selecting = false
			m.selectModel.selectingName = ""
			m.state = viewSelect
			return m, nil
		}
		// This is the path the `lucinate chat` auto-pick lands on
		// (selectModel sets selected=true → viewSelect block fires
		// CreateSession → sessionCreatedMsg). Consume the one-shot
		// initial message here so the chat view drains it after
		// loading history.
		initialMsg := m.initialMessage
		m.initialMessage = ""
		// Drop the selection-loading lock now the round-trip has
		// landed. selectModel outlives the picker view (it's only
		// reconstructed on connect, not on /agents), so without
		// this the lock would still be set when the user returns
		// via /agents — pinning the picker on "Loading <agent>..."
		// for the agent they just opened.
		m.selectModel.selecting = false
		m.selectModel.selectingName = ""
		m.chatModel = newChatModel(m.backend, msg.sessionKey, msg.agentID, msg.agentName, msg.modelID, m.prefs, m.hideInput, connectionLabel(m.activeConn), initialMsg, m.brightCursor)
		m.chatModel.setSize(m.width, m.height)
		m.state = viewChat
		return m, m.chatModel.Init()

	case TriggerActionMsg:
		return m.TriggerAction(msg.ID)

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			if m.disableExitKeys {
				// Embedded host disallows process termination — on a
				// native-platform shell whose OS forbids quitting,
				// tea.Quit would only stop the TUI loop while the
				// host view stays mounted. Swallow the keypress so
				// it doesn't accidentally tear down half the program.
				return m, nil
			}
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
				// Honour an explicit `lucinate chat --session <key>`
				// override; this beats both the literal "main" and
				// the connection's default-agent MainKey. One-shot:
				// cleared so a later selection on the same picker
				// doesn't keep landing on the original key.
				if m.initialSession != "" {
					createKey = m.initialSession
					m.initialSession = ""
				}
				timeout := m.requestTimeoutFromPrefs()
				return m, func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), timeout)
					defer cancel()
					key, err := b.CreateSession(ctx, agentID, createKey)
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

	case viewCrons:
		var cmd tea.Cmd
		m.cronsModel, cmd = m.cronsModel.Update(msg)
		return m, cmd

	case viewModelPicker:
		var cmd tea.Cmd
		m.modelPicker, cmd = m.modelPicker.Update(msg)
		return m, cmd
	}

	return m, nil
}

// connectTimeoutFromPrefs returns the per-attempt deadline for the
// initial connect, sourced from the user's config-screen value with a
// safety floor in case the on-disk prefs are corrupt.
func (m AppModel) connectTimeoutFromPrefs() time.Duration {
	secs := m.prefs.ConnectTimeoutSeconds
	if secs <= 0 {
		secs = config.DefaultConnectTimeoutSeconds
	}
	return time.Duration(secs) * time.Second
}

// requestTimeoutFromPrefs returns the per-RPC deadline used to cap
// in-TUI gateway calls so a silently-stuck request surfaces as an
// error instead of freezing the picker. Derived as 2× the socket
// connect deadline so it stays above the WebSocket handshake floor —
// a request can never legitimately complete faster than its
// connection, so a deadline tighter than the connect timeout would
// race the dial it depends on.
func (m AppModel) requestTimeoutFromPrefs() time.Duration {
	return 2 * m.connectTimeoutFromPrefs()
}

// startConnect kicks off a Connect attempt for the given connection
// without changing view state. The caller (Init) has already set the
// state to viewConnecting.
func (m AppModel) startConnect(conn *config.Connection) tea.Cmd {
	if conn == nil || m.backendFactory == nil {
		return nil
	}
	factory := m.backendFactory
	timeout := m.connectTimeoutFromPrefs()
	return func() tea.Msg {
		b, err := factory(conn)
		if err != nil {
			return connectResultMsg{connection: conn, err: err}
		}
		return runConnect(conn, b, timeout)
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
	timeout := m.connectTimeoutFromPrefs()
	return func() tea.Msg {
		return runConnect(conn, b, timeout)
	}
}

// runConnect performs a single Connect attempt and classifies the
// result so handleConnectResult can decide between transitioning to
// viewSelect (success), opening an auth modal (recoverable), or
// returning to the picker (unrecoverable).
func runConnect(conn *config.Connection, b backend.Backend, timeout time.Duration) connectResultMsg {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := b.Connect(ctx); err != nil {
		switch {
		case isNotPairedErr(err):
			return connectResultMsg{connection: conn, backend: b, authNeed: authRecoveryNotPaired, err: err}
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

// isNotPairedErr matches the gateway's NOT_PAIRED rejection. The
// openclaw-go SDK formats hello rejections as
// "connect rejected: NOT_PAIRED: pairing required", so the code is
// the most reliable substring to look for.
func isNotPairedErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "NOT_PAIRED")
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
		// Consume the one-shot --agent override here: the picker will
		// resolve it on its first agentsLoadedMsg. Cleared so a later
		// reconnect (auth retry, /connections, etc.) doesn't re-apply
		// it against an unrelated connection's agent list.
		autoPick := m.initialAgent
		m.initialAgent = ""
		m.selectModel = newSelectModel(msg.backend, m.hideActionHints, m.managed, m.activeConn, m.disableExitKeys, autoPick)
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
	// Clear `lucinate chat` overrides — a different connection
	// from the picker won't share agent / session scope.
	m.initialAgent = ""
	m.initialSession = ""
	m.initialMessage = ""
	m.connectionsModel = newConnectionsModel(m.store, m.hideActionHints, m.disableExitKeys)
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
	case viewCrons:
		v = tea.NewView(m.cronsModel.View())
	case viewModelPicker:
		v = tea.NewView(m.modelPicker.View())
	default:
		v = tea.NewView("")
	}
	v.AltScreen = true
	if !m.disableMouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	v.KeyboardEnhancements.ReportEventTypes = true
	v.ReportFocus = true
	return v
}
