package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/config"
)

func newTestSessionsModel() sessionsModel {
	m := newSessionsModel(nil, "agent-1", "Scout", "model-1", "main-key", false, nil, false)
	m.setSize(120, 40)
	return m
}

// A Hermes session row must carry a real title and preview into sessionItem.
// When the backend emitted "title"/"lastMessage" instead of the TUI's
// derivedTitle/lastMessagePreview, every row rendered its key twice.
func TestSessionsList_HermesPayloadCarriesTitleAndPreview(t *testing.T) {
	f := newFakeBackend()
	f.sessionsListHook = func(context.Context, string) (json.RawMessage, error) {
		return json.RawMessage(`{"sessions":[{
			"key":"default::20261103_235014_cf2e19",
			"derivedTitle":"Fix the word wrapper",
			"lastMessagePreview":"wrapped and tested",
			"updatedAt":1784425825300
		}]}`), nil
	}
	m := newSessionsModel(f, "default", "Hermes", "deepseek/deepseek-v4.1-flash", "", false, nil, false)

	msg, ok := m.loadSessions()().(sessionsLoadedMsg)
	if !ok {
		t.Fatal("loadSessions returned an unexpected message")
	}
	if msg.err != nil {
		t.Fatalf("loadSessions: %v", msg.err)
	}
	if len(msg.sessions) != 1 {
		t.Fatalf("sessions = %+v", msg.sessions)
	}
	it := msg.sessions[0]
	if it.title != "Fix the word wrapper" {
		t.Errorf("title = %q, want the gateway's derivedTitle", it.title)
	}
	if it.lastMessage != "wrapped and tested" {
		t.Errorf("lastMessage = %q, want the gateway's preview", it.lastMessage)
	}
	if it.title == it.key {
		t.Errorf("title fell back to the session key (%q); the row would render the key twice", it.key)
	}
}

func TestSessionsLoadedMsg_PopulatesList(t *testing.T) {
	m := newTestSessionsModel()
	m, _ = m.Update(sessionsLoadedMsg{
		sessions: []sessionItem{
			{key: "s1", title: "First chat"},
			{key: "s2", title: "Second chat"},
		},
	})
	if m.loading {
		t.Error("expected loading to be false after sessionsLoadedMsg")
	}
	if len(m.list.Items()) != 2 {
		t.Errorf("expected 2 items, got %d", len(m.list.Items()))
	}
}

func TestSessionsLoadedMsg_Error(t *testing.T) {
	m := newTestSessionsModel()
	m, _ = m.Update(sessionsLoadedMsg{err: errString("gateway error")})
	if m.err == nil {
		t.Error("expected error to be set")
	}
	if m.loading {
		t.Error("expected loading to be false")
	}
}

func TestSessionsKey_Enter_SelectsSession(t *testing.T) {
	m := newTestSessionsModel()
	m, _ = m.Update(sessionsLoadedMsg{
		sessions: []sessionItem{
			{key: "s1", title: "First chat"},
		},
	})
	m, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a cmd from enter")
	}
	msg := cmd()
	sel, ok := msg.(sessionSelectedMsg)
	if !ok {
		t.Fatalf("expected sessionSelectedMsg, got %T", msg)
	}
	if sel.sessionKey != "s1" {
		t.Errorf("expected session key %q, got %q", "s1", sel.sessionKey)
	}
}

func TestSessionsKey_Esc_GoesBack(t *testing.T) {
	m := newTestSessionsModel()
	m, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	_ = m
	if cmd == nil {
		t.Fatal("expected a cmd from esc")
	}
	msg := cmd()
	if _, ok := msg.(goBackFromSessionsMsg); !ok {
		t.Errorf("expected goBackFromSessionsMsg, got %T", msg)
	}
}

func TestSessionsKey_N_WhenLoading_Ignored(t *testing.T) {
	m := newTestSessionsModel()
	// loading is true by default
	m, cmd := m.handleKey(tea.KeyPressMsg{Code: 'n'})
	_ = m
	if cmd != nil {
		t.Error("expected nil cmd when loading")
	}
}

