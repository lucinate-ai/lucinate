package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/config"
	"github.com/lucinate-ai/lucinate/internal/rooms"
	tg "github.com/lucinate-ai/lucinate/internal/testgateway"
)

// W1 (boundary cases through the model) and W4c (a long transcript must not
// degrade the view). The gateway is the same fake the rooms package uses, so
// these tests dial a real socket instead of stubbing the client.

func w1GatewayModel(t *testing.T, s *tg.Server) roomsModel {
	t.Helper()
	setTestHome(t)
	conn := &config.Connection{ID: "conn-1", Name: "fake", URL: s.URL(), Type: config.ConnTypeHermes}
	m := newRoomsModel(conn, true)
	m.width, m.height = 100, 30
	return m
}

// requireRoomDir asserts the room's shared directory is on disk — the handoff
// contract only means anything if the directory exists by the time the room is
// open, which is what "created at room start" means for this client.
func requireRoomDir(t *testing.T, roomID string) {
	t.Helper()
	base, err := rooms.RoomsBase()
	if err != nil {
		t.Fatalf("RoomsBase: %v", err)
	}
	if dir := rooms.RoomDir(base, roomID); !strings.Contains(filepath.ToSlash(dir), "/.rooms/") {
		t.Errorf("room directory %q is not under a .rooms/ directory", dir)
	} else if _, err := os.Stat(dir); err != nil {
		t.Errorf("room directory missing after the room started: %v", err)
	}
}

// w1ResetErr stands in for the wrapped OS error a dropped socket produces.
type w1ResetErr struct{}

func (w1ResetErr) Error() string { return "read tcp 127.0.0.1:5000: connection reset by peer" }

// ── W1(a): mentions through the model ────────────────────────────────────────

func TestRoomsW1_MentionEdgesThroughTheModel(t *testing.T) {
	tests := []struct {
		name      string
		mode      rooms.RoutingMode
		moderator string
		text      string
		wantText  string
		wantNote  string
	}{
		{name: "mid-sentence", text: "hej @matt sprawdz to", wantText: "hej @matt sprawdz to", wantNote: "addressed to @matt"},
		{name: "two handles", text: "@matt @kowal oboje", wantText: "@matt @kowal oboje", wantNote: "addressed to @matt @kowal"},
		{name: "typo", text: "@mat hej", wantText: "@mat hej", wantNote: "unknown handle @mat"},
		{name: "glued to a word", text: "@mattxyz hej", wantText: "@mattxyz hej", wantNote: "unknown handle @mattxyz"},
		{name: "only a handle", text: "@matt", wantText: "@matt", wantNote: "addressed to @matt"},
		{name: "unicode truncates", text: "@młody hej", wantText: "@młody hej", wantNote: "unknown handle @m"},
		{name: "at all", text: "@all raport", wantText: "@all raport", wantNote: "whole roster"},
		{name: "broadcast prefix in moderator mode", mode: rooms.RouteModerator, moderator: "kowal",
			text: "status?", wantText: "@kowal status?", wantNote: "moderator @kowal"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := v2Model(t)
			m.prefs.Set("sztab", rooms.RoomPrefs{Mode: string(tc.mode), Moderator: tc.moderator})

			routed, note, _ := m.routeOutgoing(tc.text)
			if routed != tc.wantText {
				t.Fatalf("routed text = %q, want %q", routed, tc.wantText)
			}
			if !strings.Contains(note, tc.wantNote) {
				t.Errorf("note = %q, want it to contain %q", note, tc.wantNote)
			}
		})
	}
}

// ── W1/W4a: a reply the connection cut in half ───────────────────────────────

