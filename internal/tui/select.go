package tui

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// agentItem is a list item for the agent picker.
type agentItem struct {
	agent      protocol.AgentSummary
	sessionKey string
}

// FilterValue is the haystack the bubbles list filter (here: fuzzy
// match) runs against. Concatenating Name and ID lets the user narrow
// the list by either — typing part of a display name or part of the
// raw agent ID both hit the same agent.
func (i agentItem) FilterValue() string {
	if i.agent.Name != "" && i.agent.Name != i.agent.ID {
		return i.agent.Name + " " + i.agent.ID
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
	subStateList selectSubState = iota
	subStateCreate
	subStateConfirmDelete
)

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// selectModel is the agent selection view.
type selectModel struct {
	list      list.Model
	backend   backend.Backend
	loading   bool
	err       error
	mainKey   string
	selected  bool
	// selecting is true after the user has picked an agent and the
	// app is round-tripping CreateSession to the gateway. While set,
	// the picker freezes: list navigation is disabled and the view
	// renders a loading line so the user can't keep moving the
	// cursor while a request is in flight. Cleared by app.go when a
	// sessionCreatedMsg error bounces back into the picker.
	selecting bool
	// selectingName is the display label of the agent that triggered
	// selecting; surfaced in the loading view so the user sees which
	// agent they chose.
	selectingName string
	hideHints     bool
	// showConnections enables the "Connections" action so the user
	// can jump back to the connections picker from the agent list
	// without going through chat first. Only meaningful in managed
	// mode — legacy embedders that own the connection lifecycle
	// themselves leave this false.
	showConnections bool

	// useWorkspace mirrors backend.Capabilities.AgentWorkspace —
	// only OpenClaw uses the workspace field today. When false, the
	// create-agent form drops the workspace input and CreateAgent is
	// called with an empty Workspace (the backend fills its own
	// defaults: IDENTITY/SOUL markdown for the OpenAI-compat case).
	useWorkspace bool

	// allowAgentManagement mirrors backend.Capabilities.AgentManagement.
	// When false, the picker hides the "New agent" affordance entirely
	// — used for backends like Hermes whose agents are server-managed
	// (one profile = one agent, configured via the Hermes CLI). The
	// agent list still renders normally.
	allowAgentManagement bool

	// activeConn is rendered as a thin status row above the picker
	// so the user always sees which connection is in scope. May be
	// nil for legacy embedders without a connections store.
	activeConn *config.Connection

	// autoPickName, when non-empty on the first agentsLoadedMsg,
	// resolves to an agent (ID first, then case-insensitive Name)
	// and selects it without user interaction — the mechanism the
	// `lucinate chat --agent <name>` subcommand uses to drive the
	// picker past itself. A miss surfaces as a normal err banner so
	// the user lands on the picker with the reason. Cleared after
	// the first attempt regardless of outcome.
	autoPickName string

	// Create-agent form state.
	subState        selectSubState
	nameInput       textinput.Model
	workInput       textinput.Model
	focusedField    int // 0 = name, 1 = workspace (when useWorkspace)
	creating        bool
	createErr       error
	newAgentID      string
	workspaceEdited bool
	nameValidMsg    string

	// Confirm-delete substate.
	pendingDeleteID   string
	pendingDeleteName string
	confirmInput      textinput.Model
	deleting          bool
	deleteErr         error
	// keepFiles is the user's "preserve content / destroy content"
	// toggle. True (the default) maps to backend.DeleteAgentParams
	// DeleteFiles=false — i.e. the non-destructive option is the
	// default, so a mistaken confirmation preserves the agent's file
	// content. The user has to toggle it off (and type the agent name)
	// to destroy content.
	keepFiles bool

	// Terminal dimensions, mirrored from setSize so the create/confirm
	// substates can compute their viewport sizes.
	width  int
	height int

	// body wraps the scrollable middle of whichever substate form is
	// active so a short terminal doesn't drop the textinput, help line,
	// or error feedback off the bottom of the screen. Shared between the
	// create and confirm-delete substates because only one is active at
	// a time.
	body viewport.Model
}

// agentsLoadedMsg is sent when agents are fetched from the gateway.
type agentsLoadedMsg struct {
	result *protocol.AgentsListResult
	err    error
}

// agentDeletedMsg is sent when a delete-agent RPC completes. err is
// nil on success; the picker reloads the list. On error the confirm
// substate stays open with deleteErr surfaced inline so the user can
// retry or cancel.
type agentDeletedMsg struct {
	name string
	err  error
}

// newSelectModel constructs the agent picker. showConnections=true
// surfaces the "Connections" action so managed-mode users can return
// to the connections picker without first entering a chat session.
// The backend's Capabilities.AgentWorkspace flag drives whether the
// create-agent form renders the workspace field — this is read off b
// here so the picker doesn't have to keep asking. activeConn (when
// non-nil) is rendered as a thin status row at the top of the view
// so the user can see which connection is in scope.
func newSelectModel(b backend.Backend, hideHints, showConnections bool, activeConn *config.Connection, disableExitKeys bool, autoPickName string) selectModel {
	useWorkspace := false
	allowAgentMgmt := false
	if b != nil {
		caps := b.Capabilities()
		useWorkspace = caps.AgentWorkspace
		allowAgentMgmt = caps.AgentManagement
	}
	l := list.New(nil, agentDelegate{}, 0, 0)
	l.Title = "Select an agent"
	l.SetShowStatusBar(false)
	// The bubbles list widget renders its own keyboard-hint footer
	// ("↑/k up · ↓/j down · q quit · ? more"). Embedders that surface
	// actions through native controls suppress every hint line — those
	// keys typically aren't reachable from the host's input surface
	// anyway.
	l.SetShowHelp(!hideHints)
	l.Styles.Title = headerStyle
	// Live filtering lets the user narrow a long agent list by pressing
	// `/` and typing a query, reusing the same fuzzy matcher as the
	// model picker (see models.go). Unlike that view, the agent picker
	// starts in plain list mode — filtering is opt-in via `/` so the
	// single-letter action shortcuts (n/d/c) stay reachable until the
	// user chooses to filter.
	l.SetFilteringEnabled(true)
	l.Filter = fuzzyFilter
	if disableExitKeys {
		l.KeyMap.Quit.Unbind()
		l.KeyMap.ForceQuit.Unbind()
	}

	return selectModel{
		list:                 l,
		backend:              b,
		loading:              true,
		hideHints:            hideHints,
		showConnections:      showConnections,
		useWorkspace:         useWorkspace,
		allowAgentManagement: allowAgentMgmt,
		activeConn:           activeConn,
		autoPickName:         autoPickName,
		body:                 viewport.New(),
	}
}

func (m selectModel) loadAgents() tea.Cmd {
	return func() tea.Msg {
		result, err := m.backend.ListAgents(context.Background())
		return agentsLoadedMsg{result: result, err: err}
	}
}

func (m selectModel) createAgent(name, workspace string) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		err := b.CreateAgent(context.Background(), backend.CreateAgentParams{
			Name:      name,
			Workspace: workspace,
		})
		return agentCreatedMsg{name: name, err: err}
	}
}