func TestSessionsKey_R_RetriesOnError(t *testing.T) {
	m := newTestSessionsModel()
	m.loading = false
	m.err = errString("some error")
	m, cmd := m.handleKey(tea.KeyPressMsg{Code: 'r'})
	if !m.loading {
		t.Error("expected loading to be true after retry")
	}
	if m.err != nil {
		t.Error("expected err to be cleared after retry")
	}
	if cmd == nil {
		t.Error("expected a loadSessions cmd")
	}
}

func TestSessionsView_Loading(t *testing.T) {
	m := newTestSessionsModel()
	view := m.View()
	if view == "" {
		t.Error("expected non-empty view")
	}
}

func TestSessionsView_Empty(t *testing.T) {
	m := newTestSessionsModel()
	m.loading = false
	m, _ = m.Update(sessionsLoadedMsg{sessions: nil})
	view := m.View()
	if view == "" {
		t.Error("expected non-empty view")
	}
}

// --- cleanDerivedTitle ---

func TestCleanDerivedTitle_Plain(t *testing.T) {
	got := cleanDerivedTitle("Hello world")
	if got != "Hello world" {
		t.Errorf("got %q, want %q", got, "Hello world")
	}
}

func TestCleanDerivedTitle_StripsSenderPrefix(t *testing.T) {
	got := cleanDerivedTitle("Sender (untrusted metadata): Some title")
	if got != "Some title" {
		t.Errorf("got %q, want %q", got, "Some title")
	}
}

func TestCleanDerivedTitle_StripsMarkdownFences(t *testing.T) {
	got := cleanDerivedTitle("```json\nsome content")
	if got != "some content" {
		t.Errorf("got %q, want %q", got, "some content")
	}

	got = cleanDerivedTitle("```some content")
	if got != "some content" {
		t.Errorf("got %q, want %q", got, "some content")
	}
}

func TestCleanDerivedTitle_StripsJSONContent(t *testing.T) {
	got := cleanDerivedTitle(`{"label": "cli", "text": "hello"}`)
	if got != "" {
		t.Errorf("expected empty string for JSON content, got %q", got)
	}
}

