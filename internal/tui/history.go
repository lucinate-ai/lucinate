package tui

import (
	"context"
	"encoding/json"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"github.com/a3tai/openclaw-go/protocol"

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

// refreshHistoryAt issues a server-history fetch and tags the result
// with the boundary captured at issue time. The merge in the
// historyRefreshMsg handler keeps every existing row whose gen exceeds
// boundary (the live tail) and replaces the rest with the fetched
// state — making it safe for callers to refresh while the next turn
// is already streaming, a tool card is mid-execution, or a pending
// system row is awaiting outcome.
//
// Callers in the chat-event final/error/aborted paths pass the gen
// they captured *before* bumping (i.e. the just-finalised turn's gen),
// so that turn and everything older gets replaced by canonical state.
// Callers on the queue-drained path pass m.gen-1 for the same reason.
func (m chatModel) refreshHistoryAt(boundary uint64) tea.Cmd {
	sessionKey := m.sessionKey
	b := m.backend
	renderer := m.renderer
	limit := m.historyLimit
	return func() tea.Msg {
		msgs, err := fetchHistory(b, sessionKey, renderer, limit)
		return historyRefreshMsg{messages: msgs, boundary: boundary, err: err}
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

// buildCronTranscriptMessages reconstructs a transcript-style message
// list from a cron job and its run log. Cron-isolated runs don't keep
// a queryable chat session, but the payload (the user's prompt) and
// each run's summary/error are persisted in the run log — which is
// what the cron-detail run-history previews already display. This
// surfaces the same content as a normal-looking conversation: each run
// becomes a separator, a user turn (the payload), an assistant turn
// (the summary, or the error if the run produced no output), and a
// trailing system error note if the run logged an error or delivery
// failure alongside the summary — the agent's work succeeded but the
// run was marked failed for an ancillary reason worth surfacing.
func buildCronTranscriptMessages(payload string, runs []protocol.CronRunLogEntry, renderer *glamour.TermRenderer) []chatMessage {
	if len(runs) == 0 {
		return nil
	}
	// Run logs arrive newest-first; render oldest-first so the
	// transcript reads top-down chronologically like a real chat.
	ordered := make([]protocol.CronRunLogEntry, len(runs))
	for i, r := range runs {
		ordered[len(runs)-1-i] = r
	}
	var msgs []chatMessage
	for _, r := range ordered {
		var ts int64
		if r.RunAtMs != nil {
			ts = *r.RunAtMs
		}
		msgs = append(msgs, chatMessage{role: "separator", timestampMs: ts})
		if payload != "" {
			msgs = append(msgs, chatMessage{role: "user", content: payload, timestampMs: ts})
		}
		errNote := cronRunErrorNote(r)
		switch {
		case r.Summary != "":
			content := r.Summary
			raw := ""
			rendered := false
			if renderer != nil && looksLikeMarkdown(content) {
				if out, err := renderer.Render(content); err == nil {
					raw = content
					content = strings.TrimSpace(out)
					rendered = true
				}
			}
			msgs = append(msgs, chatMessage{role: "assistant", content: content, raw: raw, rendered: rendered, timestampMs: ts})
			if errNote != "" {
				msgs = append(msgs, chatMessage{role: "system", errMsg: errNote, timestampMs: ts})
			}
		case errNote != "":
			msgs = append(msgs, chatMessage{role: "assistant", errMsg: errNote, timestampMs: ts})
		}
	}
	return msgs
}

// cronRunErrorNote joins the run-level error and delivery-level error
// into a single reader-friendly string, deduping when the gateway has
// echoed the same text into both fields.
func cronRunErrorNote(r protocol.CronRunLogEntry) string {
	var parts []string
	seen := map[string]bool{}
	add := func(label, val string) {
		v := strings.TrimSpace(val)
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		parts = append(parts, label+": "+v)
	}
	add("Run error", r.Error)
	add("Delivery error", r.DeliveryError)
	return strings.Join(parts, "\n")
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
