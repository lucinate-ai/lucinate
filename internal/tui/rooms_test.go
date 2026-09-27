package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/config"
	"github.com/lucinate-ai/lucinate/internal/rooms"
)

func keyRune(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

func liveRoom(id, name string, profiles ...string) rooms.Room {
	return rooms.Room{
		RoomID: id, Name: name, Revision: 1,
		Members: rooms.RosterFor(profiles),
	}
}

func browseModel() roomsModel {
	m := newRoomsModel(nil, true)
	m.loaded = true
	m.presets = rooms.PresetStore{Presets: []rooms.Preset{{
		Name: "Sztab", RoomID: "sztab", Members: rooms.RosterFor([]string{"matt", "kowal"}),
	}}}
	m.live = []rooms.Room{liveRoom("sztab-kowal", "Sztab Kowal", "matt", "kowal")}
	return m
}

// The browse screen lists predefined rooms first, then the gateway's
// rooms under a divider — the order browseRows() returns, which the
// cursor indexes into.
func TestRoomsModel_BrowseRendersPresetsThenLiveRooms(t *testing.T) {
	m := browseModel()
	rows := m.browseRows()
	if len(rows) != 2 {
		t.Fatalf("browseRows = %d rows, want 2", len(rows))
	}
	if rows[0].kind != rowPreset || rows[1].kind != rowLive {
		t.Fatalf("row kinds = %v, %v; want preset then live", rows[0].kind, rows[1].kind)
	}

	out := m.View()
	for _, want := range []string{"Sztab", "sztab-kowal", "predefined", "rooms on this gateway"} {
		if !strings.Contains(out, want) {
			t.Errorf("browse view missing %q:\n%s", want, out)
		}
	}
}

func TestRoomsModel_BrowseEmptyStatePointsAtInvite(t *testing.T) {
	m := newRoomsModel(nil, true)
	m.loaded = true
	out := m.View()
	if !strings.Contains(out, "no rooms yet") || !strings.Contains(out, "press n") {
		t.Fatalf("empty browse view should point at the invite flow:\n%s", out)
	}
}

// The invite list must refuse to overfill a roster rather than let the
// create call fail on the gateway.
func TestRoomsModel_InviteToggleRespectsCeiling(t *testing.T) {
	m := newRoomsModel(nil, true)
	m.sub = roomsInvite
	for _, p := range []string{"a", "b", "c", "d", "e", "f"} {
		if warn := m.toggle(p); warn != "" {
			t.Fatalf("toggle(%s) warned early: %s", p, warn)
		}
	}
	if got := len(m.selectedProfiles()); got != rooms.MaxMembers {
		t.Fatalf("selected %d, want %d", got, rooms.MaxMembers)
	}
	if warn := m.toggle("g"); warn == "" {
		t.Fatal("toggling past the ceiling returned no warning")
	}
	if len(m.selectedProfiles()) != rooms.MaxMembers {
		t.Fatal("toggling past the ceiling still added a profile")
	}
	// Toggling an already-selected profile always frees a slot.
	if warn := m.toggle("a"); warn != "" {
		t.Fatalf("deselect warned: %s", warn)
	}
	if len(m.selectedProfiles()) != rooms.MaxMembers-1 {
		t.Fatal("deselect did not remove the profile")
	}
}

func TestRoomsModel_InviteEnterRejectsRosterBelowMinimum(t *testing.T) {
	m := newRoomsModel(nil, true)
	m.sub = roomsInvite
	m.profiles = []string{"matt", "kowal"}
	m.selected["matt"] = true

	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if next.err == nil {
		t.Fatal("creating with 1 member should surface a roster error")
	}
	if next.sub != roomsInvite {
		t.Fatalf("sub = %v, want to stay on the invite screen", next.sub)
	}
	if cmd != nil {
		t.Fatal("a rejected roster must not dial the gateway")
	}
}

func TestRoomsModel_InviteEnterCreatesWhenRosterIsValid(t *testing.T) {
	m := newRoomsModel(nil, true)
	m.sub = roomsInvite
	m.profiles = []string{"matt", "kowal"}
	m.selected["matt"] = true
	m.selected["kowal"] = true

	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if next.err != nil {
		t.Fatalf("valid roster errored: %v", next.err)
	}
	if cmd == nil {
		t.Fatal("valid roster should dispatch a create command")
	}
	if !strings.Contains(next.status, "creating") {
		t.Fatalf("status = %q, want a creating notice", next.status)
	}
}

// The invite screen names the room after its roster, so seating one
// needs no typing.
func TestRoomsModel_InviteRendersDerivedNameAndSelection(t *testing.T) {
	m := newRoomsModel(nil, true)
	m.sub = roomsInvite
	m.profiles = []string{"matt", "kowal", "deepsh"}
	m.selected["matt"] = true

	out := m.View()
	for _, want := range []string{"deepsh", "[x]", "[ ]", "selected 1/6", "Matt", "frozen"} {
		if !strings.Contains(out, want) {
			t.Errorf("invite view missing %q:\n%s", want, out)
		}
	}
}

func TestRoomsModel_EscFromBrowseGoesBackToChat(t *testing.T) {
	m := browseModel()
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("esc returned no command")
	}
	if _, ok := cmd().(goBackFromRoomsMsg); !ok {
		t.Fatalf("esc produced %T, want goBackFromRoomsMsg", cmd())
	}
}

