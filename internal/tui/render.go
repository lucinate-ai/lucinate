package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/olekukonko/tablewriter"
)

func (m *chatModel) updateViewport() {
	var b strings.Builder
	contentWidth := m.width - 4

	if m.historyLoading && len(m.messages) == 0 && len(m.pendingMessages) == 0 {
		b.WriteString(statusStyle.Render(wordWrap("Loading conversation history…", contentWidth)))
	} else if !m.historyLoading && len(m.messages) == 0 && len(m.pendingMessages) == 0 {
		b.WriteString(emptyHistoryStyle.Render(wordWrap("No conversation history for this session.", contentWidth)))
	}

	for i, msg := range m.messages {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(m.renderMessage(msg))
	}

	// Render queued messages that haven't been sent yet — shown as dim/italic
	// shadows to distinguish them from confirmed messages.
	for _, text := range m.pendingMessages {
		b.WriteString("\n")
		prefixIndent, wrapWidth := m.writePrefix(&b, pendingPrefixStyle, "You")
		body := wordWrap(text, wrapWidth)
		b.WriteString(pendingBodyStyle.Render(indentMultiline(body, prefixIndent)))
	}

	content := b.String()

	// Pad the top so messages are anchored to the bottom of the viewport.
	contentLines := strings.Count(content, "\n")
	if contentLines < m.viewport.Height() {
		padding := strings.Repeat("\n", m.viewport.Height()-contentLines)
		content = padding + content
	}

	// Only auto-follow when the user is already pinned at the bottom. If they've
	// scrolled up to read earlier messages, leave their position alone — otherwise
	// the next delta or spinner tick would yank them back down.
	wasAtBottom := m.viewport.AtBottom()
	m.viewport.SetContent(content)
	if wasAtBottom {
		m.viewport.GotoBottom()
	}
}

// renderMessage renders a single transcript row to a styled string, without
// the leading separator newline that joins consecutive rows. Shared by the
// alt-screen viewport path (updateViewport) and the inline scrollback path
// (renderLiveTail / commitToScrollback) so a row looks identical whether it
// is still live or has been committed to the terminal's native scrollback.
func (m *chatModel) renderMessage(msg chatMessage) string {
	contentWidth := m.width - 4
	var b strings.Builder
	switch msg.role {
	case "separator":
		b.WriteString(statusStyle.Render(buildSeparator(contentWidth, formatSeparatorLabel(msg.timestampMs, time.Now()))))

	case "user":
		prefixIndent, wrapWidth := m.writePrefix(&b, userPrefixStyle, "You")
		body := wordWrap(msg.content, wrapWidth)
		b.WriteString(indentMultiline(body, prefixIndent))

	case "assistant":
		prefixIndent, wrapWidth := m.writePrefix(&b, assistantPrefixStyle, m.agentName)
		if msg.errMsg != "" {
			body := wordWrap(msg.errMsg, wrapWidth)
			b.WriteString(errorStyle.Render(indentMultiline(body, prefixIndent)))
		} else {
			if msg.thinking != "" {
				b.WriteString(statusStyle.Render("◦ thinking"))
				b.WriteString("\n")
				thinkingBody := wordWrap(msg.thinking, wrapWidth)
				b.WriteString(thinkingBodyStyle.Render(indentMultiline(thinkingBody, prefixIndent)))
				b.WriteString("\n\n")
				b.WriteString(prefixIndent)
			}
			if msg.streaming {
				body := wordWrap(msg.content, wrapWidth)
				b.WriteString(indentMultiline(body, prefixIndent))
				b.WriteString(cursorStyle.Render(spinnerFrames[m.spinnerFrame%len(spinnerFrames)]))
			} else if msg.rendered {
				// Glamour-rendered content is already wrapped and contains ANSI codes.
				content := msg.content
				if m.narrowLayout() {
					// In stacked layout, strip glamour's left margin from each
					// line so the body sits flush under the prefix.
					content = stripLeadingSpacesPerLine(content)
				}
				b.WriteString(indentMultiline(content, prefixIndent))
			} else {
				body := wordWrap(msg.content, wrapWidth)
				b.WriteString(indentMultiline(body, prefixIndent))
			}
		}

	case "system":
		if msg.errMsg != "" {
			b.WriteString(errorStyle.Render(wordWrap(msg.errMsg, contentWidth)))
		} else {
			b.WriteString(statusStyle.Render(wordWrap(msg.content, contentWidth)))
			if msg.pending {
				b.WriteString(" ")
				b.WriteString(cursorStyle.Render(spinnerFrames[m.spinnerFrame%len(spinnerFrames)]))
			}
		}

	case "tool":
		b.WriteString(m.renderToolCard(msg, contentWidth))
	}
	return b.String()
}