func TestCleanDerivedTitle_CombinedPrefixAndFence(t *testing.T) {
	got := cleanDerivedTitle("Sender (untrusted metadata): ```json\n{\"foo\":\"bar\"}")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestCleanDerivedTitle_EmptyInput(t *testing.T) {
	got := cleanDerivedTitle("")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestCleanDerivedTitle_WhitespaceOnly(t *testing.T) {
	got := cleanDerivedTitle("   ")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// --- sessionGroup ---

func TestSessionGroup_Conversations(t *testing.T) {
	got := sessionGroup("session-key-123")
	if got != "Conversations" {
		t.Errorf("got %q, want %q", got, "Conversations")
	}
}

func TestSessionGroup_Scheduled(t *testing.T) {
	got := sessionGroup("agent-1:cron:daily-check")
	if got != "Scheduled" {
		t.Errorf("got %q, want %q", got, "Scheduled")
	}
}

// --- skipHeaders ---

func TestSkipHeaders_SkipsGroupHeader(t *testing.T) {
	m := newTestSessionsModel()
	m, _ = m.Update(sessionsLoadedMsg{
		sessions: []sessionItem{
			{key: "s1", title: "First", group: "Conversations"},
			{key: "s2", title: "Second", group: "Conversations"},
		},
	})
	// After loading, the list has [header, s1, s2] and selection is at index 1.
	// Move up should skip the header.
	m.list.Select(0) // put on the header
	m.skipHeaders(1) // move down
	if m.list.Index() != 1 {
		t.Errorf("expected index 1 after skipping header, got %d", m.list.Index())
	}
}

func TestSkipHeaders_StaysOnSessionItem(t *testing.T) {
	m := newTestSessionsModel()
	m, _ = m.Update(sessionsLoadedMsg{
		sessions: []sessionItem{
			{key: "s1", title: "First", group: "Conversations"},
		},
	})
	// Selection should be on s1 (index 1).
	m.skipHeaders(-1) // try moving up
	// Should stay at index 1 (the header at 0 is skipped, clamped to 0, which is header, so stays).
	idx := m.list.Index()
	if idx < 0 {
		t.Errorf("expected non-negative index, got %d", idx)
	}
}

// --- selecting state ---

func TestSessionsKey_Enter_TriggersSelectingState(t *testing.T) {
	m := newTestSessionsModel()
	m, _ = m.Update(sessionsLoadedMsg{
		sessions: []sessionItem{
			{key: "s1", title: "First chat", group: "Conversations"},
		},
	})
	if m.selecting {
		t.Fatal("selecting should be false before enter")
	}

	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if !m.selecting {
		t.Error("expected selecting=true after enter so the picker freezes")
	}
	if m.selectingTitle != "First chat" {
		t.Errorf("selectingTitle = %q, want First chat", m.selectingTitle)
	}
}

func TestSessionsSelecting_BlocksNavigation(t *testing.T) {
	m := newTestSessionsModel()
	m, _ = m.Update(sessionsLoadedMsg{
		sessions: []sessionItem{
			{key: "s1", title: "First", group: "Conversations"},
			{key: "s2", title: "Second", group: "Conversations"},
		},
	})
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.selecting {
		t.Fatal("setup: expected selecting=true after enter")
	}
	startIdx := m.list.Index()

	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	if m.list.Index() != startIdx {
		t.Errorf("list index moved while selecting: was %d, now %d", startIdx, m.list.Index())
	}
	if cmd != nil {
		t.Errorf("expected no cmd while selecting, got %T", cmd)
	}
}

func TestSessionsSelecting_HidesActions(t *testing.T) {
	m := newTestSessionsModel()
	m, _ = m.Update(sessionsLoadedMsg{
		sessions: []sessionItem{
			{key: "s1", title: "First", group: "Conversations"},
		},
	})
	if len(m.Actions()) == 0 {
		t.Fatal("setup: expected actions before enter")
	}

	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if len(m.Actions()) != 0 {
		t.Errorf("expected no actions while selecting, got %+v", m.Actions())
	}
}

func TestSessionsSelecting_ViewRendersLoading(t *testing.T) {
	m := newTestSessionsModel()
	m, _ = m.Update(sessionsLoadedMsg{
		sessions: []sessionItem{
			{key: "s1", title: "First chat", group: "Conversations"},
			{key: "s2", title: "Second", group: "Conversations"},
		},
	})
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Loading First chat") {
		t.Errorf("expected loading line naming the picked session, got:\n%s", view)
	}
	if strings.Contains(view, "Second") {
		t.Errorf("expected list to be hidden while selecting, got:\n%s", view)
	}
}

// TestSessionsModel_QuitKeysUnbound pins the fix for `v` quitting the app
// from the session browser (bubbles binds Quit to "v", labelled "select").
func TestSessionsModel_QuitKeysUnbound(t *testing.T) {
	m := newTestSessionsModel()
	if keys := m.list.KeyMap.Quit.Keys(); len(keys) != 0 {
		t.Errorf("Quit still bound to %v", keys)
	}
	if keys := m.list.KeyMap.ForceQuit.Keys(); len(keys) != 0 {
		t.Errorf("ForceQuit still bound to %v", keys)
	}
	m, _ = m.Update(sessionsLoadedMsg{sessions: []sessionItem{{key: "s1", title: "First", group: "Conversations"}}})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'v'})
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("pressing v emitted tea.Quit from the session browser")
		}
	}
}

