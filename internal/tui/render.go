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
			b.WriteString("\n")

		case "user":
			prefix := userPrefixStyle.Render(m.prefixLabel("You"))
			b.WriteString(prefix)
			b.WriteString(wordWrap(msg.content, contentWidth-m.prefixWidth()))
			b.WriteString("\n")

		case "assistant":
			prefix := assistantPrefixStyle.Render(m.prefixLabel(m.agentName))
			b.WriteString(prefix)
			wrapWidth := contentWidth - m.prefixWidth()
			if msg.errMsg != "" {
				b.WriteString(errorStyle.Render(wordWrap(msg.errMsg, wrapWidth)))
			} else if msg.streaming {
				b.WriteString(wordWrap(msg.content, wrapWidth))
				b.WriteString(cursorStyle.Render("_"))
			} else if msg.rendered {
				// Glamour-rendered content is already wrapped and contains ANSI codes.
				b.WriteString(msg.content)
			} else {
				b.WriteString(wordWrap(msg.content, wrapWidth))
			}
			b.WriteString("\n")

		case "system":
			if msg.errMsg != "" {
				b.WriteString(errorStyle.Render(wordWrap(msg.errMsg, contentWidth)))
			} else {
				b.WriteString(statusStyle.Render(wordWrap(msg.content, contentWidth)))
			}
			b.WriteString("\n")
		}
	}

	// Render queued messages that haven't been sent yet.
	for _, text := range m.pendingMessages {
		b.WriteString("\n")
		prefix := userPrefixStyle.Render(m.prefixLabel("You"))
		b.WriteString(prefix)
		b.WriteString(wordWrap(text, contentWidth-m.prefixWidth()))
		b.WriteString("\n")
	}

	content := b.String()

	// Pad the top so messages are anchored to the bottom of the viewport.
	contentLines := strings.Count(content, "\n")
	if contentLines < m.viewport.Height {
		padding := strings.Repeat("\n", m.viewport.Height-contentLines)
		content = padding + content
	}

	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
}

// prefixWidth returns the column width used for message prefixes (e.g. "You: ",
// "agentName: "). Both user and assistant prefixes are padded to the same width
// so message content aligns vertically.
func (m *chatModel) prefixWidth() int {
	w := len("You") + 2 // "You: "
	if aw := len(m.agentName) + 2; aw > w {
		w = aw
	}
	return w
}

// prefixLabel returns a right-padded label for a message prefix, ensuring all
// prefixes occupy the same column width.
func (m *chatModel) prefixLabel(name string) string {
	w := m.prefixWidth()
	label := name + ": "
	for len(label) < w {
		label += " "
	}
	return label
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

// isTableLine returns true if the line appears to be part of a rendered table.
// These lines use box-drawing characters for borders and should not be
// word-wrapped as it would destroy their alignment.
func isTableLine(line string) bool {
	return strings.ContainsRune(line, '│') || strings.ContainsRune(line, '─')
}
