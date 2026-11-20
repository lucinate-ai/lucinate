package rooms

import (
	"strings"
	"testing"
)

// roundEvent is a synthetic gateway event carrying the turn coordinates the
// hosted-room driver writes onto message.member and turn.* rows.
func roundEvent(seq int, kind, profile string, round int, payload map[string]any) Event {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["round_index"] = float64(round)
	return stEvent(seq, kind, profile, payload)
}

func TestDiscussionBudgetFromLog(t *testing.T) {
	round := func(begin int, r int, profile string, passed bool) []Event {
		return []Event{
			roundEvent(begin, "turn.started", profile, r, nil),
			roundEvent(begin+1, "message.member", profile, r, map[string]any{"text": "notatka " + profile}),
			roundEvent(begin+2, "turn.settled", profile, r, map[string]any{"passed": passed}),
		}
	}

	tests := []struct {
		name      string
		events    []Event
		want      DiscussionBudget
		headerHas string
	}{
		{
			name:   "empty log is idle",
			events: nil,
			want: DiscussionBudget{
				HasDiscussion: false,
				EndReason:     EndReasonIdle,
			},
		},
		{
			name: "one round is open",
			events: append([]Event{
				stEvent(1, "message.user", "", map[string]any{"text": "co robimy?"}),
			}, round(2, 0, "matt", false)...),
			want: DiscussionBudget{
				HasDiscussion: true,
				CurrentRound:  1,
				RoundsUsed:    1,
				MessagesUsed:  1,
				EndReason:     EndReasonNone,
			},
			headerHas: "round 1/3 · 1/10 messages",
		},
		{
			name: "three rounds hit the round cap",
			events: func() []Event {
				evs := []Event{stEvent(1, "message.user", "", map[string]any{"text": "start"})}
				seq := 2
				for i, profile := range []string{"matt", "kowal", "olesno"} {
					evs = append(evs, round(seq, i, profile, false)...)
					seq += 3
				}
				return evs
			}(),
			want: DiscussionBudget{
				HasDiscussion: true,
				CurrentRound:  3,
				RoundsUsed:    3,
				MessagesUsed:  3,
				Ended:         true,
				EndReason:     EndReasonBoundedMaxRounds,
			},
			headerHas: "bounded — max rounds",
		},
		{
			name: "ten member messages hit the message cap",
			events: func() []Event {
				evs := []Event{stEvent(1, "message.user", "", map[string]any{"text": "start"})}
				seq := 2
				for i := 0; i < MaxDiscussionMessages; i++ {
					evs = append(evs,
						roundEvent(seq, "turn.started", "matt", 0, nil),
						roundEvent(seq+1, "message.member", "matt", 0, map[string]any{"text": "punkt"}),
						roundEvent(seq+2, "turn.settled", "matt", 0, nil),
					)
					seq += 3
				}
				return evs
			}(),
			want: DiscussionBudget{
				HasDiscussion: true,
				CurrentRound:  1,
				RoundsUsed:    1,
				MessagesUsed:  10,
				Ended:         true,
				EndReason:     EndReasonBoundedMaxMessages,
			},
			headerHas: "bounded — max messages",
		},
		{
			name: "a pass-only round settles the discussion",
			events: []Event{
				stEvent(1, "message.user", "", map[string]any{"text": "hej"}),
				roundEvent(2, "turn.started", "matt", 0, nil),
				roundEvent(3, "turn.settled", "matt", 0, map[string]any{"passed": true}),
			},
			want: DiscussionBudget{
				HasDiscussion: true,
				CurrentRound:  1,
				RoundsUsed:    1,
				MessagesUsed:  0,
				Ended:         true,
				EndReason:     EndReasonSettledSilentRound,
			},
			headerHas: "settled — waiting for you",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DiscussionBudgetFor(tc.events)
			if got != tc.want {
				t.Fatalf("budget = %+v, want %+v", got, tc.want)
			}
			header := got.HeaderText(80)
			if tc.headerHas != "" && !strings.Contains(header, tc.headerHas) {
				t.Errorf("HeaderText(80) = %q, want it to contain %q", header, tc.headerHas)
			}
		})
	}
}

