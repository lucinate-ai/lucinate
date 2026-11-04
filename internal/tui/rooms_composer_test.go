package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/rooms"
)

// composerBoxRows extracts the stripped interior of the composer box: one
// entry per rendered content row, with the border, padding and gutter
// removed (the first row keeps its "you › " prompt).
func composerBoxRows(t *testing.T, m roomsModel) []string {
	t.Helper()
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	top := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "╭") {
			top = i
			break
		}
	}
	if top < 0 {
		t.Fatalf("no composer box in the view:\n%s", strings.Join(lines, "\n"))
	}
	var rows []string
	for i := top + 1; i < len(lines); i++ {
		l := lines[i]
		if strings.HasPrefix(l, "╰") {
			break
		}
		l = strings.TrimPrefix(l, "│ ")
		l = strings.TrimSuffix(l, " │")
		rows = append(rows, strings.TrimRight(l, " "))
	}
	return rows
}

// A long single-line draft must be wrapped and drawn, not clipped: the whole
// text is on screen, across more than one visual row.
func TestRoomsComposer_LongDraftWrapsAndIsFullyVisible(t *testing.T) {
	m := transcriptWith(0)
	m.width, m.height = 80, 24
	alphabet := "abcdefghijklmnopqrstuvwxyz"
	m.composer = strings.Repeat(alphabet, 300/len(alphabet)+1)[:300]

	rows := m.composerVisualRows(m.width)
	if len(rows) < 2 {
		t.Fatalf("a 300-char draft at width 80 wrapped into %d row(s), want at least 2", len(rows))
	}
	if got := strings.Join(rows, ""); got != m.composer {
		t.Fatalf("wrapping lost text: %d chars back, want %d", len(got), len(m.composer))
	}

	box := composerBoxRows(t, m)
	if len(box) < 2 {
		t.Fatalf("the composer box rendered %d content row(s), want at least 2", len(box))
	}
	var drawn strings.Builder
	for i, row := range box {
		if i == 0 {
			row = strings.TrimPrefix(row, composerPrompt)
		}
		row = strings.TrimLeft(row, " ")
		drawn.WriteString(row)
	}
	if drawn.String() != m.composer {
		t.Fatalf("the stripped view dropped part of the draft: got %d chars, want %d",
			drawn.Len(), len(m.composer))
	}
}

// A pasted ten-line draft is bottom-anchored: six rows of text and one dim
// marker row account for the four that scrolled out, and the tail stays.
func TestRoomsComposer_TenLineDraftShowsSixRowsAndAnOverflowMarker(t *testing.T) {
	m := transcriptWith(0)
	m.width, m.height = 80, 24
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = fmt.Sprintf("linia%d", i+1)
	}
	m.composer = strings.Join(lines, "\n")

	box := composerBoxRows(t, m)
	if len(box) != 7 {
		t.Fatalf("composer box has %d content rows, want 7 (6 text + marker):\n%s",
			len(box), strings.Join(box, "\n"))
	}
	if !strings.Contains(box[0], "↑ 4 earlier") {
		t.Fatalf("top row = %q, want the overflow marker", box[0])
	}
	if !strings.Contains(box[0], "[10 lines") {
		t.Fatalf("top row = %q, want the pasted document's size summary", box[0])
	}
	for i, want := range []string{"linia5", "linia6", "linia7", "linia8", "linia9", "linia10"} {
		if !strings.Contains(box[i+1], want) {
			t.Fatalf("row %d = %q, want %s", i+1, box[i+1], want)
		}
	}
}