// inlineScrollbackEnabled reports whether the inline scrollback prototype is
// active. Gated on an env var so the shipping alt-screen path is untouched by
// default and the two can be A/B'd in the same build. See the plan in
// docs/inline-scrollback.md.
func inlineScrollbackEnabled() bool {
	return os.Getenv("LUCINATE_INLINE") == "1"
}

// renderLiveTail renders the inline live region's transcript portion: the tail
// of m.messages not yet spilled to scrollback (committedCount onward), followed
// by any queued-but-unsent messages. inlineView bottom-anchors this within the
// tail budget; reflowInline spills whatever overflows the budget's top into the
// terminal's native scrollback.
func (m *chatModel) renderLiveTail() string {
	contentWidth := m.width - 4
	if m.historyLoading && len(m.messages) == 0 && len(m.pendingMessages) == 0 {
		return statusStyle.Render(wordWrap("Loading conversation history…", contentWidth))
	}

	start := m.committedCount
	if start < 0 {
		start = 0
	}
	var b strings.Builder
	first := true
	for i := start; i < len(m.messages); i++ {
		if !first {
			b.WriteString("\n")
		}
		first = false
		b.WriteString(m.renderMessage(m.messages[i]))
	}
	if pending := m.renderPendingTail(); pending != "" {
		if !first {
			b.WriteString("\n")
		}
		b.WriteString(pending)
	}
	return b.String()
}

// renderPendingTail renders the queued-but-unsent messages shown as dim
// shadows above the input. Always live (never spilled), so reflowInline counts
// its height as fixed overhead when deciding what to spill.
func (m *chatModel) renderPendingTail() string {
	if len(m.pendingMessages) == 0 {
		return ""
	}
	var b strings.Builder
	for i, text := range m.pendingMessages {
		if i > 0 {
			b.WriteString("\n")
		}
		prefixIndent, wrapWidth := m.writePrefix(&b, pendingPrefixStyle, "You")
		body := wordWrap(text, wrapWidth)
		b.WriteString(pendingBodyStyle.Render(indentMultiline(body, prefixIndent)))
	}
	return b.String()
}

// buildCommitBlock renders m.messages[from:to] joined the same way the viewport
// joins rows (a single newline between blocks, none leading). Pure: no mutation,
// so tests can assert exactly what a spill would push to scrollback.
func (m *chatModel) buildCommitBlock(from, to int) string {
	if from < 0 {
		from = 0
	}
	if to > len(m.messages) {
		to = len(m.messages)
	}
	var b strings.Builder
	for i := from; i < to; i++ {
		if i > from {
			b.WriteString("\n")
		}
		b.WriteString(m.renderMessage(m.messages[i]))
	}
	return b.String()
}

// clampToWidth truncates every line of s to at most width display cells. The
// inline renderer positions frames by moving the cursor up by the frame's line
// count; a line wider than the terminal wraps to two rows, so the real frame is
// taller than the line count and the cursor-up math corrupts (ghost frames).
// Truncating guarantees one screen row per line so the accounting holds. It is
// ANSI-aware, so styled runs (the status bar's background, etc.) stay intact.
func clampToWidth(s string, width int) string {
	if width <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if ansi.StringWidth(line) > width {
			lines[i] = ansi.Truncate(line, width, "")
		}
	}
	return strings.Join(lines, "\n")
}

