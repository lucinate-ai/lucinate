package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// newSelectionChat builds a chat model with rendered content ready for
// hit-testing: real setSize, messages rendered through updateViewport.
func newSelectionChat(t *testing.T, contents ...string) chatModel {
	t.Helper()
	m := newChatModel(newFakeBackend(), "session-key", "agent-id", "agent", "", config.DefaultPreferences(), false, "", "", false)
	m.setSize(80, 24)
	m.historyLoading = false
	for _, c := range contents {
		m.appendMessage(chatMessage{role: "system", content: c})
	}
	m.updateViewport()
	return m
}

// screenRowOf returns the 0-based screen row (as a mouse event Y) where the
// given content line is currently displayed: the inverse of hitTest.
func screenRowOf(m *chatModel, line int) int {
	return line + m.viewportTopPad - m.viewport.YOffset() + 1
}

func TestSelection_HitTest(t *testing.T) {
	m := newSelectionChat(t, "alpha", "bravo", "charlie")

	// A known content line maps back to itself through the formula.
	for want := 0; want < 3; want++ {
		y := screenRowOf(&m, want)
		p, ok := m.hitTest(0, y, true)
		if !ok || p.line != want {
			t.Fatalf("hitTest(0, %d, strict) = %+v ok=%v, want line %d", y, p, ok, want)
		}
	}

	// The header row (y=0) is rejected in strict mode.
	if _, ok := m.hitTest(0, 0, true); ok {
		t.Fatal("strict hitTest accepted the header row")
	}

	// A padding row above the content is rejected in strict mode (short
	// content is bottom-anchored, so early viewport rows are blank pad).
	if m.viewportTopPad < 2 {
		t.Fatalf("test setup expects blank pad rows, got pad=%d", m.viewportTopPad)
	}
	if _, ok := m.hitTest(0, 2, true); ok {
		t.Fatal("strict hitTest accepted a blank padding row")
	}

	// Non-strict clamps out-of-range rows into content bounds.
	if p, ok := m.hitTest(0, 0, false); !ok || p.line != 0 {
		t.Fatalf("non-strict top clamp = %+v ok=%v, want line 0", p, ok)
	}
	if p, ok := m.hitTest(0, 1+m.viewport.Height()+5, false); !ok || p.line != 2 {
		t.Fatalf("non-strict bottom clamp = %+v ok=%v, want line 2", p, ok)
	}

	// X clamps to the line's width.
	y := screenRowOf(&m, 0)
	w := ansi.StringWidth(m.selLines[0])
	if p, _ := m.hitTest(500, y, true); p.col != w-1 {
		t.Fatalf("x clamp = col %d, want %d", p.col, w-1)
	}

	// Empty content: no crash, no hit.
	var empty chatModel
	if _, ok := empty.hitTest(0, 1, true); ok {
		t.Fatal("hitTest on empty model reported a hit")
	}
}

func TestSelection_HitTest_ScrolledViewport(t *testing.T) {
	// Enough content to overflow the viewport so YOffset participates.
	var contents []string
	for i := 0; i < 40; i++ {
		contents = append(contents, strings.Repeat("x", 10))
	}
	m := newSelectionChat(t, contents...)
	if m.viewportTopPad != 0 {
		t.Fatalf("expected no top pad with overflowing content, got %d", m.viewportTopPad)
	}
	if m.viewport.YOffset() == 0 {
		t.Fatal("expected a scrolled viewport (auto-follow to bottom)")
	}
	// The first visible row (y=1) maps to the line at YOffset.
	p, ok := m.hitTest(0, 1, true)
	if !ok || p.line != m.viewport.YOffset() {
		t.Fatalf("hitTest(0,1) = %+v ok=%v, want line %d", p, ok, m.viewport.YOffset())
	}
}

func TestSelection_Extract(t *testing.T) {
	styled := userPrefixStyle.Render("You: ") + "hello world"
	lines := []string{styled, "second line here", "third"}

	// Single-line span, inclusive of the end cell, styling stripped.
	got := extractSelection(lines, selPoint{0, 5}, selPoint{0, 9})
	if got != "hello" {
		t.Fatalf("single-line extract = %q, want %q", got, "hello")
	}

	// Reversed anchor/head normalises.
	if got := extractSelection(lines, selPoint{0, 9}, selPoint{0, 5}); got != "hello" {
		t.Fatalf("reversed extract = %q, want %q", got, "hello")
	}

	// Multi-line: first-partial, whole middle, last-partial.
	got = extractSelection(lines, selPoint{0, 5}, selPoint{2, 2})
	want := "hello world\nsecond line here\nthi"
	if got != want {
		t.Fatalf("multi-line extract = %q, want %q", got, want)
	}

	// Trailing spaces are trimmed per line.
	padded := []string{"abc   ", "def"}
	if got := extractSelection(padded, selPoint{0, 0}, selPoint{1, 2}); got != "abc\ndef" {
		t.Fatalf("padded extract = %q, want %q", got, "abc\ndef")
	}

	// Wide runes at the boundary: selecting through an emoji keeps output
	// printable (ansi.Cut may substitute a space when splitting a wide cell;
	// the exact behaviour is documented by this assertion not crashing and
	// producing the full glyph when the span covers both its cells).
	wide := []string{"a🙂b"}
	if got := extractSelection(wide, selPoint{0, 0}, selPoint{0, 3}); got != "a🙂b" {
		t.Fatalf("wide-rune extract = %q, want %q", got, "a🙂b")
	}
}

