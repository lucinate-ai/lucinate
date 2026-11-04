package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// sessionItem is a list item for the session browser.
type sessionItem struct {
	key         string
	title       string
	lastMessage string
	updatedAt   int64 // Unix millis
	group       string
}

// FilterValue is the haystack the bubbles list filter runs against. The
// preview is included so `/` can find a session by something it said, not
// only by its title. A title-less session falls back to its key.
func (i sessionItem) FilterValue() string {
	parts := []string{i.title, i.lastMessage}
	if i.title == "" {
		parts[0] = i.key
	}
	return strings.Join(parts, " ")
}

// sessionGroupHeader is a non-selectable list item used as a group separator.
type sessionGroupHeader struct {
	label string
}

func (h sessionGroupHeader) FilterValue() string { return "" }

// sessionDelegate renders each item in the session list: a primary row
// with the title and how long ago the session moved, then a dim preview
// row. The raw session key is machine data, so it rides on the selected
// row only — everywhere else it would crowd out the preview.
type sessionDelegate struct{}

func (d sessionDelegate) Height() int                             { return 2 }
func (d sessionDelegate) Spacing() int                            { return 0 }
func (d sessionDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d sessionDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	switch i := item.(type) {
	case sessionGroupHeader:
		header := lipgloss.NewStyle().
			Bold(true).
			Foreground(accent).
			Render(fmt.Sprintf("  ── %s ──", i.label))
		fmt.Fprint(w, header+"\n")

	case sessionItem:
		title := i.title
		if title == "" {
			title = i.key
		}
		width := m.Width() - 2
		if width < 1 {
			width = 1
		}
		primary := truncateToWidth(title, width)
		if age := relativeAge(i.updatedAt, time.Now()); age != "" {
			// Keep the age on the primary row; if the title alone fills the
			// row the age is dropped rather than wrapped.
			if displayWidth(primary)+3+displayWidth(age) <= width {
				primary += " · " + age
			}
		}

		preview := i.lastMessage
		if index == m.Index() {
			// Selected: the key is context for this row only.
			if preview == "" {
				preview = i.key
			} else {
				preview = i.key + " · " + preview
			}
		}
		subtitle := ""
		if strings.TrimSpace(preview) != "" {
			subtitle = "\n" + lipgloss.NewStyle().
				Foreground(subtle).
				Render("  "+truncateToWidth(preview, width))
		}

		if index == m.Index() {
			str := lipgloss.NewStyle().
				Foreground(accent).
				Bold(true).
				Render(fmt.Sprintf("> %s", primary))
			fmt.Fprint(w, str+subtitle)
		} else {
			fmt.Fprint(w, fmt.Sprintf("  %s", primary)+subtitle)
		}
	}
}

