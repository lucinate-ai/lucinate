package rooms

import (
	"strings"
	"testing"
)

func TestParseMentions_ReadsTheGatewayGrammar(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		handles []string
		all     bool
		offset  int
	}{
		{name: "empty", text: "", handles: nil},
		{name: "no mention", text: "co robimy?", handles: nil},
		{name: "single", text: "@matt status?", handles: []string{"matt"}, offset: 0},
		{name: "mid sentence", text: "hej @kowal sprawdz", handles: []string{"kowal"}, offset: 4},
		{name: "trailing punctuation", text: "@matt, zrob to", handles: []string{"matt"}},
		{name: "all", text: "@all brief", handles: []string{"all"}, all: true},
		{name: "everyone", text: "@everyone brief", handles: []string{"everyone"}, all: true},
		{name: "dotted handle", text: "@hermes.lead ok", handles: []string{"hermes.lead"}},
		{name: "colon handle", text: "@agent:2 ok", handles: []string{"agent:2"}},
		{name: "leading underscore is not a mention", text: "@_matt ok", handles: nil},
		{name: "email is not a mention", text: "napisz na a@b.com", handles: nil},
		{name: "handle after a dot is not a mention", text: "x.@matt", handles: nil},
		{name: "uppercase is folded", text: "@MATT", handles: []string{"matt"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseMentions(tc.text)
			if len(got) != len(tc.handles) {
				t.Fatalf("ParseMentions(%q) = %+v, want %d mention(s)", tc.text, got, len(tc.handles))
			}
			for i, h := range tc.handles {
				if got[i].Handle != h {
					t.Errorf("mention %d = %q, want %q", i, got[i].Handle, h)
				}
				if got[i].All != (tc.all && i == 0) {
					t.Errorf("mention %d all = %v, want %v", i, got[i].All, tc.all)
				}
			}
			if tc.offset > 0 && got[0].Offset != tc.offset {
				t.Errorf("offset = %d, want %d", got[0].Offset, tc.offset)
			}
		})
	}
}

func TestParseMentions_KeepsEveryMentionInOrder(t *testing.T) {
	got := ParseMentions("@matt @kowal @matt")
	if len(got) != 3 {
		t.Fatalf("got %d mentions, want 3: %+v", len(got), got)
	}
	want := []string{"matt", "kowal", "matt"}
	for i, h := range want {
		if got[i].Handle != h {
			t.Errorf("mention %d = %q, want %q", i, got[i].Handle, h)
		}
	}
}

func TestResolveMentions_MatchesRosterAndReportsUnknown(t *testing.T) {
	roster := RosterFor([]string{"matt", "kowal", "olesno-lead-ops"})

	targets, unknown, all := ResolveMentions("@matt @olesno-lead-ops co robimy?", roster)
	if all {
		t.Error("all = true, want false")
	}
	if len(targets) != 2 || targets[0].Profile != "matt" || targets[1].Profile != "olesno-lead-ops" {
		t.Fatalf("targets = %+v, want matt + olesno-lead-ops", targets)
	}
	if len(unknown) != 0 {
		t.Errorf("unknown = %v, want none", unknown)
	}

	targets, unknown, all = ResolveMentions("@ghost hi", roster)
	if len(targets) != 0 || len(unknown) != 1 || unknown[0] != "ghost" || all {
		t.Fatalf("targets=%+v unknown=%v all=%v, want none/ghost/false", targets, unknown, all)
	}

	_, _, all = ResolveMentions("@everyone hi", roster)
	if !all {
		t.Error("@everyone should address the whole roster")
	}
}

// A message with no mention keeps the historic behaviour: the whole roster
// answers.
func TestRoute_BroadcastIsTheDefault(t *testing.T) {
	roster := RosterFor([]string{"matt", "kowal"})
	got, next := Route("co robimy?", roster, RouteBroadcast, "", 0)

	if got.Text != "co robimy?" {
		t.Errorf("text = %q, want it untouched", got.Text)
	}
	if len(got.Targets) != 2 || got.Explicit {
		t.Errorf("targets = %+v explicit = %v, want the whole roster and explicit=false", got.Targets, got.Explicit)
	}
	if got.Mode != RouteBroadcast {
		t.Errorf("mode = %q, want broadcast", got.Mode)
	}
	if next != 0 {
		t.Errorf("next round-robin index = %d, want 0 (broadcast does not advance the cursor)", next)
	}
	if !strings.Contains(got.Note, "broadcast to 2") {
		t.Errorf("note = %q, want it to say the message went to the whole roster", got.Note)
	}
}

// An explicit mention is a user instruction and outranks the room's routing
// mode — otherwise the moderator setting would silently swallow it.
func TestRoute_ExplicitMentionWinsInEveryMode(t *testing.T) {
	roster := RosterFor([]string{"matt", "kowal"})
	for _, mode := range []RoutingMode{RouteBroadcast, RouteModerator, RouteRoundRobin} {
		got, _ := Route("@kowal tylko ty", roster, mode, "matt", 0)
		if !got.Explicit {
			t.Errorf("%s: explicit = false, want true", mode)
		}
		if len(got.Targets) != 1 || got.Targets[0].Profile != "kowal" {
			t.Errorf("%s: targets = %+v, want kowal only", mode, got.Targets)
		}
		if got.Text != "@kowal tylko ty" {
			t.Errorf("%s: text = %q, want it sent as written", mode, got.Text)
		}
	}
}

