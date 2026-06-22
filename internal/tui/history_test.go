package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/a3tai/openclaw-go/protocol"
)

func TestBuildCronTranscriptMessages_ChronologicalAndCoversBothOutcomes(t *testing.T) {
	older := int64(1_000)
	newer := int64(2_000)
	// Run logs arrive newest-first.
	runs := []protocol.CronRunLogEntry{
		{RunAtMs: &newer, Status: "ok", Summary: "Newer run output."},
		{RunAtMs: &older, Status: "error", Error: "older run blew up"},
	}

	msgs := buildCronTranscriptMessages("Check the balance.", runs, nil)

	// Expect oldest first: separator, user payload, assistant error,
	// separator, user payload, assistant summary.
	if len(msgs) != 6 {
		t.Fatalf("expected 6 messages (2 runs × 3), got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].role != "separator" || msgs[0].timestampMs != older {
		t.Errorf("msgs[0] should be older separator; got %+v", msgs[0])
	}
	if msgs[1].role != "user" || msgs[1].content != "Check the balance." {
		t.Errorf("msgs[1] should be user payload for older run; got %+v", msgs[1])
	}
	if msgs[2].role != "assistant" || msgs[2].errMsg != "Run error: older run blew up" {
		t.Errorf("msgs[2] should be assistant error for older run; got %+v", msgs[2])
	}
	if msgs[3].role != "separator" || msgs[3].timestampMs != newer {
		t.Errorf("msgs[3] should be newer separator; got %+v", msgs[3])
	}
	if msgs[5].role != "assistant" || msgs[5].content != "Newer run output." {
		t.Errorf("msgs[5] should be assistant summary for newer run; got %+v", msgs[5])
	}
}

func TestBuildCronTranscriptMessages_EmptyRunsReturnsNil(t *testing.T) {
	if msgs := buildCronTranscriptMessages("payload", nil, nil); msgs != nil {
		t.Errorf("expected nil for empty run log; got %+v", msgs)
	}
}

func TestBuildCronTranscriptMessages_DeliveryErrorAfterSummary(t *testing.T) {
	// Real-world case: the agent ran fine and produced a summary, but
	// the gateway couldn't route the announcement so the run was logged
	// status=error with the actual work intact in Summary. The
	// transcript must show both — the summary and the delivery failure.
	ts := int64(1_000)
	runs := []protocol.CronRunLogEntry{
		{
			RunAtMs:       &ts,
			Status:        "error",
			Summary:       "Total Balance: $9.12",
			DeliveryError: "Delivering to Telegram requires target",
		},
	}

	msgs := buildCronTranscriptMessages("Check balance.", runs, nil)

	if len(msgs) != 4 {
		t.Fatalf("expected separator+user+assistant+system note, got %d: %+v", len(msgs), msgs)
	}
	if msgs[2].role != "assistant" || msgs[2].content != "Total Balance: $9.12" {
		t.Errorf("msgs[2] should carry the assistant summary; got %+v", msgs[2])
	}
	if msgs[3].role != "system" || !strings.Contains(msgs[3].errMsg, "Delivery error: Delivering to Telegram requires target") {
		t.Errorf("msgs[3] should be a system note carrying the delivery error; got %+v", msgs[3])
	}
}

func TestBuildCronTranscriptMessages_RunErrorOnlyStaysOnAssistantTurn(t *testing.T) {
	ts := int64(1_000)
	runs := []protocol.CronRunLogEntry{
		{RunAtMs: &ts, Status: "error", Error: "boom"},
	}

	msgs := buildCronTranscriptMessages("Check.", runs, nil)

	if len(msgs) != 3 {
		t.Fatalf("expected separator+user+assistant, got %d: %+v", len(msgs), msgs)
	}
	if msgs[2].role != "assistant" || msgs[2].errMsg != "Run error: boom" {
		t.Errorf("msgs[2] should carry the run error as assistant errMsg; got %+v", msgs[2])
	}
}

func TestBuildCronTranscriptMessages_DedupesIdenticalRunAndDeliveryError(t *testing.T) {
	ts := int64(1_000)
	runs := []protocol.CronRunLogEntry{
		{RunAtMs: &ts, Status: "error", Error: "boom", DeliveryError: "boom"},
	}

	msgs := buildCronTranscriptMessages("Check.", runs, nil)

	// One assistant turn carrying the dedup'd note — not two of the same line.
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(msgs), msgs)
	}
	if got := msgs[2].errMsg; got != "Run error: boom" {
		t.Errorf("expected dedup'd 'Run error: boom'; got %q", got)
	}
}

