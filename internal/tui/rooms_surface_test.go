package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/config"
	"github.com/lucinate-ai/lucinate/internal/rooms"
	tg "github.com/lucinate-ai/lucinate/internal/testgateway"
)

// The rooms surface is the product: creating a room lands in it, the
// transcript drives routing and approvals from keys, and the whole thing has
// to stay inside 80x24. These pin the four behaviours, each through the same
// Update/View path the TUI uses.

// ── create enters the room ───────────────────────────────────────────────────

func TestRoomsCreate_LandsInTheCreatedTranscript(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	m := w1GatewayModel(t, s)
	m.width, m.height = 80, 24
	m.sub = roomsInvite
	m.selected = map[string]bool{"matt": true, "kowal": true}

	m = feedCmd(t, m, m.createRoom())
	if m.err != nil {
		t.Fatalf("createRoom: %v", m.err)
	}
	if m.openRoomID == "" || m.openRoom == nil {
		t.Fatal("a created room should be open")
	}
	if m.sub != roomsTranscript {
		t.Fatalf("sub = %v, want the created room's transcript without a second enter", m.sub)
	}

	view := ansi.Strip(m.View())
	for _, want := range []string{
		"room " + m.openRoomID + " ready",
		"@" + m.openRoom.Members[0].Handle,
		"m: mode",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("transcript after create is missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "you ›") {
		t.Errorf("the composer is off-screen at 80x24:\n%s", view)
	}
}

// The error path still belongs to the browse list: a failed create has no
// room to enter, and the roster picker is where the user can retry.
func TestRoomsCreate_ErrorStaysOnTheBrowseList(t *testing.T) {
	m := browseModel()
	m.sub = roomsInvite

	next, _ := m.Update(roomsCreatedMsg{err: fmt.Errorf("gateway refused the room")})
	if next.sub != roomsBrowse {
		t.Fatalf("sub = %v, want browse after a failed create", next.sub)
	}
	if next.err == nil {
		t.Fatal("a failed create must surface its error")
	}
}

// The picker's count comes from the rooms view's last load, never from a
// guess: an unloaded session reports -1.
func TestRoomsLiveRoomCount(t *testing.T) {
	if got := (roomsModel{}).liveRoomCount(); got != -1 {
		t.Fatalf("unloaded liveRoomCount = %d, want -1", got)
	}
	if got := browseModel().liveRoomCount(); got != 1 {
		t.Fatalf("loaded liveRoomCount = %d, want the one live room", got)
	}
}

// ── mode key ─────────────────────────────────────────────────────────────────

func TestRoomsTranscript_MKeyCyclesTheRoutingMode(t *testing.T) {
	m := v2Model(t)
	m.prefs.Update("sztab", func(p *rooms.RoomPrefs) { p.Mode = string(rooms.RouteBroadcast) })

	steps := []struct {
		mode rooms.RoutingMode
		want string
	}{
		{rooms.RouteModerator, "moderator"},
		{rooms.RouteRoundRobin, "round-robin"},
		{rooms.RouteBroadcast, "broadcast"},
	}
	for _, step := range steps {
		next, _ := m.handleKey(keyRune('m'))
		if got := next.roomPrefs().RoutingMode(); got != step.mode {
			t.Fatalf("mode after m = %q, want %q", got, step.mode)
		}
		// The roster line is where the current mode is rendered.
		if view := ansi.Strip(next.View()); !strings.Contains(view, step.want) {
			t.Errorf("view does not render mode %q:\n%s", step.want, view)
		}
		m = next
	}
}

// 'm' is a letter in a message first: a draft keeps it.
func TestRoomsTranscript_MKeyTypesWhenADraftExists(t *testing.T) {
	m := v2Model(t)
	m.composer = "s"

	next, _ := m.handleKey(keyRune('m'))
	if next.composer != "sm" {
		t.Fatalf("composer = %q, want the draft to keep the letter", next.composer)
	}
}

// 'x' is only a shortcut while the room is blocked on one member; otherwise
// it is a letter like any other (and never the browse list's disband).
func TestRoomsTranscript_XTypesWhenNothingIsBlocked(t *testing.T) {
	m := v2Model(t)

	next, _ := m.handleKey(keyRune('x'))
	if next.composer != "x" {
		t.Fatalf("composer = %q, want x typed into the draft", next.composer)
	}
	if next.confirmDisband != "" {
		t.Fatal("x must not arm a disband from the transcript")
	}
}

// ── key-driven approval ──────────────────────────────────────────────────────

func blockedModel(t *testing.T) (roomsModel, *tg.Server) {
	t.Helper()
	s := tg.New(t, tg.DefaultOptions())
	s.SetPendingApproval("kowal", "req-7")

	m := v2Model(t)
	m.conn = &config.Connection{ID: "conn-1", Name: "fake", URL: s.URL(), Type: config.ConnTypeHermes}
	m.pending = []rooms.PendingAction{{
		Kind: "approval", MemberID: "m2", TaskID: "task-kowal",
		ExecutionGeneration: 1, RequestID: "req-7",
	}}
	return m, s
}

func TestRoomsTranscript_EnterApprovesTheSingleBlockedMember(t *testing.T) {
	m, s := blockedModel(t)

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on a single blocked member should dispatch an approval")
	}
	next = feedCmd(t, next, cmd)
	if next.err != nil {
		t.Fatalf("approve: %v", next.err)
	}
	if got := s.ApproveCalls(); len(got) != 1 || got[0] != "kowal=once" {
		t.Fatalf("approve calls = %v, want kowal=once", got)
	}
	if !strings.Contains(next.status, "approved @kowal") {
		t.Errorf("status = %q, want the approval reported", next.status)
	}
}