// fitAbove sizes the "above the input" block (live tail + completion menu +
// notifications + routine status) for the inline layout. It pads the TOP with
// blank rows up to floor — so the block never renders shorter than its
// high-water height and the input below it can't jump up when the tail or menu
// shrinks — and clips the OLDEST rows if it would exceed maxRows, keeping the
// newest content (and the menu, which sits at the bottom of the block) visible.
func fitAbove(s string, floor, maxRows int) string {
	var lines []string
	if s != "" {
		lines = strings.Split(s, "\n")
	}
	if len(lines) < floor {
		lines = append(make([]string, floor-len(lines)), lines...)
	}
	if maxRows >= 0 && len(lines) > maxRows {
		lines = lines[len(lines)-maxRows:]
	}
	return strings.Join(lines, "\n")
}

// reflowInline spills the OLDEST finished messages into the terminal's native
// scrollback, but only once the accumulated live tail would overflow the space
// above the input — so finished turns stay live (no frame shrink, no input
// jump) until the screen is full, then the oldest scroll off into scrollback.
// The newest message is always kept live (it may still be streaming, and an
// over-tall streaming turn is clipped for display rather than spilled). Returns
// the print command, or nil when nothing needs spilling.
func (m *chatModel) reflowInline() tea.Cmd {
	if !m.inline {
		return nil
	}
	if m.committedCount > len(m.messages) {
		m.committedCount = len(m.messages)
	}
	if len(m.messages) == 0 {
		return nil
	}

	// Budget for the live message tail: the rows above the input, minus the
	// non-message chrome that also sits there (menu, notifications, routine
	// status, queued messages).
	c := m.buildChrome()
	budget := m.inlineMaxAbove()
	if c.menu != "" {
		budget -= lipgloss.Height(c.menu)
	}
	if c.notifications != "" {
		budget -= lipgloss.Height(c.notifications)
	}
	if c.routineStatus != "" {
		budget -= lipgloss.Height(c.routineStatus)
	}
	if p := m.renderPendingTail(); p != "" {
		budget -= lipgloss.Height(p)
	}
	if budget < 1 {
		budget = 1
	}

	// Keep the newest run of messages that fits; spill everything older.
	used := 0
	newCommitted := len(m.messages)
	for i := len(m.messages) - 1; i >= m.committedCount; i-- {
		h := lipgloss.Height(m.renderMessage(m.messages[i]))
		if newCommitted < len(m.messages) && used+h > budget {
			break
		}
		used += h
		newCommitted = i
	}
	if newCommitted <= m.committedCount {
		return nil
	}
	block := m.buildCommitBlock(m.committedCount, newCommitted)
	m.committedCount = newCommitted
	if block == "" {
		return nil
	}
	return tea.Printf("%s", block)
}

// updateInlineFloor tracks the high-water height of everything above the input
// (tail + menu + notifications + routine status). Called after every chat
// update. The block may grow the floor but the view never renders it shorter,
// so the input holds position instead of jumping when a row is dropped or the
// menu filters down. Resets once the block empties (idle).
//
// Note: the floor is deliberately NOT released when the completion menu closes.
// Releasing it shrinks the frame, and Bubble Tea's top-anchored inline renderer
// then strands the status bar mid-screen with blank rows beneath it (worse than
// the small gap a held floor leaves). The menu opening scrolls native scrollback
// up to make room, and that can't be scrolled back down programmatically — an
// inherent limit of rendering into the terminal's own scrollback.
func (m *chatModel) updateInlineFloor() {
	if !m.inline {
		return
	}
	if h := m.aboveInputHeight(); h == 0 {
		m.turnTailFloor = 0
	} else if h > m.turnTailFloor {
		m.turnTailFloor = h
	}
}