// Disbanding is destructive and irreversible (the room id is tombstoned),
// so it must take a second keystroke.
func TestRoomsModel_DisbandRequiresConfirmation(t *testing.T) {
	m := browseModel()
	m.cursor = 1 // the live room

	armed, cmd := m.handleKey(keyRune('x'))
	if armed.confirmDisband != "sztab-kowal" {
		t.Fatalf("confirmDisband = %q, want the room id", armed.confirmDisband)
	}
	if cmd != nil {
		t.Fatal("arming the confirm must not disband yet")
	}
	cancelled, cmd := armed.handleKey(keyRune('n'))
	if cancelled.confirmDisband != "" {
		t.Fatal("n should clear the confirm")
	}
	if cmd != nil {
		t.Fatal("n must not disband")
	}

	armed2, _ := m.handleKey(keyRune('x'))
	_, cmd = armed2.handleKey(keyRune('y'))
	if cmd == nil {
		t.Fatal("y should dispatch the disband")
	}
}

func TestRoomsModel_TranscriptComposerAccumulatesAndBackspaces(t *testing.T) {
	m := browseModel()
	m.sub = roomsTranscript
	m.openRoomID = "sztab-kowal"

	m, _ = m.handleKey(keyRune('h'))
	m, _ = m.handleKey(keyRune('i'))
	if m.composer != "hi" {
		t.Fatalf("composer = %q, want %q", m.composer, "hi")
	}
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.composer != "h" {
		t.Fatalf("composer after backspace = %q, want %q", m.composer, "h")
	}

	out := m.View()
	if !strings.Contains(out, "you › h") {
		t.Fatalf("transcript should render the composer:\n%s", out)
	}
}

func TestRoomsModel_TranscriptEnterOnEmptyComposerIsANoop(t *testing.T) {
	m := browseModel()
	m.sub = roomsTranscript
	m.openRoomID = "sztab-kowal"
	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("an empty composer must not send")
	}
	if next.composer != "" {
		t.Fatalf("composer = %q", next.composer)
	}
}

// The invite list is every local profile (dozens on a real host). Without
// a window the summary and the key hints are pushed off-screen, so the
// menu says nothing about what is selected — the bug this pins.
func TestRoomsModel_InviteWindowsLongProfileLists(t *testing.T) {
	m := newRoomsModel(nil, true)
	m.sub = roomsInvite
	for i := 0; i < 56; i++ {
		m.profiles = append(m.profiles, fmt.Sprintf("profile-%02d", i))
	}
	m.selected["profile-00"] = true

	out := m.View()
	if !strings.Contains(out, "selected 1/6") {
		t.Fatalf("selection summary pushed off-screen:\n%s", out)
	}
	if !strings.Contains(out, "new room name:") {
		t.Fatalf("derived name pushed off-screen:\n%s", out)
	}
	if !strings.Contains(out, "↓ ") {
		t.Fatalf("hidden rows below the window are not reported:\n%s", out)
	}
	rendered := strings.Count(out, "[ ]") + strings.Count(out, "[x]")
	if rendered > roomsVisibleRows {
		t.Fatalf("rendered %d rows, want at most %d", rendered, roomsVisibleRows)
	}

	// Walking the cursor to the end must keep the summary visible.
	m.inviteCursor = len(m.profiles) - 1
	out = m.View()
	if !strings.Contains(out, "profile-55") || !strings.Contains(out, "selected 1/6") {
		t.Fatalf("cursor at the end lost the selection summary:\n%s", out)
	}
	if !strings.Contains(out, "↑ ") {
		t.Fatalf("hidden rows above the window are not reported:\n%s", out)
	}
}

func TestRoomsModel_InviteCursorMovesOneStepAndClamps(t *testing.T) {
	m := newRoomsModel(nil, true)
	m.sub = roomsInvite
	m.profiles = []string{"a", "b", "c"}

	m, _ = m.handleKey(keyRune('j'))
	if m.inviteCursor != 1 {
		t.Fatalf("after j: cursor = %d, want 1", m.inviteCursor)
	}
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.inviteCursor != 2 {
		t.Fatalf("after down: cursor = %d, want 2", m.inviteCursor)
	}
	m, _ = m.handleKey(keyRune('j'))
	if m.inviteCursor != 2 {
		t.Fatalf("cursor past the end = %d, want it clamped at 2", m.inviteCursor)
	}
	m, _ = m.handleKey(keyRune('k'))
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	m, _ = m.handleKey(keyRune('k'))
	if m.inviteCursor != 0 {
		t.Fatalf("cursor above the start = %d, want it clamped at 0", m.inviteCursor)
	}
}