// A new user message opens the next discussion: the ended budget of the
// previous one must not leak into the header.
func TestDiscussionBudgetResetsOnNewUserMessage(t *testing.T) {
	events := []Event{
		stEvent(1, "message.user", "", map[string]any{"text": "start"}),
		roundEvent(2, "message.member", "matt", 0, map[string]any{"text": "a"}),
		roundEvent(3, "message.member", "kowal", 1, map[string]any{"text": "b"}),
		roundEvent(4, "message.member", "olesno", 2, map[string]any{"text": "c"}),
		stEvent(5, "message.user", "", map[string]any{"text": "nowe pytanie"}),
		roundEvent(6, "message.member", "matt", 0, map[string]any{"text": "odpowiadam"}),
	}
	got := DiscussionBudgetFor(events)
	if got.Ended || got.CurrentRound != 1 || got.MessagesUsed != 1 || got.RoundsUsed != 1 {
		t.Fatalf("budget after the new user message = %+v, want a fresh one-round discussion", got)
	}
	if strings.Contains(got.HeaderText(80), "bounded") {
		t.Errorf("header %q still reports the previous discussion's end", got.HeaderText(80))
	}
}

// An unreadable round_index is shown as "?", never as a number and never as
// a panic.
func TestDiscussionBudgetUnknownRoundIsQuestionMark(t *testing.T) {
	events := []Event{
		stEvent(1, "message.user", "", map[string]any{"text": "start"}),
		stEvent(2, "message.member", "matt", map[string]any{"text": "hm", "round_index": "?"}),
	}
	got := DiscussionBudgetFor(events)
	if !got.RoundUnknown {
		t.Fatalf("budget = %+v, want RoundUnknown", got)
	}
	if header := got.HeaderText(0); !strings.Contains(header, "round ?/3") {
		t.Errorf("HeaderText = %q, want round ?/3", header)
	}
}

func TestDiscussionBudgetHeaderDegradesAndNeverOverflows(t *testing.T) {
	b := DiscussionBudget{HasDiscussion: true, CurrentRound: 2, RoundsUsed: 2, MessagesUsed: 7}
	full := "round 2/3 · 7/10 messages"
	short := "2/3 · 7/10"

	if got := b.HeaderText(80); got != full {
		t.Errorf("HeaderText(80) = %q, want %q", got, full)
	}
	if got := b.HeaderText(12); got != short {
		t.Errorf("HeaderText(12) = %q, want %q", got, short)
	}
	for _, width := range []int{1, 3, 5, len(short) - 1} {
		got := b.HeaderText(width)
		if displayCols(got) > width {
			t.Errorf("HeaderText(%d) = %q, %d columns wide", width, got, displayCols(got))
		}
	}
	if got := (DiscussionBudget{EndReason: EndReasonIdle}).HeaderText(80); got != "" {
		t.Errorf("idle HeaderText = %q, want empty", got)
	}
}

// ── peer pulls ───────────────────────────────────────────────────────────────

func pullRoster() []Member { return RosterFor([]string{"matt", "kowal", "olesno"}) }

func TestPeerPullsFindsTheCitingMessageAndPeer(t *testing.T) {
	events := []Event{
		stEvent(1, "message.user", "", map[string]any{"text": "start"}),
		roundEvent(2, "message.member", "matt", 0, map[string]any{"text": "@kowal sprawdz to"}),
	}
	pulls := PeerPulls(events, pullRoster())
	if len(pulls) != 1 {
		t.Fatalf("PeerPulls = %+v, want one pull", pulls)
	}
	p := pulls[0]
	if p.Event.Seq != 2 || p.Citer != "matt" || len(p.Handles) != 1 || p.Handles[0] != "kowal" {
		t.Fatalf("pull = %+v, want matt citing kowal in event 2", p)
	}
	if got, want := p.Label(), "@matt pulled @kowal into the next round"; got != want {
		t.Errorf("Label = %q, want %q", got, want)
	}
}

