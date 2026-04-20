package tui

import (
	"fmt"
	"strings"

	"github.com/olekukonko/tablewriter"
)

func (m *chatModel) updateViewport() {
	var b strings.Builder
	contentWidth := m.width - 4

	for i, msg := range m.messages {
		if i > 0 {
			b.WriteString("\n")
		}
		switch msg.role {
		case "separator":
			sep := strings.Repeat("─", contentWidth)
			b.WriteString(statusStyle.Render(sep))

		case "user":
			label := m.prefixLabel("You")
			prefix := userPrefixStyle.Render(label)
			prefixIndent := strings.Repeat(" ", len(label))
			b.WriteString(prefix)
			body := wordWrap(msg.content, contentWidth-len(label))
			b.WriteString(indentMultiline(body, prefixIndent))

		case "assistant":
			label := m.prefixLabel(m.agentName)
			prefix := assistantPrefixStyle.Render(label)
			prefixIndent := strings.Repeat(" ", len(label))
			b.WriteString(prefix)
			wrapWidth := contentWidth - len(label)
			if msg.errMsg != "" {
				body := wordWrap(msg.errMsg, wrapWidth)
				b.WriteString(errorStyle.Render(indentMultiline(body, prefixIndent)))
			} else if msg.streaming {
				body := wordWrap(msg.content, wrapWidth)
				b.WriteString(indentMultiline(body, prefixIndent))
				b.WriteString(cursorStyle.Render(spinnerFrames[m.spinnerFrame%len(spinnerFrames)]))
			} else if msg.rendered {
				// Glamour-rendered content is already wrapped and contains ANSI codes.
				b.WriteString(indentMultiline(msg.content, prefixIndent))
			} else {
				body := wordWrap(msg.content, wrapWidth)
				b.WriteString(indentMultiline(body, prefixIndent))
			}

		case "system":
			if msg.errMsg != "" {
				b.WriteString(errorStyle.Render(wordWrap(msg.errMsg, contentWidth)))
			} else {
				b.WriteString(statusStyle.Render(wordWrap(msg.content, contentWidth)))
			}
		}
	}

	// Render queued messages that haven't been sent yet.
	for _, text := range m.pendingMessages {
		b.WriteString("\n")
		label := m.prefixLabel("You")
		prefix := userPrefixStyle.Render(label)
		prefixIndent := strings.Repeat(" ", len(label))
		b.WriteString(prefix)
		body := wordWrap(text, contentWidth-len(label))
		b.WriteString(indentMultiline(body, prefixIndent))
	}

	content := b.String()

	// Pad the top so messages are anchored to the bottom of the viewport.
	contentLines := strings.Count(content, "\n")
	if contentLines < m.viewport.Height() {
		padding := strings.Repeat("\n", m.viewport.Height()-contentLines)
		content = padding + content
	}

	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
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

// isTableLine returns true if the line appears to be part of a rendered table.
// These lines use box-drawing characters for borders and should not be
// word-wrapped as it would destroy their alignment.
func isTableLine(line string) bool {
	return strings.ContainsRune(line, '│') || strings.ContainsRune(line, '─')
}
