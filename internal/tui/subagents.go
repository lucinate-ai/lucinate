package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// subagentItem is a list-row representation of one subagent child.
type subagentItem struct {
	info backend.SubagentInfo
}

func (i subagentItem) FilterValue() string {
	if label := strings.TrimSpace(i.info.Label); label != "" {
		return label
	}
	return i.info.SessionKey
}

// subagentDelegate renders each row of the browser list. Two visual
// lines: a foregrounded label / session key on top, a subtitle with
// status, agent, model, depth, and the last-message preview below.
type subagentDelegate struct{}

func (d subagentDelegate) Height() int                             { return 2 }
func (d subagentDelegate) Spacing() int                            { return 1 }
func (d subagentDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d subagentDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	row, ok := item.(subagentItem)
	if !ok {
		return
	}

	title := strings.TrimSpace(row.info.Label)
	if title == "" {
		title = row.info.SessionKey
	}

	statusBits := []string{statusGlyph(row.info.Status) + " " + row.info.Status}
	if row.info.AgentID != "" {
		statusBits = append(statusBits, "agent="+row.info.AgentID)
	}
	if row.info.Model != "" {
		statusBits = append(statusBits, "model="+row.info.Model)
	}
	if row.info.SpawnDepth > 0 {
		statusBits = append(statusBits, fmt.Sprintf("depth=%d", row.info.SpawnDepth))
	}
	subtitle := strings.Join(statusBits, "  ")
	if preview := strings.TrimSpace(row.info.LastMessage); preview != "" {
		if len(preview) > 60 {
			preview = preview[:57] + "..."
		}
		subtitle = subtitle + "  " + preview
	}

	if index == m.Index() {
		str := lipgloss.NewStyle().
			Foreground(accent).
			Bold(true).
			Render(fmt.Sprintf("> %s", title))
		str += "\n" + lipgloss.NewStyle().
			Foreground(subtle).
			Render(fmt.Sprintf("  %s", subtitle))
		fmt.Fprint(w, str)
		return
	}
	str := fmt.Sprintf("  %s", title)
	str += "\n" + lipgloss.NewStyle().
		Foreground(subtle).
		Render(fmt.Sprintf("  %s", subtitle))
	fmt.Fprint(w, str)
}

// statusGlyph maps a subagent status string to a short glyph for the
// list-row subtitle. Unknown statuses fall through to a neutral dot so
// the row still reads cleanly.
func statusGlyph(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "active", "in_progress":
		return "●"
	case "completed", "success", "ok":
		return "✓"
	case "failed", "error":
		return "✗"
	case "aborted", "cancelled", "canceled":
		return "⨯"
	case "timed-out", "timed_out", "timeout":
		return "⏳"
	default:
		return "·"
	}
}

// subagentsMode tracks which sub-screen the browser is on.
type subagentsMode int

const (
	subagentsModeList subagentsMode = iota
	subagentsModeSpawn
	subagentsModeSteer
)

// subagentsModel is the /subagents browser view.
type subagentsModel struct {
	list          list.Model
	backend       backend.Backend
	parentKey     string
	parentAgentID string
	hideHints     bool
	loading       bool
	err           error
	notice        string
	activeConn    *config.Connection

	mode          subagentsMode
	form          textarea.Model
	formFor       string // session key being steered, when mode == subagentsModeSteer
	spawnDefaults config.SubagentDefaults
}

// newSubagentsModel constructs a fresh browser. parentKey is the
// active chat session whose subagents should appear; parentAgentID is
// passed to spawn-form defaults. The activeConn is rendered above the
// list (banner) so the user always sees scope.
func newSubagentsModel(b backend.Backend, parentKey, parentAgentID string, hideHints bool, activeConn *config.Connection, disableExitKeys bool, defaults config.SubagentDefaults) subagentsModel {
	l := list.New(nil, subagentDelegate{}, 0, 0)
	l.Title = "Subagents"
	l.SetShowStatusBar(false)
	l.SetShowHelp(!hideHints)
	l.Styles.Title = headerStyle
	l.SetFilteringEnabled(false)
	if disableExitKeys {
		l.KeyMap.Quit.Unbind()
		l.KeyMap.ForceQuit.Unbind()
	}

	form := textarea.New()
	form.Placeholder = ""
	form.CharLimit = 0
	form.SetHeight(4)
	form.ShowLineNumbers = false
	form.Prompt = ""

	return subagentsModel{
		list:          l,
		backend:       b,
		parentKey:     parentKey,
		parentAgentID: parentAgentID,
		hideHints:     hideHints,
		loading:       true,
		activeConn:    activeConn,
		form:          form,
		spawnDefaults: defaults,
	}
}

