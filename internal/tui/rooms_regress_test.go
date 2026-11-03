package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/rooms"
)

// findEvent is one member message as the gateway reports it.
func findEvent(seq int, text string, createdAt int64) rooms.Event {
	return rooms.Event{
		RoomID:    "budy-default",
		Seq:       seq,
		EventID:   "evt-" + text,
		Kind:      "message.member",
		Actor:     rooms.Actor{DisplayName: "matt", Profile: "matt"},
		Payload:   map[string]any{"text": text},
		CreatedAt: float64(createdAt),
	}
}

// The live poll/replay can deliver the same page twice. It must not enter
// the transcript twice, or /find prints every match twice.
func TestRoomsLogMsg_DeduplicatesReplayedEvents(t *testing.T) {
	created := time.Date(2025, 1, 2, 22, 55, 0, 0, time.UTC).Unix()
	ev := findEvent(5, "lucinate is ready", created)
	page := rooms.LogPage{Events: []rooms.Event{ev}, Cursor: 5, LatestSeq: 5}

	m := roomsModel{}
	m, _ = m.Update(roomsLogMsg{page: page})
	m, _ = m.Update(roomsLogMsg{page: page})

	if len(m.page.Events) != 1 {
		t.Fatalf("transcript holds %d events, want 1 after a replayed page", len(m.page.Events))
	}
}

// /find must report one line per event and stamp it in local time, exactly
// like the transcript.
func TestRoomsCmdFind_NoDuplicatesAndLocalTime(t *testing.T) {
	created := time.Date(2025, 1, 2, 22, 55, 0, 0, time.UTC).Unix()
	ev := findEvent(5, "lucinate is ready", created)
	page := rooms.LogPage{Events: []rooms.Event{ev}, Cursor: 5, LatestSeq: 5}

	m := roomsModel{}
	m, _ = m.Update(roomsLogMsg{page: page})
	m, _ = m.Update(roomsLogMsg{page: page})

	next, _, handled := m.cmdFind([]string{"lucinate"})
	if !handled {
		t.Fatal("/find was not handled")
	}
	if got := strings.Count(next.notice, "@matt"); got != 1 {
		t.Fatalf("/find reported %d matching lines for one event:\n%s", got, next.notice)
	}
	wantStamp := time.Unix(created, 0).Format("15:04:05")
	if !strings.Contains(next.notice, wantStamp+" @matt:") {
		t.Errorf("/find line is not stamped in local time (%s):\n%s", wantStamp, next.notice)
	}
}

// The invite copy and the footer hint must wrap to the view width instead of
// being cut at the terminal edge.
func TestRoomsView_WrapsHintAndInviteCopyToWidth(t *testing.T) {
	const width = 56

	assertWrapped := func(t *testing.T, m roomsModel, wants []string) {
		t.Helper()
		view := ansi.Strip(m.View())
		for _, line := range strings.Split(view, "\n") {
			if w := ansi.StringWidth(line); w > width {
				t.Errorf("line is %d columns, want <= %d: %q", w, width, line)
			}
		}
		flat := strings.Join(strings.Fields(view), " ")
		for _, want := range wants {
			if !strings.Contains(flat, want) {
				t.Errorf("wrapped view lost %q\ngot:\n%s", want, view)
			}
		}
	}

	invite := roomsModel{width: width, height: 30, sub: roomsInvite, selected: map[string]bool{}}
	assertWrapped(t, invite, []string{
		"seat 2-6 local Hermes profiles. The roster is frozen once the room exists — to change members, seat a new room.",
		"↑/↓ move · space toggle · a all · c clear · r refresh profiles · enter create · esc cancel",
	})

	transcript := roomsModel{width: width, height: 30, sub: roomsTranscript}
	assertWrapped(t, transcript, []string{
		"enter send · ↑↓/PgUp/PgDn/wheel scroll · End live · /help · /mode · /model · /export · /compact · /find · /cost · /rooms back",
	})
}
