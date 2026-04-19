package tui

import "testing"

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