// TestHistoryMessageUnmarshalJSON pins the content-field normalisation
// against the two Anthropic wire shapes a history turn may carry — a
// plain string (the short form) and an array of typed blocks — plus the
// empty/null/unknown fallbacks that must never abort the decode. The
// string case is the direct regression guard for the gateway sending
// content as a bare string, which previously failed the whole history
// load with "cannot unmarshal string into ... []tui.chatContentBlock".
func TestHistoryMessageUnmarshalJSON(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		wantRole  string
		wantTs    int64
		wantBlock []chatContentBlock
		wantErr   bool
	}{
		{
			name:      "string content wrapped in text block",
			raw:       `{"role":"user","content":"hello there"}`,
			wantRole:  "user",
			wantBlock: []chatContentBlock{{Type: "text", Text: "hello there"}},
		},
		{
			name:      "string content preserves timestamp",
			raw:       `{"role":"assistant","content":"hi","timestamp":1700000000000}`,
			wantRole:  "assistant",
			wantTs:    1700000000000,
			wantBlock: []chatContentBlock{{Type: "text", Text: "hi"}},
		},
		{
			name:     "empty string content yields no blocks",
			raw:      `{"role":"user","content":""}`,
			wantRole: "user",
		},
		{
			name:     "array content preserved",
			raw:      `{"role":"assistant","content":[{"type":"thinking","text":"hmm"},{"type":"text","text":"answer"}]}`,
			wantRole: "assistant",
			wantBlock: []chatContentBlock{
				{Type: "thinking", Text: "hmm"},
				{Type: "text", Text: "answer"},
			},
		},
		{
			name:     "empty array content",
			raw:      `{"role":"user","content":[]}`,
			wantRole: "user",
		},
		{
			name:     "missing content key",
			raw:      `{"role":"user"}`,
			wantRole: "user",
		},
		{
			name:     "null content",
			raw:      `{"role":"user","content":null}`,
			wantRole: "user",
		},
		{
			name:     "unknown content shape left empty not error",
			raw:      `{"role":"user","content":42}`,
			wantRole: "user",
		},
		{
			name:    "malformed json errors",
			raw:     `{not json`,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hm historyMessage
			err := json.Unmarshal([]byte(tc.raw), &hm)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if hm.Role != tc.wantRole {
				t.Errorf("role = %q, want %q", hm.Role, tc.wantRole)
			}
			if hm.Timestamp != tc.wantTs {
				t.Errorf("timestamp = %d, want %d", hm.Timestamp, tc.wantTs)
			}
			if len(hm.Content) != len(tc.wantBlock) {
				t.Fatalf("content = %+v, want %+v", hm.Content, tc.wantBlock)
			}
			for i, b := range hm.Content {
				if b != tc.wantBlock[i] {
					t.Errorf("content[%d] = %+v, want %+v", i, b, tc.wantBlock[i])
				}
			}
		})
	}
}

