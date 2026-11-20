package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/rooms"
)

// ruleEv is one synthetic room event carrying the turn coordinates the
// gateway writes onto message.member and turn.* rows.
func ruleEv(seq int, kind, profile, text string, round int) rooms.Event {
	payload := map[string]any{}
	if text != "" {
		payload["text"] = text
	}
	if round >= 0 {
		payload["round_index"] = float64(round)
	}
	return rooms.Event{
		RoomID: "sztab", Seq: seq, EventID: fmt.Sprintf("e%d", seq), Kind: kind,
		Actor:     rooms.Actor{Kind: "member", ID: "m1", Profile: profile, DisplayName: profile},
		Payload:   payload,
		CreatedAt: float64(1700000000 + seq),
	}
}

// ── /rules ───────────────────────────────────────────────────────────────────

// `/rules` quotes the constitution and the budgets, and its overlay is capped
// to the pane exactly like /help: the composer stays, esc closes it first.
func TestRoomsRules_OverlayShowsConstitutionAndBudgets(t *testing.T) {
	m := v2Model(t)
	m.setSize(80, 24)
	m.page.Events = []rooms.Event{ruleEv(1, "message.user", "", "co robimy?", -1)}

	mm, _ := m.handleTranscriptCommand("/rules")
	out := ansi.Strip(mm.View())

	for _, want := range []string{
		"room rules",
		"1. one message per turn",
		"2. nothing new to add",
		"… ",
		"esc closes this",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("/rules view is missing %q:\n%s", want, out)
		}
	}
	// The live budget already rides on the room header (`· 1/3 · 0/10`), so
	// the overlay may be capped before its own budget line at this size.
	if !strings.Contains(out, "1/3 · 0/10") {
		t.Errorf("room header lost the discussion budget:\n%s", out)
	}
	if !strings.Contains(out, "you › ") {
		t.Fatalf("composer pushed off-screen by /rules:\n%s", out)
	}
	if !strings.Contains(out, "/rooms back") {
		t.Fatalf("hint bar pushed off-screen by /rules:\n%s", out)
	}
	if n := strings.Count(out, "\n"); n > 24 {
		t.Fatalf("/rules view is %d lines for a 24-line terminal:\n%s", n, out)
	}

	next, cmd := mm.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if next.notice != "" {
		t.Fatalf("esc should close the rules notice, notice=%q", next.notice)
	}
	if next.sub != roomsTranscript {
		t.Fatalf("esc left the room instead of closing /rules (sub=%v)", next.sub)
	}
	if cmd != nil {
		if _, back := cmd().(goBackFromRoomsMsg); back {
			t.Fatal("esc on /rules emitted goBackFromRoomsMsg")
		}
	}
}

// A taller pane shows the whole overlay: all four member rules and the pass
// token are reachable without leaving the room.
func TestRoomsRules_TallerPaneShowsAllFourRules(t *testing.T) {
	m := v2Model(t)
	m.setSize(90, 46)
	m.page.Events = []rooms.Event{ruleEv(1, "message.user", "", "hej", -1)}

	mm, _ := m.handleTranscriptCommand("/rules")
	out := ansi.Strip(mm.View())
	for _, want := range []string{
		"1. one message per turn, only when you have something new",
		`2. nothing new to add? reply exactly "(pass)"`,
		"3. mention a teammate by handle to pull them into the next round",
		"4. never reveal private conversations — replies are published verbatim",
		"budget: 2–6 members · 3 rounds · 10 member messages",
		"round 0 is your message",
		"settled — waiting for you",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("/rules view is missing %q:\n%s", want, out)
		}
	}
}

func TestRoomsRules_HelpListsRules(t *testing.T) {
	m := v2Model(t)
	mm, _ := m.handleTranscriptCommand("/help")
	if !strings.Contains(mm.notice, "/rules") {
		t.Errorf("/help must list /rules:\n%s", mm.notice)
	}
}

// ── header budget ────────────────────────────────────────────────────────────

// headerBudgetEvents is two rounds and seven member messages: the spec's
// worked example for the header line.
func headerBudgetEvents() []rooms.Event {
	evs := []rooms.Event{ruleEv(1, "message.user", "", "start", -1)}
	seq := 2
	for i := 0; i < 4; i++ {
		evs = append(evs, ruleEv(seq, "message.member", "matt", "pierwsza runda", 0))
		seq++
	}
	for i := 0; i < 3; i++ {
		evs = append(evs, ruleEv(seq, "message.member", "kowal", "druga runda", 1))
		seq++
	}
	return evs
}

func TestRoomsHeader_ShowsBudgetDerivedFromTheLog(t *testing.T) {
	m := v2Model(t)
	m.setSize(80, 24)
	m.page.Events = headerBudgetEvents()

	out := ansi.Strip(m.View())
	if !strings.Contains(out, "2/3 · 7/10") {
		t.Fatalf("header at 80 columns should carry the degraded budget:\n%s", out)
	}
	if strings.Contains(out, "round 2/3") {
		t.Errorf("header at 80 columns must not use the long budget form:\n%s", out)
	}

	wide := m
	wide.setSize(160, 24)
	wideOut := ansi.Strip(wide.View())
	if !strings.Contains(wideOut, "round 2/3 · 7/10 messages") {
		t.Fatalf("header on a wide pane should carry the full budget:\n%s", wideOut)
	}
}