func TestPeerPullsHandlesSeveralPeersOnce(t *testing.T) {
	events := []Event{
		stEvent(1, "message.user", "", map[string]any{"text": "start"}),
		roundEvent(2, "message.member", "matt", 0, map[string]any{"text": "@kowal @olesno @kowal patrzcie"}),
	}
	pulls := PeerPulls(events, pullRoster())
	if len(pulls) != 1 {
		t.Fatalf("PeerPulls = %+v, want one pull", pulls)
	}
	if got, want := pulls[0].Handles, []string{"kowal", "olesno"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("handles = %v, want %v", got, want)
	}
	if got := pulls[0].Label(); !strings.Contains(got, "@kowal and @olesno") {
		t.Errorf("Label = %q, want both peers named", got)
	}
}

func TestPeerPullsIgnoresWhatTheGatewayWillNotPull(t *testing.T) {
	tests := []struct {
		name   string
		events []Event
	}{
		{
			name: "self mention",
			events: []Event{
				stEvent(1, "message.user", "", map[string]any{"text": "start"}),
				roundEvent(2, "message.member", "matt", 0, map[string]any{"text": "@matt note to self"}),
			},
		},
		{
			name: "unknown handle",
			events: []Event{
				stEvent(1, "message.user", "", map[string]any{"text": "start"}),
				roundEvent(2, "message.member", "matt", 0, map[string]any{"text": "@ghost hej"}),
			},
		},
		{
			name: "whole-room mention",
			events: []Event{
				stEvent(1, "message.user", "", map[string]any{"text": "start"}),
				roundEvent(2, "message.member", "matt", 0, map[string]any{"text": "@all raport"}),
			},
		},
		{
			name: "peer already heard from",
			events: []Event{
				stEvent(1, "message.user", "", map[string]any{"text": "start"}),
				roundEvent(2, "message.member", "matt", 0, map[string]any{"text": "pierwszy"}),
				roundEvent(3, "message.member", "kowal", 0, map[string]any{"text": "drugi"}),
				roundEvent(4, "message.member", "kowal", 1, map[string]any{"text": "@matt wracam"}),
			},
		},
		{
			name: "no round left to open",
			events: []Event{
				stEvent(1, "message.user", "", map[string]any{"text": "start"}),
				roundEvent(2, "message.member", "matt", 2, map[string]any{"text": "@kowal koniec"}),
			},
		},
		{
			name: "message cap already spent",
			events: func() []Event {
				evs := []Event{stEvent(1, "message.user", "", map[string]any{"text": "start"})}
				for i := 0; i < MaxDiscussionMessages; i++ {
					text := "punkt"
					if i == MaxDiscussionMessages-1 {
						text = "@kowal ostatni"
					}
					evs = append(evs, roundEvent(i+2, "message.member", "matt", 0, map[string]any{"text": text}))
				}
				return evs
			}(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if pulls := PeerPulls(tc.events, pullRoster()); len(pulls) != 0 {
				t.Fatalf("PeerPulls = %+v, want none", pulls)
			}
		})
	}
}

// A peer who passed is still a member the discussion has heard from; pulling
// them again would spend a turn on someone who just declined.
func TestPeerPullsTreatsAPassAsHeard(t *testing.T) {
	events := []Event{
		stEvent(1, "message.user", "", map[string]any{"text": "start"}),
		roundEvent(2, "turn.started", "kowal", 0, nil),
		roundEvent(3, "turn.settled", "kowal", 0, map[string]any{"passed": true}),
		roundEvent(4, "message.member", "matt", 0, map[string]any{"text": "@kowal jeszcze raz"}),
	}
	if pulls := PeerPulls(events, pullRoster()); len(pulls) != 0 {
		t.Fatalf("PeerPulls = %+v, want none — kowal already passed", pulls)
	}
}