func TestRoomsW1_InterruptedReplyStaysVisibleAndMarksItself(t *testing.T) {
	m := v2Model(t)
	m.page.Events = []rooms.Event{
		v2Event(1, "message.user", "", "co robimy?"),
		v2Event(2, "turn.started", "matt", ""),
		v2Event(3, "message.member.delta", "matt", "Zacząłem analizować"),
	}
	m.since, m.page.LatestSeq = 3, 3
	m.working = true

	next, cmd := m.Update(roomsLogMsg{err: w1ResetErr{}, client: &rooms.Client{}})
	if cmd == nil {
		t.Fatal("a dropped socket must arm a redial")
	}
	if next.working {
		t.Error("the room must return to a ready state — nothing is serving that turn")
	}
	if rows := next.streamRows(); len(rows) != 0 {
		t.Errorf("streamRows = %v, want the interrupted reply to stop animating", rows)
	}
	rows := ansi.Strip(strings.Join(next.interruptedRows(), "\n"))
	if !strings.Contains(rows, "interrupted") || !strings.Contains(rows, "matt") {
		t.Fatalf("interrupted rows = %q, want the member and the interruption named", rows)
	}
	if !strings.Contains(rows, "Zacząłem analizować") {
		t.Errorf("interrupted rows = %q, want the partial text that did arrive", rows)
	}
	if !strings.Contains(ansi.Strip(next.View()), "reply interrupted") {
		t.Error("the transcript view must show the marker")
	}

	// The spinner must stop: nothing new is being written.
	if _, spin := next.handleSpinnerTick(); spin != nil {
		t.Error("the spinner kept ticking against a dead turn")
	}
}

func TestRoomsW1_InterruptionMarkerClearsOnlyOnSomethingNew(t *testing.T) {
	m := v2Model(t)
	m.page.Events = []rooms.Event{
		v2Event(1, "turn.started", "matt", ""),
		v2Event(2, "message.member.delta", "matt", "pół zdania"),
	}
	m.since, m.page.LatestSeq = 2, 2

	m, _ = m.Update(roomsLogMsg{err: w1ResetErr{}, client: &rooms.Client{}})
	if len(m.interrupted) != 1 {
		t.Fatalf("interrupted = %+v, want the cut-off reply recorded", m.interrupted)
	}

	// The reconnect replays history the client already saw: that proves
	// nothing about the turn, so the marker must survive.
	replay := m
	replay, _ = replay.Update(roomsLogMsg{page: rooms.LogPage{
		Events:    []rooms.Event{v2Event(2, "message.member.delta", "matt", "pół zdania")},
		Cursor:    2,
		LatestSeq: 2,
	}})
	if len(replay.interrupted) != 1 {
		t.Error("the reconnect's own replay cleared the interruption marker")
	}

	// A member answering again does clear it.
	resumed := replay
	resumed, _ = resumed.Update(roomsLogMsg{page: rooms.LogPage{
		Events:    []rooms.Event{v2Event(3, "message.member", "matt", "skończone po wznowieniu")},
		Cursor:    3,
		LatestSeq: 3,
	}})
	if len(resumed.interrupted) != 0 {
		t.Errorf("interrupted = %+v, want it cleared once the member answered", resumed.interrupted)
	}
}

func TestRoomsW1_InterruptionWithNoOutputSaysSo(t *testing.T) {
	m := v2Model(t)
	m.page.Events = []rooms.Event{v2Event(1, "turn.started", "kowal", "")}
	m.since, m.page.LatestSeq = 1, 1

	m, _ = m.Update(roomsLogMsg{err: w1ResetErr{}, client: &rooms.Client{}})
	rows := ansi.Strip(strings.Join(m.interruptedRows(), "\n"))
	if !strings.Contains(rows, "no output arrived") {
		t.Errorf("interrupted rows = %q, want it to state that nothing arrived", rows)
	}
}

// ── W4c: a long transcript ───────────────────────────────────────────────────

