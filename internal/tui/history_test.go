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