// The transcript budget must follow the composer's real height: a growing
// draft takes rows from the transcript, and the transcript keeps at least 3.
func TestRoomsComposer_TranscriptBudgetFollowsTheBoxHeight(t *testing.T) {
	m := transcriptWith(0)
	m.width, m.height = 80, 24
	base := m.transcriptRows()

	lines := make([]string, 10)
	for i := range lines {
		lines[i] = fmt.Sprintf("linia%d", i+1)
	}
	m.composer = strings.Join(lines, "\n")
	grown := m.transcriptRows()
	h := m.composerHeight(m.width)

	if want := base - (h - 3); grown != want {
		t.Fatalf("transcriptRows = %d after the draft grew, want %d (base %d, composerHeight %d)",
			grown, want, base, h)
	}
	if grown < 3 {
		t.Fatalf("transcript kept %d rows, want at least 3", grown)
	}

	// At 80x24 a six-row composer must still leave a transcript.
	m.composer = strings.Join(lines[:6], "\n")
	if h6 := m.composerHeight(m.width); h6 != 8 {
		t.Fatalf("composerHeight for 6 text rows = %d, want 8 (6 + borders)", h6)
	}
	if rows := m.transcriptRows(); rows < 3 {
		t.Fatalf("a 6-row composer left the transcript %d rows at 80x24", rows)
	}
}

// The caret follows the wrapped tail: last visual row, after the draft's last
// rune, inside the box.
func TestRoomsComposer_CursorFollowsTheWrappedTail(t *testing.T) {
	m := transcriptWith(0)
	m.width, m.height = 80, 24
	m.composer = strings.Repeat("ab ", 100)

	cur := m.Cursor()
	if cur == nil {
		t.Fatal("no cursor for a wrapped draft")
	}
	rows := m.composerVisualRows(m.width)
	if len(rows) < 2 {
		t.Fatalf("draft wrapped into %d row(s), want a wrapped draft", len(rows))
	}
	box := composerBoxRows(t, m)
	if len(box) < 2 {
		t.Fatalf("composer box rendered %d content rows, want a wrapped draft", len(box))
	}
	if cur.Position.Y < 1 || cur.Position.Y >= len(strings.Split(ansi.Strip(m.View()), "\n")) {
		t.Fatalf("cursor row %d is outside the view", cur.Position.Y)
	}
	// The cursor sits on the last content row of the box.
	line := strings.Split(ansi.Strip(m.View()), "\n")[cur.Position.Y]
	if !strings.Contains(line, strings.TrimSpace(rows[len(rows)-1])) {
		t.Fatalf("cursor row is not the last visual row: %q", line)
	}
	want := 2 + displayWidth(composerPrompt) + displayWidth(rows[len(rows)-1])
	if cur.Position.X != want {
		t.Fatalf("cursor col = %d, want %d", cur.Position.X, want)
	}
	if cur.Position.X >= displayWidth(line)-2 {
		t.Fatalf("cursor col %d is on or past the box border (row %d wide)", cur.Position.X, displayWidth(line))
	}
}

// A cancelled turn is visible: one dim annotation row in the evidence
// gutter, counted in the transcript like any other row.
func TestRoomsComposer_CancelledTurnRendersAnAnnotation(t *testing.T) {
	m := transcriptWith(0)
	ev := rooms.Event{
		RoomID: "sztab-kowal", Seq: 99, EventID: "cancel1", Kind: "turn.cancelled",
		Actor:   rooms.Actor{Kind: "member", ID: "m1", Profile: "matt", DisplayName: "matt"},
		Payload: map[string]any{"reason": "superseded_by_newer_user_event"},
	}

	lines := strings.Split(ansi.Strip(m.formatEvent(ev)), "\n")
	if len(lines) != 1 {
		t.Fatalf("turn.cancelled rendered %d rows, want 1: %q", len(lines), lines)
	}
	for _, want := range []string{"@matt", "przerwano", "nowsza wiadomość"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("cancelled row %q missing %q", lines[0], want)
		}
	}
	if !strings.HasPrefix(lines[0], "  ") {
		t.Errorf("cancelled row %q is not in the evidence gutter", lines[0])
	}

	before := len(m.transcriptLines())
	m.page.Events = append(m.page.Events, ev)
	if after := len(m.transcriptLines()); after != before+1 {
		t.Fatalf("transcriptLines grew by %d, want 1 (the cancelled row is counted)", after-before)
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "przerwano") {
		t.Fatalf("the cancelled turn is not in the rendered transcript:\n%s", view)
	}
}