func TestRoute_ModeratorAddsTheMentionForTheChosenMember(t *testing.T) {
	roster := RosterFor([]string{"matt", "kowal"})
	got, next := Route("co robimy?", roster, RouteModerator, "kowal", 1)

	if len(got.Targets) != 1 || got.Targets[0].Profile != "kowal" {
		t.Fatalf("targets = %+v, want kowal", got.Targets)
	}
	if got.Text != "@kowal co robimy?" {
		t.Errorf("text = %q, want the mention prepended so the gateway routes it", got.Text)
	}
	if next != 1 {
		t.Errorf("next index = %d, want unchanged (moderator is not a cursor)", next)
	}
	if !strings.Contains(got.Note, "moderator") {
		t.Errorf("note = %q, want it to name the moderator", got.Note)
	}
}

func TestRoute_RoundRobinWalksTheRoster(t *testing.T) {
	roster := RosterFor([]string{"matt", "kowal", "olesno-lead-ops"})
	index := 0
	var picked []string
	for i := 0; i < 4; i++ {
		got, next := Route("dalej", roster, RouteRoundRobin, "", index)
		picked = append(picked, got.Targets[0].Profile)
		index = next
	}
	want := []string{"matt", "kowal", "olesno-lead-ops", "matt"}
	for i := range want {
		if picked[i] != want[i] {
			t.Fatalf("round-robin order = %v, want %v", picked, want)
		}
	}
}

func TestRoute_RoundRobinSurvivesANegativeCursor(t *testing.T) {
	roster := RosterFor([]string{"matt", "kowal"})
	got, next := Route("dalej", roster, RouteRoundRobin, "", -3)
	if len(got.Targets) != 1 {
		t.Fatalf("targets = %+v, want one member", got.Targets)
	}
	if next < 0 {
		t.Errorf("next = %d, want a non-negative cursor", next)
	}
}

func TestRoute_ModeratorFallsBackToTheFirstMember(t *testing.T) {
	roster := RosterFor([]string{"matt", "kowal"})
	got, _ := Route("status?", roster, RouteModerator, "nie-ma-takiego", 0)
	if len(got.Targets) != 1 || got.Targets[0].Profile != "matt" {
		t.Fatalf("targets = %+v, want the first member as fallback", got.Targets)
	}
	if !strings.Contains(got.Note, "no member matches moderator") {
		t.Errorf("note = %q, want it to explain the fallback", got.Note)
	}
}

func TestRoute_UnknownMentionIsSentAsWritten(t *testing.T) {
	roster := RosterFor([]string{"matt", "kowal"})
	got, _ := Route("@ghost hej", roster, RouteModerator, "matt", 0)

	if got.Text != "@ghost hej" {
		t.Errorf("text = %q, want it untouched — the gateway decides", got.Text)
	}
	if len(got.Targets) != 0 {
		t.Errorf("targets = %+v, want none resolved", got.Targets)
	}
	if !strings.Contains(got.Note, "unknown handle @ghost") {
		t.Errorf("note = %q, want the unknown handle named", got.Note)
	}
}

func TestRoute_EmptyRosterIsReportedNotPanicked(t *testing.T) {
	for _, mode := range []RoutingMode{RouteBroadcast, RouteModerator, RouteRoundRobin} {
		got, _ := Route("hej", nil, mode, "matt", 0)
		if got.Text != "hej" {
			t.Errorf("%s: text = %q, want it untouched", mode, got.Text)
		}
		if len(got.Targets) != 0 {
			t.Errorf("%s: targets = %+v, want none", mode, got.Targets)
		}
	}
}

func TestRoute_UnknownModeIsBroadcastWithANotice(t *testing.T) {
	roster := RosterFor([]string{"matt", "kowal"})
	got, _ := Route("hej", roster, RoutingMode("wszyscy-naraz"), "", 0)
	if got.Mode != RouteBroadcast {
		t.Errorf("mode = %q, want broadcast", got.Mode)
	}
	if !strings.Contains(got.Note, "unknown routing mode") {
		t.Errorf("note = %q, want it to flag the bad mode", got.Note)
	}
}

func TestParseRoutingMode(t *testing.T) {
	tests := []struct {
		in      string
		want    RoutingMode
		wantErr bool
	}{
		{in: "", want: RouteBroadcast},
		{in: "broadcast", want: RouteBroadcast},
		{in: "ALL", want: RouteBroadcast},
		{in: "moderator", want: RouteModerator},
		{in: "mod", want: RouteModerator},
		{in: "round-robin", want: RouteRoundRobin},
		{in: "roundrobin", want: RouteRoundRobin},
		{in: "round_robin", want: RouteRoundRobin},
		{in: "rr", want: RouteRoundRobin},
		{in: "  MODERATOR  ", want: RouteModerator},
		{in: "dyktator", wantErr: true},
	}
	for _, tc := range tests {
		got, err := ParseRoutingMode(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseRoutingMode(%q) = %q, want an error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseRoutingMode(%q) error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseRoutingMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFindMember_MatchesHandleAndProfile(t *testing.T) {
	roster := RosterFor([]string{"matt", "olesno-lead-ops"})
	if m := FindMember(roster, "MATT"); m == nil || m.Profile != "matt" {
		t.Errorf("FindMember(matt) = %+v, want the matt member", m)
	}
	if m := FindMember(roster, "olesno-lead-ops"); m == nil {
		t.Error("FindMember should match a derived handle")
	}
	if m := FindMember(roster, "ghost"); m != nil {
		t.Errorf("FindMember(ghost) = %+v, want nil", m)
	}
}
