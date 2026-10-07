package rooms

import "testing"

func stEvent(seq int, kind, profile string, payload map[string]any) Event {
	return Event{
		RoomID: "sztab", Seq: seq, Kind: kind,
		Actor:     Actor{Kind: "member", ID: "m1", Profile: profile, DisplayName: displayNameFor(profile)},
		Payload:   payload,
		CreatedAt: float64(1700000000 + seq),
	}
}

// A member's reply arrives as a run of partial events and ends on the full
// message; the view must follow it without ever showing two rows for one turn.
func TestStreamsFor_TracksAStreamingReply(t *testing.T) {
	events := []Event{
		stEvent(1, "turn.started", "matt", nil),
		stEvent(2, "message.member.delta", "matt", map[string]any{"text": "Hel"}),
		stEvent(3, "message.member.delta", "matt", map[string]any{"text": "Hello the"}),
		stEvent(4, "message.member", "matt", map[string]any{"text": "Hello there"}),
	}
	got := StreamsFor(events)
	if len(got) != 1 {
		t.Fatalf("StreamsFor = %d streams, want 1: %+v", len(got), got)
	}
	s := got[0]
	if s.Member != "matt" || s.Profile != "matt" {
		t.Errorf("stream member = %q/%q, want matt", s.Member, s.Profile)
	}
	if s.Text != "Hello there" {
		t.Errorf("text = %q, want the finished message", s.Text)
	}
	if !s.Started || !s.Settled || s.Empty {
		t.Errorf("stream state = started:%v settled:%v empty:%v, want started+settled and not empty", s.Started, s.Settled, s.Empty)
	}
}

// Room deltas are cumulative like the chat backend's, so a later delta
// replaces the text rather than duplicating it. A delta that explicitly asks
// to be appended is honoured — a gateway may send either shape.
func TestStreamsFor_DeltasReplaceUnlessTheyAskToBeAppended(t *testing.T) {
	events := []Event{
		stEvent(1, "turn.started", "matt", nil),
		stEvent(2, "message.member.delta", "matt", map[string]any{"text": "Hel"}),
		stEvent(3, "message.member.delta", "matt", map[string]any{"text": "Hello"}),
	}
	if got := StreamsFor(events)[0].Text; got != "Hello" {
		t.Errorf("cumulative delta text = %q, want %q", got, "Hello")
	}

	events = append(events, stEvent(4, "message.member.delta", "matt", map[string]any{"text": " there", "append": true}))
	if got := StreamsFor(events)[0].Text; got != "Hello there" {
		t.Errorf("appending delta text = %q, want %q", got, "Hello there")
	}
}

// An empty answer is a real outcome (the member passed on the turn); the
// placeholder must be dropped instead of spinning forever.
func TestStreamsFor_EmptyReplyIsMarkedEmpty(t *testing.T) {
	events := []Event{
		stEvent(1, "turn.started", "matt", nil),
		stEvent(2, "turn.settled", "matt", map[string]any{"passed": true}),
	}
	got := StreamsFor(events)
	if len(got) != 1 {
		t.Fatalf("streams = %+v, want one", got)
	}
	if !got[0].Empty {
		t.Errorf("stream = %+v, want Empty", got[0])
	}
	if len(ActiveStreams(events)) != 0 {
		t.Errorf("ActiveStreams = %+v, want none once the turn settled", ActiveStreams(events))
	}
}

func TestStreamsFor_MemberMessageWithoutTextIsEmpty(t *testing.T) {
	events := []Event{
		stEvent(1, "turn.started", "kowal", nil),
		stEvent(2, "message.member", "kowal", map[string]any{"text": ""}),
	}
	got := StreamsFor(events)
	if len(got) != 1 || !got[0].Empty {
		t.Fatalf("streams = %+v, want one empty stream", got)
	}
}

func TestStreamsFor_UnavailableMemberSettlesEmpty(t *testing.T) {
	events := []Event{
		stEvent(1, "turn.started", "ghost", nil),
		stEvent(2, "member.unavailable", "ghost", map[string]any{"reason": "not local"}),
	}
	got := StreamsFor(events)
	if len(got) != 1 || !got[0].Settled || !got[0].Empty {
		t.Fatalf("streams = %+v, want one settled+empty stream", got)
	}
}

