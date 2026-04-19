package tui

import (
	"context"
	"encoding/json"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"

	"github.com/outofcoffee/repclaw/internal/client"
)

const historyLimit = 20

// historyResponse is the structure of the chat.history RPC response.
type historyResponse struct {
	Messages []historyMessage `json:"messages"`
}

type historyMessage struct {
	Role    string             `json:"role"`
	Content []chatContentBlock `json:"content"`
}

func (m chatModel) loadHistory() tea.Cmd {
	sessionKey := m.sessionKey
	cl := m.client
	renderer := m.renderer
	return func() tea.Msg {
		msgs, err := fetchHistory(cl, sessionKey, renderer)
		return historyLoadedMsg{messages: msgs, err: err}
	}
}

func (m chatModel) refreshHistory() tea.Cmd {
	sessionKey := m.sessionKey
	cl := m.client
	renderer := m.renderer
	return func() tea.Msg {
		msgs, err := fetchHistory(cl, sessionKey, renderer)
		return historyRefreshMsg{messages: msgs, err: err}
	}
}

func fetchHistory(cl *client.Client, sessionKey string, renderer *glamour.TermRenderer) ([]chatMessage, error) {
	raw, err := cl.ChatHistory(context.Background(), sessionKey, historyLimit)
	if err != nil {
		return nil, err
	}
	var resp historyResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	var msgs []chatMessage
	for _, hm := range resp.Messages {
		role := hm.Role
		if role != "user" && role != "assistant" {
			continue
		}
		var parts []string
		for _, block := range hm.Content {
			if block.Type == "text" && block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		text := strings.Join(parts, "\n")
		if text == "" {
			continue
		}
		if role == "user" {
			text = stripSystemLines(text)
			if text == "" {
				continue
			}
		}
		rendered := false
		if role == "assistant" && renderer != nil && looksLikeMarkdown(text) {
			if out, err := renderer.Render(text); err == nil {
				text = strings.TrimSpace(out)
				rendered = true
			}
		}
		msgs = append(msgs, chatMessage{role: role, content: text, rendered: rendered})
	}
	return msgs, nil
}

// stripSystemLines removes "System:" prefixed lines and leading whitespace
// from user messages, returning only the human-authored portion.
// Also matches "System (untrusted):" which the gateway may substitute.
func stripSystemLines(s string) string {
	lines := strings.Split(s, "\n")
	var kept []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isSystemLine(trimmed) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// isSystemLine returns true if the line starts with a System prefix,
// matching both "System:" and gateway-rewritten forms like "System (untrusted):".
func isSystemLine(line string) bool {
	if strings.HasPrefix(line, "System:") {
		return true
	}
	if strings.HasPrefix(line, "System (") {
		// Match "System (<anything>):" pattern.
		if idx := strings.Index(line, "):"); idx >= 0 {
			return true
		}
	}
	return false
}

// looksLikeMarkdown returns true when assistant text likely benefits from
// Glamour rendering. Plain single-line replies should stay unrendered so they
// don't pick up paragraph indentation from the markdown renderer.
func looksLikeMarkdown(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}

	for _, marker := range []string{"```", "`", "**", "__", "* ", "- ", "> ", "|", "\n#"} {
		if strings.Contains(s, marker) {
			return true
		}
	}

	lines := strings.Split(s, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			return true
		}
		if len(line) >= 3 && line[0] >= '0' && line[0] <= '9' && line[1] == '.' && line[2] == ' ' {
			return true
		}
	}

	return false
}