// relativeAge renders a session's last-updated stamp as an age a reader
// can act on ("2h ago") rather than a machine timestamp. Missing or
// future stamps render empty.
func relativeAge(updatedAtMillis int64, now time.Time) string {
	if updatedAtMillis <= 0 {
		return ""
	}
	t := time.UnixMilli(updatedAtMillis)
	d := now.Sub(t)
	switch {
	case d < 0:
		return ""
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

// sessionsModel is the session browser view.
type sessionsModel struct {
	list      list.Model
	backend   backend.Backend
	agentID   string
	agentName string
	modelID   string
	mainKey   string
	loading   bool
	err       error
	hideHints bool
	// selecting is true after the user has picked a session and we're
	// about to transition into the chat view. The window is brief —
	// sessionSelectedMsg dispatches synchronously and the chat view's
	// own loading state takes over on the next tick — but during it
	// the list is still on screen, so we freeze input + render a
	// loading line to match the agent picker's behaviour and stop the
	// user from racing the cursor against the transition.
	selecting bool
	// selectingTitle is the chosen session's display label, surfaced
	// in the loading view so the user can confirm what they picked.
	selectingTitle string
	activeConn     *config.Connection // rendered above the session list — see renderConnectionBanner.
	// width and height mirror setSize so the view can reserve exactly the
	// rows its banner and wrapped hint block occupy.
	width  int
	height int
}

func newSessionsModel(b backend.Backend, agentID, agentName, modelID, mainKey string, hideHints bool, activeConn *config.Connection, disableExitKeys bool) sessionsModel {
	l := list.New(nil, sessionDelegate{}, 0, 0)
	l.Title = "Sessions"
	l.SetShowStatusBar(false)
	// The widget's own footer advertised `v select` (its Quit binding) —
	// pressing it quit the app. The view renders its own hint line from
	// Actions() instead, so the footer stays off in every host.
	l.SetShowHelp(false)
	l.Styles.Title = headerStyle
	// `/` filters by title and preview, matching the vocabulary the rest
	// of the app advertises for list screens.
	l.SetFilteringEnabled(true)
	l.Filter = fuzzyFilter
	l.KeyMap.Quit.Unbind()
	l.KeyMap.ForceQuit.Unbind()

	return sessionsModel{
		list:       l,
		backend:    b,
		agentID:    agentID,
		agentName:  agentName,
		modelID:    modelID,
		mainKey:    mainKey,
		loading:    true,
		hideHints:  hideHints,
		activeConn: activeConn,
	}
}

// sessionsListResponse is the structure of the sessions.list RPC response.
type sessionsListResponse struct {
	Sessions []json.RawMessage `json:"sessions"`
}

// sessionListEntry contains the fields we care about from a session entry.
// The gateway returns many additional fields which we ignore.
type sessionListEntry struct {
	Key                string `json:"key"`
	DerivedTitle       string `json:"derivedTitle"`
	LastMessagePreview string `json:"lastMessagePreview"`
	UpdatedAt          int64  `json:"updatedAt"`
	Model              string `json:"model"`
}

// cleanDerivedTitle strips gateway metadata prefixes from the derived title.
func cleanDerivedTitle(title string) string {
	// Strip "Sender (untrusted metadata): " and similar prefixes.
	if idx := strings.Index(title, "Sender (untrusted metadata):"); idx == 0 {
		title = strings.TrimSpace(title[len("Sender (untrusted metadata):"):])
	}
	// Strip leading markdown fences that sometimes appear.
	title = strings.TrimPrefix(title, "```json")
	title = strings.TrimPrefix(title, "```")
	title = strings.TrimSpace(title)
	// Strip JSON-like content at the start (e.g. '{ "label": "cli",...').
	if strings.HasPrefix(title, "{") {
		title = ""
	}
	return title
}

// sessionGroup returns a human-readable group name based on the session key.
func sessionGroup(key string) string {
	if strings.Contains(key, ":cron:") {
		return "Scheduled"
	}
	return "Conversations"
}

func (m sessionsModel) loadSessions() tea.Cmd {
	b := m.backend
	agentID := m.agentID
	return func() tea.Msg {
		raw, err := b.SessionsList(context.Background(), agentID)
		if err != nil {
			return sessionsLoadedMsg{err: err}
		}
		slog.Debug("sessions list", "raw", string(raw))
		var resp sessionsListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return sessionsLoadedMsg{err: err}
		}
		var items []sessionItem
		for _, rawEntry := range resp.Sessions {
			var entry sessionListEntry
			if err := json.Unmarshal(rawEntry, &entry); err != nil {
				slog.Debug("sessions list entry parse error", "err", err)
				continue
			}
			title := cleanDerivedTitle(entry.DerivedTitle)
			title = truncateToWidth(title, 80)
			items = append(items, sessionItem{
				key:         entry.Key,
				title:       title,
				lastMessage: entry.LastMessagePreview,
				updatedAt:   entry.UpdatedAt,
				group:       sessionGroup(entry.Key),
			})
		}
		// Sort by updatedAt descending within each group.
		sort.Slice(items, func(i, j int) bool {
			if items[i].group != items[j].group {
				// "Conversations" before "Scheduled"
				return items[i].group < items[j].group
			}
			return items[i].updatedAt > items[j].updatedAt
		})
		return sessionsLoadedMsg{sessions: items}
	}
}

func (m sessionsModel) Init() tea.Cmd {
	return m.loadSessions()
}

func (m sessionsModel) Update(msg tea.Msg) (sessionsModel, tea.Cmd) {
	// After enter is pressed we hand control to the parent which
	// will swap us out for the chat view. Drop further input —
	// keystrokes, list-internal cmds — so the user can't keep
	// moving the cursor while the transition is in flight.
	if m.selecting {
		return m, nil
	}

	switch msg := msg.(type) {
	case sessionsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		// Build list items with group headers.
		var listItems []list.Item
		lastGroup := ""
		for _, s := range msg.sessions {
			if s.group != lastGroup {
				listItems = append(listItems, sessionGroupHeader{label: s.group})
				lastGroup = s.group
			}
			listItems = append(listItems, s)
		}
		m.list.SetItems(listItems)
		// Skip past the first group header so a session is selected.
		if len(listItems) > 1 {
			m.list.Select(1)
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// skipHeaders adjusts the list selection to skip over group headers
// in the given direction (+1 for down, -1 for up).
func (m *sessionsModel) skipHeaders(dir int) {
	items := m.list.Items()
	idx := m.list.Index()
	for idx >= 0 && idx < len(items) {
		if _, isHeader := items[idx].(sessionGroupHeader); !isHeader {
			break
		}
		idx += dir
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= len(items) {
		idx = len(items) - 1
	}
	m.list.Select(idx)
}

func (m sessionsModel) handleKey(msg tea.KeyPressMsg) (sessionsModel, tea.Cmd) {
	// Enter picks the highlighted session from any filter state, so a
	// typed query can be confirmed in one keystroke (the bubbles default
	// while filtering is to apply the filter, forcing a second enter).
	if msg.String() == "enter" && !m.loading && m.err == nil {
		if item, ok := m.list.SelectedItem().(sessionItem); ok {
			m.selecting = true
			m.selectingTitle = item.title
			if m.selectingTitle == "" {
				m.selectingTitle = item.key
			}
			return m, func() tea.Msg {
				return sessionSelectedMsg{
					sessionKey: item.key,
					agentName:  m.agentName,
					modelID:    m.modelID,
				}
			}
		}
	}

	// While the filter input is focused every other key belongs to the
	// list — typing narrows the result set, esc clears it.
	if m.list.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}

	// Cursor navigation stays inline — these are intrinsic list controls,
	// not discoverable view-level commands.
	switch msg.String() {
	case "up", "k":
		m.list, _ = m.list.Update(msg)
		m.skipHeaders(-1)
		return m, nil

	case "down", "j":
		m.list, _ = m.list.Update(msg)
		m.skipHeaders(1)
		return m, nil
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

// Actions returns the discoverable, view-level commands the session
// browser currently exposes. Loading/error transitions are reflected
// because the list is recomputed on every Update tick.
func (m sessionsModel) Actions() []Action {
	if m.selecting {
		// Mirror the loading view: no actionable surface while we
		// wait for the chat view to take over.
		return nil
	}
	if m.filtering() {
		// The filter input owns the keys (esc clears the query, enter
		// picks); advertising the view shortcuts here would mislead.
		return nil
	}
	var actions []Action
	if !m.loading && m.err == nil {
		actions = append(actions, Action{ID: "new-session", Label: "New session", Key: "n"})
	}
	actions = append(actions, Action{ID: "back", Label: "Back", Key: "esc"})
	if m.err != nil {
		actions = append(actions, Action{ID: "retry", Label: "Retry", Key: "r"})
	}
	return actions
}

// TriggerAction invokes the named action. Both keystrokes (via
// handleKey) and embedder calls (via Program.TriggerAction) reach the
// same dispatcher.
func (m sessionsModel) TriggerAction(id string) (sessionsModel, tea.Cmd) {
	switch id {
	case "new-session":
		if m.loading || m.err != nil {
			return m, nil
		}
		b := m.backend
		agentID := m.agentID
		agentName := m.agentName
		modelID := m.modelID
		return m, func() tea.Msg {
			key := time.Now().Format("2006-01-02T15:04:05")
			sessionKey, err := b.CreateSession(context.Background(), agentID, key)
			return newSessionCreatedMsg{
				sessionKey: sessionKey,
				agentName:  agentName,
				modelID:    modelID,
				err:        err,
			}
		}
	case "back":
		return m, func() tea.Msg { return goBackFromSessionsMsg{} }
	case "retry":
		if m.err == nil {
			return m, nil
		}
		m.loading = true
		m.err = nil
		return m, m.loadSessions()
	}
	return m, nil
}

func (m sessionsModel) View() string {
	if m.loading {
		return "\n  Loading sessions...\n"
	}
	if m.selecting {
		// Pinned during the transition into chat so the list
		// disappears with the rest of the picker UI; the connection
		// banner stays so the user still sees scope.
		banner := renderConnectionBanner(m.activeConn)
		title := m.selectingTitle
		if title == "" {
			title = "session"
		}
		return banner + fmt.Sprintf("\n  Loading %s...\n", title)
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
	if len(m.list.Items()) == 0 {
		var b strings.Builder
		b.WriteString(banner)
		b.WriteString("\n")
		b.WriteString(headerStyle.Render(" Sessions "))
		b.WriteString("\n\n")
		b.WriteString("  No sessions found.\n\n")
		b.WriteString(hints)
		b.WriteString("\n")
		return b.String()
	}
	return banner + m.sizedListView(banner, hints) + "\n" + hints
}

// sizedListView renders the list at the height this frame's chrome leaves,
// so a hint block that wraps (or one that appears after the list was sized
// while loading) cannot push the last row off the screen.
func (m sessionsModel) sizedListView(banner, hints string) string {
	lm := m.list
	if m.height > 0 {
		lm.SetSize(m.width, maxInt(1, m.height-blockRows(banner)-blockRows(hints)))
	}
	return lm.View()
}

func (m *sessionsModel) setSize(w, h int) {
	m.width = w
	m.height = h
	m.list.SetSize(w, m.listHeight())
}

// listHeight sizes the list to what the view leaves after the connection
// banner and the action-hint block (which wraps on a narrow terminal).
// The old flat h-2 pushed the hint row off a 24-row terminal.
func (m sessionsModel) listHeight() int {
	if m.height <= 0 {
		return 0
	}
	return maxInt(1, m.height-blockRows(renderConnectionBanner(m.activeConn))-blockRows(m.renderHints()))
}

// renderHints is the app-owned hint block the view renders below the list.
func (m sessionsModel) renderHints() string {
	if m.hideHints {
		return ""
	}
	if m.filtering() {
		return renderFilterHint(m.width)
	}
	return renderHintLine(m.Actions(), m.width)
}

// filtering reports whether the session list's live filter input is
// focused, so computeWantsInput and Actions can treat typed keys as text.
func (m sessionsModel) filtering() bool {
	return m.list.FilterState() == list.Filtering
}