func TestRoomsHeader_ShowsTheEndReasonInWords(t *testing.T) {
	tests := []struct {
		name   string
		events []rooms.Event
		want   string
	}{
		{
			name: "settled silent round",
			events: func() []rooms.Event {
				settled := ruleEv(3, "turn.settled", "matt", "", 0)
				settled.Payload["passed"] = true
				return []rooms.Event{
					ruleEv(1, "message.user", "", "start", -1),
					ruleEv(2, "turn.started", "matt", "", 0),
					settled,
				}
			}(),
			want: "settled — waiting for you",
		},
		{
			name: "bounded by rounds",
			events: []rooms.Event{
				ruleEv(1, "message.user", "", "start", -1),
				ruleEv(2, "message.member", "matt", "a", 0),
				ruleEv(3, "message.member", "kowal", "b", 1),
				ruleEv(4, "message.member", "olesno", "c", 2),
			},
			want: "bounded — max rounds",
		},
		{
			name: "bounded by messages",
			events: func() []rooms.Event {
				evs := []rooms.Event{ruleEv(1, "message.user", "", "start", -1)}
				for i := 0; i < rooms.MaxDiscussionMessages; i++ {
					evs = append(evs, ruleEv(i+2, "message.member", "matt", "punkt", 0))
				}
				return evs
			}(),
			want: "bounded — max messages",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := v2Model(t)
			m.setSize(80, 24)
			m.page.Events = tc.events
			if out := ansi.Strip(m.View()); !strings.Contains(out, tc.want) {
				t.Fatalf("header is missing %q:\n%s", tc.want, out)
			}
		})
	}
}

// A new user message opens the next discussion, and the header drops the old
// end reason instead of reporting a settled room as still settled.
func TestRoomsHeader_ClearsWhenANewDiscussionOpens(t *testing.T) {
	m := v2Model(t)
	m.setSize(80, 24)
	m.page.Events = append(headerBudgetEvents(),
		ruleEv(20, "message.user", "", "nowe pytanie", -1),
		ruleEv(21, "message.member", "matt", "odpowiadam", 0),
	)
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "1/3 · 1/10") {
		t.Fatalf("header should report the new discussion:\n%s", out)
	}
	if strings.Contains(out, "bounded —") || strings.Contains(out, "settled —") {
		t.Fatalf("header still reports the previous discussion's end:\n%s", out)
	}
}

// ── peer-pull annotation ─────────────────────────────────────────────────────

func TestRoomsTranscript_AnnotatesTheCitingMessage(t *testing.T) {
	m := v2Model(t)
	m.setSize(100, 30)
	m.page.Events = []rooms.Event{
		ruleEv(1, "message.user", "", "start", -1),
		ruleEv(2, "message.member", "matt", "@kowal sprawdz to", 0),
		ruleEv(3, "message.member", "kowal", "sprawdzone", 1),
	}
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "@matt pulled @kowal into the next round") {
		t.Fatalf("transcript is missing the pull annotation:\n%s", out)
	}
}

// A mention that pulls nobody (unknown handle, or the citer themselves) must
// not claim a round the gateway will not open.
func TestRoomsTranscript_NoAnnotationWhenTheMentionPullsNobody(t *testing.T) {
	m := v2Model(t)
	m.setSize(100, 30)
	m.page.Events = []rooms.Event{
		ruleEv(1, "message.user", "", "start", -1),
		ruleEv(2, "message.member", "matt", "@ghost i @matt hej", 0),
	}
	if out := ansi.Strip(m.View()); strings.Contains(out, "pulled") {
		t.Fatalf("transcript claims a pull the gateway will not make:\n%s", out)
	}
}

// ── mode line ────────────────────────────────────────────────────────────────

// The mode line states the effect on the gateway's turn selection, including
// who runs later rounds — the client does not decide that.
func TestRoomsHeader_ModeLineDescribesTheEffect(t *testing.T) {
	tests := []struct {
		name      string
		prefs     rooms.RoomPrefs
		wants     []string
		mustNotBe []string
	}{
		{
			name:  "broadcast",
			prefs: rooms.RoomPrefs{Mode: string(rooms.RouteBroadcast)},
			wants: []string{
				"broadcast:",
				"everyone on the roster answers your message",
				"later rounds",
				"peer",
			},
		},
		{
			name:  "moderator names the member",
			prefs: rooms.RoomPrefs{Mode: string(rooms.RouteModerator), Moderator: "kowal"},
			wants: []string{
				"moderator: un-mentioned messages go to @kowal",
				"later rounds follow",
			},
		},
		{
			name:  "round-robin describes the client and the gateway",
			prefs: rooms.RoomPrefs{Mode: string(rooms.RouteRoundRobin)},
			wants: []string{
				"round-robin: the client addresses the next member",
				"later rounds still follow",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := v2Model(t)
			m.setSize(100, 30)
			m.prefs.Set("sztab", tc.prefs)
			out := ansi.Strip(m.View())
			for _, want := range tc.wants {
				if !strings.Contains(out, want) {
					t.Errorf("mode line is missing %q:\n%s", want, out)
				}
			}
		})
	}
}
