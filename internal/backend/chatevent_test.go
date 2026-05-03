package backend

import (
	"encoding/json"
	"testing"

	"github.com/a3tai/openclaw-go/protocol"
)

// TestExtractChatText pins the parser against the two wire shapes
// chat events ship — the plain JSON string used by streaming deltas
// and the structured {role, content[]} object emitted by final events
// — plus the malformed/edge-case fallbacks the TUI and the one-shot
// CLI both depend on for not crashing on a surprise payload.
func TestExtractChatText(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{
			name: "nil input",
			raw:  nil,
			want: "",
		},
		{
			name: "empty slice",
			raw:  json.RawMessage{},
			want: "",
		},
		{
			name: "delta plain string",
			raw:  json.RawMessage(`"Hello, world!"`),
			want: "Hello, world!",
		},
		{
			name: "delta empty string",
			raw:  json.RawMessage(`""`),
			want: "",
		},
		{
			name: "delta string with embedded JSON characters",
			raw:  json.RawMessage(`"line one\nline two\t{not parsed}"`),
			want: "line one\nline two\t{not parsed}",
		},
		{
			name: "final single text block",
			raw:  json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"Only paragraph."}]}`),
			want: "Only paragraph.",
		},
		{
			name: "final multiple text blocks joined by newline",
			raw:  json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"First."},{"type":"text","text":"Second."}]}`),
			want: "First.\nSecond.",
		},
		{
			name: "final mixed blocks keeps only text type",
			raw:  json.RawMessage(`{"role":"assistant","content":[{"type":"thinking","text":"hidden"},{"type":"tool_use","text":""},{"type":"text","text":"Visible."}]}`),
			want: "Visible.",
		},
		{
			name: "final empty content array",
			raw:  json.RawMessage(`{"role":"assistant","content":[]}`),
			want: "",
		},
		{
			name: "final text block with empty text is skipped",
			raw:  json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":""},{"type":"text","text":"After empty."}]}`),
			want: "After empty.",
		},
		{
			name: "final with no content key",
			raw:  json.RawMessage(`{"role":"assistant"}`),
			want: "",
		},
		{
			name: "final preserves text containing newlines",
			raw:  json.RawMessage(`{"content":[{"type":"text","text":"top\nbottom"}]}`),
			want: "top\nbottom",
		},
		{
			name: "json number fallback",
			raw:  json.RawMessage(`12345`),
			want: "12345",
		},
		{
			name: "json bool fallback",
			raw:  json.RawMessage(`true`),
			want: "true",
		},
		{
			name: "json null returns empty",
			raw:  json.RawMessage(`null`),
			// json.Unmarshal of `null` into chatFinalMessage succeeds
			// with a zero-value struct, so the helper takes the
			// structured branch and returns "" rather than the literal
			// "null" — semantically a null payload means "no content".
			want: "",
		},
		{
			name: "malformed json fallback",
			raw:  json.RawMessage(`{not json`),
			want: "{not json",
		},
		{
			name: "json array fallback",
			raw:  json.RawMessage(`["a","b"]`),
			// arrays do not match chatFinalMessage and are not strings;
			// returned verbatim so a curious operator can still see the
			// payload rather than getting a silent "".
			want: `["a","b"]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractChatText(tc.raw)
			if got != tc.want {
				t.Errorf("ExtractChatText(%s) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestExtractChatThinking pins the thinking-block extractor against
// its narrow contract: only structured final events ever carry
// thinking blocks, so plain-string delta payloads, empty inputs, and
// malformed JSON all return "" rather than leaking the raw text into
// the thinking surface.
func TestExtractChatThinking(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{
			name: "nil input",
			raw:  nil,
			want: "",
		},
		{
			name: "empty slice",
			raw:  json.RawMessage{},
			want: "",
		},
		{
			name: "delta plain string ignored",
			raw:  json.RawMessage(`"a delta string is never a thinking block"`),
			want: "",
		},
		{
			name: "single thinking block",
			raw:  json.RawMessage(`{"content":[{"type":"thinking","text":"reasoning step"}]}`),
			want: "reasoning step",
		},
		{
			name: "multiple thinking blocks joined by newline",
			raw:  json.RawMessage(`{"content":[{"type":"thinking","text":"step 1"},{"type":"thinking","text":"step 2"}]}`),
			want: "step 1\nstep 2",
		},
		{
			name: "mixed blocks keeps only thinking type",
			raw:  json.RawMessage(`{"content":[{"type":"text","text":"visible"},{"type":"thinking","text":"hidden"},{"type":"tool_use","text":"tool"}]}`),
			want: "hidden",
		},
		{
			name: "no thinking blocks returns empty",
			raw:  json.RawMessage(`{"content":[{"type":"text","text":"visible"}]}`),
			want: "",
		},
		{
			name: "thinking block with empty text skipped",
			raw:  json.RawMessage(`{"content":[{"type":"thinking","text":""},{"type":"thinking","text":"kept"}]}`),
			want: "kept",
		},
		{
			name: "empty content array",
			raw:  json.RawMessage(`{"content":[]}`),
			want: "",
		},
		{
			name: "json number ignored",
			raw:  json.RawMessage(`42`),
			want: "",
		},
		{
			name: "malformed json ignored",
			raw:  json.RawMessage(`{not json`),
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractChatThinking(tc.raw)
			if got != tc.want {
				t.Errorf("ExtractChatThinking(%s) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestExtractChatText_RoundTripsProtocolChatEvent ensures the helper
// still works when the Message field is produced by marshalling a
// real protocol.ChatEvent rather than hand-written JSON — guards
// against drift if the wire format ever sprouts new fields the
// helper should ignore.
func TestExtractChatText_RoundTripsProtocolChatEvent(t *testing.T) {
	finalMsg := map[string]any{
		"role": "assistant",
		"content": []map[string]any{
			{"type": "text", "text": "Round-tripped."},
		},
	}
	rawMsg, err := json.Marshal(finalMsg)
	if err != nil {
		t.Fatalf("marshal final message: %v", err)
	}
	ev := protocol.ChatEvent{
		RunID:      "run-x",
		SessionKey: "sess",
		State:      "final",
		Message:    rawMsg,
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal chat event: %v", err)
	}
	var decoded protocol.ChatEvent
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal chat event: %v", err)
	}
	if got := ExtractChatText(decoded.Message); got != "Round-tripped." {
		t.Errorf("ExtractChatText after round trip = %q, want %q", got, "Round-tripped.")
	}
}
