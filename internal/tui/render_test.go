package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/config"
)

func TestFormatCost(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{0.0, "$0.0000"},
		{0.005, "$0.0050"},
		{0.01, "$0.01"},
		{1.50, "$1.50"},
		{24.13, "$24.13"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatCost(tt.input)
			if got != tt.want {
				t.Errorf("formatCost(%f) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatStatsTable(t *testing.T) {
	m := &chatModel{
		stats: &sessionStats{
			inputTokens:       728,
			outputTokens:      70857,
			cacheRead:         28538316,
			cacheWrite:        3868238,
			totalCost:         24.13,
			inputCost:         0.002,
			outputCost:        1.06,
			cacheReadCost:     8.56,
			cacheWriteCost:    14.51,
			totalMessages:     100,
			userMessages:      45,
			assistantMessages: 55,
		},
	}

	table := m.formatStatsTable()

	for _, label := range []string{"Input", "Output", "Cache read", "Cache write", "Total", "User", "Assistant"} {
		if !strings.Contains(table, label) {
			t.Errorf("table should contain %q", label)
		}
	}
	if !strings.Contains(table, "28.5M") {
		t.Error("table should contain formatted cache read tokens")
	}
	if !strings.Contains(table, "24.13") {
		t.Error("table should contain total cost")
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{500, "500"},
		{999, "999"},
		{1000, "1.0K"},
		{1500, "1.5K"},
		{32478, "32.5K"},
		{999999, "1000.0K"},
		{1000000, "1.0M"},
		{32478139, "32.5M"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatTokens(tt.input)
			if got != tt.want {
				t.Errorf("formatTokens(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestWordWrap_ShortText(t *testing.T) {
	got := wordWrap("hello", 80)
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestWordWrap_ZeroWidth(t *testing.T) {
	got := wordWrap("hello world", 0)
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestWordWrap_NegativeWidth(t *testing.T) {
	got := wordWrap("hello world", -5)
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestWordWrap_WrapsLongLine(t *testing.T) {
	got := wordWrap("the quick brown fox jumps", 15)
	if got == "the quick brown fox jumps" {
		t.Error("expected text to be wrapped")
	}
	if !strings.Contains(got, "\n") {
		t.Error("expected at least one newline")
	}
}

func TestWordWrap_PreservesExistingNewlines(t *testing.T) {
	got := wordWrap("line one\nline two", 80)
	if got != "line one\nline two" {
		t.Errorf("got %q", got)
	}
}

func TestWordWrap_PreservesTableLines(t *testing.T) {
	tableLine := "│ Input  │ 1.2K │ $0.01 │"
	got := wordWrap(tableLine, 10) // width much smaller than line
	if got != tableLine {
		t.Errorf("table line should pass through unchanged, got %q", got)
	}
}

func TestWordWrap_PreservesTableBorders(t *testing.T) {
	border := "┌────────┬────────┬────────┐"
	got := wordWrap(border, 10)
	if got != border {
		t.Errorf("table border should pass through unchanged, got %q", got)
	}
}

func TestWordWrap_MixedTableAndText(t *testing.T) {
	input := "Some header text that is long enough to wrap around\n│ Row │ Data │\nMore text here"
	got := wordWrap(input, 30)
	// Table line should be intact.
	if !strings.Contains(got, "│ Row │ Data │") {
		t.Error("table row should be preserved unchanged")
	}
	// Text should be wrapped.
	lines := strings.Split(got, "\n")
	for _, line := range lines {
		if strings.ContainsRune(line, '│') {
			continue // skip table lines
		}
		if len(line) > 30 {
			t.Errorf("non-table line should be wrapped, got %d chars: %q", len(line), line)
		}
	}
}

func TestIndentMultiline(t *testing.T) {
	got := indentMultiline("first\nsecond\nthird", "      ")
	want := "first\n      second\n      third"
	if got != want {
		t.Errorf("indentMultiline() = %q, want %q", got, want)
	}
}

func TestIsTableLine(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"│ Input │ 1.2K │", true},
		{"───────────────", true},
		{"┌──────┬──────┐", true},
		{"plain text", false},
		{"  /review — Code review", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			if got := isTableLine(tt.line); got != tt.want {
				t.Errorf("isTableLine(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestPrefixWidth_AlignedBetweenUserAndAgent(t *testing.T) {
	tests := []struct {
		agentName string
		wantWidth int
	}{
		{"ai", 5},
		{"main", 6},
		{"claude", 8},
		{"You", 5},
		{"longagent", 11},
	}

	for _, tt := range tests {
		t.Run(tt.agentName, func(t *testing.T) {
			m := &chatModel{agentName: tt.agentName}
			if got := m.prefixWidth(); got != tt.wantWidth {
				t.Errorf("prefixWidth() = %d, want %d", got, tt.wantWidth)
			}
		})
	}
}

func TestPrefixLabel_UsesAlignedTrailingPadding(t *testing.T) {
	m := &chatModel{agentName: "claude"}

	if got := m.prefixLabel("You"); got != "You:    " {
		t.Errorf("prefixLabel(You) = %q, want %q", got, "You:    ")
	}
	if got := m.prefixLabel("claude"); got != "claude: " {
		t.Errorf("prefixLabel(claude) = %q, want %q", got, "claude: ")
	}
}

func TestUpdateViewport_BottomAnchoring(t *testing.T) {
	vp := viewport.New()
	vp.SetWidth(80)
	vp.SetHeight(20)
	m := &chatModel{
		viewport:  vp,
		width:     80,
		agentName: "test",
		messages: []chatMessage{
			{role: "user", content: "hi"},
			{role: "assistant", content: "hello"},
		},
	}

	m.updateViewport()

	if len(m.viewport.View()) == 0 {
		t.Error("viewport content should not be empty")
	}
}

func TestUpdateViewport_HistoryLoadingPlaceholder(t *testing.T) {
	vp := viewport.New()
	vp.SetWidth(80)
	vp.SetHeight(20)
	m := &chatModel{
		viewport:       vp,
		width:          80,
		agentName:      "test",
		historyLoading: true,
	}

	m.updateViewport()

	view := ansi.Strip(m.viewport.View())
	if !strings.Contains(view, "Loading conversation history") {
		t.Errorf("viewport should show loading placeholder while historyLoading is true; got %q", view)
	}

	m.historyLoading = false
	m.updateViewport()

	view = ansi.Strip(m.viewport.View())
	if strings.Contains(view, "Loading conversation history") {
		t.Errorf("placeholder should disappear once historyLoading is cleared; got %q", view)
	}
}

func TestUpdateViewport_EmptyHistoryShowsHint(t *testing.T) {
	vp := viewport.New()
	vp.SetWidth(80)
	vp.SetHeight(20)
	m := &chatModel{
		viewport:       vp,
		width:          80,
		agentName:      "test",
		historyLoading: false,
	}

	m.updateViewport()

	view := ansi.Strip(m.viewport.View())
	if !strings.Contains(view, "No conversation history for this session.") {
		t.Errorf("viewport should show empty-state hint when history finished loading with no messages; got %q", view)
	}
}

func TestUpdateViewport_PlaceholderYieldsToPendingMessage(t *testing.T) {
	vp := viewport.New()
	vp.SetWidth(80)
	vp.SetHeight(20)
	m := &chatModel{
		viewport:        vp,
		width:           80,
		agentName:       "test",
		historyLoading:  true,
		pendingMessages: []string{"queued before history loaded"},
	}

	m.updateViewport()

	view := ansi.Strip(m.viewport.View())
	if strings.Contains(view, "Loading conversation history") {
		t.Errorf("placeholder should yield to a queued message; got %q", view)
	}
	if !strings.Contains(view, "queued before history loaded") {
		t.Errorf("queued message should still render; got %q", view)
	}
}

func TestUpdateViewport_PreservesScrollWhenUserScrolledUp(t *testing.T) {
	vp := viewport.New()
	vp.SetWidth(80)
	vp.SetHeight(5)
	m := &chatModel{
		viewport:  vp,
		width:     80,
		agentName: "main",
	}
	for i := 0; i < 30; i++ {
		m.messages = append(m.messages, chatMessage{role: "user", content: "filler line"})
	}
	m.messages = append(m.messages, chatMessage{role: "assistant", streaming: true, awaitingDelta: true})
	m.updateViewport()

	m.viewport.SetYOffset(0)
	if m.viewport.AtBottom() {
		t.Fatalf("precondition: viewport should not be at bottom after scrolling up")
	}
	scrolledOffset := m.viewport.YOffset()

	last := &m.messages[len(m.messages)-1]
	last.content = "incoming delta text"
	last.awaitingDelta = false
	m.updateViewport()

	if got := m.viewport.YOffset(); got != scrolledOffset {
		t.Errorf("scroll position should be preserved while user is scrolled up: got YOffset=%d, want %d", got, scrolledOffset)
	}
}

func TestUpdate_MouseWheelScrollSurvivesStreamingDeltas(t *testing.T) {
	m := newChatModel(nil, "main", "", "test", "", config.DefaultPreferences(), false, "", "")
	m.viewport = viewport.New()
	m.width = 80
	m.height = 30
	m.viewport.SetWidth(80)
	m.viewport.SetHeight(5)
	for i := 0; i < 30; i++ {
		m.messages = append(m.messages, chatMessage{role: "user", content: "filler line"})
	}
	m.messages = append(m.messages, chatMessage{role: "assistant", streaming: true, awaitingDelta: true})
	m.updateViewport()

	if !m.viewport.AtBottom() {
		t.Fatalf("precondition: should start pinned at bottom")
	}

	wheelUp := tea.MouseWheelMsg{Button: tea.MouseWheelUp}
	m, _ = m.Update(wheelUp)
	if m.viewport.AtBottom() {
		t.Fatalf("after MouseWheelUp the viewport should no longer be at bottom; YOffset=%d", m.viewport.YOffset())
	}
	scrolledOffset := m.viewport.YOffset()

	last := &m.messages[len(m.messages)-1]
	for i, deltaText := range []string{"a", "a b", "a b c", "a b c d"} {
		last.content = deltaText
		last.awaitingDelta = false
		m.updateViewport()
		if got := m.viewport.YOffset(); got != scrolledOffset {
			t.Errorf("delta %d (%q): YOffset=%d, want %d (user should remain scrolled up)", i, deltaText, got, scrolledOffset)
		}
	}

	tick := spinnerTickMsg{}
	m.spinnerTicking = true
	m, _ = m.Update(tick)
	if got := m.viewport.YOffset(); got != scrolledOffset {
		t.Errorf("spinner tick reset scroll: YOffset=%d, want %d", got, scrolledOffset)
	}
}

func TestUpdateViewport_FollowsBottomWhenPinned(t *testing.T) {
	vp := viewport.New()
	vp.SetWidth(80)
	vp.SetHeight(5)
	m := &chatModel{
		viewport:  vp,
		width:     80,
		agentName: "main",
	}
	for i := 0; i < 30; i++ {
		m.messages = append(m.messages, chatMessage{role: "user", content: "filler line"})
	}
	m.messages = append(m.messages, chatMessage{role: "assistant", streaming: true, awaitingDelta: true})
	m.updateViewport()

	if !m.viewport.AtBottom() {
		t.Fatalf("precondition: viewport should be at bottom after initial render")
	}

	last := &m.messages[len(m.messages)-1]
	last.content = strings.Repeat("delta line\n", 10)
	last.awaitingDelta = false
	m.updateViewport()

	if !m.viewport.AtBottom() {
		t.Errorf("viewport should follow new content to bottom when user was pinned at bottom")
	}
}

func TestUpdateViewport_IndentsWrappedContentAfterPrefix(t *testing.T) {
	vp := viewport.New()
	vp.SetWidth(80)
	vp.SetHeight(20)
	m := &chatModel{
		viewport:  vp,
		width:     80,
		agentName: "main",
		messages: []chatMessage{
			{role: "user", content: strings.Repeat("alpha ", 20) + "gamma"},
			{role: "assistant", content: "line one\nline two", rendered: true},
		},
	}

	m.updateViewport()
	view := ansi.Strip(m.viewport.View())

	if !strings.Contains(view, "You:  alpha") {
		t.Fatalf("expected first user line with prefix, got %q", view)
	}
	if !strings.Contains(view, "\n      ") {
		t.Fatalf("expected wrapped user continuation to be indented, got %q", view)
	}
	if !strings.Contains(view, "main: line one") {
		t.Fatalf("expected first assistant line with prefix, got %q", view)
	}
	if !strings.Contains(view, "\n      line two") {
		t.Fatalf("expected assistant continuation to be indented, got %q", view)
	}
}

func TestUpdateViewport_NarrowLayoutStacksPrefixAboveBody(t *testing.T) {
	vp := viewport.New()
	vp.SetWidth(20)
	vp.SetHeight(20)
	m := &chatModel{
		viewport:  vp,
		width:     20,
		agentName: "main",
		messages: []chatMessage{
			{role: "user", content: "alpha beta gamma"},
			{role: "assistant", content: "line one\nline two", rendered: true},
		},
	}

	m.updateViewport()
	// Strip ANSI and right-trim each viewport line so we can match logical
	// content without the viewport's per-line trailing-space padding.
	lines := strings.Split(ansi.Strip(m.viewport.View()), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	view := strings.Join(lines, "\n")

	if !strings.Contains(view, "You:\nalpha beta gamma") {
		t.Fatalf("expected stacked user prefix above body, got %q", view)
	}
	if !strings.Contains(view, "main:\nline one\nline two") {
		t.Fatalf("expected stacked assistant prefix above body, got %q", view)
	}
}

func TestStripLeadingSpacesPerLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "  hello\n  world", "hello\nworld"},
		{"no_leading", "hello\nworld", "hello\nworld"},
		{"ansi_then_spaces", "\x1b[0m  hello\n\x1b[31m  world\x1b[0m", "\x1b[0mhello\n\x1b[31mworld\x1b[0m"},
		{"interior_spaces_preserved", "  a b\n  c d", "a b\nc d"},
		{"empty_lines", "\n  hi\n", "\nhi\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripLeadingSpacesPerLine(tt.in); got != tt.want {
				t.Errorf("stripLeadingSpacesPerLine(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFormatSeparatorLabel(t *testing.T) {
	now := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		ts   time.Time
		zero bool // pass ms=0 instead of ts
		want string
	}{
		{"missing", time.Time{}, true, ""},
		{"today", time.Date(2026, 4, 29, 8, 30, 0, 0, time.UTC), false, "08:30"},
		{"yesterday", time.Date(2026, 4, 28, 22, 5, 0, 0, time.UTC), false, "Yesterday 22:05"},
		{"earlier_this_year", time.Date(2026, 1, 12, 14, 0, 0, 0, time.UTC), false, "12 Jan 14:00"},
		{"prior_year", time.Date(2025, 12, 31, 23, 59, 0, 0, time.UTC), false, "31 Dec 2025 23:59"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ms int64
			if !tt.zero {
				ms = tt.ts.UnixMilli()
			}
			got := formatSeparatorLabel(ms, now)
			if got != tt.want {
				t.Errorf("formatSeparatorLabel(%s) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestBuildSeparator(t *testing.T) {
	tests := []struct {
		name  string
		width int
		label string
		want  string
	}{
		{"plain", 10, "", "──────────"},
		{"centred_label", 13, "08:30", "─── 08:30 ───"},
		{"odd_padding", 12, "08:30", "── 08:30 ───"},
		{"label_too_wide", 5, "08:30", "─────"},
		{"zero_width", 0, "08:30", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSeparator(tt.width, tt.label)
			if got != tt.want {
				t.Errorf("buildSeparator(%d, %q) = %q, want %q", tt.width, tt.label, got, tt.want)
			}
		})
	}
}

func TestLastTimestampMs(t *testing.T) {
	msgs := []chatMessage{
		{role: "user", timestampMs: 100},
		{role: "assistant", timestampMs: 200},
		{role: "user"},
	}
	if got := lastTimestampMs(msgs); got != 200 {
		t.Errorf("lastTimestampMs() = %d, want 200", got)
	}
	if got := lastTimestampMs(nil); got != 0 {
		t.Errorf("lastTimestampMs(nil) = %d, want 0", got)
	}
}

func TestRenderToolCard_Running(t *testing.T) {
	vp := viewport.New()
	vp.SetWidth(80)
	vp.SetHeight(10)
	m := &chatModel{
		viewport:  vp,
		width:     80,
		agentName: "main",
		messages: []chatMessage{
			{role: "tool", toolName: "search", toolCallID: "tc1", toolArgsLine: `query="hello"`, toolState: "running"},
		},
	}
	m.updateViewport()
	view := ansi.Strip(m.viewport.View())
	if !strings.Contains(view, "search") {
		t.Errorf("rendered view missing tool name: %q", view)
	}
	if !strings.Contains(view, `query="hello"`) {
		t.Errorf("rendered view missing args summary: %q", view)
	}
	if !strings.Contains(view, spinnerFrames[0]) {
		t.Errorf("running tool card should render the spinner frame, got: %q", view)
	}
}

func TestRenderToolCard_Success(t *testing.T) {
	vp := viewport.New()
	vp.SetWidth(80)
	vp.SetHeight(10)
	m := &chatModel{
		viewport:  vp,
		width:     80,
		agentName: "main",
		messages: []chatMessage{
			{role: "tool", toolName: "search", toolCallID: "tc1", toolState: "success"},
		},
	}
	m.updateViewport()
	view := ansi.Strip(m.viewport.View())
	if !strings.Contains(view, "✓") {
		t.Errorf("success tool card should render ✓, got: %q", view)
	}
	if !strings.Contains(view, "search") {
		t.Errorf("rendered view missing tool name: %q", view)
	}
}

func TestRenderToolCard_Error(t *testing.T) {
	vp := viewport.New()
	vp.SetWidth(80)
	vp.SetHeight(10)
	m := &chatModel{
		viewport:  vp,
		width:     80,
		agentName: "main",
		messages: []chatMessage{
			{role: "tool", toolName: "read", toolCallID: "tc1", toolState: "error", toolError: "file not found"},
		},
	}
	m.updateViewport()
	view := ansi.Strip(m.viewport.View())
	if !strings.Contains(view, "✖") {
		t.Errorf("error tool card should render ✖, got: %q", view)
	}
	if !strings.Contains(view, "file not found") {
		t.Errorf("error tool card should include error detail, got: %q", view)
	}
}

func TestNarrowLayout_Threshold(t *testing.T) {
	tests := []struct {
		name       string
		agentName  string
		width      int
		wantNarrow bool
	}{
		{"wide_short_agent", "main", 100, false},
		{"narrow_short_agent", "main", 30, true},
		{"boundary_short_agent_narrow", "main", 69, true},
		{"boundary_short_agent_wide", "main", 70, false},
		{"wide_long_agent", "longagentname", 100, false},
		{"narrow_long_agent", "longagentname", 70, true},
		{"zero_width", "main", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &chatModel{agentName: tt.agentName, width: tt.width}
			if got := m.narrowLayout(); got != tt.wantNarrow {
				t.Errorf("narrowLayout() width=%d agent=%q = %v, want %v",
					tt.width, tt.agentName, got, tt.wantNarrow)
			}
		})
	}
}