// commitLoadedHistory spills the resumed conversation history — the canonical
// rows plus a trailing resume divider — into the terminal's native scrollback
// as one ordered block, and sets committedCount to len(canonical). Keeping the
// live region empty right after load is what makes the input sit at the bottom
// of the natural flow whenever the history fills the screen; on a short session
// the input floats where the content ends (normal terminal behaviour).
//
// Note: an earlier version also prepended blank-line padding to seat the input
// at the bottom on short sessions. That pins the small live region to the
// bottom of the screen, which leaves it no room to grow — opening the
// completion menu then forces a scroll that Bubble Tea's inline renderer can't
// clear, ghosting the input and status bar. The float is the robust choice.
//
// The divider is scrollback-only decoration (never stored in m.messages) so it
// can't shift the watermark when a later refresh, which never returns it,
// re-anchors the canonical prefix.
func (m *chatModel) commitLoadedHistory(canonical []chatMessage, lastTs int64) tea.Cmd {
	if !m.inline || len(canonical) == 0 {
		return nil
	}
	block := m.buildCommitBlock(0, len(canonical))
	block += "\n" + m.renderMessage(chatMessage{role: "separator", timestampMs: lastTs})
	m.committedCount = len(canonical)
	return tea.Printf("%s", block)
}

// commitToScrollback spills the newly-canonical leading rows of m.messages into
// scrollback and advances committedCount past them, keeping the live region
// small (a large live region fights insertAbove-based spills). canonicalLen is
// the number of leading rows now known to be server-canonical — len(server)
// right after a history refresh. Only canonical rows are ever committed, so the
// committed prefix is append-only and never needs rewriting (which scrollback
// can't do); the count is re-anchored each call, so a client-only row the
// server later drops self-corrects without a gap or a duplicate.
func (m *chatModel) commitToScrollback(canonicalLen int) tea.Cmd {
	if !m.inline {
		return nil
	}
	if canonicalLen > len(m.messages) {
		canonicalLen = len(m.messages)
	}
	if m.committedCount >= canonicalLen {
		m.committedCount = canonicalLen
		return nil
	}
	block := m.buildCommitBlock(m.committedCount, canonicalLen)
	m.committedCount = canonicalLen
	if block == "" {
		return nil
	}
	return tea.Printf("%s", block)
}

// narrowBodyMinWidth is the minimum body column width below which the inline
// prefix layout flips to a stacked layout with the prefix on its own line.
const narrowBodyMinWidth = 60

// narrowLayout reports whether the viewport is too narrow for an inline
// prefix to leave enough room for the message body.
func (m *chatModel) narrowLayout() bool {
	return (m.width - 4) - m.prefixWidth() < narrowBodyMinWidth
}

// writePrefix renders the message prefix into b and returns the per-continuation
// indent and the wrap width for the message body. In narrow mode the prefix is
// followed by a literal newline (written outside the styled Render call to avoid
// lipgloss right-padding the trailing empty line) so the body stacks beneath.
func (m *chatModel) writePrefix(b *strings.Builder, style lipgloss.Style, name string) (indent string, wrapWidth int) {
	contentWidth := m.width - 4
	if m.narrowLayout() {
		b.WriteString(style.Render(name + ":"))
		b.WriteString("\n")
		return "", contentWidth
	}
	label := m.prefixLabel(name)
	b.WriteString(style.Render(label))
	return strings.Repeat(" ", len(label)), contentWidth - len(label)
}

// prefixWidth returns the shared width used for message prefixes so message
// bodies start in the same column for both user and assistant rows.
func (m *chatModel) prefixWidth() int {
	w := len("You:")
	if aw := len(m.agentName + ":"); aw > w {
		w = aw
	}
	return w + 1
}