func TestRoomsW4_LongTranscriptStaysWindowedAndScrollable(t *testing.T) {
	m := v2Model(t)
	m.height = 24
	members := []string{"matt", "kowal", "olesno"}
	for i := 1; i <= 150; i++ {
		kind := "message.member"
		profile := members[i%len(members)]
		if i%5 == 0 {
			kind, profile = "message.user", ""
		}
		m.page.Events = append(m.page.Events, v2Event(i, kind, profile, fmt.Sprintf("linia %d treści rozmowy", i)))
	}

	out := m.View()
	if got := strings.Count(out, "\n"); got > m.height {
		t.Fatalf("view is %d lines for a %d-line terminal", got, m.height)
	}
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "earlier line(s) hidden") {
		t.Errorf("the view must say how much is hidden:\n%s", plain)
	}
	if !strings.Contains(plain, "linia 150") {
		t.Errorf("the newest message must stay visible:\n%s", plain)
	}

	// Scrolling reaches the oldest line without ballooning the view.
	scrolled := m
	scrolled, _ = scrolled.scrollTranscript(25)
	if scrolled.scroll != 25 {
		t.Fatalf("scroll = %d, want 25", scrolled.scroll)
	}
	if got := strings.Count(scrolled.View(), "\n"); got > m.height {
		t.Errorf("a scrolled view is %d lines, want it bounded by %d", got, m.height)
	}
	oldest := m
	oldest, _ = oldest.scrollTranscript(len(oldest.transcriptLines()) * 2)
	if !strings.Contains(ansi.Strip(oldest.View()), "linia 1 treści") {
		t.Errorf("scrolling to the top must reach the first message:\n%s", ansi.Strip(oldest.View()))
	}
	if got := strings.Count(oldest.View(), "\n"); got > m.height {
		t.Errorf("the scrolled-to-top view is %d lines, want it bounded by %d", got, m.height)
	}

	// Rendering is stable and cheap: the transcript is windowed, not rebuilt
	// from scratch in a way that degrades with history.
	lines := m.transcriptLines()
	if len(lines) != len(m.transcriptLines()) {
		t.Error("transcriptLines is not stable between calls")
	}
	start := time.Now()
	for i := 0; i < 20; i++ {
		_ = m.View()
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("20 renders took %s, want well under a second per frame", elapsed)
	}
}

// ── coverage: the room lifecycle through a real socket ───────────────────────

func TestRoomsW1_InitLoadsPresetsRoomsAndProfiles(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	m := w1GatewayModel(t, s)

	m = feedCmd(t, m, m.Init())
	if !m.loaded {
		t.Fatalf("Init did not finish loading (err=%v)", m.err)
	}
	if len(m.live) != 1 || m.live[0].RoomID != "sztab" {
		t.Fatalf("live rooms = %+v, want the fake gateway's room", m.live)
	}
	if m.err != nil {
		t.Errorf("init error = %v", m.err)
	}
}

func TestRoomsW1_CreateRoomFromTheInviteList(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	m := w1GatewayModel(t, s)
	m.sub = roomsInvite
	m.selected = map[string]bool{"matt": true, "kowal": true}

	m = feedCmd(t, m, m.createRoom())
	if m.err != nil {
		t.Fatalf("createRoom: %v", m.err)
	}
	if m.openRoomID == "" || m.openRoom == nil {
		t.Fatal("a created room should be opened")
	}
	requireRoomDir(t, m.openRoomID)
	if m.sub != roomsBrowse {
		t.Errorf("sub = %v, want back at the browse list after a create", m.sub)
	}
	if !strings.Contains(m.status, "ready") {
		t.Errorf("status = %q, want the new room reported", m.status)
	}
}

func TestRoomsW1_CreateRoomRejectsAnInvalidRoster(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	m := w1GatewayModel(t, s)
	m.selected = map[string]bool{"matt": true} // one member is below the minimum

	m = feedCmd(t, m, m.createRoom())
	if m.err == nil {
		t.Fatal("a one-member roster must be refused before it reaches the gateway")
	}
	for _, call := range s.Calls() {
		if call == "groups.create" {
			t.Fatal("the invalid roster reached the gateway")
		}
	}
}

func TestRoomsW1_CreateRoomSurfacesADialFailure(t *testing.T) {
	setTestHome(t)
	conn := &config.Connection{ID: "conn-x", Name: "dead", URL: "http://127.0.0.1:1", Type: config.ConnTypeHermes}
	m := newRoomsModel(conn, true)
	m.selected = map[string]bool{"matt": true, "kowal": true}

	m = feedCmd(t, m, m.createRoom())
	if m.err == nil {
		t.Fatal("creating against a dead endpoint must surface an error")
	}
}