// deleteAgent dispatches the backend delete RPC. keepFiles inverts to
// DeleteAgentParams.DeleteFiles so the on-screen toggle reads
// naturally ("keep files" / "delete files") while the backend
// receives the destructive flag.
func (m selectModel) deleteAgent(agentID, name string, keepFiles bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		err := b.DeleteAgent(context.Background(), backend.DeleteAgentParams{
			AgentID:     agentID,
			DeleteFiles: !keepFiles,
		})
		return agentDeletedMsg{name: name, err: err}
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

func (m *selectModel) initCreateForm() tea.Cmd {
	m.subState = subStateCreate
	m.focusedField = 0
	m.creating = false
	m.createErr = nil
	m.workspaceEdited = false
	m.nameValidMsg = "Required"

	m.nameInput = textinput.New()
	m.nameInput.CharLimit = 64
	cmd := m.nameInput.Focus()

	if m.useWorkspace {
		m.workInput = textinput.New()
		m.workInput.Placeholder = "~/.openclaw/workspaces/my-agent"
		m.workInput.CharLimit = 256
	}

	m.body.SetYOffset(0)
	return cmd
}

// initConfirmDelete prepares the delete-confirm substate from the
// passed-in item. The display name and ID are snapshotted at entry
// so a list re-render mid-flight cannot resolve to the wrong agent.
// keepFiles defaults to true (non-destructive): the user has to
// actively toggle it off to destroy file content, so a mistaken
// confirmation preserves the agent's files.
func (m *selectModel) initConfirmDelete(item agentItem) tea.Cmd {
	display := item.agent.Name
	if display == "" {
		display = item.agent.ID
	}
	m.subState = subStateConfirmDelete
	m.pendingDeleteID = item.agent.ID
	m.pendingDeleteName = display
	m.deleting = false
	m.deleteErr = nil
	m.keepFiles = true

	m.confirmInput = textinput.New()
	m.confirmInput.CharLimit = 64
	m.confirmInput.Placeholder = display
	m.body.SetYOffset(0)
	return m.confirmInput.Focus()
}

// nameMatches reports whether the typed confirmation matches the
// pending agent's display name. Comparison is case-insensitive with
// whitespace trim — exact-character pedantry is hostile UX, and the
// destructive intent is gated by the typing act, not by perfect
// transcription.
func (m selectModel) nameMatches() bool {
	return m.pendingDeleteName != "" &&
		strings.EqualFold(strings.TrimSpace(m.confirmInput.Value()), m.pendingDeleteName)
}

// keepFilesLabel renders the toggle button label so its current state
// reads off the action surface ("Keep files" when about to switch
// from destructive to preserve, and vice versa).
func (m selectModel) keepFilesLabel() string {
	if m.keepFiles {
		return "Delete files"
	}
	return "Keep files"
}

// switchFocus toggles between the form's input fields. With the
// workspace field hidden (non-OpenClaw backends) there's only one
// focusable input — switchFocus is a no-op there.
func (m *selectModel) switchFocus() tea.Cmd {
	if !m.useWorkspace {
		return nil
	}
	if m.focusedField == 0 {
		m.focusedField = 1
		m.nameInput.Blur()
		return m.workInput.Focus()
	}
	m.focusedField = 0
	m.workInput.Blur()
	return m.nameInput.Focus()
}

func (m selectModel) Update(msg tea.Msg) (selectModel, tea.Cmd) {
	// Once the user has picked an agent we've handed control to
	// app.go, which is round-tripping CreateSession. Drop further
	// input — keystrokes, list-internal cmds — so the user can't
	// keep moving the cursor while we wait. The agentsLoadedMsg /
	// agentCreatedMsg / agentDeletedMsg paths are unreachable here
	// (loadAgents fires only on retry, create, or delete) so it's
	// safe to short-circuit at the top.
	if m.selecting {
		return m, nil
	}

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

		// Honour an external auto-pick request (e.g. `lucinate chat
		// --agent foo`) before the convenience auto-pick of a
		// single-agent list and before the post-create branch — a
		// caller-supplied name must beat both, so a `--agent`
		// mismatch surfaces as an error rather than silently
		// picking the only available agent.
		if m.autoPickName != "" {
			query := m.autoPickName
			m.autoPickName = ""
			matched := false
			for i, a := range msg.result.Agents {
				if a.ID == query {
					m.list.Select(i)
					m.selected = true
					matched = true
					break
				}
			}
			if !matched {
				lc := strings.ToLower(query)
				for i, a := range msg.result.Agents {
					if strings.ToLower(a.Name) == lc {
						m.list.Select(i)
						m.selected = true
						matched = true
						break
					}
				}
			}
			if !matched {
				m.err = fmt.Errorf("agent %q not found", query)
			}
			return m, nil
		}

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

	case agentDeletedMsg:
		if msg.err != nil {
			// Stay in the confirm substate so the user can read
			// the error and either retry (correct the typed name
			// if needed) or cancel cleanly. Don't clear the
			// pending fields — those are needed for retry.
			m.deleting = false
			m.deleteErr = msg.err
			return m, nil
		}
		m.subState = subStateList
		m.pendingDeleteID = ""
		m.pendingDeleteName = ""
		m.deleting = false
		m.deleteErr = nil
		m.keepFiles = true
		m.loading = true
		return m, m.loadAgents()

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	if m.subState == subStateCreate {
		return m.updateCreateForm(msg)
	}
	if m.subState == subStateConfirmDelete {
		return m.updateConfirmDelete(msg)
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m selectModel) handleKey(msg tea.KeyPressMsg) (selectModel, tea.Cmd) {
	if m.subState == subStateCreate {
		return m.handleCreateKey(msg)
	}
	if m.subState == subStateConfirmDelete {
		return m.handleConfirmDeleteKey(msg)
	}

	// Enter is intrinsic list navigation (select the highlighted item)
	// rather than a discoverable view-level command, so it stays inline.
	// It selects from a live filter too, so the user can type to narrow
	// then pick in one keystroke instead of first applying the filter
	// (the bubbles default while typing). Falls through when nothing is
	// selectable — e.g. a filter matching zero agents — so the list
	// still gets the keystroke.
	if msg.String() == "enter" {
		if !m.loading && m.err == nil {
			if item, ok := m.list.SelectedItem().(agentItem); ok {
				m.selected = true
				m.selecting = true
				m.selectingName = item.agent.Name
				if m.selectingName == "" {
					m.selectingName = item.agent.ID
				}
				return m, nil
			}
		}
	}

	// While the filter input is focused, every other key (printable
	// chars, backspace, esc-to-clear, cursor nav) belongs to the list
	// so typing narrows the results instead of firing action shortcuts
	// like `n`/`d`/`c` that collide with characters in agent names.
	if m.list.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}

	// Discoverable shortcuts route through TriggerAction so the help
	// line and the keystroke share a single source of truth (Actions()).
	for _, a := range m.Actions() {
		if a.Key == msg.String() {
			return m.TriggerAction(a.ID)
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// Actions returns the discoverable, view-level commands the agent
// picker currently exposes. The TUI auto-renders the help line from
// this list and dispatches matching keystrokes through TriggerAction;
// embedders render the same list as buttons.
//
// The create-agent sub-state has its own modal Tab/Enter/Esc
// interactions and intentionally exposes no actions — those keys are
// inherent form navigation, not discoverable commands.
func (m selectModel) Actions() []Action {
	if m.selecting {
		// Mirror the loading view: no actionable surface while the
		// gateway round-trip is in flight, so embedders that render
		// Actions as buttons can't smuggle a re-entry past the gate.
		return nil
	}
	if m.filtering() {
		// While the fuzzy filter is focused the only interactions are
		// intrinsic (type to narrow, enter to pick, esc to clear) — the
		// list's own help footer surfaces those. The action shortcuts
		// (n/d/c) have their keys captured as filter text here, so
		// advertising them in the hint line or as embedder buttons
		// would mislead.
		return nil
	}
	if m.subState == subStateConfirmDelete {
		// In confirm-delete the only available actions are the
		// destructive ones. confirm-delete only appears when the
		// typed name matches and no request is in flight — that's
		// how the disabled state is expressed for native embedders
		// (the Action struct has no Enabled flag).
		actions := []Action{
			{ID: "toggle-keep-files", Label: m.keepFilesLabel(), Key: "tab"},
			{ID: "cancel-delete", Label: "Cancel", Key: "esc"},
		}
		if m.nameMatches() && !m.deleting {
			actions = append([]Action{{ID: "confirm-delete", Label: "Delete", Key: "enter"}}, actions...)
		}
		return actions
	}
	if m.subState != subStateList {
		return nil
	}
	var actions []Action
	if !m.loading && m.err == nil && m.allowAgentManagement {
		actions = append(actions, Action{ID: "new-agent", Label: "New agent", Key: "n"})
		if m.list.SelectedItem() != nil {
			actions = append(actions, Action{ID: "delete-agent", Label: "Delete agent", Key: "d"})
		}
	}
	if m.err != nil {
		actions = append(actions, Action{ID: "retry", Label: "Retry", Key: "r"})
	}
	if m.showConnections {
		actions = append(actions, Action{ID: "connections", Label: "Connections", Key: "c"})
		// Config is gated on the same managed-mode flag as Connections;
		// it's also reachable from chat and the connections list.
		actions = append(actions, Action{ID: "config", Label: "Config", Key: ","})
	}
	return actions
}

// TriggerAction invokes the named action. Both keystrokes (via
// handleKey) and embedder calls (via Program.TriggerAction) reach the
// same dispatcher so logic is not duplicated.
func (m selectModel) TriggerAction(id string) (selectModel, tea.Cmd) {
	switch id {
	case "new-agent":
		if m.loading || m.err != nil || !m.allowAgentManagement {
			return m, nil
		}
		return m, m.initCreateForm()
	case "delete-agent":
		if m.loading || m.err != nil || !m.allowAgentManagement {
			return m, nil
		}
		item, ok := m.list.SelectedItem().(agentItem)
		if !ok {
			return m, nil
		}
		return m, m.initConfirmDelete(item)
	case "confirm-delete":
		// Re-validate even though Actions() only emits this when
		// nameMatches()&&!deleting — embedders are still in charge of
		// their own dispatch and we don't trust them to filter.
		if !m.nameMatches() || m.deleting {
			return m, nil
		}
		m.deleting = true
		m.deleteErr = nil
		return m, m.deleteAgent(m.pendingDeleteID, m.pendingDeleteName, m.keepFiles)
	case "cancel-delete":
		m.subState = subStateList
		m.pendingDeleteID = ""
		m.pendingDeleteName = ""
		m.deleting = false
		m.deleteErr = nil
		m.keepFiles = true
		return m, nil
	case "toggle-keep-files":
		m.keepFiles = !m.keepFiles
		return m, nil
	case "retry":
		if m.err == nil {
			return m, nil
		}
		m.loading = true
		m.err = nil
		return m, m.loadAgents()
	case "connections":
		return m, func() tea.Msg { return showConnectionsMsg{} }
	case "config":
		return m, func() tea.Msg { return showConfigMsg{} }
	}
	return m, nil
}

func (m selectModel) handleCreateKey(msg tea.KeyPressMsg) (selectModel, tea.Cmd) {
	if m.creating {
		// Create RPC in flight; ignore all input. The call has
		// already left, so there's nothing to cancel — mirror
		// handleConfirmDeleteKey's freeze while m.deleting.
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.subState = subStateList
		return m, nil

	case "tab", "shift+tab":
		return m, m.switchFocus()

	case "enter":
		name := m.nameInput.Value()
		m.nameValidMsg = validateName(name)
		if m.nameValidMsg != "" {
			return m, nil
		}
		workspace := ""
		if m.useWorkspace {
			workspace = m.workInput.Value()
			if workspace == "" {
				workspace = "~/.openclaw/workspaces/" + name
			}
		}
		m.creating = true
		m.createErr = nil
		return m, m.createAgent(name, workspace)
	}

	return m.updateCreateForm(msg)
}

// handleConfirmDeleteKey routes keystrokes inside the confirm-delete
// substate. The name input is the focal element so most printable
// keys are passed straight through to it; only intrinsic form keys
// (esc / enter / tab) are intercepted. Plain `d` for "delete-agent"
// is intentionally NOT bound here — it's a printable character the
// user might type as part of the agent name. The action key surface
// (Action{ID:"toggle-keep-files", Key:"tab"}) handles native
// embedder dispatch; tab/esc/enter handle direct keystrokes.
func (m selectModel) handleConfirmDeleteKey(msg tea.KeyPressMsg) (selectModel, tea.Cmd) {
	if m.deleting {
		// Request in flight; ignore further input. The user can't
		// cancel an in-flight DeleteAgent — the network call has
		// already left.
		return m, nil
	}
	switch msg.String() {
	case "esc":
		return m.TriggerAction("cancel-delete")
	case "tab", "shift+tab":
		return m.TriggerAction("toggle-keep-files")
	case "enter":
		if m.nameMatches() {
			return m.TriggerAction("confirm-delete")
		}
		// Enter without a matching name is a no-op so the user
		// can't bypass the type-to-confirm gate.
		return m, nil
	}
	var cmd tea.Cmd
	m.confirmInput, cmd = m.confirmInput.Update(msg)
	return m, cmd
}

// updateConfirmDelete forwards non-key messages (cursor blink, etc.)
// to the textinput so it animates correctly while focused.
func (m selectModel) updateConfirmDelete(msg tea.Msg) (selectModel, tea.Cmd) {
	if m.deleting {
		return m, nil
	}
	var cmd tea.Cmd
	m.confirmInput, cmd = m.confirmInput.Update(msg)
	return m, cmd
}

func (m selectModel) updateCreateForm(msg tea.Msg) (selectModel, tea.Cmd) {
	if m.creating {
		// Freeze the inputs while the create RPC is in flight so
		// cursor blink / paste / etc. can't mutate the form mid-commit
		// — matches updateConfirmDelete's guard on m.deleting.
		return m, nil
	}
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

		// Auto-suggest workspace when name changes and user hasn't
		// edited workspace. Skipped for backends without a workspace
		// concept — the suggestion path is OpenClaw-specific.
		if m.useWorkspace && m.nameInput.Value() != prevName && !m.workspaceEdited {
			name := m.nameInput.Value()
			if name != "" {
				m.workInput.SetValue("~/.openclaw/workspaces/" + name)
			} else {
				m.workInput.SetValue("")
			}
		}
	} else if m.useWorkspace {
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
	if m.selecting {
		// Pinned during the CreateSession round-trip so navigation
		// affordances disappear with the rest of the picker UI; the
		// connection banner stays so the user still sees scope.
		banner := renderConnectionBanner(m.activeConn)
		name := m.selectingName
		if name == "" {
			name = "agent"
		}
		return banner + fmt.Sprintf("\n  Loading %s...\n", name)
	}
	hints := m.renderHints()
	banner := renderConnectionBanner(m.activeConn)
	if m.err != nil {
		var b strings.Builder
		b.WriteString(banner)
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("  Error: %v", m.err)))
		b.WriteString("\n\n")
		b.WriteString(hints)
		b.WriteString("\n")
		return b.String()
	}
	if m.subState == subStateCreate {
		return banner + m.viewCreateForm()
	}
	if m.subState == subStateConfirmDelete {
		return banner + m.viewConfirmDelete()
	}
	return banner + m.list.View() + "\n" + hints
}

// renderHints emits the inline action-hint help line, or the empty
// string when the embedder has signalled (via HideActionHints) that it
// will surface the same actions through native controls.
func (m selectModel) renderHints() string {
	if m.hideHints {
		return ""
	}
	return helpStyle.Render(renderActionHints(m.Actions()))
}

func (m selectModel) viewCreateForm() string {
	titleLine := "\n" + headerStyle.Render(" Create new agent ") + "\n"

	// Track per-field line ranges so the focused field stays visible
	// when the terminal is too short to render the whole form.
	var body strings.Builder
	lineCount := 0
	writeLine := func(s string) {
		body.WriteString(s)
		body.WriteString("\n")
		lineCount += strings.Count(s, "\n") + 1
	}

	writeLine("  Name (e.g. my-agent):")
	nameStart := lineCount
	writeLine("  " + m.nameInput.View())
	nameEnd := lineCount - 1
	if m.nameValidMsg != "" && m.nameInput.Value() != "" {
		writeLine("  " + errorStyle.Render(m.nameValidMsg))
		nameEnd = lineCount - 1
	}
	writeLine("")

	wsStart, wsEnd := 0, 0
	if m.useWorkspace {
		writeLine("  Workspace:")
		wsStart = lineCount
		writeLine("  " + m.workInput.View())
		wsEnd = lineCount - 1
		writeLine("")
	} else {
		// Local-agent backends (OpenAI-compat) seed the agent's
		// IDENTITY.md / SOUL.md with defaults at create time. The
		// user can edit those files on disk afterwards — the path
		// is shown so they know where to find them.
		writeLine(helpStyle.Render("  Identity and Soul markdown will be seeded with defaults under\n  ~/.lucinate/agents/<connection>/<agent>/ — edit them to customise."))
		writeLine("")
	}

	var footer strings.Builder
	if m.creating {
		footer.WriteString(statusStyle.Render("  Creating agent..."))
	} else if m.createErr != nil {
		footer.WriteString(errorStyle.Render(fmt.Sprintf("  Error: %v", m.createErr)))
		footer.WriteString("\n")
		footer.WriteString(helpStyle.Render("  Enter: retry | Esc: cancel"))
	} else if m.useWorkspace {
		footer.WriteString(helpStyle.Render("  Tab: switch fields | Enter: create | Esc: cancel"))
	} else {
		footer.WriteString(helpStyle.Render("  Enter: create | Esc: cancel"))
	}

	if m.height <= 0 {
		return titleLine + body.String() + footer.String()
	}

	m.body.SetContent(body.String())
	focusStart, focusEnd := nameStart, nameEnd
	if m.focusedField == 1 && m.useWorkspace {
		focusStart, focusEnd = wsStart, wsEnd
	}
	m.scrollBodyTo(focusStart, focusEnd, lineCount)
	return lipgloss.JoinVertical(lipgloss.Left, titleLine, m.body.View(), footer.String())
}

// viewConfirmDelete renders the loud delete-confirmation view. The
// page deliberately spends real estate on warnings: the user is
// about to lose the agent's identity, soul, transcript, and (unless
// they toggle Keep files) the underlying file content.
func (m selectModel) viewConfirmDelete() string {
	titleLine := "\n" + headerStyle.Render(" Delete agent ") + "\n"

	// Build the scrollable body and track the line at which the
	// confirmation textinput starts so the viewport can scroll to keep
	// it visible on short terminals — the textinput is the only
	// interactive element here and must always be reachable.
	var body strings.Builder
	lineCount := 0
	writeLine := func(s string) {
		body.WriteString(s)
		body.WriteString("\n")
		lineCount += strings.Count(s, "\n") + 1
	}

	heading := fmt.Sprintf("  Delete %q?", m.pendingDeleteName)
	writeLine(errorStyle.Render(heading))
	writeLine(errorStyle.Render("  ⚠  This is permanent and cannot be undone."))
	writeLine("")

	writeLine("  This will remove:")
	writeLine("    • The agent's metadata and listing")
	writeLine("    • The full conversation transcript")
	if m.useWorkspace {
		writeLine("    • Gateway bindings for this agent")
	}
	writeLine("")

	currentFilesMode := "Delete files"
	if m.keepFiles {
		currentFilesMode = "Keep files"
	}
	writeLine("  Files mode: " + toggleView(currentFilesMode, "Delete files", "Keep files") + helpStyle.Render("   (tab to toggle)"))
	writeLine("    " + helpStyle.Render(m.filesModeDescription()))
	writeLine("")

	writeLine("  Back up anything you want to keep first:")
	writeLine("    " + helpStyle.Render(m.backupHint()))
	writeLine("")

	writeLine(fmt.Sprintf("  Type the agent name (%q) to confirm:", m.pendingDeleteName))
	inputStart := lineCount
	writeLine("  " + m.confirmInput.View())
	inputEnd := lineCount - 1
	if v := strings.TrimSpace(m.confirmInput.Value()); v != "" && !m.nameMatches() {
		writeLine("  " + errorStyle.Render("Name doesn't match"))
		inputEnd = lineCount - 1
	}

	var footer strings.Builder
	switch {
	case m.deleting:
		footer.WriteString(statusStyle.Render("  Deleting..."))
	case m.deleteErr != nil:
		footer.WriteString(errorStyle.Render(fmt.Sprintf("  Error: %v", m.deleteErr)))
		footer.WriteString("\n")
		footer.WriteString(helpStyle.Render("  enter: retry (when name matches)  ·  esc: cancel"))
	case m.nameMatches():
		footer.WriteString(helpStyle.Render("  enter: delete  ·  tab: toggle files mode  ·  esc: cancel"))
	default:
		footer.WriteString(helpStyle.Render("  type the name above, then enter to delete  ·  tab: toggle files mode  ·  esc: cancel"))
	}

	// When the parent hasn't called setSize yet (e.g. unit tests, or
	// the very first frame before WindowSizeMsg arrives) the viewport
	// has zero dimensions and would swallow the body content. Fall
	// back to rendering the body inline so the rendered string still
	// reflects what the user will see.
	if m.height <= 0 {
		return titleLine + body.String() + footer.String()
	}

	m.body.SetContent(body.String())
	m.scrollBodyTo(inputStart, inputEnd, lineCount)
	return lipgloss.JoinVertical(lipgloss.Left, titleLine, m.body.View(), footer.String())
}

// filesModeDescription explains what the current Keep/Delete files
// toggle will actually do — wording is backend-aware so OpenClaw users
// see "gateway workspace" copy and OpenAI-compat users see the local
// archive path.
func (m selectModel) filesModeDescription() string {
	if m.useWorkspace {
		if m.keepFiles {
			return "Agent workspace files on the gateway will be left in place; only bindings are removed."
		}
		return "Agent workspace files on the gateway will be deleted along with the agent."
	}
	if m.keepFiles {
		return "The agent directory will be moved to <root>/.archive/<id>-<timestamp>/ so IDENTITY.md, SOUL.md and the transcript are recoverable on disk."
	}
	return "IDENTITY.md, SOUL.md, and history.jsonl will be removed from disk."
}

// backupHint returns the path the user should back up before deleting,
// formatted for the active backend type. For local-disk agents we try
// to render the actual conn-id-rooted path; for OpenClaw the relevant
// data lives on the gateway filesystem and we describe it in words.
func (m selectModel) backupHint() string {
	if m.useWorkspace {
		return "Agent workspace and bindings on the gateway"
	}
	connID := ""
	if m.activeConn != nil {
		connID = m.activeConn.ID
	}
	if connID == "" {
		connID = "<connection>"
	}
	return fmt.Sprintf("~/.lucinate/agents/%s/%s/", connID, m.pendingDeleteID)
}

func (m selectModel) selectedAgent() (agentItem, bool) {
	item, ok := m.list.SelectedItem().(agentItem)
	return item, ok
}

// filtering reports whether the agent list's fuzzy filter input is
// focused — i.e. the user pressed `/` and is typing a query. The app
// consults this so the q-to-quit shortcut and the embedder input-focus
// signal treat a typed `q` as filter text rather than a quit request.
func (m selectModel) filtering() bool {
	return m.list.FilterState() == list.Filtering
}

func (m *selectModel) setSize(w, h int) {
	m.width = w
	m.height = h
	m.list.SetSize(w, h-2)
	m.sizeFormBody()
}

// sizeFormBody sizes the body viewport used by both the create-agent
// and confirm-delete substates. The visible body height is whatever
// the terminal leaves after the title (3 lines: leading blank + header
// + trailing blank) and footer (3 lines: blank + help/error + trailing
// margin), floored so at least one row is visible on pathological
// heights.
func (m *selectModel) sizeFormBody() {
	const titleLines = 3
	const footerLines = 3
	const minBodyHeight = 5

	bodyH := m.height - titleLines - footerLines
	if bodyH < minBodyHeight {
		bodyH = minBodyHeight
	}
	bodyW := m.width
	if bodyW < 1 {
		bodyW = 1
	}
	m.body.SetWidth(bodyW)
	m.body.SetHeight(bodyH)
}

// scrollBodyTo nudges the body viewport so the line range [start, end]
// (inclusive) within the current body content is fully visible. Used
// by viewCreateForm / viewConfirmDelete to keep the focused textinput
// on-screen.
func (m *selectModel) scrollBodyTo(start, end, totalLines int) {
	height := m.body.Height()
	if height <= 0 || totalLines == 0 {
		return
	}
	offset := m.body.YOffset()
	switch {
	case start < offset:
		offset = start
	case end >= offset+height:
		offset = end - height + 1
	}
	if offset < 0 {
		offset = 0
	}
	maxOffset := totalLines - height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	m.body.SetYOffset(offset)
}