// TestFetchHistory_StringContentTurn reproduces the reported bug: a
// gateway history payload whose turns carry content as a bare string
// (rather than a block array) must load cleanly instead of failing the
// whole conversation with an unmarshal error.
func TestFetchHistory_StringContentTurn(t *testing.T) {
	payload := `{"messages":[` +
		`{"role":"user","content":"turn the lights on"},` +
		`{"role":"assistant","content":"Done — lights are on."}` +
		`]}`
	fb := &fakeBackend{
		chatHistoryHook: func(ctx context.Context, sessionKey string, limit int) (json.RawMessage, error) {
			return json.RawMessage(payload), nil
		},
	}

	msgs, err := fetchHistory(fb, "sess", nil, 50)
	if err != nil {
		t.Fatalf("fetchHistory returned error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].role != "user" || msgs[0].content != "turn the lights on" {
		t.Errorf("msgs[0] = %+v, want user 'turn the lights on'", msgs[0])
	}
	if msgs[1].role != "assistant" || msgs[1].content != "Done — lights are on." {
		t.Errorf("msgs[1] = %+v, want assistant 'Done — lights are on.'", msgs[1])
	}
}

// TestFetchHistory_MixedStringAndBlockTurns guards the realistic case
// where a single conversation interleaves string-content and
// block-array turns — a string turn must not break decoding of the
// block turns around it.
func TestFetchHistory_MixedStringAndBlockTurns(t *testing.T) {
	payload := `{"messages":[` +
		`{"role":"user","content":"first"},` +
		`{"role":"assistant","content":[{"type":"thinking","text":"reasoning"},{"type":"text","text":"second"}]},` +
		`{"role":"user","content":"third"}` +
		`]}`
	fb := &fakeBackend{
		chatHistoryHook: func(ctx context.Context, sessionKey string, limit int) (json.RawMessage, error) {
			return json.RawMessage(payload), nil
		},
	}

	msgs, err := fetchHistory(fb, "sess", nil, 50)
	if err != nil {
		t.Fatalf("fetchHistory returned error: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[1].content != "second" {
		t.Errorf("msgs[1].content = %q, want %q", msgs[1].content, "second")
	}
	if msgs[1].thinking != "reasoning" {
		t.Errorf("msgs[1].thinking = %q, want %q", msgs[1].thinking, "reasoning")
	}
}

func TestLooksLikeMarkdown(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "plain text", text: "pong 🦞", want: false},
		{name: "plain multiline", text: "hello\nthere", want: false},
		{name: "heading", text: "# Title", want: true},
		{name: "bullet", text: "- item", want: true},
		{name: "numbered list", text: "1. first", want: true},
		{name: "blockquote", text: "> quote", want: true},
		{name: "table", text: "| a | b |", want: true},
		{name: "inline code", text: "use `rg`", want: true},
		{name: "bold", text: "**important**", want: true},
		{name: "fence", text: "```go\nfmt.Println()\n```", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeMarkdown(tt.text); got != tt.want {
				t.Errorf("looksLikeMarkdown(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestStripSystemLines_OnlySystemLines(t *testing.T) {
	input := "System: [2026-04-18] Node connected\nSystem: [2026-04-18] reason launch"
	got := stripSystemLines(input)
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestStripSystemLines_MixedContent(t *testing.T) {
	input := "System: [2026-04-18] Node connected\n\n[Sat 2026-04-18 20:27] hello there"
	got := stripSystemLines(input)
	want := "[Sat 2026-04-18 20:27] hello there"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripSystemLines_NoSystemLines(t *testing.T) {
	input := "just a normal message"
	got := stripSystemLines(input)
	if got != input {
		t.Errorf("got %q, want %q", got, input)
	}
}

func TestStripSystemLines_EmptyInput(t *testing.T) {
	got := stripSystemLines("")
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestStripSystemLines_IndentedSystemLine(t *testing.T) {
	input := "  System: indented system line\nuser text"
	got := stripSystemLines(input)
	if got != "user text" {
		t.Errorf("got %q, want %q", got, "user text")
	}
}

func TestStripSystemLines_UntrustedPrefix(t *testing.T) {
	input := "System (untrusted): Available agent skills\nSystem (untrusted):   - review: Code review\nping"
	got := stripSystemLines(input)
	if got != "ping" {
		t.Errorf("got %q, want %q", got, "ping")
	}
}

func TestStripSystemLines_MixedPrefixes(t *testing.T) {
	input := "System: line one\nSystem (untrusted): line two\nhello"
	got := stripSystemLines(input)
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestStripLocalAgentSkillBlocks(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "no envelope",
			in:   "just plain text",
			want: "just plain text",
		},
		{
			name: "single block",
			in: "Please use the following skill:\n\n" +
				"<local-agent-skill name=\"foo\">\nbody\n</local-agent-skill>\n\n" +
				"use the \"foo\" skill above on x",
			want: "use the \"foo\" skill above on x",
		},
		{
			name: "multi-line body",
			in: "Please use the following skill:\n\n" +
				"<local-agent-skill name=\"foo\">\nline1\nline2\nline3\n</local-agent-skill>\n\n" +
				"trailing prose",
			want: "trailing prose",
		},
		{
			name: "two blocks",
			in: "Please use the following skills:\n\n" +
				"<local-agent-skill name=\"foo\">\nfoo body\n</local-agent-skill>\n\n" +
				"<local-agent-skill name=\"bar\">\nbar body\n</local-agent-skill>\n\n" +
				"both above",
			want: "both above",
		},
		{
			name: "empty input",
			in:   "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripLocalAgentSkillBlocks(tt.in); got != tt.want {
				t.Errorf("stripLocalAgentSkillBlocks() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStripInternalContextBlocks(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "no envelope",
			in:   "just plain text",
			want: "just plain text",
		},
		{
			name: "block then user text",
			in: "<<<BEGIN_OPENCLAW_INTERNAL_CONTEXT>>>\n" +
				"injected gateway context\n" +
				"<<<END_OPENCLAW_INTERNAL_CONTEXT>>>\n" +
				"what's the weather?",
			want: "what's the weather?",
		},
		{
			name: "user text then block",
			in: "do the thing\n" +
				"<<<BEGIN_OPENCLAW_INTERNAL_CONTEXT>>>ctx<<<END_OPENCLAW_INTERNAL_CONTEXT>>>",
			want: "do the thing",
		},
		{
			name: "only the envelope",
			in: "<<<BEGIN_OPENCLAW_INTERNAL_CONTEXT>>>\n" +
				"nothing but plumbing\n" +
				"<<<END_OPENCLAW_INTERNAL_CONTEXT>>>",
			want: "",
		},
		{
			name: "two blocks around text",
			in: "<<<BEGIN_OPENCLAW_INTERNAL_CONTEXT>>>a<<<END_OPENCLAW_INTERNAL_CONTEXT>>>" +
				"real message" +
				"<<<BEGIN_OPENCLAW_INTERNAL_CONTEXT>>>b<<<END_OPENCLAW_INTERNAL_CONTEXT>>>",
			want: "real message",
		},
		{
			name: "unterminated block strips to end",
			in: "hello\n<<<BEGIN_OPENCLAW_INTERNAL_CONTEXT>>>\n" +
				"truncated context with no end marker",
			want: "hello",
		},
		{
			name: "empty input",
			in:   "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripInternalContextBlocks(tt.in); got != tt.want {
				t.Errorf("stripInternalContextBlocks() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsSystemLine(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"System: hello", true},
		{"System (untrusted): hello", true},
		{"System (trusted): hello", true},
		{"System (foo): bar", true},
		{"SystemError: oops", false},
		{"System hello", false},
		{"not a system line", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			if got := isSystemLine(tt.line); got != tt.want {
				t.Errorf("isSystemLine(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}
