package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
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
		}
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

	// Cache the rendered lines for mouse selection (hit-testing + copy) and
	// bake in the highlight while a drag is active. Coordinates are line
	// indexes into these lines, so they survive pure restyles (a spinner
	// frame swap keeps the count) but not row shifts — if the rendered line
	// count changes mid-drag (streaming growth, history refresh), drop the
	// selection rather than highlight the wrong text.
	lines := strings.Split(content, "\n")
	if m.sel.dragging && len(lines) != len(m.selLines) {
		m.sel = selectionState{}
	}
	m.selLines = lines
	if m.sel.dragging {
		display := make([]string, len(lines))
		copy(display, lines)
		applySelectionHighlight(display, m.sel.anchor, m.sel.head)
		content = strings.Join(display, "\n")
	}

	// Pad the top so messages are anchored to the bottom of the viewport.
	// Record the pad so selection hit-testing can subtract the blank rows.
	m.viewportTopPad = 0
	contentLines := strings.Count(content, "\n")
	if contentLines < m.viewport.Height() {
		m.viewportTopPad = m.viewport.Height() - contentLines
		content = strings.Repeat("\n", m.viewportTopPad) + content
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

// maxToolStripRows bounds the height of the live tool-activity strip so a
// turn that fans out many tools can't push the input off-screen. When there
// are more, the oldest collapse into a single "…N earlier" line, keeping the
// most recent (usually still-running) entries visible.
const maxToolStripRows = 6

// renderToolActivity builds the ephemeral tool-activity strip shown above the
// input. While the turn is in flight — or any tool is still running — each
// tool is listed on its own line, running ones animating on the shared
// spinner frame. Once the turn is idle and every tool has resolved, the strip
// collapses to a single summary line so the transcript stays clean. Returns
// "" when no tools ran this turn.
func (m *chatModel) renderToolActivity() string {
	if len(m.activeTools) == 0 {
		return ""
	}
	width := m.width - 4
	if width < 1 {
		width = 1
	}
	if m.sending || m.anyToolRunning() {
		return m.renderToolStripExpanded(width)
	}
	return m.renderToolSummary(width)
}

// toolStripHeight reports the rendered row count of the tool-activity strip,
// so applyLayout can shrink the viewport to make room. 0 when the strip is
// empty.
func (m *chatModel) toolStripHeight() int {
	s := m.renderToolActivity()
	if s == "" {
		return 0
	}
	return lipgloss.Height(s)
}

// renderToolStripExpanded lists the current turn's tools one per line. Entries
// past maxToolStripRows are folded into a leading "…N earlier" summary so the
// newest (typically running) tools stay on screen.
func (m *chatModel) renderToolStripExpanded(width int) string {
	tools := m.activeTools
	var lines []string
	if len(tools) > maxToolStripRows {
		hidden := len(tools) - (maxToolStripRows - 1)
		tools = tools[len(tools)-(maxToolStripRows-1):]
		lines = append(lines, statusStyle.Render(truncateToWidth(fmt.Sprintf("  …%d earlier", hidden), width)))
	}
	for _, t := range tools {
		lines = append(lines, renderToolLine(t, m.spinnerFrame, width))
	}
	return strings.Join(lines, "\n")
}

// renderToolLine formats one tool entry as a single truncated row: a state
// glyph (animated while running), the tool name, its argument summary, and an
// error detail when it failed.
func renderToolLine(t toolActivity, frame, width int) string {
	glyph, lineStyle := toolGlyphStyle(t, frame)
	name := t.name
	if name == "" {
		name = "tool"
	}
	line := glyph + " " + name
	if t.argsLine != "" {
		line += " (" + t.argsLine + ")"
	}
	if t.state == "error" && t.errText != "" {
		line += " — " + t.errText
	}
	return lineStyle.Render(truncateToWidth(line, width))
}

// renderToolSummary collapses a completed turn's tools into one line, grouping
// by name in first-seen order with call counts and any failure tally, e.g.
// "✓ called web_search ×3, web_fetch ×2". The glyph turns to ✖ if any call
// failed.
func (m *chatModel) renderToolSummary(width int) string {
	type group struct {
		name  string
		count int
		errs  int
	}
	var order []*group
	byName := map[string]*group{}
	anyErr := false
	for _, t := range m.activeTools {
		g := byName[t.name]
		if g == nil {
			g = &group{name: t.name}
			byName[t.name] = g
			order = append(order, g)
		}
		g.count++
		if t.state == "error" {
			g.errs++
			anyErr = true
		}
	}
	var parts []string
	for _, g := range order {
		p := g.name
		if p == "" {
			p = "tool"
		}
		if g.count > 1 {
			p += fmt.Sprintf(" ×%d", g.count)
		}
		if g.errs > 0 {
			p += fmt.Sprintf(" (%d failed)", g.errs)
		}
		parts = append(parts, p)
	}
	glyph, lineStyle := "✓", toolSuccessStyle
	if anyErr {
		glyph, lineStyle = "✖", errorStyle
	}
	line := glyph + " called " + strings.Join(parts, ", ")
	return lineStyle.Render(truncateToWidth(line, width))
}

// toolGlyphStyle maps a tool's state to its leading glyph and line style.
// Running reuses the shared spinner frame so the strip animates in lockstep
// with the streaming cursor.
func toolGlyphStyle(t toolActivity, frame int) (string, lipgloss.Style) {
	switch t.state {
	case "success":
		return "✓", toolSuccessStyle
	case "error":
		return "✖", errorStyle
	default:
		return spinnerFrames[frame%len(spinnerFrames)], toolRunningStyle
	}
}

// truncateToWidth clips s to a rune width, appending an ellipsis when it
// overflows, so a single strip row can never wrap and break the height
// accounting. Operates on plain text — call before styling.
func truncateToWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(r[:width-1]) + "…"
}