func TestWindowRows(t *testing.T) {
	cases := []struct {
		name                          string
		total, cursor, size           int
		wantStart, wantEnd, wantAbove int
		wantBelow                     int
	}{
		{"short list, no window", 5, 0, 14, 0, 5, 0, 0},
		{"exactly the window", 14, 13, 14, 0, 14, 0, 0},
		{"cursor at start", 56, 0, 14, 0, 14, 0, 42},
		{"cursor in the middle", 56, 28, 14, 21, 35, 21, 21},
		{"cursor at end", 56, 55, 14, 42, 56, 42, 0},
		{"cursor past the end clamps", 56, 99, 14, 42, 56, 42, 0},
		{"negative cursor clamps", 56, -3, 14, 0, 14, 0, 42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, e, a, b := windowRows(tc.total, tc.cursor, tc.size)
			if s != tc.wantStart || e != tc.wantEnd || a != tc.wantAbove || b != tc.wantBelow {
				t.Fatalf("windowRows(%d,%d,%d) = (%d,%d,%d,%d), want (%d,%d,%d,%d)",
					tc.total, tc.cursor, tc.size, s, e, a, b,
					tc.wantStart, tc.wantEnd, tc.wantAbove, tc.wantBelow)
			}
			// The cursor must always land inside the window.
			if tc.cursor >= 0 && tc.cursor < tc.total && (tc.cursor < s || tc.cursor >= e) {
				t.Fatalf("cursor %d outside window [%d,%d)", tc.cursor, s, e)
			}
		})
	}
}

// Typing /rooms in the transcript must go back, not post "/rooms" into
// the room and set the whole roster answering — the exact misfire that
// happened live.
func TestRoomsModel_TranscriptSlashCommandGoesBack(t *testing.T) {
	m := browseModel()
	m.sub = roomsTranscript
	m.openRoomID = "sztab-kowal"
	for _, r := range "/rooms" {
		m, _ = m.handleKey(keyRune(r))
	}
	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if next.sub != roomsBrowse {
		t.Fatalf("sub = %v, want browse after /rooms", next.sub)
	}
	if next.composer != "" {
		t.Fatalf("composer = %q, want it cleared", next.composer)
	}
	if cmd == nil {
		t.Fatal("/rooms should refresh the room list")
	}
}

func TestRoomsModel_TranscriptUnknownCommandIsRefusedNotSent(t *testing.T) {
	m := browseModel()
	m.sub = roomsTranscript
	m.openRoomID = "sztab-kowal"
	for _, r := range "/nonsense" {
		m, _ = m.handleKey(keyRune(r))
	}
	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("an unknown command must not dispatch anything")
	}
	if next.err == nil {
		t.Fatal("an unknown command should say so")
	}
	if next.sub != roomsTranscript {
		t.Fatalf("sub = %v, want to stay in the transcript", next.sub)
	}
}

func TestRoomsModel_TranscriptBackslashEscapesACommandLine(t *testing.T) {
	m := browseModel()
	m.sub = roomsTranscript
	m.openRoomID = "sztab-kowal"
	for _, r := range `\rooms is fine` {
		m, _ = m.handleKey(keyRune(r))
	}
	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if next.err != nil {
		t.Fatalf("escaped line errored: %v", next.err)
	}
	if cmd == nil {
		t.Fatal("the escaped line should be sent")
	}
	if next.sub != roomsTranscript {
		t.Fatalf("sub = %v, want to stay in the transcript", next.sub)
	}
}

// The room grows as members reply; an unbounded transcript slides the
// composer off-screen (and the cursor with it) on every poll.
func TestRoomsModel_TranscriptWindowsLongHistories(t *testing.T) {
	m := browseModel()
	m.sub = roomsTranscript
	m.openRoomID = "sztab-kowal"
	m.height = 20
	m.composer = "piszac"
	for i := 0; i < 200; i++ {
		m.page.Events = append(m.page.Events, rooms.Event{
			Kind: "message.member", Actor: rooms.Actor{DisplayName: "Matt"},
			Payload: map[string]any{"text": fmt.Sprintf("linia %d", i)},
		})
	}
	out := m.View()
	if !strings.Contains(out, "you › piszac") {
		t.Fatalf("composer pushed off-screen:\n%s", out)
	}
	if !strings.Contains(out, "earlier line(s) hidden") {
		t.Fatalf("hidden lines not reported:\n%s", out)
	}
	if n := strings.Count(out, "\n"); n > m.height {
		t.Fatalf("view is %d lines for a %d-line terminal", n, m.height)
	}
	if !strings.Contains(out, "linia 199") {
		t.Fatalf("the newest line must stay visible:\n%s", out)
	}
}