// prefixLabel returns the displayed label for a message prefix.
func (m *chatModel) prefixLabel(name string) string {
	label := name + ":"
	for len(label) < m.prefixWidth()-1 {
		label += " "
	}
	return label + " "
}

// formatStatsTable renders session stats as a formatted table.
func (m *chatModel) formatStatsTable() string {
	s := m.stats
	var buf strings.Builder

	allTokens := s.inputTokens + s.outputTokens + s.cacheRead + s.cacheWrite

	t := tablewriter.NewWriter(&buf)
	t.Header([]string{"", "Tokens", "Cost"})
	t.Bulk([][]string{
		{"Input", formatTokens(s.inputTokens), formatCost(s.inputCost)},
		{"Output", formatTokens(s.outputTokens), formatCost(s.outputCost)},
		{"Cache read", formatTokens(s.cacheRead), formatCost(s.cacheReadCost)},
		{"Cache write", formatTokens(s.cacheWrite), formatCost(s.cacheWriteCost)},
		{"Total", formatTokens(allTokens), formatCost(s.totalCost)},
	})
	t.Footer(nil)
	t.Render()

	buf.WriteString("\n")

	t2 := tablewriter.NewWriter(&buf)
	t2.Header([]string{"Messages", "Count"})
	t2.Bulk([][]string{
		{"User", fmt.Sprintf("%d", s.userMessages)},
		{"Assistant", fmt.Sprintf("%d", s.assistantMessages)},
		{"Total", fmt.Sprintf("%d", s.totalMessages)},
	})
	t2.Footer(nil)
	t2.Render()

	if n := len(m.skills); n > 0 {
		buf.WriteString(fmt.Sprintf("\nAgent skills: %d loaded\n", n))
	}

	return buf.String()
}

// formatCost formats a dollar amount.
func formatCost(c float64) string {
	if c < 0.01 {
		return fmt.Sprintf("$%.4f", c)
	}
	return fmt.Sprintf("$%.2f", c)
}

// formatTokens formats a token count with K/M suffixes.
func formatTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// formatTokensShort formats a token count using lowercase k/m suffixes
// in the "65k/1.0m" style used by the context-usage header. Thousands
// round to whole units (no decimal) so the figure stays compact in the
// status bar.
func formatTokensShort(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dk", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// wordWrap wraps text to the given width, preserving existing newlines.
// Lines that contain box-drawing characters (table output) are passed through
// unchanged to preserve column alignment.
func wordWrap(s string, width int) string {
	if width <= 0 || len(s) <= width {
		return s
	}
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if len(line) <= width || isTableLine(line) {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(line)
			continue
		}
		words := strings.Fields(line)
		lineLen := 0
		for i, w := range words {
			if i > 0 && lineLen+1+len(w) > width {
				b.WriteString("\n")
				lineLen = 0
			} else if i > 0 {
				b.WriteString(" ")
				lineLen++
			}
			b.WriteString(w)
			lineLen += len(w)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderErrorLine formats an error message as a single styled paragraph
// that wraps cleanly within the given viewport width. The "  Error: "
// prefix is included and continuation lines are indented to match, so
// long gateway error strings (like the JSON-schema validator's
// multi-clause messages) don't run off the side of the terminal.
//
// Pass width=0 to disable wrapping (useful when the caller doesn't have
// a width to hand, e.g. in fixed-width contexts).
func renderErrorLine(msg string, width int) string {
	const prefix = "  Error: "
	body := prefix + msg
	if width > 0 {
		body = indentMultiline(wordWrap(body, width), "  ")
	}
	return errorStyle.Render(body)
}

// indentMultiline indents every line after the first by the given prefix.
func indentMultiline(s, indent string) string {
	if indent == "" || !strings.Contains(s, "\n") {
		return s
	}

	lines := strings.Split(s, "\n")
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteString("\n")
			if i < len(lines)-1 || line != "" {
				b.WriteString(indent)
			}
		}
		b.WriteString(line)
	}
	return b.String()
}

// stripLeadingSpacesPerLine drops leading ASCII spaces from each line while
// preserving any ANSI escape sequences that precede them. Glamour adds a left
// document margin to its rendered output; in narrow stacked layout we want the
// body flush against column 0 so it lines up under the prefix.
func stripLeadingSpacesPerLine(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		var sb strings.Builder
		j := 0
		seenContent := false
		for j < len(line) {
			c := line[j]
			if c == 0x1b {
				k := j + 1
				if k < len(line) && line[k] == '[' {
					k++
					for k < len(line) && !((line[k] >= 0x40) && (line[k] <= 0x7e)) {
						k++
					}
					if k < len(line) {
						k++
					}
				}
				sb.WriteString(line[j:k])
				j = k
				continue
			}
			if !seenContent && c == ' ' {
				j++
				continue
			}
			seenContent = true
			sb.WriteByte(c)
			j++
		}
		lines[i] = sb.String()
	}
	return strings.Join(lines, "\n")
}