func TestRoomsW1_CreateFromPreset(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	m := w1GatewayModel(t, s)
	preset := rooms.Preset{Name: "Pre", RoomID: "pre", Members: rooms.RosterFor([]string{"matt", "kowal"})}

	m = feedCmd(t, m, m.createFromPreset(preset))
	if m.err != nil {
		t.Fatalf("createFromPreset: %v", m.err)
	}
	if m.openRoomID != "pre" {
		t.Fatalf("openRoomID = %q, want the preset's room", m.openRoomID)
	}
	requireRoomDir(t, "pre")
}

func TestRoomsW1_SavePresetFromTheBrowseList(t *testing.T) {
	home := setTestHome(t)
	m := browseModel()
	m.cursor = 1 // the live room follows the preset in browseRows()
	if m.browseRows()[m.cursor].kind != rowLive {
		t.Fatal("test setup: the cursor should sit on the live room")
	}

	next, cmd := m.handleBrowseKey("p")
	if cmd == nil {
		t.Fatal("p should save the room as a predefined one")
	}
	next = feedCmd(t, next, cmd)
	if next.err != nil {
		t.Fatalf("savePreset: %v", next.err)
	}

	path := home + "/.lucinate/rooms.json"
	store, err := rooms.LoadPresets(path)
	if err != nil {
		t.Fatalf("reading the preset store: %v", err)
	}
	if store.Find("Sztab Kowal") == nil {
		t.Fatalf("presets = %+v, want the saved room", store.Presets)
	}
}

func TestRoomsW1_DisbandFromTheBrowseList(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	m := w1GatewayModel(t, s)
	m.loaded = true
	m.live = []rooms.Room{liveRoom("sztab", "Sztab", "matt", "kowal")}
	m.cursor = 0

	next, _ := m.handleBrowseKey("x")
	if next.confirmDisband == "" {
		t.Fatal("x should ask for confirmation, not disband silently")
	}
	next, cmd := next.handleKey(keyRune('y'))
	if cmd == nil {
		t.Fatal("y should confirm and disband")
	}
	next = feedCmd(t, next, cmd)
	if next.err != nil {
		t.Fatalf("disband: %v", next.err)
	}
	for _, call := range s.Calls() {
		if call == "groups.disband" {
			return
		}
	}
	t.Fatalf("disband never reached the gateway (calls: %v)", s.Calls())
}

func TestRoomsW1_SendAndTickAndStateThroughTheModel(t *testing.T) {
	// Without a worktree in the environment the first message carries no
	// project context — but it still carries the room's shared directory
	// and handoff contract, so the routed text is asserted as a suffix
	// rather than as the whole payload.
	t.Setenv("ORCA_WORKTREE_ID", "")
	s := tg.New(t, tg.DefaultOptions())
	m := w1GatewayModel(t, s)
	m.sub = roomsTranscript
	m.openRoomID = "sztab"
	room := liveRoom("sztab", "Sztab", "matt", "kowal")
	m.openRoom = &room

	m.composer = "co robimy?"
	next := feedCmd(t, m, m.send())
	if got := s.Sent(); len(got) != 1 || !strings.HasSuffix(got[0], "co robimy?") {
		t.Fatalf("gateway received %q, want the broadcast text at the end", got)
	} else if !strings.Contains(got[0], "kontrakt handoff pokoju") {
		t.Fatalf("first message does not carry the handoff contract: %q", got[0])
	}
	if !next.working {
		t.Error("sending should mark the roster as answering until the state poll says otherwise")
	}

	// The state poll reads the driver's flag.
	s.SetWorking(false)
	next = feedCmd(t, next, next.loadState())
	if next.err != nil {
		t.Fatalf("loadState: %v", next.err)
	}
	if next.working {
		t.Error("working should follow the driver's flag")
	}

	// The 1.5s poll has to exist for the view to refresh at all; calling it
	// once proves it schedules rather than panics.
	if tick := roomsTick(); tick == nil {
		t.Error("roomsTick produced no command")
	}
	if next2, cmd := next.Update(roomsTickMsg{}); cmd == nil {
		t.Errorf("a tick inside the transcript should schedule the next poll (model: %+v)", next2.reconnecting)
	}
}

