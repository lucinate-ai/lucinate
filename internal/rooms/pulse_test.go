package rooms

import (
	"strings"
	"testing"
)

func TestPulseText_IsDeterministicAndAsksForAnOpinion(t *testing.T) {
	board := "w toku: api (@ania)"
	first := PulseText("  dociągnąć API ", board)
	second := PulseText("  dociągnąć API ", board)

	if first != second {
		t.Fatalf("PulseText is not deterministic:\n%q\n%q", first, second)
	}
	if !strings.Contains(first, "[Puls pokoju] dociągnąć API") {
		t.Errorf("goal is not trimmed into the header:\n%s", first)
	}
	for _, want := range []string{"(1) twoje stanowisko", "(2) czego nie wiesz", "(3) co bierzesz"} {
		if !strings.Contains(first, want) {
			t.Errorf("pulse is missing %q:\n%s", want, first)
		}
	}
	if !strings.Contains(first, "Nie wykonuj pracy w tej rundzie") {
		t.Errorf("pulse does not forbid work:\n%s", first)
	}
	if !strings.Contains(first, "Tablica robot:\n"+board) {
		t.Errorf("pulse does not include the board:\n%s", first)
	}
}

func TestPulseText_EmptyGoalAndBoardDefaults(t *testing.T) {
	empty := PulseText("", "")

	if !strings.HasPrefix(empty, "[Puls pokoju] co dalej\n\n") {
		t.Errorf("empty goal did not fall back to co dalej:\n%s", empty)
	}
	if strings.Contains(empty, "Tablica robot:") {
		t.Errorf("empty board should omit the table header:\n%s", empty)
	}
	if !strings.HasSuffix(empty, pulseAsk) {
		t.Errorf("pulse does not end on the opinion prompt:\n%s", empty)
	}
}

// A pulse whose brief digest moved must say so; one whose digest matches the
// room's pointer must not, or the line becomes noise members learn to skip.
func TestPulseTextCarriesTheRefreshedBriefDigest(t *testing.T) {
	const oldDigest = "0123456789ab"
	const newDigest = "ba9876543210"

	unchanged := PulseTextWithBrief("", "", oldDigest, oldDigest)
	if strings.Contains(unchanged, "brief sha=") {
		t.Errorf("unchanged digest still adds a refresh line:\n%s", unchanged)
	}

	changed := PulseTextWithBrief("", "", newDigest, oldDigest)
	if !strings.Contains(changed, "brief sha="+newDigest+" — odświeżony, przeczytaj ponownie") {
		t.Errorf("changed digest missing the refresh line:\n%s", changed)
	}
	if !strings.HasPrefix(changed, "[Puls pokoju]") || !strings.HasSuffix(changed, pulseAsk) {
		t.Errorf("the refresh line broke the pulse's shape:\n%s", changed)
	}

	if line := BriefRefreshLine("", ""); line != "" {
		t.Errorf("BriefRefreshLine with no digests = %q, want empty", line)
	}
}

func TestShouldPulse(t *testing.T) {
	tests := []struct {
		name    string
		idle    bool
		lastSeq int
		pulse   int
		want    bool
	}{
		{name: "idle and new activity", idle: true, lastSeq: 9, pulse: 4, want: true},
		{name: "idle and nothing since the pulse", idle: true, lastSeq: 4, pulse: 4, want: false},
		{name: "idle on a silent room", idle: true, lastSeq: 0, pulse: 0, want: false},
		{name: "working with new activity", idle: false, lastSeq: 9, pulse: 4, want: false},
		{name: "working on a silent room", idle: false, lastSeq: 0, pulse: 0, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldPulse(tt.idle, tt.lastSeq, tt.pulse); got != tt.want {
				t.Fatalf("ShouldPulse(%v, %d, %d) = %v, want %v", tt.idle, tt.lastSeq, tt.pulse, got, tt.want)
			}
		})
	}
}