// A message typed while the roster is answering must WAIT: sending it now
// cancels the in-flight turns (superseded_by_newer_user_event), which is
// what a roster that "stopped responding" actually was.
func TestRoomsModel_TranscriptQueuesWhileTheRosterAnswers(t *testing.T) {
	m := browseModel()
	m.sub = roomsTranscript
	m.openRoomID = "sztab-kowal"
	m.working = true
	m.lastProgress = time.Now() // the transcript is moving
	// transcriptWith ends on a member message: a turn set is in flight.
	m.page.Events = append(m.page.Events, rooms.Event{Kind: "message.member",
		Actor: rooms.Actor{DisplayName: "Matt"}, Payload: map[string]any{"text": "pracuje"}})

	for _, r := range "drugie" {
		m, _ = m.handleKey(keyRune(r))
	}
	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("sending while the roster answers must not dispatch")
	}
	if len(next.queue) != 1 || next.queue[0] != "drugie" {
		t.Fatalf("queue = %v, want [drugie]", next.queue)
	}
	if next.composer != "" {
		t.Fatalf("composer = %q, want it cleared", next.composer)
	}
	if !strings.Contains(next.View(), "1 queued") {
		t.Fatalf("the queue is not surfaced:\n%s", next.View())
	}
}

func TestRoomsModel_QueueDrainsWhenTheRosterSettles(t *testing.T) {
	m := browseModel()
	m.sub = roomsTranscript
	m.openRoomID = "sztab-kowal"
	m.working = true
	m.queue = []string{"pierwsze", "drugie"}

	next, cmd := m.Update(roomsStateMsg{working: false})
	if cmd == nil {
		t.Fatal("a settled roster should dispatch the queued message")
	}
	if len(next.queue) != 1 || next.queue[0] != "drugie" {
		t.Fatalf("queue = %v, want [drugie] left", next.queue)
	}
	if !next.working {
		t.Fatal("draining dispatches a send, so the roster is busy again")
	}

	// A still-busy roster drains nothing.
	busy := next
	busy.queue = []string{"x"}
	after, cmd2 := busy.Update(roomsStateMsg{working: true})
	if cmd2 != nil {
		t.Fatal("a busy roster must not drain the queue")
	}
	if len(after.queue) != 1 {
		t.Fatalf("queue = %v, want it untouched", after.queue)
	}
}

// A message typed right after a send must queue too: the driver state poll
// is up to 1.5s behind, so waiting for it would let the second message
// cancel the turn set the first one just started.
func TestRoomsModel_SecondMessageQueuesBeforeTheStatePoll(t *testing.T) {
	m := browseModel()
	m.sub = roomsTranscript
	m.openRoomID = "sztab-kowal"

	// First send: the roster was idle, so it goes out.
	sent, cmd := m.post("pierwsze")
	if cmd == nil {
		t.Fatal("the first message should be sent")
	}
	if !sent.working {
		t.Fatal("a successful send must mark the roster busy without waiting for the poll")
	}

	// Second message, before any state poll: it must wait.
	queued, cmd2 := sent.post("drugie")
	if cmd2 != nil {
		t.Fatal("the second message must not be sent while the roster is busy")
	}
	if len(queued.queue) != 1 || queued.queue[0] != "drugie" {
		t.Fatalf("queue = %v, want [drugie]", queued.queue)
	}
}

// Bracketed paste arrives as tea.PasteMsg, not as keystrokes. The rooms
// composer is a plain string, so without an explicit case the whole paste
// is silently dropped.
func TestRoomsModel_PasteInsertsIntoTheComposer(t *testing.T) {
	m := browseModel()
	m.sub = roomsTranscript
	m.openRoomID = "sztab-kowal"
	m.composer = "przed "

	next, _ := m.Update(tea.PasteMsg{Content: "duży\nwielolinijkowy\ntekst"})
	want := "przed duży\nwielolinijkowy\ntekst"
	if next.composer != want {
		t.Fatalf("composer = %q, want %q", next.composer, want)
	}
}

// Outside the transcript there is no composer, so a paste must not leak
// into a room or start anything.
func TestRoomsModel_PasteIsIgnoredOutsideTheTranscript(t *testing.T) {
	m := browseModel()
	m.sub = roomsBrowse

	next, cmd := m.Update(tea.PasteMsg{Content: "x"})
	if cmd != nil {
		t.Fatalf("paste in browse dispatched %v", cmd)
	}
	if next.composer != "" {
		t.Fatalf("composer = %q, want empty", next.composer)
	}
}

