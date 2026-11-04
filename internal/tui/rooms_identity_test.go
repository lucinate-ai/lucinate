package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/rooms"
)

func alojzRoster() []rooms.Member {
	return rooms.RosterFor([]string{"alojz"})
}

// The gateway is the actor on every turn event, but the row must name the
// member the payload places on the roster — never the installation id.
func TestTurnRowsNameTheMemberNotTheGateway(t *testing.T) {
	roster := alojzRoster()
	actor := rooms.Actor{Kind: "gateway", ID: "install:abc"}

	settled := rooms.Event{
		Kind: "turn.settled", Actor: actor,
		Payload: map[string]any{"member_id": "m1", "member_index": float64(0)},
	}
	got := ansi.Strip(formatRoomsEventStyled(settled, settled.Text(), assistantPrefixStyle, roster))
	if !strings.Contains(got, "@alojz finished") {
		t.Errorf("settled row = %q, want @alojz finished", got)
	}
	if strings.Contains(got, "install:") {
		t.Errorf("settled row named the gateway: %q", got)
	}

	cancelled := rooms.Event{
		Kind: "turn.cancelled", Actor: actor,
		Payload: map[string]any{"member_id": "m1", "member_index": float64(0), "reason": "member turn cancelled"},
	}
	if got := ansi.Strip(formatRoomsEventStyled(cancelled, cancelled.Text(), assistantPrefixStyle, roster)); !strings.Contains(got, "@alojz — przerwano: member turn cancelled") {
		t.Errorf("cancelled row = %q, want @alojz — przerwano: member turn cancelled", got)
	}
}

// A member the roster cannot place keeps the actor's label rather than
// inventing a handle.
func TestTurnRowUnknownMemberFallsBackToActor(t *testing.T) {
	ev := rooms.Event{
		Kind: "turn.settled", Actor: rooms.Actor{Kind: "gateway", ID: "install:abc"},
		Payload: map[string]any{"member_id": "m9", "member_index": float64(7)},
	}
	got := ansi.Strip(formatRoomsEventStyled(ev, ev.Text(), assistantPrefixStyle, alojzRoster()))
	if !strings.Contains(got, "install:abc finished") {
		t.Errorf("row = %q, want the actor fallback", got)
	}
}