// A state poll that times out must be bounded and must not wedge the view.
//
// The failure itself is deliberately not surfaced: the log poll is the primary
// signal, and a driver-status hiccup that painted an error row on every tick
// would be noise. What matters is that the call ends, and that the view
// recovers when the gateway answers again.
func TestRoomsW1_StatePollIsBoundedAndRecovers(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	m := w1GatewayModel(t, s)
	m.sub = roomsTranscript
	m.openRoomID = "sztab"
	room := liveRoom("sztab", "Sztab", "matt", "kowal")
	m.openRoom = &room

	restore := roomsDialer
	roomsDialer = func(ctx context.Context, baseURL, token string) (*rooms.Client, error) {
		c, err := rooms.DialBaseURL(ctx, baseURL, token)
		if err != nil {
			return nil, err
		}
		c.SetTimeout(300 * time.Millisecond)
		return c, nil
	}
	t.Cleanup(func() { roomsDialer = restore })

	s.SetSilent("groups.state")
	start := time.Now()
	m = feedCmd(t, m, m.loadState())
	elapsed := time.Since(start)
	if elapsed > 3*time.Second {
		t.Fatalf("the state poll took %s, want it bounded by the 300ms call timeout", elapsed)
	}
	t.Logf("silent state poll ended after %s (err=%v)", elapsed.Round(time.Millisecond), m.err)

	// The gateway answers again: the view recovers without a restart.
	s.ClearSilent("groups.state")
	s.SetWorking(true)
	m = feedCmd(t, m, m.loadState())
	if !m.working {
		t.Fatalf("working = false after the gateway recovered, want the driver's flag (err=%v)", m.err)
	}
}

func TestRoomsW1_CloseIsNilSafeAndReleasesTheSocket(t *testing.T) {
	var zero roomsModel
	zero.Close()

	s := tg.New(t, tg.DefaultOptions())
	m := w1GatewayModel(t, s)
	m = feedCmd(t, m, m.loadLog("sztab", 0))
	if m.shared.get() == nil {
		t.Fatal("the log load should have opened a connection")
	}
	m.Close()
	if m.shared.get() != nil {
		t.Error("Close should release the cached connection")
	}
	// A model whose holder was never set must not panic either.
	fresh := newRoomsModel(nil, true)
	fresh.shared = nil
	fresh.Close()
}

func TestRoomsW1_SetSizeAndHintStayConsistent(t *testing.T) {
	m := v2Model(t)
	m.setSize(120, 40)
	if m.width != 120 || m.height != 40 {
		t.Fatalf("setSize = %dx%d, want 120x40", m.width, m.height)
	}
	if m.hint() == "" {
		t.Error("the transcript hint must never be empty")
	}
}

// ── rendering contract ───────────────────────────────────────────────────────

// Every event kind the gateway emits has a row: an unhandled kind must still
// render something a reader can act on, not an empty line.
func TestRoomsW1_EveryEventKindRenders(t *testing.T) {
	m := v2Model(t)
	kinds := []string{
		"message.user", "message.member", "turn.started", "turn.settled",
		"turn.failed", "turn.deferred", "room.activity", "member.unavailable",
		"room.created",
	}
	for i, kind := range kinds {
		payload := map[string]any{"text": "tresc", "error": "boom", "reason": "czekam", "status": "start"}
		if kind == "turn.settled" && i%2 == 0 {
			payload["passed"] = true
		}
		ev := rooms.Event{
			Seq: i + 1, Kind: kind, CreatedAt: float64(1700000000 + i),
			Actor:   rooms.Actor{Kind: "member", Profile: "matt", DisplayName: "Matt"},
			Payload: payload,
		}
		for _, line := range strings.Split(m.formatEvent(ev), "\n") {
			if strings.TrimSpace(ansi.Strip(line)) == "" {
				t.Errorf("%s rendered an empty row", kind)
			}
		}
		if got := ansi.Strip(formatRoomsEvent(ev)); got == "" {
			t.Errorf("%s rendered nothing through the plain formatter", kind)
		}
	}
}