func TestSanitizePaste_NormalisesLineEndingsAndDropsControlChars(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a\r\nb", "a\nb"},
		{"a\rb", "a\nb"},
		{"a\x00b", "ab"},
		{"a\x1b[31mb", "a[31mb"},
		{"a\tb", "a\tb"},
		{"bez zmian", "bez zmian"},
	}
	for _, c := range cases {
		if got := sanitizePaste(c.in); got != c.want {
			t.Errorf("sanitizePaste(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A pasted document must not stretch the composer box down the screen:
// the box keeps its shape and shows a size summary instead.
func TestRoomsModel_MultiLineComposerKeepsTheBoxShape(t *testing.T) {
	m := browseModel()
	m.sub = roomsTranscript
	m.openRoomID = "sztab-kowal"
	m.composer = "linia1\nlinia2\nlinia3"

	out := ansi.Strip(m.View())
	if rows := strings.Count(out, "\n") + 1; rows > 24 {
		t.Fatalf("the view grew to %d rows — the box is stretching", rows)
	}
	if !strings.Contains(out, "3 lines") {
		t.Fatalf("no line summary in:\n%s", out)
	}
	if strings.Contains(out, "linia1") || strings.Contains(out, "linia2") {
		t.Fatalf("only the tail should render, got:\n%s", out)
	}
}

// transcriptWith builds a transcript holding n one-line messages.
func transcriptWith(n int) roomsModel {
	m := browseModel()
	m.sub = roomsTranscript
	m.openRoomID = "sztab-kowal"
	m.height = 24
	m.width = 100
	for i := 0; i < n; i++ {
		m.page.Events = append(m.page.Events, rooms.Event{
			Kind: "message.member", Actor: rooms.Actor{DisplayName: "Matt"},
			Payload: map[string]any{"text": fmt.Sprintf("linia %d", i)},
		})
	}
	return m
}

func visibleHistory(out string) []string {
	var got []string
	for _, l := range strings.Split(out, "\n") {
		if i := strings.Index(l, "linia "); i >= 0 {
			got = append(got, strings.TrimSpace(l[i:]))
		}
	}
	return got
}

// Scrolling back must reveal older lines and say how many newer ones are
// below; End returns to the live tail.
func TestRoomsModel_TranscriptScrollsBackThroughHistory(t *testing.T) {
	m := transcriptWith(60)
	if !strings.Contains(m.View(), "linia 59") {
		t.Fatalf("the live tail should show the newest line:\n%s", m.View())
	}

	m, _ = m.scrollTranscript(10)
	back := m.View()
	if strings.Contains(back, "linia 59") {
		t.Fatalf("after scrolling back the newest line should be out of view:\n%s", back)
	}
	if !strings.Contains(back, "newer line(s)") {
		t.Fatalf("no scroll-back indicator:\n%s", back)
	}

	m, _ = m.scrollTranscript(-10)
	if got := m.View(); !strings.Contains(got, "linia 59") {
		t.Fatalf("scrolling down should return to the tail:\n%s", got)
	}
	if m.scroll != 0 {
		t.Fatalf("scroll = %d, want 0 after returning to the tail", m.scroll)
	}
}

func TestRoomsModel_ScrollIsClampedToTheHistory(t *testing.T) {
	m := transcriptWith(40)
	m, _ = m.scrollTranscript(100000)
	if total := len(m.transcriptLines()); m.scroll != total {
		t.Fatalf("scroll = %d, want it clamped to the %d-line history", m.scroll, total)
	}
	if m, _ = m.scrollTranscript(-100000); m.scroll != 0 {
		t.Fatalf("scroll = %d, want it pinned at 0", m.scroll)
	}
}

func TestRoomsModel_WheelScrollsTheTranscript(t *testing.T) {
	m := transcriptWith(60)
	next, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if next.scroll == 0 {
		t.Fatal("wheel up did not scroll back")
	}
	back, _ := next.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if back.scroll >= next.scroll {
		t.Fatalf("wheel down did not scroll toward the tail: %d -> %d", next.scroll, back.scroll)
	}
	// Outside the transcript there is nothing to scroll.
	browse := browseModel()
	if out, _ := browse.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp}); out.scroll != 0 {
		t.Fatalf("browse wheel scrolled to %d", out.scroll)
	}
}

// While the user reads history, an incoming message must not yank the view.
func TestRoomsModel_ScrolledViewKeepsItsPlaceOnNewEvents(t *testing.T) {
	m := transcriptWith(60)
	m, _ = m.scrollTranscript(10)
	before := visibleHistory(m.View())
	if len(before) == 0 {
		t.Fatal("nothing visible to pin")
	}

	m, _ = m.Update(roomsLogMsg{page: rooms.LogPage{
		Events: []rooms.Event{{Kind: "message.member", Actor: rooms.Actor{DisplayName: "Matt"},
			Payload: map[string]any{"text": "nowa wiadomosc"}}},
		Cursor: 99,
	}})

	if strings.Contains(m.View(), "nowa wiadomosc") {
		t.Fatalf("a new message yanked the scrolled view:\n%s", m.View())
	}
	after := visibleHistory(m.View())
	if strings.Join(before, "|") != strings.Join(after, "|") {
		t.Fatalf("the visible history moved:\n before: %v\n  after: %v", before, after)
	}
}

func TestRoomsModel_TranscriptWrapsLongLinesToThePaneWidth(t *testing.T) {
	m := transcriptWith(0)
	m.width = 60
	m.height = 24
	m.page.Events = append(m.page.Events, rooms.Event{
		Kind: "message.member", Actor: rooms.Actor{DisplayName: "Matt"},
		Payload: map[string]any{"text": strings.Repeat("wyrazenie ", 30)},
	})

	for _, l := range m.transcriptLines() {
		if w := lipgloss.Width(l); w > m.width {
			t.Fatalf("a transcript line is %d wide for a %d-wide pane: %q", w, m.width, l)
		}
	}
	// Wrapping transcript lines myself is what keeps the row count equal to
	// the line count, so the whole view must still fit the terminal. (The
	// hint is allowed to wrap — the chrome budgets two rows for it.)
	if n := strings.Count(m.View(), "\n") + 1; n > m.height {
		t.Fatalf("the view is %d rows for a %d-row terminal", n, m.height)
	}
}

// The rooms composer is a plain string: with no cursor method the terminal
// painted its own outside the box, below it.
func TestRoomsModel_CursorSitsInsideTheComposer(t *testing.T) {
	m := transcriptWith(3)
	m.composer = "siema siema"

	cur := m.Cursor()
	if cur == nil {
		t.Fatal("no cursor for the transcript composer")
	}
	lines := strings.Split(ansi.Strip(m.View()), "\n")

	wantRow := -1
	for i, l := range lines {
		if strings.Contains(l, "you › siema siema") {
			wantRow = i
		}
	}
	if wantRow < 0 {
		t.Fatalf("the composer text is not rendered:\n%s", strings.Join(lines, "\n"))
	}
	if cur.Position.Y != wantRow {
		t.Fatalf("cursor row = %d, want %d (the composer line)", cur.Position.Y, wantRow)
	}

	idx := strings.Index(lines[wantRow], "siema siema")
	wantCol := lipgloss.Width(lines[wantRow][:idx]) + lipgloss.Width("siema siema")
	if cur.Position.X != wantCol {
		t.Fatalf("cursor col = %d, want %d", cur.Position.X, wantCol)
	}
	if cur.Position.X >= lipgloss.Width(lines[wantRow]) {
		t.Fatalf("cursor at col %d is outside the box (row is %d wide)", cur.Position.X, lipgloss.Width(lines[wantRow]))
	}
	if !strings.Contains(lines[cur.Position.Y], "│") {
		t.Fatalf("cursor row is not the box interior: %q", lines[cur.Position.Y])
	}
}

// A pasted document is summarised in the box, so the caret must sit after
// the summary, not after the invisible tail.
func TestRoomsModel_CursorFollowsTheMultiLineSummary(t *testing.T) {
	m := transcriptWith(1)
	m.composer = "linia1\nlinia2\nlinia3"

	cur := m.Cursor()
	if cur == nil {
		t.Fatal("no cursor for a multi-line composer")
	}
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	row := lines[cur.Position.Y]
	if !strings.Contains(row, "3 lines") {
		t.Fatalf("cursor is not on the summary row: %q", row)
	}
	want := 2 + lipgloss.Width("you › ") + lipgloss.Width(composerText(m.composer))
	if cur.Position.X != want {
		t.Fatalf("cursor col = %d, want %d", cur.Position.X, want)
	}
	if cur.Position.X >= lipgloss.Width(row) {
		t.Fatalf("cursor col %d is outside the box (row is %d wide)", cur.Position.X, lipgloss.Width(row))
	}
}

func TestRoomsModel_CursorIsHiddenOutsideTheTranscript(t *testing.T) {
	if m := browseModel(); m.Cursor() != nil {
		t.Fatal("the browse view should not position a cursor")
	}
}

// The bug was in the app's view switch, not in the composer: viewRooms
// handed the terminal no cursor at all, so the terminal painted its own
// wherever the last write ended — outside the box.
func TestAppModel_RoomsViewPositionsTheCursorInTheComposer(t *testing.T) {
	m := AppModel{state: viewRooms, width: 100, height: 30}
	m.roomsModel = transcriptWith(2)
	m.roomsModel.composer = "siema siema"

	v := m.View()
	if v.Cursor == nil {
		t.Fatal("the rooms view must position the cursor")
	}
	lines := strings.Split(ansi.Strip(m.roomsModel.View()), "\n")
	if v.Cursor.Position.Y >= len(lines) {
		t.Fatalf("cursor row %d is past the %d-line view", v.Cursor.Position.Y, len(lines))
	}
	if !strings.Contains(lines[v.Cursor.Position.Y], "siema siema") {
		t.Fatalf("the cursor is not on the composer line: %q", lines[v.Cursor.Position.Y])
	}

	// Browsing has no input, so the cursor must stay hidden there.
	m.roomsModel.sub = roomsBrowse
	if m.View().Cursor != nil {
		t.Fatal("the browse view should leave the cursor hidden")
	}
}

// A pointer-receiver dial() was silently defeated by value-receiver
// callers: the cache was written into a temporary, so EVERY poll opened a
// new connection and leaked it. Thousands of leaked sockets exhausted the
// Windows socket buffers (WSAENOBUFS) and room members could no longer
// reach the model APIs — reported as "the roster stopped answering".
func TestRoomsModel_DialReusesOneConnectionAcrossCopies(t *testing.T) {
	m := newRoomsModel(&config.Connection{ID: "c1", URL: "http://127.0.0.1:1"}, true)
	shared := &rooms.Client{}
	m.shared.set(shared)

	got, err := m.dial()
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if got != shared {
		t.Fatal("dial did not reuse the cached connection")
	}

	// Update hands the model around by value; a copy must still see it.
	copied := m
	again, err := copied.dial()
	if err != nil {
		t.Fatalf("dial on a copy: %v", err)
	}
	if again != shared {
		t.Fatal("a model copy lost the connection — that is the leak")
	}
}

func TestRoomsModel_CloseDropsTheSharedConnection(t *testing.T) {
	m := newRoomsModel(&config.Connection{ID: "c1", URL: "http://127.0.0.1:1"}, true)
	m.shared.set(&rooms.Client{})

	m.Close()
	if c := m.shared.get(); c != nil {
		t.Fatal("Close left the connection cached")
	}
}

// The driver's `working` flag sticks at true after a turn dies (the gateway
// keeps a stale `running` count). The transcript does not lie: a room that
// finished its turn set ends on a settle, not on the user's message. Holding
// a message behind the stale flag is the silence the user reported.
func TestRoomsModel_StaleWorkingFlagDoesNotHoldMessagesForever(t *testing.T) {
	m := transcriptWith(3)
	m.working = true
	// transcriptWith ends on message.member; a settle means the set finished.
	m.page.Events = append(m.page.Events, rooms.Event{Kind: "turn.settled"})

	next, cmd := m.post("nie czekaj wiecznie")
	if cmd == nil {
		t.Fatal("a stale working flag must not hold the message")
	}
	if len(next.queue) != 0 {
		t.Fatalf("the message was queued behind a dead driver: %v", next.queue)
	}
	// The transcript shows the set finished, so there is nothing to warn
	// about — the "quiet roster" notice is for the pinned-task case, which
	// TestRoomsModel_PinnedTaskReleasesTheHold covers.
}

// A transcript that ends mid-answer still holds the message.
func TestRoomsModel_LiveTurnSetStillHoldsMessages(t *testing.T) {
	m := transcriptWith(3) // ends on message.member: the set is in flight
	m.working = true
	m.lastProgress = time.Now()

	next, cmd := m.post("poczekaj")
	if cmd != nil {
		t.Fatal("a live turn set must hold the message")
	}
	if len(next.queue) != 1 {
		t.Fatalf("queue = %v, want the message held", next.queue)
	}
}

// The transcript says a set is in flight, but it has not moved for longer
// than staleWorkingAfter — a pinned task. The message must go out.
func TestRoomsModel_PinnedTaskReleasesTheHold(t *testing.T) {
	m := transcriptWith(3) // ends on message.member
	m.working = true
	m.lastProgress = time.Now().Add(-2 * staleWorkingAfter)

	next, cmd := m.post("nie czekaj wiecznie")
	if cmd == nil {
		t.Fatal("a pinned task must not hold the message forever")
	}
	if len(next.queue) != 0 {
		t.Fatalf("the message was queued behind a pinned task: %v", next.queue)
	}
	if !strings.Contains(next.status, "quiet") {
		t.Fatalf("the quiet roster is not surfaced: %q", next.status)
	}
}

// Only a real seq advance counts as progress: the poll re-delivers the same
// events when the cursor does not move, which would refresh the hold forever.
func TestRoomsModel_OnlyASeqAdvanceCountsAsProgress(t *testing.T) {
	m := transcriptWith(2)
	m.working = true
	m.page.LatestSeq = 10
	m.lastProgress = time.Time{} // zero: everything looks stale

	m, _ = m.Update(roomsLogMsg{page: rooms.LogPage{
		Events: []rooms.Event{{Kind: "message.member", Actor: rooms.Actor{DisplayName: "Matt"},
			Payload: map[string]any{"text": "stare"}}},
		LatestSeq: 10,
	}})
	if !m.lastProgress.IsZero() {
		t.Fatal("re-delivering the same seq counted as progress")
	}

	m, _ = m.Update(roomsLogMsg{page: rooms.LogPage{LatestSeq: 11}})
	if m.lastProgress.IsZero() {
		t.Fatal("a seq advance did not count as progress")
	}
}

// The queue must not read as a hang: while it holds, the status shows how
// long the roster has been silent and when the message will go out anyway.
func TestRoomsModel_QueueStatusCountsDown(t *testing.T) {
	m := transcriptWith(3) // ends on message.member: a set in flight
	m.working = true
	m.queue = []string{"trzymana"}
	m.lastProgress = time.Now().Add(-30 * time.Second)

	next, _ := m.Update(roomsStateMsg{working: true})
	if !strings.Contains(next.status, "queued 1") {
		t.Fatalf("status does not report the queue: %q", next.status)
	}
	if !strings.Contains(next.status, "quiet for 30s") {
		t.Fatalf("status does not report the silence: %q", next.status)
	}
	if !strings.Contains(next.status, "sends in") {
		t.Fatalf("status does not count down to the send: %q", next.status)
	}
}

// Once the roster is idle the queue drains and the countdown is gone.
func TestRoomsModel_QueueStatusClearsWhenIdle(t *testing.T) {
	m := transcriptWith(3)
	m.working = true
	m.queue = []string{"trzymana"}
	m.lastProgress = time.Now()

	next, _ := m.Update(roomsStateMsg{working: false})
	if strings.Contains(next.status, "quiet for") {
		t.Fatalf("countdown survived the settle: %q", next.status)
	}
	if len(next.queue) != 0 {
		t.Fatalf("queue did not drain: %v", next.queue)
	}
}

// A user message with no answer yet is also a set in flight.
func TestRoomsModel_UnansweredUserMessageHoldsMessages(t *testing.T) {
	m := transcriptWith(2)
	m.page.Events = append(m.page.Events, rooms.Event{Kind: "message.user",
		Actor: rooms.Actor{DisplayName: "you"}, Payload: map[string]any{"text": "czekam"}})
	m.working = true
	m.lastProgress = time.Now()

	if _, cmd := m.post("jeszcze raz"); cmd != nil {
		t.Fatal("an unanswered user message means the roster is working on it")
	}
}

// The composer only ever handled runes and backspace, so the shell habits
// users reach for first (ctrl+u, ctrl+w) did nothing.
func TestRoomsModel_ComposerControlKeys(t *testing.T) {
	m := transcriptWith(2)
	m.composer = "pierwsze drugie trzecie"

	m, _ = m.handleKey(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	if m.composer != "pierwsze drugie " {
		t.Fatalf("ctrl+w -> %q, want %q", m.composer, "pierwsze drugie ")
	}

	m, _ = m.handleKey(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if m.composer != "" {
		t.Fatalf("ctrl+u -> %q, want the draft cleared", m.composer)
	}
}

func TestDeleteLastWord(t *testing.T) {
	cases := []struct{ in, want string }{
		{"pierwsze drugie", "pierwsze "},
		{"jedno", ""},
		{"z  podwojna  spacja", "z  podwojna  "},
		{"ogon   ", ""},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := deleteLastWord(c.in); got != c.want {
			t.Errorf("deleteLastWord(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// /mouse must work from the rooms transcript too: mouse capture is what
// disables the terminal's own click-drag selection, and the rooms view had
// no way to turn it off.
func TestRoomsModel_MouseCommandRaisesTheAppMsg(t *testing.T) {
	m := transcriptWith(1)

	for _, tc := range []struct{ in, want string }{
		{"/mouse off", "off"},
		{"/mouse on", "on"},
		{"/mouse toggle", "toggle"},
		{"/mouse", "status"},
	} {
		next, cmd := m.handleTranscriptCommand(tc.in)
		if cmd == nil {
			t.Fatalf("%s raised no command", tc.in)
		}
		msg, ok := cmd().(mouseModeMsg)
		if !ok {
			t.Fatalf("%s raised %T, want mouseModeMsg", tc.in, cmd())
		}
		if msg.action != tc.want {
			t.Fatalf("%s -> action %q, want %q", tc.in, msg.action, tc.want)
		}
		if next.composer != "" {
			t.Fatalf("%s left the composer dirty: %q", tc.in, next.composer)
		}
	}
}

func TestRoomsModel_MouseCommandRejectsBadArgument(t *testing.T) {
	m := transcriptWith(1)
	next, cmd := m.handleTranscriptCommand("/mouse sideways")
	if cmd != nil {
		t.Fatal("a bad /mouse argument must not raise a command")
	}
	if next.err == nil || !strings.Contains(next.err.Error(), "sideways") {
		t.Fatalf("err = %v, want it to name the bad argument", next.err)
	}
}

func TestFormatRoomsEvent_Shapes(t *testing.T) {
	cases := []struct {
		ev   rooms.Event
		want string
	}{
		{rooms.Event{Kind: "message.user", Payload: map[string]any{"text": "hej"}}, "you →"},
		{rooms.Event{Kind: "message.member", Actor: rooms.Actor{DisplayName: "Matt"},
			Payload: map[string]any{"text": "odpowiedz"}}, "@Matt:"},
		{rooms.Event{Kind: "turn.settled", Actor: rooms.Actor{DisplayName: "Matt"},
			Payload: map[string]any{"passed": true}}, "passed"},
		{rooms.Event{Kind: "turn.failed", Actor: rooms.Actor{DisplayName: "Matt"},
			Payload: map[string]any{"error": "boom"}}, "failed: boom"},
		{rooms.Event{Kind: "room.activity", Payload: map[string]any{"status": "settled"}}, "activity: settled"},
	}
	for _, tc := range cases {
		got := formatRoomsEvent(tc.ev)
		if !strings.Contains(got, tc.want) {
			t.Errorf("formatRoomsEvent(%s) = %q, want it to contain %q", tc.ev.Kind, got, tc.want)
		}
	}
}

// The mention handle shown in the transcript must be one the gateway can
// route — a roster label that no @mention matches is a silent dead end.
func TestRosterLabel_ListsMentionHandles(t *testing.T) {
	room := liveRoom("r", "R", "matt", "deepsh")
	if got := rosterLabel(room); got != "@matt @deepsh" {
		t.Fatalf("rosterLabel = %q", got)
	}
	if got := rosterLabel(rooms.Room{}); got != "no members" {
		t.Fatalf("rosterLabel(empty) = %q", got)
	}
}