func TestRoomsTranscript_XDeniesTheSingleBlockedMember(t *testing.T) {
	m, s := blockedModel(t)

	next, cmd := m.Update(keyRune('x'))
	if cmd == nil {
		t.Fatal("x on a single blocked member should dispatch a denial")
	}
	next = feedCmd(t, next, cmd)
	if next.err != nil {
		t.Fatalf("deny: %v", next.err)
	}
	if got := s.ApproveCalls(); len(got) != 1 || got[0] != "kowal=deny" {
		t.Fatalf("approve calls = %v, want kowal=deny", got)
	}
	// x is disband in the browse list; in the transcript it must not arm it.
	if next.confirmDisband != "" {
		t.Fatalf("x armed a disband confirm in the transcript (%q)", next.confirmDisband)
	}
}

// Several blocked members keep the explicit command: the keys must not guess
// which turn the user meant.
func TestRoomsTranscript_ShortcutRefusedWithSeveralBlockedMembers(t *testing.T) {
	m := v2Model(t)
	m.pending = []rooms.PendingAction{
		{Kind: "approval", MemberID: "m1", TaskID: "t1", RequestID: "r1"},
		{Kind: "approval", MemberID: "m2", TaskID: "t2", RequestID: "r2"},
	}

	if _, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Fatal("enter must not approve when several members are blocked")
	}
	if _, cmd := m.handleKey(keyRune('x')); cmd != nil {
		t.Fatal("x must not deny when several members are blocked")
	}
	hint := m.hint()
	if !strings.Contains(hint, "several blocked — /approve @handle once|deny") {
		t.Errorf("hint does not point at the explicit form: %q", hint)
	}
}

// A retry-only block is not an approval: enter and x stay inert.
func TestRoomsTranscript_ShortcutIgnoredForARetryableTask(t *testing.T) {
	m := v2Model(t)
	m.pending = []rooms.PendingAction{{Kind: "retry", MemberID: "m2", TaskID: "t"}}

	if _, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Fatal("enter must not act on a retryable task")
	}
	if _, cmd := m.handleKey(keyRune('x')); cmd != nil {
		t.Fatal("x must not act on a retryable task")
	}
}

func TestRoomsTranscript_HintNamesTheModeAndApprovalKeys(t *testing.T) {
	m, _ := blockedModel(t)
	hint := m.hint()
	for _, want := range []string{"m: mode", "enter: approve once · x: deny"} {
		if !strings.Contains(hint, want) {
			t.Errorf("transcript hint missing %q: %q", want, hint)
		}
	}
}

// A draft still goes to the room: enter posts it rather than approving.
func TestRoomsTranscript_EnterKeepsSendingADraft(t *testing.T) {
	m, s := blockedModel(t)
	m.composer = "co robimy?"

	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter with a draft should dispatch the message")
	}
	if next.composer != "" {
		t.Errorf("composer = %q, want it cleared by the send", next.composer)
	}
	if got := s.ApproveCalls(); len(got) != 0 {
		t.Fatalf("typing a message approved something: %v", got)
	}
}

// ── 80x24 ────────────────────────────────────────────────────────────────────

// The approval hint must not push the composer off a 24-row terminal.
func TestRoomsTranscript_BlockedRoomFits80x24(t *testing.T) {
	m, _ := blockedModel(t)
	m.width, m.height = 80, 24
	m.page.Events = []rooms.Event{v2Event(1, "message.member", "matt", "gotowe")}

	out := ansi.Strip(m.View())
	if !strings.Contains(out, "you ›") {
		t.Fatalf("the composer is off-screen at 80x24:\n%s", out)
	}
	if n := strings.Count(out, "\n"); n > m.height {
		t.Fatalf("view is %d lines for a %d-line terminal:\n%s", n, m.height, out)
	}
	if !strings.Contains(out, "enter: approve once") {
		t.Errorf("the hint lost the approval keys:\n%s", out)
	}
}