func TestRoomsW1_ComposerAndPasteHelpers(t *testing.T) {
	if got := sanitizePaste("a\r\nb\rc\x00d"); got != "a\nb\ncd" {
		t.Errorf("sanitizePaste = %q, want CRLF/CR normalised and control bytes dropped", got)
	}
	if got := deleteLastWord("dwa slowa "); got != "dwa " {
		t.Errorf("deleteLastWord = %q, want the trailing word and spaces gone", got)
	}
	if got := deleteLastWord("jedno"); got != "" {
		t.Errorf("deleteLastWord(single word) = %q, want it cleared", got)
	}

	multi := "pierwsza linia\ndruga linia\ntrzecia"
	summary := composerText(multi)
	if !strings.Contains(summary, "3 lines") || !strings.Contains(summary, "trzecia") {
		t.Errorf("composerText(multi-line) = %q, want the tail plus a size summary", summary)
	}
	if single := composerText("krotko"); single != "krotko" {
		t.Errorf("composerText(single line) = %q, want it unchanged", single)
	}
	if box := composerView(multi); !strings.Contains(ansi.Strip(box), "you ›") {
		t.Errorf("composerView = %q, want the prompt inside its box", box)
	}
	if on := mouseStatusText(true); !strings.Contains(on, "mouse capture on") {
		t.Errorf("mouseStatusText(on) = %q", on)
	}
	if off := mouseStatusText(false); !strings.Contains(off, "mouse capture off") {
		t.Errorf("mouseStatusText(off) = %q", off)
	}
}

func TestRoomsW1_CursorSitsInsideTheComposer(t *testing.T) {
	m := v2Model(t)
	if cur := m.Cursor(); cur == nil || cur.Y <= 0 {
		t.Fatalf("Cursor() = %+v, want a caret inside the composer box", cur)
	}
	m.sub = roomsBrowse
	if m.Cursor() != nil {
		t.Error("the browse screen has no composer, so it must have no caret")
	}
}

func TestRoomsW1_WindowRowsEdges(t *testing.T) {
	if start, end, above, below := windowRows(0, 0, 5); start != 0 || end != 0 || above != 0 || below != 0 {
		t.Errorf("windowRows(empty) = %d,%d,%d,%d", start, end, above, below)
	}
	if start, end, _, _ := windowRows(3, 1, 10); start != 0 || end != 3 {
		t.Errorf("windowRows(short list) = %d,%d, want everything", start, end)
	}
	if start, end, _, _ := windowRows(50, -5, 4); start != 0 || end != 4 {
		t.Errorf("windowRows(negative cursor) = %d,%d, want the first window", start, end)
	}
	if start, end, _, _ := windowRows(50, 99, 0); end-start != 1 {
		t.Errorf("windowRows(size 0) = [%d,%d), want a usable window", start, end)
	}
	start, end, above, below := windowRows(50, 25, 10)
	if end-start != 10 || above != start || below != 50-end {
		t.Errorf("windowRows = [%d,%d) above=%d below=%d", start, end, above, below)
	}
}

func TestRoomsW1_InviteScreenTogglesAndRefusesOverfill(t *testing.T) {
	m := v2Model(t)
	m.sub = roomsInvite
	m.profiles = []string{"matt", "kowal", "olesno", "adam", "ewa", "zoe", "extra"}

	view := ansi.Strip(m.viewInvite())
	if !strings.Contains(view, "Invite profiles") || !strings.Contains(view, "matt") {
		t.Fatalf("invite view = %q, want the profile list", view)
	}

	next, _ := m.handleInviteKey(" ")
	if !next.selected["matt"] {
		t.Error("space must toggle the profile under the cursor")
	}
	next, _ = next.handleInviteKey("a")
	if len(next.selectedProfiles()) != rooms.MaxMembers {
		t.Fatalf("selected = %d, want the ceiling", len(next.selectedProfiles()))
	}
	next, _ = next.handleInviteKey("a")
	if len(next.selectedProfiles()) > rooms.MaxMembers {
		t.Error("select-all must stop at the roster ceiling")
	}
	if next.hint() == "" {
		t.Error("the invite hint must never be empty")
	}
	next, _ = next.handleInviteKey("c")
	if len(next.selectedProfiles()) != 0 {
		t.Error("c must clear the selection")
	}
	next, _ = next.handleInviteKey("esc")
	if next.sub != roomsBrowse {
		t.Error("esc must go back to the browse list")
	}
	// A roster below the floor is refused instead of sent.
	short := v2Model(t)
	short.sub = roomsInvite
	short.selected = map[string]bool{"matt": true}
	short, _ = short.handleInviteKey("enter")
	if short.err == nil {
		t.Error("a one-member roster must be refused at the invite screen")
	}
}

