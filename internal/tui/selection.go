package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
)

// In-app mouse text selection for the chat transcript.
//
// Mouse capture (SGR cell-motion tracking) is on by default, which means the
// terminal no longer performs native click-drag selection over the chat —
// instead the wheel scrolls the viewport and these handlers implement
// selection: press anchors, drag extends (with a reverse-video highlight),
// release copies the selected text to the clipboard. /mouse off restores the
// terminal's native selection for anyone who prefers it.
//
// Positions are stored in content coordinates (line index into the rendered
// transcript, display-cell column), not screen coordinates, so an in-progress
// selection survives viewport scrolling — including wheel scrolling mid-drag.

// selPoint is a position in the rendered transcript: line indexes
// chatModel.selLines (the unpadded rendered content lines); col is a
// display-cell column within that line.
type selPoint struct {
	line int
	col  int
}

// selectionState tracks an in-progress drag selection.
type selectionState struct {
	dragging bool     // left button held; the highlight renders while true
	anchor   selPoint // where the press landed
	head     selPoint // latest drag position
}

// hitTest maps a 0-based screen cell (x, y) to a content position. Screen row
// 0 is the header; the viewport occupies rows 1..Height() at column 0, and
// updateViewport top-pads short content with viewportTopPad blank rows, so:
//
//	contentLine = viewport.YOffset() + (y − 1) − viewportTopPad
//
// strict=true returns ok=false outside the transcript (header, pad rows,
// chrome below the viewport) — used for the initial click so presses on the
// input area don't start a selection; strict=false clamps into content bounds
// — used for the drag head so dragging past an edge selects to that edge.
func (m *chatModel) hitTest(x, y int, strict bool) (selPoint, bool) {
	if len(m.selLines) == 0 {
		return selPoint{}, false
	}
	row := y - 1 // row 0 is the header
	if strict && (row < 0 || row >= m.viewport.Height()) {
		return selPoint{}, false
	}
	line := m.viewport.YOffset() + row - m.viewportTopPad
	if strict && (line < 0 || line >= len(m.selLines)) {
		return selPoint{}, false
	}
	line = clampInt(line, 0, len(m.selLines)-1)
	w := ansi.StringWidth(m.selLines[line])
	col := clampInt(x, 0, max(0, w-1)) // empty line → col 0
	return selPoint{line: line, col: col}, true
}

// handleMouseClick starts a drag selection on a left press inside the
// transcript, and clears any previous selection on every left press.
func (m *chatModel) handleMouseClick(msg tea.MouseClickMsg) {
	mo := msg.Mouse()
	if mo.Button != tea.MouseLeft {
		return
	}
	had := m.sel.dragging
	m.sel = selectionState{}
	if p, ok := m.hitTest(mo.X, mo.Y, true); ok {
		m.sel = selectionState{dragging: true, anchor: p, head: p}
	}
	if had || m.sel.dragging {
		m.updateViewport()
	}
}

// handleMouseMotion extends the drag selection. Dragging onto the header row
// or below the transcript auto-scrolls the viewport one line per motion event
// so a drag can reach content beyond the visible region.
func (m *chatModel) handleMouseMotion(msg tea.MouseMotionMsg) {
	if !m.sel.dragging {
		return
	}
	mo := msg.Mouse()
	if mo.Button != tea.MouseLeft {
		return
	}
	if mo.Y <= 0 {
		m.viewport.SetYOffset(m.viewport.YOffset() - 1)
	} else if mo.Y >= 1+m.viewport.Height() {
		m.viewport.SetYOffset(m.viewport.YOffset() + 1)
	}
	if p, ok := m.hitTest(mo.X, mo.Y, false); ok {
		m.sel.head = p
	}
	m.updateViewport()
}

// handleMouseRelease finalises the drag: a non-empty selection is extracted
// as plain text and copied to the clipboard, then the highlight clears.
// MouseNone is accepted because some terminals report SGR release without
// echoing the pressed button.
func (m *chatModel) handleMouseRelease(msg tea.MouseReleaseMsg) tea.Cmd {
	if !m.sel.dragging {
		return nil
	}
	mo := msg.Mouse()
	if mo.Button != tea.MouseLeft && mo.Button != tea.MouseNone {
		return nil
	}
	a, b := m.sel.anchor, m.sel.head
	m.sel = selectionState{}
	var cmd tea.Cmd
	if a != b {
		if text := extractSelection(m.selLines, a, b); text != "" {
			cmd = copySelectionCmd(text)
			m.notify(copyNotice(text))
		}
	}
	m.updateViewport()
	return cmd
}

