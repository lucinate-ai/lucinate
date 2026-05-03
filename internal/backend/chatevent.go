package backend

import (
	"encoding/json"
	"strings"
)

// chatFinalContentBlock mirrors the {type, text} shape backends use for
// the structured Content array of a final chat event. It is duplicated
// here (rather than shared with the TUI's chatContentBlock) so the
// helper sits at the wire boundary instead of in the rendering layer.
type chatFinalContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// chatFinalMessage is the structured shape of a final chat event's
// Message field: a list of typed content blocks. Delta events instead
// send a plain JSON string.
type chatFinalMessage struct {
	Content []chatFinalContentBlock `json:"content"`
}

// ExtractChatText parses the Message field of a protocol.ChatEvent and
// returns the human-readable text. Delta events are encoded as a plain
// JSON string and are returned verbatim; final events carry a
// structured object whose text-typed blocks are joined with newlines.
// An empty raw payload returns "".
//
// The helper is shared by every code path that wants the visible body
// of an assistant turn — the TUI's streaming chat view and the
// one-shot CLI's Send loop both call it so the wire-format parsing
// stays in one place.
func ExtractChatText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var msg chatFinalMessage
	if json.Unmarshal(raw, &msg) == nil {
		var parts []string
		for _, block := range msg.Content {
			if block.Type == "text" && block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}

// ExtractChatThinking returns the concatenated thinking-block text from
// a final chat event's Message field. Delta events never carry
// thinking blocks, so a plain-string payload returns "". An empty raw
// payload returns "" as well.
func ExtractChatThinking(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var msg chatFinalMessage
	if json.Unmarshal(raw, &msg) != nil {
		return ""
	}
	var parts []string
	for _, block := range msg.Content {
		if block.Type == "thinking" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}
