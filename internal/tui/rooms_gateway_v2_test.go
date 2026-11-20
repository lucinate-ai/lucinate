package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/config"
	"github.com/lucinate-ai/lucinate/internal/rooms"
	tg "github.com/lucinate-ai/lucinate/internal/testgateway"
)

// The durable room log never carries member text deltas on a real gateway
// (upstream keeps them out of it), so member_activity is the only live signal.
// Without this row a room looks silent while a member is working.
func TestRoomsV2_ActivityRowNamesTheAnsweringMember(t *testing.T) {
	m := v2Model(t)
	m.activity = []rooms.MemberActivity{{MemberID: "m1", Status: "running"}}

	rows := m.streamRows()
	if len(rows) != 1 || !strings.Contains(ansi.Strip(rows[0]), "@matt") {
		t.Fatalf("stream rows = %q, want a working row for @matt", rows)
	}
	if !m.spinnerActive() {
		t.Error("an active member should animate the spinner")
	}
	if got := ansi.Strip(m.View()); !strings.Contains(got, "@matt") {
		t.Errorf("the view does not render the working member:\n%s", got)
	}
}

// A table row already streaming from a delta log must not be duplicated by the
// activity list — the two sources describe the same member.
func TestRoomsV2_ActivityDoesNotDuplicateADeltaStream(t *testing.T) {
	m := v2Model(t)
	m.page.Events = []rooms.Event{
		{RoomID: "sztab", Seq: 1, Kind: "turn.started", Actor: rooms.Actor{Kind: "member", ID: "m1", Profile: "matt"}},
		{RoomID: "sztab", Seq: 2, Kind: "message.member", Actor: rooms.Actor{Kind: "member", ID: "m1", Profile: "matt"},
			Payload: map[string]any{"text": "Pracuje", "partial": true}},
	}
	m.activity = []rooms.MemberActivity{{MemberID: "m1", Status: "running"}}

	rows := m.streamRows()
	if len(rows) != 1 {
		t.Fatalf("stream rows = %d (%q), want one row for @matt", len(rows), rows)
	}
}

func TestRoomsV2_BlockedBannerNamesTheMemberAndTheCommand(t *testing.T) {
	m := v2Model(t)
	m.pending = []rooms.PendingAction{{
		Kind: "approval", MemberID: "m2", TaskID: "task-kowal",
		ExecutionGeneration: 1, RequestID: "req-1",
	}}

	view := ansi.Strip(m.View())
	for _, want := range []string{"blocked:", "@kowal", "/approve @kowal once|deny"} {
		if !strings.Contains(view, want) {
			t.Errorf("blocked banner missing %q:\n%s", want, view)
		}
	}
}

func TestRoomsV2_ApproveWithNothingPendingIsRefused(t *testing.T) {
	m := v2Model(t)

	next, cmd, handled := m.handleRoomUXCommand("/approve @kowal once")
	if !handled {
		t.Fatal("/approve should be handled")
	}
	if cmd != nil {
		t.Fatalf("/approve with nothing pending dialled the gateway (%T)", cmd())
	}
	if next.err == nil || !strings.Contains(next.err.Error(), "no pending action") {
		t.Fatalf("err = %v, want a refusal naming the missing action", next.err)
	}
}

func TestRoomsV2_ApproveResolvesTheExactAction(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	s.SetPendingApproval("kowal", "req-7")

	m := v2Model(t)
	m.conn = &config.Connection{ID: "conn-1", Name: "fake", URL: s.URL(), Type: config.ConnTypeHermes}
	m.pending = []rooms.PendingAction{{
		Kind: "approval", MemberID: "m2", TaskID: "task-kowal",
		ExecutionGeneration: 1, RequestID: "req-7",
	}}

	next, cmd, handled := m.handleRoomUXCommand("/approve @kowal once")
	if !handled || cmd == nil {
		t.Fatalf("handled=%v cmd=%v, want a dispatched approval", handled, cmd)
	}
	next = feedCmd(t, next, cmd)
	if next.err != nil {
		t.Fatalf("approve: %v", next.err)
	}
	if got := s.ApproveCalls(); len(got) != 1 || got[0] != "kowal=once" {
		t.Fatalf("approve calls = %v, want kowal=once", got)
	}
	if !strings.Contains(next.status, "approved @kowal") {
		t.Errorf("status = %q", next.status)
	}
}

func TestRoomsV2_ApproveRefusesAnUnknownChoice(t *testing.T) {
	m := v2Model(t)
	m.pending = []rooms.PendingAction{{Kind: "approval", MemberID: "m2", TaskID: "t", RequestID: "r"}}

	next, cmd, _ := m.handleRoomUXCommand("/approve @kowal sometimes")
	if cmd != nil {
		t.Fatal("an invalid choice must not dial")
	}
	if next.err == nil || !strings.Contains(next.err.Error(), "once or deny") {
		t.Fatalf("err = %v, want the accepted choices", next.err)
	}
}

