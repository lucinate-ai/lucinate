package routines

import "testing"

func TestScanDirectives_OwnLineMatches(t *testing.T) {
	in := `Sure, I'll handle that.

/routine:stop
`
	got := ScanDirectives(in)
	if len(got) != 1 || got[0].Kind != DirectiveStop {
		t.Errorf("got %+v, want [stop]", got)
	}
}

func TestScanDirectives_LeadingWhitespaceOK(t *testing.T) {
	in := "  /routine:pause"
	got := ScanDirectives(in)
	if len(got) != 1 || got[0].Kind != DirectivePause {
		t.Errorf("got %+v, want [pause]", got)
	}
}

func TestScanDirectives_InlineMentionIgnored(t *testing.T) {
	cases := []string{
		"You could send /routine:stop here",
		"`/routine:stop` is the directive",
		"Reply with /routine:stop to halt.",
	}
	for _, in := range cases {
		got := ScanDirectives(in)
		if len(got) != 0 {
			t.Errorf("inline %q -> %+v, want none", in, got)
		}
	}
}

func TestScanDirectives_Continue(t *testing.T) {
	got := ScanDirectives("/routine:continue")
	if len(got) != 1 || got[0].Kind != DirectiveContinue {
		t.Errorf("got %+v, want [continue]", got)
	}
}

func TestScanDirectives_ModeSwitch(t *testing.T) {
	cases := []struct {
		in   string
		kind DirectiveKind
	}{
		{"/routine:mode auto", DirectiveModeAuto},
		{"/routine:mode  manual", DirectiveModeManual},
		{"  /routine:mode auto  ", DirectiveModeAuto},
	}
	for _, tc := range cases {
		got := ScanDirectives(tc.in)
		if len(got) != 1 || got[0].Kind != tc.kind {
			t.Errorf("%q -> %+v, want kind=%v", tc.in, got, tc.kind)
		}
	}
}

func TestScanDirectives_MultipleInOrder(t *testing.T) {
	in := `/routine:mode auto
/routine:pause
/routine:stop`
	got := ScanDirectives(in)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (%+v)", len(got), got)
	}
	want := []DirectiveKind{DirectiveModeAuto, DirectivePause, DirectiveStop}
	for i := range want {
		if got[i].Kind != want[i] {
			t.Errorf("[%d] = %v, want %v", i, got[i].Kind, want[i])
		}
	}
}

func TestScanDirectives_UnknownDirectiveIgnored(t *testing.T) {
	in := "/routine:explode\n/routine:mode\n/routine:mode neutral"
	got := ScanDirectives(in)
	if len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}

func TestScanDirectives_EmptyReply(t *testing.T) {
	if got := ScanDirectives(""); len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}
