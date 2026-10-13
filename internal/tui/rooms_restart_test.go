package tui

import (
	"strings"
	"testing"

	"github.com/lucinate-ai/lucinate/internal/rooms"
	tg "github.com/lucinate-ai/lucinate/internal/testgateway"
)

// /restart keeps the roster and starts it in a new room. The old room is left
// alone — a restart is not a delete — and the new room gets its own shared
// directory, per the handoff contract.

// restartModel opens an existing room with a roster and a short transcript,
// which is exactly the state /restart restarts from.
func restartModel(t *testing.T, s *tg.Server) (roomsModel, rooms.Room) {
	t.Helper()
	m := w1GatewayModel(t, s)
	room := liveRoom("stary-pokoj", "Stary pokój", "matt", "kowal")
	m.sub = roomsTranscript
	m.openRoomID = room.RoomID
	m.openRoom = &room
	m.page = rooms.LogPage{Events: []rooms.Event{
		v2Event(1, "message.user", "", "ustalamy zakres"),
		v2Event(2, "message.member", "matt", "decyzja: INDEX w katalogu pokoju"),
	}}
	m.page.LatestSeq = 2
	return m, room
}

func TestRoomsRestart_SameRosterOpensANewRoomAndKeepsTheOld(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	m, old := restartModel(t, s)

	m, cmd := m.handleTranscriptCommand("/restart")
	if cmd == nil {
		t.Fatal("/restart armed no command — the room was never recreated")
	}
	m = feedCmd(t, m, cmd)
	if m.err != nil {
		t.Fatalf("/restart: %v", m.err)
	}
	if m.openRoomID == "" || m.openRoomID == old.RoomID {
		t.Fatalf("open room = %q, want a NEW room (old was %q)", m.openRoomID, old.RoomID)
	}
	if m.sub != roomsTranscript {
		t.Errorf("view = %v, want the transcript of the new room", m.sub)
	}
	// The roster is what a restart exists for.
	for _, handle := range []string{"matt", "kowal"} {
		if _, ok := s.MemberRow(handle); !ok {
			t.Errorf("roster member %q is missing from the new room", handle)
		}
	}
	requireRoomDir(t, m.openRoomID)
	for _, call := range s.Calls() {
		if call == "groups.disband" {
			t.Fatal("/restart disbanded the old room — restarting must not delete anything")
		}
	}
}

func TestRoomsRestart_WithSummaryCarriesTheOldRoomAsFirstMessage(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	m, old := restartModel(t, s)

	m, cmd := m.handleTranscriptCommand("/restart with-summary")
	if cmd == nil {
		t.Fatal("/restart with-summary armed no command")
	}
	// Two hops: the restart opens the room, its result arms the first send.
	msgs := runCmdMsgs(cmd)
	if len(msgs) != 1 {
		t.Fatalf("restart produced %d messages, want 1", len(msgs))
	}
	next, follow := m.Update(msgs[0])
	if next.err != nil {
		t.Fatalf("restart: %v", next.err)
	}
	if next.openRoomID == old.RoomID || next.openRoomID == "" {
		t.Fatalf("open room = %q, want a new room", next.openRoomID)
	}
	if follow == nil {
		t.Fatal("with-summary must arm the hand-over message")
	}
	m = feedCmd(t, m, follow)

	sent := s.Sent()
	if len(sent) == 0 {
		t.Fatal("the new room got no message — with-summary must post one")
	}
	first := sent[0]
	for _, want := range []string{old.RoomID, "INDEX", "katalog", "decyzja"} {
		if !strings.Contains(first, want) {
			t.Errorf("hand-over message is missing %q:\n%s", want, first)
		}
	}
	// The new room is also told where its own shared directory is.
	if !strings.Contains(first, m.openRoomID) {
		t.Errorf("hand-over message does not name the new room %q", m.openRoomID)
	}
}

func TestRoomsRestart_CarriesTheMemberModels(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	s.SetMemberModelSupported(true)
	m, _ := restartModel(t, s)
	m.memberModels = map[string]string{"matt": "gpt-5-mini"}

	m, cmd := m.handleTranscriptCommand("/restart")
	if cmd == nil {
		t.Fatal("/restart armed no command")
	}
	m = feedCmd(t, m, cmd)
	if m.err != nil {
		t.Fatalf("/restart: %v", m.err)
	}
	calls := s.MemberModelCalls()
	if len(calls) != 1 || calls[0] != "matt=gpt-5-mini" {
		t.Errorf("member models seated = %v, want [matt=gpt-5-mini]", calls)
	}
}

func TestRoomsRestart_RefusesAModeItDoesNotKnow(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	m, _ := restartModel(t, s)

	m, cmd := m.handleTranscriptCommand("/restart bumpa")
	if cmd != nil {
		t.Error("an unknown /restart argument armed a command — it must be refused")
	}
	if m.err == nil || !strings.Contains(m.err.Error(), "with-summary") {
		t.Errorf("err = %v, want a refusal naming the accepted modes", m.err)
	}
}

func TestRoomsRestart_RefusesWithoutAnOpenRoom(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	m := w1GatewayModel(t, s)

	m, cmd := m.handleTranscriptCommand("/restart")
	if cmd != nil {
		t.Error("no open room, but a command was armed anyway")
	}
	if m.err == nil {
		t.Error("/restart without an open room must explain itself, not fail silently")
	}
}