// lastTimestampMs returns the timestamp of the last message in msgs that
// carries one, or 0 if none do. Used to label the resume-point separator.
func lastTimestampMs(msgs []chatMessage) int64 {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].timestampMs > 0 {
			return msgs[i].timestampMs
		}
	}
	return 0
}

// formatSeparatorLabel renders a timestamp suffix for the history separator.
// Returns "" when the timestamp is missing so older backends fall back to a
// plain rule.
func formatSeparatorLabel(ms int64, now time.Time) string {
	if ms <= 0 {
		return ""
	}
	t := time.UnixMilli(ms).In(now.Location())
	if sameYMD(t, now) {
		return t.Format("15:04")
	}
	if sameYMD(t, now.AddDate(0, 0, -1)) {
		return "Yesterday " + t.Format("15:04")
	}
	if t.Year() == now.Year() {
		return t.Format("2 Jan 15:04")
	}
	return t.Format("2 Jan 2006 15:04")
}

func sameYMD(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// buildSeparator returns a horizontal rule of the given width with an optional
// centred label. The total visible width always equals width.
func buildSeparator(width int, label string) string {
	if width <= 0 {
		return ""
	}
	if label == "" {
		return strings.Repeat("─", width)
	}
	decorated := " " + label + " "
	if len(decorated) >= width {
		return strings.Repeat("─", width)
	}
	pad := (width - len(decorated)) / 2
	return strings.Repeat("─", pad) + decorated + strings.Repeat("─", width-pad-len(decorated))
}

// isTableLine returns true if the line appears to be part of a rendered table.
// These lines use box-drawing characters for borders and should not be
// word-wrapped as it would destroy their alignment.
func isTableLine(line string) bool {
	return strings.ContainsRune(line, '│') || strings.ContainsRune(line, '─')
}

// renderToolCard renders a single inline tool-status line. Running cards
// animate via the shared spinner frame; success and error states use static
// glyphs.
func (m *chatModel) renderToolCard(msg chatMessage, contentWidth int) string {
	var glyph string
	var lineStyle lipgloss.Style
	switch msg.toolState {
	case "success":
		glyph = "✓"
		lineStyle = toolSuccessStyle
	case "error":
		glyph = "✖"
		lineStyle = errorStyle
	default:
		glyph = spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
		lineStyle = toolRunningStyle
	}

	name := msg.toolName
	if name == "" {
		name = "tool"
	}

	var head strings.Builder
	head.WriteString(glyph)
	head.WriteString(" ")
	head.WriteString(toolNameStyle.Render(name))
	if msg.toolArgsLine != "" {
		head.WriteString(" ")
		head.WriteString("(")
		head.WriteString(msg.toolArgsLine)
		head.WriteString(")")
	}
	if msg.toolState == "error" && msg.toolError != "" {
		head.WriteString(" — ")
		head.WriteString(msg.toolError)
	}
	return lineStyle.Render(wordWrap(head.String(), contentWidth))
}
