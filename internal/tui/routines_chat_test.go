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
