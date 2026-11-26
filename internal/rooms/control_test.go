package rooms

import "testing"

// A gateway installation id is not a member and must never be rendered as one.
func TestIsInstallationID(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"install:6361e5ff3c924dcd8c742d702c4921da", true},
		{" install:abc", true},
		{"alojz", false},
		{"m1", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsInstallationID(tc.in); got != tc.want {
			t.Errorf("IsInstallationID(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// A control event reads as what happened, and its gateway actor is dropped.
func TestEventLabelControlKinds(t *testing.T) {
	stop := Event{Kind: "room.stop_requested", Actor: Actor{Kind: "gateway", ID: "install:abc"}}
	if got := EventLabel(stop); got != "stop requested" {
		t.Errorf("EventLabel(stop) = %q, want \"stop requested\"", got)
	}

	created := Event{Kind: "room.created", Actor: Actor{Kind: "gateway", ID: "install:abc"}}
	if got := EventLabel(created); got != "room created" {
		t.Errorf("EventLabel(created) = %q, want \"room created\"", got)
	}

	roster := Event{Kind: "room.members_changed", Actor: Actor{Kind: "gateway", ID: "install:abc"}}
	if got := EventLabel(roster); got != "roster changed" {
		t.Errorf("EventLabel(members_changed) = %q, want \"roster changed\"", got)
	}
}

// An unknown kind keeps its own name; a person speaking keeps its label.
func TestEventLabelUnknownKind(t *testing.T) {
	unknown := Event{Kind: "room.something_new", Actor: Actor{Kind: "gateway", ID: "install:abc"}}
	if got := EventLabel(unknown); got != "room.something_new" {
		t.Errorf("EventLabel(unknown) = %q, want the kind alone", got)
	}

	byMember := Event{Kind: "room.something_new", Actor: Actor{Kind: "member", Profile: "alojz"}}
	if got := EventLabel(byMember); got != "room.something_new alojz" {
		t.Errorf("EventLabel(member) = %q, want \"room.something_new alojz\"", got)
	}
}
