package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// newInlineChat builds a chat model with the inline scrollback prototype forced
// on (bypassing the env gate) and a usable size.
func newInlineChat(t *testing.T) chatModel {
	t.Helper()
	m := newChatModel(newFakeBackend(), "session-key", "agent-id", "agent", "", config.DefaultPreferences(), false, "", "", false)
	m.inline = true
	m.setSize(80, 24)
	return m
}

func TestFitAbove_PadsToFloorAndClipsToMax(t *testing.T) {
	// Pads the top up to the floor, seating content at the bottom.
	got := fitAbove("a\nb", 5, 100)
	lines := strings.Split(got, "\n")
	if len(lines) != 5 || lines[3] != "a" || lines[4] != "b" {
		t.Fatalf("floor pad wrong: %q", got)
	}
	// Clips the oldest rows past maxRows, keeping the newest.
	if got := fitAbove("a\nb\nc\nd", 0, 2); got != "c\nd" {
		t.Fatalf("clip kept %q, want %q", got, "c\nd")
	}
	// Empty with a floor produces that many blank rows (holds the input down).
	if got := fitAbove("", 3, 10); got != "\n\n" {
		t.Fatalf("empty+floor produced %q, want two newlines (3 rows)", got)
	}
	// Empty with no floor stays empty (idle: no reserved space).
	if got := fitAbove("", 0, 10); got != "" {
		t.Fatalf("empty+no-floor produced %q, want empty", got)
	}
}

func TestInline_LoadSpillsHistoryLeavingSmallLiveRegion(t *testing.T) {
	m := newInlineChat(t)
	m, cmd := m.Update(historyLoadedMsg{messages: []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "hi there"},
	}})

	// The loaded history is spilled straight to scrollback, so the live region
	// is empty — the input sits at the bottom of the flow with history above.
	if m.committedCount != 2 {
		t.Fatalf("committedCount = %d, want 2 (history spilled)", m.committedCount)
	}
	if tail := m.renderLiveTail(); tail != "" {
		t.Fatalf("renderLiveTail = %q, want empty after load", tail)
	}
	if cmd == nil {
		t.Fatal("expected a scrollback print command from history load")
	}
}

func TestInline_FinishedTurnsStayLive(t *testing.T) {
	m := newInlineChat(t)
	m, _ = m.Update(historyLoadedMsg{messages: []chatMessage{
		{role: "user", content: "u0"},
		{role: "assistant", content: "a0"},
	}})
	if m.committedCount != 2 {
		t.Fatalf("after load committedCount = %d, want 2", m.committedCount)
	}

	// A fresh turn stays in the live region.
	m.appendMessage(chatMessage{role: "user", content: "u1"})
	m.appendMessage(chatMessage{role: "assistant", content: "a1"})

	// After the post-turn refresh, the finished turn must NOT be spilled to
	// scrollback — it stays live so the frame doesn't shrink and jump the input.
	server := []chatMessage{
		{role: "user", content: "u0"},
		{role: "assistant", content: "a0"},
		{role: "user", content: "u1"},
		{role: "assistant", content: "a1"},
	}
	m, _ = m.Update(historyRefreshMsg{messages: server, boundary: 1})
	if m.committedCount != 2 {
		t.Fatalf("after refresh committedCount = %d, want 2 (finished turn stays live, not spilled)", m.committedCount)
	}
	if tail := m.renderLiveTail(); !strings.Contains(tail, "u1") || !strings.Contains(tail, "a1") {
		t.Fatalf("finished turn left the live region: %q", tail)
	}

	// reflowInline only spills once the tail overflows the screen. On a tall
	// terminal these few rows fit, so nothing spills.
	if cmd := m.reflowInline(); cmd != nil {
		t.Fatal("reflowInline spilled a tail that fits the screen")
	}

	// Shrink the screen so the tail overflows: now the oldest spills, newest stays.
	m.setSize(80, 6)
	if cmd := m.reflowInline(); cmd == nil {
		t.Fatal("reflowInline did not spill an overflowing tail")
	}
	if m.committedCount <= 2 {
		t.Fatalf("reflowInline advanced committedCount to %d, want > 2 (oldest spilled)", m.committedCount)
	}
	if tail := m.renderLiveTail(); !strings.Contains(tail, "a1") {
		t.Fatal("reflowInline spilled the newest message (should always keep it live)")
	}
}