func TestRoomsW1_BrowseStatesAndOrientation(t *testing.T) {
	loading := v2Model(t)
	loading.sub = roomsBrowse
	loading.loaded = false
	loading.presets = rooms.PresetStore{}
	loading.live = nil
	if !strings.Contains(ansi.Strip(loading.View()), "loading rooms") {
		t.Error("an unloaded browse list should say it is loading")
	}

	empty := v2Model(t)
	empty.sub = roomsBrowse
	empty.loaded = true
	if !strings.Contains(ansi.Strip(empty.View()), "no rooms yet") {
		t.Error("an empty browse list should point at the invite flow")
	}

	// A failed load is not "no rooms": the view must say so. v2Model's
	// connection points at a dead endpoint, so this load really fails.
	failed := v2Model(t)
	failed.sub = roomsBrowse
	failed = feedCmd(t, failed, failed.loadAll())
	if failed.err == nil {
		t.Fatal("test setup: loadAll should have failed")
	}
	if failed.loaded {
		t.Error("loaded must stay false when the gateway did not answer")
	}
	if view := ansi.Strip(failed.View()); strings.Contains(view, "no rooms yet") {
		t.Errorf("a failed load must not read as an empty account:\n%s", view)
	}

	// A preset carries its worktree, so the room's first message tells the
	// members where they are. The preset must be saved to the path the
	// lookup actually uses: setTestHome hands out a UNIQUE temp dir per
	// call (v2Model calls it too), so a home captured before v2Model is not
	// the home orientationFor reads — the test then only passed when
	// ORCA_WORKTREE_ID happened to supply the fallback.
	m := v2Model(t)
	store := rooms.PresetStore{}
	if err := store.Add(rooms.Preset{
		Name: "Pre", RoomID: "pre", Worktree: "E:/proj",
		Members: rooms.RosterFor([]string{"matt", "kowal"}),
	}); err != nil {
		t.Fatal(err)
	}
	path, err := rooms.DefaultPresetsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := rooms.SavePresets(path, store); err != nil {
		t.Fatal(err)
	}
	if orient := m.orientationFor("pre"); orient.Empty() {
		t.Errorf("a room seated from a preset with a worktree should carry that project context (presets path %s)", path)
	}
}

func TestRoomsW1_LoadAllWithoutAConnectionReportsIt(t *testing.T) {
	setTestHome(t)
	m := newRoomsModel(nil, true)
	next := feedCmd(t, m, m.loadAll())
	if next.err == nil {
		t.Fatal("loading rooms with no connection must report why")
	}
	if next.loaded {
		t.Error("loaded should only be set by a successful load")
	}
}

// ── keys: the browse list and the transcript ─────────────────────────────────

func TestRoomsW1_BrowseKeysDriveTheList(t *testing.T) {
	m := browseModel()
	m.loaded = true

	m, _ = m.handleBrowseKey("down")
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 after moving down", m.cursor)
	}
	m, _ = m.handleBrowseKey("j")
	if m.cursor != 1 {
		t.Error("j should stop at the end of the list, not wrap")
	}
	m, _ = m.handleBrowseKey("up")
	m, _ = m.handleBrowseKey("k")
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 back at the top", m.cursor)
	}

	if _, cmd := m.handleBrowseKey("r"); cmd == nil {
		t.Error("r should refresh the room list")
	}
	invite, cmd := m.handleBrowseKey("n")
	if invite.sub != roomsInvite || cmd == nil {
		t.Error("n should open the invite list and re-discover profiles")
	}
	if _, cmd := m.handleBrowseKey("enter"); cmd == nil {
		t.Error("enter on a predefined room should start it")
	}

	m.cursor = 1 // the live room
	transcript, cmd := m.handleBrowseKey("enter")
	if transcript.sub != roomsTranscript || transcript.openRoomID != "sztab-kowal" || cmd == nil {
		t.Fatalf("enter on a live room should open its transcript (sub=%v id=%q)", transcript.sub, transcript.openRoomID)
	}
	if transcript.interrupted != nil {
		t.Error("opening a room must not carry another room's interrupted markers")
	}

	confirm, _ := m.handleBrowseKey("x")
	if confirm.confirmDisband == "" {
		t.Fatal("x should ask before disbanding")
	}
	cancelled, _ := confirm.handleKey(keyRune('n'))
	if cancelled.confirmDisband != "" {
		t.Error("any key other than y must cancel the disband confirm")
	}
	if _, cmd := m.handleBrowseKey("esc"); cmd == nil {
		t.Error("esc should leave the rooms view")
	}
}

