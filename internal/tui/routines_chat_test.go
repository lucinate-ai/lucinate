package tui

import (
	"strings"
	"testing"

	"github.com/lucinate-ai/lucinate/internal/routines"
)

// TestRoutineStatusLine_AwaitingUserShowsCallToAction pins the manual-mode
// "press Enter" cue: when the routine is parked on the user, the trailing
// segment must surface the next message body alongside actionable wording.
func TestRoutineStatusLine_AwaitingUserShowsCallToAction(t *testing.T) {
	cases := []struct {
		name     string
		mode     routines.Mode
		paused   bool
		sending  bool
		wantCue  bool // true => expect "Press Enter"; false => expect passive "next:"
		wantSent string
	}{
		{name: "manual idle awaits user", mode: routines.ModeManual, wantCue: true, wantSent: "1/2"},
		{name: "manual sending shows next", mode: routines.ModeManual, sending: true, wantCue: false, wantSent: "1/2"},
		{name: "auto running shows next", mode: routines.ModeAuto, wantCue: false, wantSent: "1/2"},
		{name: "auto paused awaits user", mode: routines.ModeAuto, paused: true, wantCue: true, wantSent: "1/2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &chatModel{
				width:   120,
				sending: tc.sending,
				activeRoutine: &activeRoutine{
					routine: routines.Routine{
						Name:  "demo",
						Steps: []string{"first step", "second step body"},
					},
					mode:   tc.mode,
					paused: tc.paused,
					sent:   1,
				},
			}
			line := m.routineStatusLine()
			if !strings.Contains(line, "sent: "+tc.wantSent) {
				t.Errorf("missing progress %q in %q", tc.wantSent, line)
			}
			hasCue := strings.Contains(line, "▶ Press Enter to send:")
			if hasCue != tc.wantCue {
				t.Errorf("call-to-action presence: got %v, want %v (line=%q)", hasCue, tc.wantCue, line)
			}
			if tc.wantCue && !strings.Contains(line, "second step body") {
				t.Errorf("awaiting-user line should preview the next step body, got %q", line)
			}
			if !tc.wantCue && !strings.Contains(line, "next: ") {
				t.Errorf("non-awaiting line should use passive 'next:' segment, got %q", line)
			}
		})
	}
}

// TestRoutineStatusLine_PreviewGrowsWithWidth pins that the preview length
// scales with terminal width — the awaiting-user cue is only useful if the
// user can actually read enough of the upcoming message.
func TestRoutineStatusLine_PreviewGrowsWithWidth(t *testing.T) {
	long := strings.Repeat("abcdefghij ", 30) // 330 chars
	m := &chatModel{
		width: 200,
		activeRoutine: &activeRoutine{
			routine: routines.Routine{Name: "demo", Steps: []string{long}},
			mode:    routines.ModeManual,
		},
	}
	wide := m.routineStatusLine()
	m.width = 60
	narrow := m.routineStatusLine()
	if len(wide) <= len(narrow) {
		t.Errorf("expected wider terminal to yield longer preview: wide=%d narrow=%d", len(wide), len(narrow))
	}
}

// TestMaybeAdvanceRoutine_ManualCompletion pins that the final step of a
// manual routine ends the routine on its final event — the user otherwise
// sees no "completed" notification and the input stays parked in routine
// mode forever.
func TestMaybeAdvanceRoutine_ManualCompletion(t *testing.T) {
	m := &chatModel{
		width: 120,
		activeRoutine: &activeRoutine{
			routine: routines.Routine{Name: "demo", Steps: []string{"a", "b"}},
			mode:    routines.ModeManual,
			sent:    2, // both steps already dispatched; this is the final reply
		},
	}
	if cmd := m.maybeAdvanceRoutine(); cmd != nil {
		t.Fatalf("manual completion should not dispatch a follow-up step (got cmd=%v)", cmd)
	}
	if m.activeRoutine != nil {
		t.Fatalf("activeRoutine should be cleared after completion, got %+v", m.activeRoutine)
	}
	if len(m.notifications) == 0 {
		t.Fatalf("expected a completion notification, got none")
	}
	last := m.notifications[len(m.notifications)-1]
	if !strings.Contains(last.text, "completed") || !strings.Contains(last.text, "demo") {
		t.Errorf("notification should announce 'demo completed', got %q", last.text)
	}
	if last.isError {
		t.Errorf("completion notification should not be an error")
	}
}

// TestMaybeAdvanceRoutine_ManualMidStepIsNoOp pins that mid-routine manual
// finals stay quiet — the user, not the controller, drives the next step.
func TestMaybeAdvanceRoutine_ManualMidStepIsNoOp(t *testing.T) {
	m := &chatModel{
		width: 120,
		activeRoutine: &activeRoutine{
			routine: routines.Routine{Name: "demo", Steps: []string{"a", "b"}},
			mode:    routines.ModeManual,
			sent:    1, // one of two steps dispatched; user must press Enter for step 2
		},
	}
	if cmd := m.maybeAdvanceRoutine(); cmd != nil {
		t.Fatalf("manual mid-routine final should not auto-advance (got cmd=%v)", cmd)
	}
	if m.activeRoutine == nil {
		t.Fatalf("activeRoutine should still be set mid-routine")
	}
	if len(m.notifications) != 0 {
		t.Errorf("manual mid-routine final should not emit a notification, got %+v", m.notifications)
	}
}