// TestSessionsModel_HintVisibleAt80x24 covers the clipped hint row: the
// list was sized h-2 while the view also renders the banner and the hint.
func TestSessionsModel_HintVisibleAt80x24(t *testing.T) {
	conn := &config.Connection{ID: "c1", Name: "local", URL: "http://localhost:9119", Type: config.ConnTypeOpenClaw}
	m := newSessionsModel(nil, "agent-1", "Scout", "model-1", "main-key", false, conn, false)
	m.setSize(80, 24)
	m, _ = m.Update(sessionsLoadedMsg{sessions: []sessionItem{
		{key: "s1", title: "First chat", group: "Conversations"},
		{key: "s2", title: "Second chat", group: "Conversations"},
	}})

	view := m.View()
	if strings.Contains(view, "v select") || strings.Contains(view, "? more") {
		t.Errorf("widget footer keys must not be advertised, got:\n%s", view)
	}
	if !strings.Contains(view, "esc: back") {
		t.Errorf("expected the app-owned action hint, got:\n%s", view)
	}
	if lines := strings.Split(strings.TrimRight(view, "\n"), "\n"); len(lines) > 24 {
		t.Errorf("view is %d rows at 80x24; hint row is clipped", len(lines))
	}
}

// TestSessionItem_FilterValueIncludesPreview keeps `/` able to find a
// session by something it said, not only by its title.
func TestSessionItem_FilterValueIncludesPreview(t *testing.T) {
	it := sessionItem{key: "s1", title: "Release notes", lastMessage: "wrapped the transcript"}
	for _, want := range []string{"Release notes", "wrapped the transcript"} {
		if !strings.Contains(it.FilterValue(), want) {
			t.Errorf("FilterValue = %q, missing %q", it.FilterValue(), want)
		}
	}
}

// TestRelativeAge pins the primary-row age rendering.
func TestRelativeAge(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		ago  time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m ago"},
		{2 * time.Hour, "2h ago"},
		{3 * 24 * time.Hour, "3d ago"},
	}
	for _, tc := range cases {
		got := relativeAge(now.Add(-tc.ago).UnixMilli(), now)
		if got != tc.want {
			t.Errorf("relativeAge(%s) = %q, want %q", tc.ago, got, tc.want)
		}
	}
	if relativeAge(0, now) != "" {
		t.Error("missing updatedAt should render no age")
	}
}

// TestSessionDelegate_RendersAgeAndKeyOnSelectedOnly covers the density
// fix: the age sits on the primary row, and the machine key appears on
// the selected row only (it used to prefix every row's subtitle).
func TestSessionDelegate_RendersAgeAndKeyOnSelectedOnly(t *testing.T) {
	updated := time.Now().Add(-2 * time.Hour).UnixMilli()
	m := newSessionsModel(nil, "a", "A", "m", "k", false, nil, false)
	m.setSize(80, 24)
	m, _ = m.Update(sessionsLoadedMsg{sessions: []sessionItem{
		{key: "key-one", title: "First chat", lastMessage: "hello from the first", updatedAt: updated, group: "Conversations"},
		{key: "key-two", title: "Second chat", lastMessage: "hello from the second", updatedAt: updated, group: "Conversations"},
	}})
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "2h ago") {
		t.Errorf("expected the relative age on the primary row, got:\n%s", view)
	}
	if strings.Count(view, "key-one") != 1 {
		t.Errorf("expected the selected row's key exactly once, got:\n%s", view)
	}
	if strings.Contains(view, "key-two") {
		t.Errorf("unselected rows must not show the machine key, got:\n%s", view)
	}
	if !strings.Contains(view, "hello from the second") {
		t.Errorf("expected the unselected row's preview, got:\n%s", view)
	}
}

// renderSessionRow renders one unselected row through the delegate so the
// source tag can be asserted without driving the whole list view.
func renderSessionRow(it sessionItem, width int) string {
	l := list.New([]list.Item{it}, sessionDelegate{}, width, 2)
	var b strings.Builder
	sessionDelegate{}.Render(&b, l, -1, it)
	return ansi.Strip(b.String())
}