func TestRoomsW1_TranscriptKeysAndCommands(t *testing.T) {
	m := v2Model(t)

	for _, key := range []string{"up", "down", "pgup", "pgdown", "home", "end", "ctrl+u", "ctrl+w", "backspace"} {
		next, _ := m.handleTranscriptKey(tea.KeyPressMsg{}, key)
		m = next
	}
	if m.scroll != 0 {
		t.Errorf("scroll = %d, want the tail after end", m.scroll)
	}

	typed, _ := m.handleTranscriptKey(tea.KeyPressMsg{Text: "a"}, "a")
	m = typed
	if m.composer != "a" {
		t.Fatalf("composer = %q, want the typed rune", m.composer)
	}
	m, _ = m.handleTranscriptKey(tea.KeyPressMsg{}, "ctrl+u")
	if m.composer != "" {
		t.Error("ctrl+u should clear the draft")
	}

	// A pasted document lands in the composer.
	m, _ = m.handleTranscriptKey(tea.KeyPressMsg{}, "up")
	pasted, _ := m.Update(tea.PasteMsg{Content: "linia 1\r\nlinia 2"})
	if !strings.Contains(pasted.composer, "linia 2") {
		t.Errorf("paste = %q, want the sanitised text", pasted.composer)
	}

	// The wheel scrolls, and the transcript reports the capture mode. A wheel
	// only moves a window that has history behind it, so this model needs one.
	scrollable := transcriptWith(60)
	scrolled, _ := scrollable.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if scrolled.scroll == 0 {
		t.Error("wheel up should scroll back")
	}
	scrolled, _ = scrolled.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if scrolled.scroll != 0 {
		t.Error("wheel down should return to the tail")
	}

	// A command the room does not know is refused, never posted.
	refused, _ := m.handleTranscriptKey(tea.KeyPressMsg{}, "esc") // back to browse
	refused.sub = roomsTranscript
	refused.composer = "/straszna-komenda"
	refused, cmd := refused.handleTranscriptKey(tea.KeyPressMsg{Code: tea.KeyEnter}, "enter")
	if cmd != nil {
		t.Error("an unknown command must not send anything to the room")
	}
	if refused.err == nil {
		t.Error("an unknown command should explain itself")
	}

	// An escaped slash is a message, and /mouse is forwarded to the app.
	m.composer = "\\/mode is a command"
	escaped, cmd := m.handleTranscriptKey(tea.KeyPressMsg{Code: tea.KeyEnter}, "enter")
	if cmd == nil || escaped.composer != "" {
		t.Error("an escaped slash should be sent as a message")
	}
	m.composer = "/mouse off"
	mouse, cmd := m.handleTranscriptKey(tea.KeyPressMsg{Code: tea.KeyEnter}, "enter")
	if cmd == nil {
		t.Error("/mouse should forward the mode change to the app")
	}
	if !strings.Contains(mouse.hint(), "/mouse") && mouse.hint() == "" {
		t.Error("the transcript hint should still render after /mouse")
	}
	left, cmd := m.handleTranscriptCommand("/quit")
	if cmd == nil {
		t.Error("/quit should leave the rooms view")
	}
	if left.sub != roomsTranscript {
		t.Error("/quit returns a message; it must not switch the substate itself")
	}
}
