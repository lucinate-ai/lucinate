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
			}

		case "tool":
			b.WriteString(m.renderToolCard(msg, contentWidth))
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
