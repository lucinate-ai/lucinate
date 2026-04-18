package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
)

func TestFormatCost(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{0.0, "$0.0000"},
		{0.005, "$0.0050"},
		{0.01, "$0.01"},
		{1.50, "$1.50"},
		{24.13, "$24.13"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatCost(tt.input)
			if got != tt.want {
				t.Errorf("formatCost(%f) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatStatsTable(t *testing.T) {
	m := &chatModel{
		stats: &sessionStats{
			inputTokens:       728,
			outputTokens:      70857,
			cacheRead:         28538316,
			cacheWrite:        3868238,
			totalCost:         24.13,
			inputCost:         0.002,
			outputCost:        1.06,
			cacheReadCost:     8.56,
			cacheWriteCost:    14.51,
			totalMessages:     100,
			userMessages:      45,
			assistantMessages: 55,
		},
	}

	table := m.formatStatsTable()

	for _, label := range []string{"Input", "Output", "Cache read", "Cache write", "Total", "User", "Assistant"} {
		if !strings.Contains(table, label) {
			t.Errorf("table should contain %q", label)
		}
	}
	if !strings.Contains(table, "28.5M") {
		t.Error("table should contain formatted cache read tokens")
	}
	if !strings.Contains(table, "24.13") {
		t.Error("table should contain total cost")
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{500, "500"},
		{999, "999"},
		{1000, "1.0K"},
		{1500, "1.5K"},
		{32478, "32.5K"},
		{999999, "1000.0K"},
		{1000000, "1.0M"},
		{32478139, "32.5M"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatTokens(tt.input)
			if got != tt.want {
				t.Errorf("formatTokens(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestWordWrap_ShortText(t *testing.T) {
	got := wordWrap("hello", 80)
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestWordWrap_ZeroWidth(t *testing.T) {
	got := wordWrap("hello world", 0)
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestWordWrap_NegativeWidth(t *testing.T) {
	got := wordWrap("hello world", -5)
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestWordWrap_WrapsLongLine(t *testing.T) {
	got := wordWrap("the quick brown fox jumps", 15)
	if got == "the quick brown fox jumps" {
		t.Error("expected text to be wrapped")
	}
	if !strings.Contains(got, "\n") {
		t.Error("expected at least one newline")
	}
}

func TestWordWrap_PreservesExistingNewlines(t *testing.T) {
	got := wordWrap("line one\nline two", 80)
	if got != "line one\nline two" {
		t.Errorf("got %q", got)
	}
}

func TestUpdateViewport_BottomAnchoring(t *testing.T) {
	m := &chatModel{
		viewport:  viewport.New(80, 20),
		width:     80,
		agentName: "test",
		messages: []chatMessage{
			{role: "user", content: "hi"},
			{role: "assistant", content: "hello"},
		},
	}

	m.updateViewport()

	if len(m.viewport.View()) == 0 {
		t.Error("viewport content should not be empty")
	}
}