func TestStreamsFor_FailedTurnKeepsThePartialText(t *testing.T) {
	events := []Event{
		stEvent(1, "turn.started", "matt", nil),
		stEvent(2, "message.member.delta", "matt", map[string]any{"text": "half an ans"}),
		stEvent(3, "turn.failed", "matt", map[string]any{"error": "timeout"}),
	}
	got := StreamsFor(events)
	if len(got) != 1 || !got[0].Settled {
		t.Fatalf("streams = %+v, want one settled stream", got)
	}
	if got[0].Text != "half an ans" {
		t.Errorf("text = %q, want the partial text kept for the failure notice", got[0].Text)
	}
	if got[0].Empty {
		t.Error("a failed turn with partial text is not an empty reply")
	}
}

func TestStreamsFor_KeepsFirstAppearanceOrder(t *testing.T) {
	events := []Event{
		stEvent(1, "turn.started", "kowal", nil),
		stEvent(2, "turn.started", "matt", nil),
		stEvent(3, "message.member", "matt", map[string]any{"text": "ja pierwszy"}),
		stEvent(4, "message.member", "kowal", map[string]any{"text": "ja drugi"}),
	}
	got := StreamsFor(events)
	if len(got) != 2 {
		t.Fatalf("streams = %+v, want 2", got)
	}
	if got[0].Member != "kowal" || got[1].Member != "matt" {
		t.Errorf("order = %s, %s; want the order they started in", got[0].Member, got[1].Member)
	}
}

func TestStreamsFor_NewTurnResetsTheText(t *testing.T) {
	events := []Event{
		stEvent(1, "turn.started", "matt", nil),
		stEvent(2, "message.member", "matt", map[string]any{"text": "pierwsza odpowiedz"}),
		stEvent(3, "turn.started", "matt", nil),
	}
	got := StreamsFor(events)
	if len(got) != 1 {
		t.Fatalf("streams = %+v, want one entry per member", got)
	}
	if got[0].Text != "" || got[0].Settled {
		t.Errorf("stream = %+v, want a fresh in-flight turn", got[0])
	}
	if len(ActiveStreams(events)) != 1 {
		t.Errorf("ActiveStreams = %+v, want the live turn", ActiveStreams(events))
	}
}

func TestStreamsFor_UsesThePayloadHandleWhenPresent(t *testing.T) {
	ev := stEvent(1, "turn.started", "", map[string]any{"handle": "olesno-lead-ops"})
	got := StreamsFor([]Event{ev})
	if len(got) != 1 || got[0].Member != "olesno-lead-ops" {
		t.Fatalf("streams = %+v, want the payload handle", got)
	}
	if got[0].Label() != "@olesno-lead-ops" {
		t.Errorf("Label() = %q", got[0].Label())
	}
}

func TestIsStreamDelta(t *testing.T) {
	tests := []struct {
		name string
		ev   Event
		want bool
	}{
		{name: "kind", ev: stEvent(1, "message.member.delta", "matt", nil), want: true},
		{name: "partial flag", ev: stEvent(2, "message.member", "matt", map[string]any{"partial": true}), want: true},
		{name: "streaming flag", ev: stEvent(3, "message.member", "matt", map[string]any{"streaming": true}), want: true},
		{name: "final message", ev: stEvent(4, "message.member", "matt", map[string]any{"text": "gotowe"}), want: false},
		{name: "turn event", ev: stEvent(5, "turn.started", "matt", nil), want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsStreamDelta(tc.ev); got != tc.want {
				t.Errorf("IsStreamDelta(%s) = %v, want %v", tc.ev.Kind, got, tc.want)
			}
		})
	}
}

func TestStreamsFor_IgnoresUserTurnsWhenTrackingReplies(t *testing.T) {
	events := []Event{
		{Kind: "message.user", Seq: 1, Payload: map[string]any{"text": "co robimy?"}},
		stEvent(2, "turn.started", "matt", nil),
	}
	got := StreamsFor(events)
	if len(got) != 1 || got[0].Member != "matt" {
		t.Fatalf("streams = %+v, want only the member turn", got)
	}
}
