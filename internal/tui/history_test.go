package tui

import (
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
	if msgs[2].role != "assistant" || msgs[2].errMsg != "older run blew up" {
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