func TestRoomsV2_RetryWithNothingToRetryIsRefused(t *testing.T) {
	m := v2Model(t)

	next, cmd, handled := m.handleRoomUXCommand("/retry")
	if !handled || cmd != nil {
		t.Fatalf("handled=%v cmd=%v, want a local refusal", handled, cmd)
	}
	if next.err == nil || !strings.Contains(next.err.Error(), "nothing to retry") {
		t.Fatalf("err = %v", next.err)
	}
}

func TestRoomsV2_RetryDispatchesTheTaskCoordinates(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	s.SetPendingRetry("matt")

	m := v2Model(t)
	m.conn = &config.Connection{ID: "conn-1", Name: "fake", URL: s.URL(), Type: config.ConnTypeHermes}
	m.pending = []rooms.PendingAction{{
		Kind: "retry", MemberID: "m1", TaskID: "task-matt", ExecutionGeneration: 1,
	}}

	next, cmd, _ := m.handleRoomUXCommand("/retry")
	if cmd == nil {
		t.Fatal("/retry should dispatch")
	}
	next = feedCmd(t, next, cmd)
	if next.err != nil {
		t.Fatalf("retry: %v", next.err)
	}
	if got := s.RetryCalls(); len(got) != 1 || got[0] != "matt" {
		t.Fatalf("retry calls = %v, want matt", got)
	}
}

// A turn set pinned on one member blocks every later message; /stop is the
// escape hatch and must leave the room usable.
func TestRoomsV2_StopCancelsTurnsAndReturnsToReady(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())

	m := v2Model(t)
	m.conn = &config.Connection{ID: "conn-1", Name: "fake", URL: s.URL(), Type: config.ConnTypeHermes}
	m.working = true
	m.activity = []rooms.MemberActivity{{MemberID: "m1", Status: "running"}}

	next, cmd, _ := m.handleRoomUXCommand("/stop")
	if cmd == nil {
		t.Fatal("/stop should dispatch")
	}
	next = feedCmd(t, next, cmd)
	if next.err != nil {
		t.Fatalf("stop: %v", next.err)
	}
	if next.working {
		t.Error("the room still reports working after /stop")
	}
	if len(next.activity) != 0 {
		t.Errorf("activity = %+v, want none after /stop", next.activity)
	}
	if !strings.Contains(next.status, "cancelled 1 turn") {
		t.Errorf("status = %q, want the cancelled count", next.status)
	}
}

func TestRoomsV2_HelpListsApprovalsRetryStop(t *testing.T) {
	m := v2Model(t)
	m.notice = ""

	next, _, handled := m.handleRoomUXCommand("/help")
	if !handled {
		// /help lives in rooms.go, not the UX handler; call the transcript
		// command entry point the composer uses.
		next, _ = m.handleTranscriptCommand("/help")
	}
	for _, want := range []string{"/approve", "/retry", "/stop"} {
		if !strings.Contains(next.notice, want) {
			t.Errorf("/help missing %q:\n%s", want, next.notice)
		}
	}
}

// The banner clears only when the gateway's next poll says the action is
// gone — never optimistically when the command is typed.
func TestRoomsV2_BlockedBannerClearsOnTheNextStatePoll(t *testing.T) {
	m := v2Model(t)
	m.pending = []rooms.PendingAction{{Kind: "approval", MemberID: "m2", TaskID: "t", RequestID: "r"}}

	if !strings.Contains(ansi.Strip(m.View()), "blocked:") {
		t.Fatal("the banner did not render before the poll")
	}
	next, _ := m.Update(roomsStateMsg{blocked: false})
	if strings.Contains(ansi.Strip(next.View()), "blocked:") {
		t.Errorf("the banner survived a poll with no pending actions:\n%s", ansi.Strip(next.View()))
	}
}

// An interrupted reply keeps its own marker row; the state poll must not stack
// a spinner on top of it.
func TestRoomsV2_ActivitySkipsAnInterruptedMember(t *testing.T) {
	m := v2Model(t)
	m.activity = []rooms.MemberActivity{{MemberID: "m1", Status: "running"}}
	m.interrupted = map[string]interruptedTurn{"matt": {Member: "matt", Text: "Pracuje", Reason: "connection lost"}}

	for _, row := range m.streamRows() {
		if strings.Contains(ansi.Strip(row), "@matt") {
			t.Fatalf("a working row was rendered for an interrupted member: %q", row)
		}
	}
}

func TestRoomsV2_ApproveRefusesAnUnknownMember(t *testing.T) {
	m := v2Model(t)
	next, cmd, _ := m.handleRoomUXCommand("/approve @ghost once")
	if cmd != nil {
		t.Fatal("an unknown handle must not dial")
	}
	if next.err == nil || !strings.Contains(next.err.Error(), "no member @ghost") {
		t.Fatalf("err = %v", next.err)
	}
}