func TestSelection_HighlightWidthInvariant(t *testing.T) {
	lines := []string{
		userPrefixStyle.Render("You: ") + "hello world",
		assistantPrefixStyle.Render("agent: ") + statusStyle.Render("styled body"),
		"plain",
	}
	widths := make([]int, len(lines))
	for i, l := range lines {
		widths[i] = ansi.StringWidth(l)
	}

	applySelectionHighlight(lines, selPoint{0, 2}, selPoint{2, 3})

	for i, l := range lines {
		if got := ansi.StringWidth(l); got != widths[i] {
			t.Errorf("line %d width changed %d → %d after highlight", i, widths[i], got)
		}
	}
	// The reverse-video SGR must be present on every intersecting line.
	for i, l := range lines {
		if !strings.Contains(l, "\x1b[7m") {
			t.Errorf("line %d missing reverse-video SGR: %q", i, l)
		}
	}
}

func TestSelection_InvalidatedOnLineCountChange(t *testing.T) {
	m := newSelectionChat(t, "alpha", "bravo")
	m.sel = selectionState{dragging: true, anchor: selPoint{0, 0}, head: selPoint{1, 2}}

	// A re-render with the same line count preserves the selection (spinner
	// restyles do exactly this every frame).
	m.updateViewport()
	if !m.sel.dragging {
		t.Fatal("selection dropped by a same-count re-render")
	}

	// Appending a message changes the rendered line count → selection drops.
	m.appendMessage(chatMessage{role: "system", content: "charlie"})
	m.updateViewport()
	if m.sel.dragging {
		t.Fatal("selection survived a line-count change; would highlight the wrong rows")
	}
}

func TestSelection_DragSelectCopyFlow(t *testing.T) {
	m := newSelectionChat(t, "alpha", "bravo", "charlie")

	y0 := screenRowOf(&m, 0)
	y2 := screenRowOf(&m, 2)

	// Press on line 0 starts a drag.
	m, _ = m.Update(tea.MouseClickMsg{X: 0, Y: y0, Button: tea.MouseLeft})
	if !m.sel.dragging {
		t.Fatal("left press on the transcript did not start a drag")
	}

	// Drag to line 2: the transcript viewport shows the reverse-video
	// highlight. (Scoped to the viewport — the composer cursor also uses
	// reverse video, so the full frame always contains the SGR.)
	m, _ = m.Update(tea.MouseMotionMsg{X: 4, Y: y2, Button: tea.MouseLeft})
	if m.sel.head.line != 2 {
		t.Fatalf("drag head line = %d, want 2", m.sel.head.line)
	}
	if !strings.Contains(m.viewport.View(), "\x1b[7m") {
		t.Fatal("no selection highlight in the viewport during drag")
	}

	// Release copies and clears.
	m, cmd := m.Update(tea.MouseReleaseMsg{X: 4, Y: y2, Button: tea.MouseLeft})
	if cmd == nil {
		t.Fatal("release of a non-empty selection returned no copy command")
	}
	if m.sel.dragging {
		t.Fatal("selection still active after release")
	}
	if strings.Contains(m.viewport.View(), "\x1b[7m") {
		t.Fatal("highlight still present in the viewport after release")
	}
	if len(m.notifications) == 0 {
		t.Fatal("no copy notification after release")
	}

	// A plain click (press+release, no movement) copies nothing.
	m, _ = m.Update(tea.MouseClickMsg{X: 0, Y: y0, Button: tea.MouseLeft})
	m, cmd = m.Update(tea.MouseReleaseMsg{X: 0, Y: y0, Button: tea.MouseLeft})
	if cmd != nil {
		t.Fatal("plain click produced a copy command")
	}

	// A press outside the transcript (header row) does not start a drag.
	m, _ = m.Update(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	if m.sel.dragging {
		t.Fatal("press on the header started a drag")
	}
}

func TestNewApp_MouseCaptureDefaultsOn(t *testing.T) {
	m := NewApp(newFakeBackend(), AppOptions{})
	if !m.mouseCapture {
		t.Fatal("NewApp must default mouseCapture on (wheel scroll + in-app selection)")
	}
	// And the view advertises cell-motion tracking.
	m.state = viewChat
	m.chatModel = chatModel{viewport: viewport.New(), hideInput: true}
	if got := m.View(); got.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("default MouseMode = %v, want MouseModeCellMotion", got.MouseMode)
	}
}