// loadSubagents fetches the canonical subagent list from the backend.
// Returns a tea.Cmd that resolves to subagentsLoadedMsg.
func (m subagentsModel) loadSubagents() tea.Cmd {
	sub, ok := m.backend.(backend.SubagentBackend)
	if !ok {
		return func() tea.Msg {
			return subagentsLoadedMsg{err: errors.New("subagent management is not available on this connection")}
		}
	}
	parent := m.parentKey
	return func() tea.Msg {
		items, err := sub.SubagentsList(context.Background(), parent)
		return subagentsLoadedMsg{items: items, err: err}
	}
}

func (m subagentsModel) Init() tea.Cmd {
	return m.loadSubagents()
}

// rebuildList re-populates the bubbles list from a slice of SubagentInfo.
func (m *subagentsModel) rebuildList(items []backend.SubagentInfo) {
	listItems := make([]list.Item, 0, len(items))
	for _, info := range items {
		listItems = append(listItems, subagentItem{info: info})
	}
	m.list.SetItems(listItems)
}

func (m subagentsModel) Update(msg tea.Msg) (subagentsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case subagentsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.rebuildList(msg.items)
		return m, nil

	case subagentSpawnedMsg:
		m.mode = subagentsModeList
		m.form.Reset()
		if msg.err != nil {
			m.notice = "spawn failed: " + msg.err.Error()
			return m, nil
		}
		if msg.info != nil {
			m.notice = "spawned: " + msg.info.SessionKey
		} else {
			m.notice = "spawned"
		}
		m.loading = true
		return m, m.loadSubagents()

	case subagentKilledMsg:
		if msg.err != nil {
			m.notice = "kill failed: " + msg.err.Error()
		} else {
			m.notice = "killed: " + msg.sessionKey
		}
		m.loading = true
		return m, m.loadSubagents()

	case subagentSteeredMsg:
		m.mode = subagentsModeList
		m.form.Reset()
		if msg.err != nil {
			m.notice = "steer failed: " + msg.err.Error()
		} else {
			m.notice = "steered: " + msg.sessionKey
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	if m.mode == subagentsModeList {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.form, cmd = m.form.Update(msg)
	return m, cmd
}

// handleKey routes a keypress through the active mode. The list mode
// has its own selection / action keys; the form modes capture Enter
// (submit), Esc (cancel), Ctrl+C (cancel) and pass everything else to
// the underlying textarea.
func (m subagentsModel) handleKey(msg tea.KeyPressMsg) (subagentsModel, tea.Cmd) {
	if m.mode == subagentsModeSpawn || m.mode == subagentsModeSteer {
		switch msg.String() {
		case "esc":
			m.mode = subagentsModeList
			m.form.Reset()
			return m, nil
		case "ctrl+s", "alt+enter":
			return m.submitForm()
		}
		var cmd tea.Cmd
		m.form, cmd = m.form.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "up", "k":
		m.list, _ = m.list.Update(msg)
		return m, nil
	case "down", "j":
		m.list, _ = m.list.Update(msg)
		return m, nil
	case "r":
		return m.TriggerAction("refresh")
	case "n":
		return m.TriggerAction("spawn")
	case "K", "x":
		return m.TriggerAction("kill")
	case "s":
		return m.TriggerAction("steer")
	}

	for _, a := range m.Actions() {
		if a.Key == msg.String() {
			return m.TriggerAction(a.ID)
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// submitForm dispatches the active form (spawn / steer) to the backend.
func (m subagentsModel) submitForm() (subagentsModel, tea.Cmd) {
	text := strings.TrimSpace(m.form.Value())
	if text == "" {
		return m, nil
	}
	sub, ok := m.backend.(backend.SubagentBackend)
	if !ok {
		m.notice = "subagent management is not available on this connection"
		m.mode = subagentsModeList
		m.form.Reset()
		return m, nil
	}
	switch m.mode {
	case subagentsModeSpawn:
		parent := m.parentKey
		agentID := m.parentAgentID
		model := strings.TrimSpace(m.spawnDefaults.Model)
		label := strings.TrimSpace(m.spawnDefaults.Label)
		return m, func() tea.Msg {
			info, err := sub.SubagentSpawn(context.Background(), parent, backend.SubagentSpawnParams{
				AgentID: agentID,
				Task:    text,
				Model:   model,
				Label:   label,
			})
			return subagentSpawnedMsg{info: info, err: err}
		}
	case subagentsModeSteer:
		key := m.formFor
		return m, func() tea.Msg {
			err := sub.SubagentSteer(context.Background(), key, text)
			return subagentSteeredMsg{sessionKey: key, err: err}
		}
	}
	return m, nil
}

// Actions returns the discoverable, view-level commands the subagent
// browser exposes. The list shrinks while a form is open so the help
// strip reflects what's reachable.
func (m subagentsModel) Actions() []Action {
	if m.mode == subagentsModeSpawn {
		return []Action{
			{ID: "submit", Label: "Spawn", Key: "ctrl+s"},
			{ID: "cancel-form", Label: "Cancel", Key: "esc"},
		}
	}
	if m.mode == subagentsModeSteer {
		return []Action{
			{ID: "submit", Label: "Steer", Key: "ctrl+s"},
			{ID: "cancel-form", Label: "Cancel", Key: "esc"},
		}
	}
	actions := []Action{}
	if !m.loading && m.err == nil {
		actions = append(actions, Action{ID: "spawn", Label: "Spawn", Key: "n"})
		if m.list.SelectedItem() != nil {
			actions = append(actions,
				Action{ID: "steer", Label: "Steer", Key: "s"},
				Action{ID: "kill", Label: "Kill", Key: "x"},
			)
		}
		actions = append(actions, Action{ID: "refresh", Label: "Refresh", Key: "r"})
	}
	actions = append(actions, Action{ID: "back", Label: "Back", Key: "esc"})
	if m.err != nil {
		actions = append(actions, Action{ID: "retry", Label: "Retry", Key: "r"})
	}
	return actions
}

// TriggerAction is the dispatcher for both keypresses and embedder
// triggers; mirrors the pattern used by sessionsModel / cronsModel.
func (m subagentsModel) TriggerAction(id string) (subagentsModel, tea.Cmd) {
	switch id {
	case "back":
		if m.mode != subagentsModeList {
			m.mode = subagentsModeList
			m.form.Reset()
			return m, nil
		}
		return m, func() tea.Msg { return goBackFromSubagentsMsg{} }
	case "cancel-form":
		m.mode = subagentsModeList
		m.form.Reset()
		return m, nil
	case "refresh", "retry":
		m.loading = true
		m.err = nil
		return m, m.loadSubagents()
	case "spawn":
		m.mode = subagentsModeSpawn
		m.form.Reset()
		m.form.Placeholder = "Task description for the new subagent (Ctrl+S to submit, Esc to cancel)..."
		m.form.Focus()
		m.notice = ""
		return m, nil
	case "steer":
		item, ok := m.list.SelectedItem().(subagentItem)
		if !ok {
			return m, nil
		}
		m.mode = subagentsModeSteer
		m.formFor = item.info.SessionKey
		m.form.Reset()
		m.form.Placeholder = "Guidance message for " + item.info.SessionKey + " (Ctrl+S to submit, Esc to cancel)..."
		m.form.Focus()
		m.notice = ""
		return m, nil
	case "kill":
		item, ok := m.list.SelectedItem().(subagentItem)
		if !ok {
			return m, nil
		}
		sub, ok := m.backend.(backend.SubagentBackend)
		if !ok {
			m.notice = "subagent management is not available on this connection"
			return m, nil
		}
		key := item.info.SessionKey
		return m, func() tea.Msg {
			err := sub.SubagentKill(context.Background(), key)
			return subagentKilledMsg{sessionKey: key, err: err}
		}
	case "submit":
		return m.submitForm()
	}
	return m, nil
}

func (m subagentsModel) View() string {
	banner := renderConnectionBanner(m.activeConn)
	hints := ""
	if !m.hideHints {
		hints = helpStyle.Render(renderActionHints(m.Actions()))
	}

	if m.mode == subagentsModeSpawn || m.mode == subagentsModeSteer {
		var b strings.Builder
		b.WriteString(banner)
		b.WriteString("\n")
		header := "Spawn new subagent"
		if m.mode == subagentsModeSteer {
			header = "Steer subagent " + m.formFor
		}
		b.WriteString(headerStyle.Render(" " + header + " "))
		b.WriteString("\n\n")
		b.WriteString(m.form.View())
		b.WriteString("\n")
		if m.notice != "" {
			b.WriteString(lipgloss.NewStyle().Foreground(subtle).Render("  " + m.notice))
			b.WriteString("\n")
		}
		b.WriteString(hints)
		b.WriteString("\n")
		return b.String()
	}

	if m.loading {
		var b strings.Builder
		b.WriteString(banner)
		b.WriteString("\n  Loading subagents...\n")
		return b.String()
	}

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

	if len(m.list.Items()) == 0 {
		var b strings.Builder
		b.WriteString(banner)
		b.WriteString("\n")
		b.WriteString(headerStyle.Render(" Subagents "))
		b.WriteString("\n\n")
		b.WriteString("  No subagents in this session yet — press 'n' to spawn one.\n\n")
		if m.notice != "" {
			b.WriteString(lipgloss.NewStyle().Foreground(subtle).Render("  " + m.notice))
			b.WriteString("\n\n")
		}
		b.WriteString(hints)
		b.WriteString("\n")
		return b.String()
	}

	var out strings.Builder
	out.WriteString(banner)
	out.WriteString(m.list.View())
	if m.notice != "" {
		out.WriteString("\n")
		out.WriteString(lipgloss.NewStyle().Foreground(subtle).Render("  " + m.notice))
	}
	out.WriteString("\n")
	out.WriteString(hints)
	return out.String()
}

func (m *subagentsModel) setSize(w, h int) {
	m.list.SetSize(w, h-2)
	if w > 4 {
		m.form.SetWidth(w - 4)
	}
}
