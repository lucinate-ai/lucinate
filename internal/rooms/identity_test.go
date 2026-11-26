package rooms

import "testing"

func testRoster() []Member {
	return RosterFor([]string{"alojz", "default"})
}

func TestMemberHandle_MemberIDWins(t *testing.T) {
	roster := testRoster()
	// member_index 0 would be alojz; the id says m2, so the id must win.
	ev := Event{
		Kind:    "turn.settled",
		Actor:   Actor{Kind: "gateway", ID: "install:abc"},
		Payload: map[string]any{"member_id": "m2", "member_index": float64(0)},
	}
	if got := MemberHandle(ev, roster); got != "default" {
		t.Fatalf("MemberHandle = %q, want default", got)
	}
}

func TestMemberHandle_MemberIndexFallback(t *testing.T) {
	roster := testRoster()
	ev := Event{
		Kind:    "turn.started",
		Actor:   Actor{Kind: "gateway", ID: "install:abc"},
		Payload: map[string]any{"member_index": float64(0)},
	}
	if got := MemberHandle(ev, roster); got != "alojz" {
		t.Fatalf("MemberHandle = %q, want alojz", got)
	}
}

func TestMemberHandle_UnknownMemberIsEmpty(t *testing.T) {
	roster := testRoster()
	ev := Event{
		Kind:    "turn.cancelled",
		Actor:   Actor{Kind: "gateway", ID: "install:abc"},
		Payload: map[string]any{"member_id": "m9", "member_index": float64(7)},
	}
	if got := MemberHandle(ev, roster); got != "" {
		t.Fatalf("MemberHandle = %q, want an empty fallback", got)
	}
}

// A member message names its author in the actor, so it resolves even without
// a roster or a payload id.
func TestMemberHandle_MemberMessageUsesActor(t *testing.T) {
	ev := Event{
		Kind:  "message.member",
		Actor: Actor{Kind: "member", ID: "m1", Profile: "alojz", DisplayName: "Alojz"},
	}
	if got := MemberHandle(ev, nil); got != "alojz" {
		t.Fatalf("MemberHandle = %q, want alojz", got)
	}
}