// TestSessionDelegate_RendersSourceTag pins the dim source tag on the
// primary row, ahead of the relative age.
func TestSessionDelegate_RendersSourceTag(t *testing.T) {
	updated := time.Now().Add(-2 * time.Hour).UnixMilli()
	row := renderSessionRow(sessionItem{key: "k", title: "Release notes", updatedAt: updated, source: "bot_room"}, 80)
	if !strings.Contains(row, "bot_room · 2h ago") {
		t.Errorf("row = %q, want the source tag before the relative age", row)
	}
}

// TestSessionDelegate_OmitsSourceTagWhenEmpty keeps a source-less row in its
// old shape: title and age, no placeholder tag or extra separator.
func TestSessionDelegate_OmitsSourceTagWhenEmpty(t *testing.T) {
	updated := time.Now().Add(-2 * time.Hour).UnixMilli()
	row := renderSessionRow(sessionItem{key: "k", title: "Release notes", updatedAt: updated}, 80)
	if !strings.Contains(row, "Release notes · 2h ago") {
		t.Fatalf("row = %q, want the unchanged title and age shape", row)
	}
	if n := strings.Count(row, " · "); n != 1 {
		t.Errorf("row = %q, want a single separator (no source tag)", row)
	}
}

// TestSessionItem_FilterValueIncludesSource lets `/bot_room` find a room
// session without a new key binding.
func TestSessionItem_FilterValueIncludesSource(t *testing.T) {
	it := sessionItem{key: "s1", title: "Release notes", lastMessage: "wrapped it", source: "bot_room"}
	for _, want := range []string{"Release notes", "wrapped it", "bot_room"} {
		if !strings.Contains(it.FilterValue(), want) {
			t.Errorf("FilterValue = %q, missing %q", it.FilterValue(), want)
		}
	}
}

// TestSessionsModel_FilterMatchesSourceAndTitle proves the live `/` filter
// narrows on the source and still matches a title-only query.
func TestSessionsModel_FilterMatchesSourceAndTitle(t *testing.T) {
	m := newTestSessionsModel()
	m, _ = m.Update(sessionsLoadedMsg{sessions: []sessionItem{
		{key: "s1", title: "Release notes", source: "bot_room", group: "Conversations"},
		{key: "s2", title: "Bug triage", source: "cron", group: "Conversations"},
	}})
	m, _ = m.handleKey(tea.KeyPressMsg{Code: '/', Text: "/"})
	if m.list.FilterState() != list.Filtering {
		t.Fatalf("expected filtering state after '/', got %v", m.list.FilterState())
	}

	m.list.SetFilterText("bot_room")
	if n := len(m.list.VisibleItems()); n != 1 {
		t.Fatalf("source filter narrowed to %d sessions, want 1", n)
	}

	m.list.SetFilterText("triage")
	if n := len(m.list.VisibleItems()); n != 1 {
		t.Fatalf("title-only filter narrowed to %d sessions, want 1", n)
	}
}

// TestSessionsModel_FilterNarrowsList proves `/` filtering is live.
func TestSessionsModel_FilterNarrowsList(t *testing.T) {
	m := newTestSessionsModel()
	m, _ = m.Update(sessionsLoadedMsg{sessions: []sessionItem{
		{key: "s1", title: "Release notes", lastMessage: "wrapped the transcript", group: "Conversations"},
		{key: "s2", title: "Bug triage", group: "Conversations"},
	}})
	m, _ = m.handleKey(tea.KeyPressMsg{Code: '/', Text: "/"})
	if m.list.FilterState() != list.Filtering {
		t.Fatalf("expected filtering state after '/', got %v", m.list.FilterState())
	}
	m.list.SetFilterText("wrapped the tr")
	if n := len(m.list.VisibleItems()); n != 1 {
		t.Fatalf("expected the filter to narrow to 1 session, got %d", n)
	}
}