// TestInline_ViewNeverExceedsTerminalBounds is the regression for the ghosting
// bug: in a narrow terminal the full-width status bar (and other chrome) would
// wrap, making the real frame taller than the terminal so the inline renderer
// couldn't clear the previous frame. The view must be at most `height` rows and
// no line wider than `width`, regardless of header length or menu state.
func TestInline_ViewNeverExceedsTerminalBounds(t *testing.T) {
	m := newChatModel(newFakeBackend(), "s", "agent-id",
		"a-really-long-agent-name", "provider/some-long-model-id-v4-flash",
		config.DefaultPreferences(), false, "a-long-connection-name", "", false)
	m.inline = true
	m.setSize(60, 20)
	m.stats = &sessionStats{totalCost: 12.34, inputTokens: 1000, outputTokens: 2000}

	// An over-tall in-flight turn plus an open completion menu — the worst case.
	m.appendMessage(chatMessage{role: "assistant", content: strings.Repeat("a long streaming line of tokens\n", 60)})
	m.textarea.SetValue("/age")
	m.refreshCompletionMenu()

	view := m.inlineView()
	lines := strings.Split(view, "\n")
	if len(lines) > 20 {
		t.Fatalf("inline view is %d rows, exceeds terminal height 20", len(lines))
	}
	for i, ln := range lines {
		if w := ansi.StringWidth(ln); w > 60 {
			t.Fatalf("line %d width %d exceeds terminal width 60 (would wrap and ghost): %q", i, w, ln)
		}
	}
	// The status bar is the very last row.
	if last := lines[len(lines)-1]; !strings.Contains(last, "lucinate") {
		t.Fatalf("status bar is not the last row: %q", last)
	}
}

// TestInline_AboveInputFloorHoldsOnShrink verifies the input doesn't jump up
// when the content above it shrinks (a dropped placeholder, an ephemeral tool
// card, or the completion menu filtering down): the frame height is held at its
// high-water mark, and resets only once the block empties.
func TestInline_AboveInputFloorHoldsOnShrink(t *testing.T) {
	m := newInlineChat(t)
	m.historyLoading = false // load has completed by the time a turn streams
	m.committedCount = 0

	m.messages = []chatMessage{
		{role: "user", content: "do a thing"},
		{role: "assistant", content: "working"},
		{role: "tool", toolName: "read", toolState: "running"},
		{role: "tool", toolName: "write", toolState: "running"},
	}
	m.updateInlineFloor()
	grown := lipgloss.Height(m.inlineView())

	// A row vanishes — the frame must not get shorter.
	m.messages = m.messages[:2]
	m.updateInlineFloor()
	if held := lipgloss.Height(m.inlineView()); held != grown {
		t.Fatalf("frame shrank %d→%d on row removal; input would jump up", grown, held)
	}

	// The floor resets once the block empties.
	m.messages = nil
	m.updateInlineFloor()
	if m.turnTailFloor != 0 {
		t.Fatalf("turnTailFloor = %d after block emptied, want 0", m.turnTailFloor)
	}
}

func TestInline_ChatViewLeavesAltScreen(t *testing.T) {
	fake := newFakeBackend()

	m := AppModel{backend: fake, width: 120, height: 40}
	m.chatModel = newChatModel(fake, "s", "a", "agent", "", config.DefaultPreferences(), false, "", "", false)
	m.chatModel.inline = true
	m.chatModel.setSize(120, 40)
	m.state = viewChat
	if v := m.View(); v.AltScreen {
		t.Error("inline chat view must not use the alternate screen")
	}

	m.state = viewConnections
	if v := m.View(); !v.AltScreen {
		t.Error("modal views must keep the alternate screen")
	}

	m.chatModel.inline = false
	m.state = viewChat
	if v := m.View(); !v.AltScreen {
		t.Error("non-inline chat must keep the alternate screen")
	}
}

func TestInline_Disabled_NoScrollbackAndKeepsSeparator(t *testing.T) {
	m := newChatModel(newFakeBackend(), "session-key", "agent-id", "agent", "", config.DefaultPreferences(), false, "", "", false)
	m.setSize(80, 24)
	m, cmd := m.Update(historyLoadedMsg{messages: []chatMessage{
		{role: "user", content: "u0"},
		{role: "assistant", content: "a0"},
	}})
	// Non-inline keeps the client separator row in the transcript.
	if got := len(m.messages); got != 3 {
		t.Fatalf("non-inline messages = %d, want 3 (2 rows + separator)", got)
	}
	if cmd != nil {
		t.Fatal("non-inline mode must not print to scrollback")
	}
	if m.committedCount != 0 {
		t.Fatalf("non-inline committedCount = %d, want 0", m.committedCount)
	}
}