// normalizeSel orders a before b (by line, then col).
func normalizeSel(a, b selPoint) (selPoint, selPoint) {
	if a.line > b.line || (a.line == b.line && a.col > b.col) {
		return b, a
	}
	return a, b
}

// extractSelection returns the plain text between a and b, inclusive of the
// cell under b (ansi.Cut is half-open, hence the +1 on the end column).
// Trailing spaces are trimmed per line so padded layout gaps don't end up on
// the clipboard.
func extractSelection(lines []string, a, b selPoint) string {
	if len(lines) == 0 {
		return ""
	}
	a, b = normalizeSel(a, b)
	a.line = clampInt(a.line, 0, len(lines)-1)
	b.line = clampInt(b.line, 0, len(lines)-1)

	cut := func(line string, from, to int) string {
		w := ansi.StringWidth(line)
		from = clampInt(from, 0, w)
		to = clampInt(to, from, w)
		return strings.TrimRight(ansi.Strip(ansi.Cut(line, from, to)), " ")
	}

	if a.line == b.line {
		return cut(lines[a.line], a.col, b.col+1)
	}
	out := make([]string, 0, b.line-a.line+1)
	out = append(out, cut(lines[a.line], a.col, ansi.StringWidth(lines[a.line])))
	for i := a.line + 1; i < b.line; i++ {
		out = append(out, strings.TrimRight(ansi.Strip(lines[i]), " "))
	}
	out = append(out, cut(lines[b.line], 0, b.col+1))
	return strings.Join(out, "\n")
}

// applySelectionHighlight rewrites the lines intersecting the selection,
// restyling the selected span with selectionStyle over ANSI-stripped text —
// the selection overrides inner styling, like native terminal selection.
// Width-invariant: stripping removes only zero-width escape sequences and the
// restyle adds only SGR sequences, so row alignment is unaffected.
func applySelectionHighlight(lines []string, a, b selPoint) {
	a, b = normalizeSel(a, b)
	for i := max(a.line, 0); i <= b.line && i < len(lines); i++ {
		w := ansi.StringWidth(lines[i])
		x0, x1 := 0, w
		if i == a.line {
			x0 = clampInt(a.col, 0, w)
		}
		if i == b.line {
			x1 = clampInt(b.col+1, 0, w)
		}
		if x0 >= x1 {
			// A zero-width span on an empty (or fully-left) line: still mark
			// whole middle lines of a multi-line selection visibly? No — an
			// empty line has nothing to invert; skip it.
			continue
		}
		pre := ansi.Cut(lines[i], 0, x0)
		mid := selectionStyle.Render(ansi.Strip(ansi.Cut(lines[i], x0, x1)))
		post := ansi.Cut(lines[i], x1, w)
		lines[i] = pre + mid + post
	}
}

// copySelectionCmd writes text to the clipboard two ways: OSC 52 through the
// renderer (reaches the hosting terminal — a native platform's terminal view,
// tmux with set-clipboard, iTerm2) and the local OS clipboard (covers
// terminals that ignore OSC 52, e.g. Terminal.app for local sessions). OS
// clipboard errors are ignored — OSC 52 is the fallback.
func copySelectionCmd(text string) tea.Cmd {
	return tea.Batch(
		tea.SetClipboard(text),
		func() tea.Msg {
			_ = clipboard.WriteAll(text)
			return nil
		},
	)
}

// copyNotice renders the ephemeral confirmation row for a completed copy.
func copyNotice(text string) string {
	chars := len([]rune(text))
	if n := strings.Count(text, "\n") + 1; n > 1 {
		return fmt.Sprintf("Copied %d lines (%d chars) to clipboard.", n, chars)
	}
	return fmt.Sprintf("Copied %d chars to clipboard.", chars)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
