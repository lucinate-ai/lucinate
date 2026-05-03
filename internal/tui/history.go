package tui

import (
	"context"
	"encoding/json"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"

	"github.com/lucinate-ai/lucinate/internal/backend"
)

// historyResponse is the structure of the chat.history RPC response.
type historyResponse struct {
	Messages []historyMessage `json:"messages"`
}

type historyMessage struct {
	Role      string             `json:"role"`
	Content   []chatContentBlock `json:"content"`
	Timestamp int64              `json:"timestamp,omitempty"` // unix millis, when present
}

func (m chatModel) loadHistory() tea.Cmd {
	sessionKey := m.sessionKey
	b := m.backend
	renderer := m.renderer
	limit := m.historyLimit
	return func() tea.Msg {
		msgs, err := fetchHistory(b, sessionKey, renderer, limit)
		return historyLoadedMsg{messages: msgs, err: err}
	}
}

func (m chatModel) refreshHistory() tea.Cmd {
	sessionKey := m.sessionKey
	b := m.backend
	renderer := m.renderer
	limit := m.historyLimit
	return func() tea.Msg {
		msgs, err := fetchHistory(b, sessionKey, renderer, limit)
		return historyRefreshMsg{messages: msgs, err: err}
	}
}

func fetchHistory(b backend.Backend, sessionKey string, renderer *glamour.TermRenderer, limit int) ([]chatMessage, error) {
	raw, err := b.ChatHistory(context.Background(), sessionKey, limit)
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
		var thinkingParts []string
		for _, block := range hm.Content {
			if block.Type == "text" && block.Text != "" {
				parts = append(parts, block.Text)
			}
			if block.Type == "thinking" && block.Text != "" {
				thinkingParts = append(thinkingParts, block.Text)
			}
		}
		text := strings.Join(parts, "\n")
		thinking := strings.Join(thinkingParts, "\n")
		if text == "" {
			continue
		}
		if role == "user" {
			text = stripLocalAgentSkillBlocks(text)
			text = stripSystemLines(text)
			if text == "" {
				continue
			}
		}
		rendered := false
		raw := ""
		if role == "assistant" && renderer != nil && looksLikeMarkdown(text) {
			if out, err := renderer.Render(text); err == nil {
				raw = text
				text = strings.TrimSpace(out)
				rendered = true
			}
		}
		msgs = append(msgs, chatMessage{role: role, content: text, raw: raw, thinking: thinking, rendered: rendered, timestampMs: hm.Timestamp})
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

// stripLocalAgentSkillBlocks removes the <local-agent-skill> envelope so it
// is hidden from the rendered transcript. Strips the "Please use the following
// skill(s):" preamble line and every <local-agent-skill ...>...</local-agent-skill>
// block (inclusive). Collapses runs of blank lines left behind.
func stripLocalAgentSkillBlocks(s string) string {
	if s == "" || !strings.Contains(s, "<local-agent-skill") {
		return s
	}
	lines := strings.Split(s, "\n")
	var kept []string
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inBlock {
			if strings.Contains(trimmed, "</local-agent-skill>") {
				inBlock = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "<local-agent-skill") {
			if !strings.Contains(trimmed, "</local-agent-skill>") {
				inBlock = true
			}
			continue
		}
		if trimmed == "Please use the following skill:" || trimmed == "Please use the following skills:" {
			continue
		}
		kept = append(kept, line)
	}
	// Collapse runs of blank lines.
	var out []string
	prevBlank := false
	for _, line := range kept {
		blank := strings.TrimSpace(line) == ""
		if blank && prevBlank {
			continue
		}
		out = append(out, line)
		prevBlank = blank
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
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
