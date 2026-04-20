package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/outofcoffee/repclaw/internal/client"
)

// sessionItem is a list item for the session browser.
type sessionItem struct {
	key         string
	title       string
	lastMessage string
	updatedAt   int64 // Unix millis
	group       string
}

func (i sessionItem) FilterValue() string {
	if i.title != "" {
		return i.title
	}
	return i.key
}

// sessionGroupHeader is a non-selectable list item used as a group separator.
type sessionGroupHeader struct {
	label string
}

func (h sessionGroupHeader) FilterValue() string { return "" }

// sessionDelegate renders each item in the session list.
type sessionDelegate struct{}

func (d sessionDelegate) Height() int                             { return 2 }
func (d sessionDelegate) Spacing() int                            { return 1 }
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

		subtitle := i.key
		if i.lastMessage != "" {
			preview := i.lastMessage
			if len(preview) > 60 {
				preview = preview[:57] + "..."
			}
			subtitle = i.key + " · " + preview
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
		} else {
			str := fmt.Sprintf("  %s", title)
			str += "\n" + lipgloss.NewStyle().
				Foreground(subtle).
				Render(fmt.Sprintf("  %s", subtitle))
			fmt.Fprint(w, str)
		}
	}
}

// sessionsModel is the session browser view.
type sessionsModel struct {
	list      list.Model
	client    *client.Client
	agentID   string
	agentName string
	modelID   string
	mainKey   string
	loading   bool
	err       error
}

func newSessionsModel(c *client.Client, agentID, agentName, modelID, mainKey string) sessionsModel {
	l := list.New(nil, sessionDelegate{}, 0, 0)
	l.Title = "Sessions"
	l.SetShowStatusBar(false)
	l.SetShowHelp(true)
	l.Styles.Title = headerStyle
	l.SetFilteringEnabled(false)

	return sessionsModel{
		list:      l,
		client:    c,
		agentID:   agentID,
		agentName: agentName,
		modelID:   modelID,
		mainKey:   mainKey,
		loading:   true,
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
	cl := m.client
	agentID := m.agentID
	return func() tea.Msg {
		raw, err := cl.SessionsList(context.Background(), agentID)
		if err != nil {
			return sessionsLoadedMsg{err: err}
		}
		logEvent("SESSIONS_LIST raw=%s", string(raw))
		var resp sessionsListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return sessionsLoadedMsg{err: err}
		}
		var items []sessionItem
		for _, rawEntry := range resp.Sessions {
			var entry sessionListEntry
			if err := json.Unmarshal(rawEntry, &entry); err != nil {
				logEvent("SESSIONS_LIST entry parse error: %v", err)
				continue
			}
			title := cleanDerivedTitle(entry.DerivedTitle)
			if len(title) > 80 {
				title = title[:77] + "..."
			}
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
	switch msg.String() {
	case "up", "k":
		m.list, _ = m.list.Update(msg)
		m.skipHeaders(-1)
		return m, nil

	case "down", "j":
		m.list, _ = m.list.Update(msg)
		m.skipHeaders(1)
		return m, nil

	case "enter":
		if m.loading || m.err != nil {
			return m, nil
		}
		if item, ok := m.list.SelectedItem().(sessionItem); ok {
			return m, func() tea.Msg {
				return sessionSelectedMsg{
					sessionKey: item.key,
					agentName:  m.agentName,
					modelID:    m.modelID,
				}
			}
		}

	case "n":
		if m.loading || m.err != nil {
			return m, nil
		}
		cl := m.client
		agentID := m.agentID
		agentName := m.agentName
		modelID := m.modelID
		return m, func() tea.Msg {
			key := time.Now().Format("2006-01-02T15:04:05")
			sessionKey, err := cl.CreateSession(context.Background(), agentID, key)
			return newSessionCreatedMsg{
				sessionKey: sessionKey,
				agentName:  agentName,
				modelID:    modelID,
				err:        err,
			}
		}

	case "esc":
		return m, func() tea.Msg { return goBackFromSessionsMsg{} }

	case "r":
		if m.err != nil {
			m.loading = true
			m.err = nil
			return m, m.loadSessions()
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m sessionsModel) View() string {
	if m.loading {
		return "\n  Loading sessions...\n"
	}
	if m.err != nil {
		var b strings.Builder
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("  Error: %v", m.err)))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  Press 'r' to retry, Esc to go back"))
		b.WriteString("\n")
		return b.String()
	}
	if len(m.list.Items()) == 0 {
		var b strings.Builder
		b.WriteString("\n")
		b.WriteString(headerStyle.Render(" Sessions "))
		b.WriteString("\n\n")
		b.WriteString("  No sessions found.\n\n")
		b.WriteString(helpStyle.Render("  n: new session | Esc: back"))
		b.WriteString("\n")
		return b.String()
	}
	return m.list.View() + "\n" + helpStyle.Render("  n: new session | Esc: back")
}

func (m *sessionsModel) setSize(w, h int) {
	m.list.SetSize(w, h-2)
}
