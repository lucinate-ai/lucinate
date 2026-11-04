package rooms

import (
	"encoding/json"
	"strings"
	"testing"
)

func usageEvent(seq int, kind, profile string, payload map[string]any) Event {
	return Event{
		RoomID: "sztab", Seq: seq, Kind: kind,
		Actor:   Actor{Kind: "member", ID: "m1", Profile: profile, DisplayName: displayNameFor(profile)},
		Payload: payload,
	}
}

func TestUsageFromPayload_Shape(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]any
		want  Usage
		ok    bool
	}{
		{
			name:  "nested usage block",
			input: map[string]any{"usage": map[string]any{"input_tokens": 1200.0, "output_tokens": 300.0, "total_tokens": 1500.0, "cost_usd": 0.0123}},
			want:  Usage{InputTokens: 1200, OutputTokens: 300, TotalTokens: 1500, CostUSD: 0.0123, HasCost: true},
			ok:    true,
		},
		{
			name:  "prompt and completion names",
			input: map[string]any{"usage": map[string]any{"prompt_tokens": 10.0, "completion_tokens": 5.0}},
			want:  Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
			ok:    true,
		},
		{
			name:  "tokens in/out",
			input: map[string]any{"tokens": map[string]any{"input": 7, "output": 3}},
			want:  Usage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10},
			ok:    true,
		},
		{
			name:  "tokens spelled in out",
			input: map[string]any{"tokens": map[string]any{"in": 2, "out": 4}},
			want:  Usage{InputTokens: 2, OutputTokens: 4, TotalTokens: 6},
			ok:    true,
		},
		{
			name:  "flat keys",
			input: map[string]any{"input_tokens": 5, "output_tokens": 6},
			want:  Usage{InputTokens: 5, OutputTokens: 6, TotalTokens: 11},
			ok:    true,
		},
		{
			name:  "cost only",
			input: map[string]any{"cost_usd": 0.5},
			want:  Usage{CostUSD: 0.5, HasCost: true},
			ok:    true,
		},
		{
			name:  "cost inside usage",
			input: map[string]any{"usage": map[string]any{"cost": 0.25}},
			want:  Usage{CostUSD: 0.25, HasCost: true},
			ok:    true,
		},
		{
			name:  "string numbers",
			input: map[string]any{"ens": nil, "input_tokens": "42", "output_tokens": "8"},
			want:  Usage{InputTokens: 42, OutputTokens: 8, TotalTokens: 50},
			ok:    true,
		},
		{
			name:  "json number",
			input: map[string]any{"usage": map[string]any{"total_tokens": json.Number("900")}},
			want:  Usage{TotalTokens: 900},
			ok:    true,
		},
		{
			name:  "negative counts are ignored",
			input: map[string]any{"input_tokens": -5.0, "output_tokens": 3.0},
			want:  Usage{OutputTokens: 3, TotalTokens: 3},
			ok:    true,
		},
		{
			name:  "no usage",
			input: map[string]any{"text": "hello"},
			ok:    false,
		},
		{
			name:  "empty payload",
			input: map[string]any{},
			ok:    false,
		},
		{
			name:  "nil payload",
			input: nil,
			ok:    false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := UsageFromPayload(tc.input)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (got %+v)", ok, tc.ok, got)
			}
			if !tc.ok {
				return
			}
			if got != tc.want {
				t.Errorf("usage = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestUsageFor_CountsTurnsMessagesAndTokens(t *testing.T) {
	members := RosterFor([]string{"matt", "kowal"})
	events := []Event{
		usageEvent(1, "turn.started", "matt", nil),
		usageEvent(2, "message.member", "matt", map[string]any{
			"text":  "gotowe",
			"usage": map[string]any{"input_tokens": 1200.0, "output_tokens": 300.0, "cost_usd": 0.01},
		}),
		usageEvent(3, "turn.settled", "matt", map[string]any{"passed": true}),
	}

	rows := UsageFor(events, members)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want one per roster member", len(rows))
	}
	if rows[0].Handle != "matt" || rows[0].Turns != 1 || rows[0].Messages != 1 {
		t.Fatalf("matt row = %+v, want 1 turn and 1 message", rows[0])
	}
	if rows[0].InputTokens != 1200 || rows[0].OutputTokens != 300 || rows[0].TotalTokens != 1500 {
		t.Errorf("matt tokens = %+v, want the reported totals", rows[0].Usage)
	}
	if !rows[0].HasCost || rows[0].CostUSD != 0.01 {
		t.Errorf("matt cost = %+v, want 0.01 reported", rows[0].Usage)
	}
	if rows[1].Handle != "kowal" || rows[1].Turns != 0 || rows[1].HasCost {
		t.Errorf("kowal row = %+v, want an empty row", rows[1])
	}
}

// A gateway that only emits the terminal event still counts a turn, but a
// started+settled pair must not count twice.
func TestUsageFor_CountsEachTurnExactlyOnce(t *testing.T) {
	members := RosterFor([]string{"matt"})
	events := []Event{
		usageEvent(1, "turn.started", "matt", nil),
		usageEvent(2, "turn.settled", "matt", nil),
		usageEvent(3, "turn.started", "matt", nil),
		usageEvent(4, "turn.failed", "matt", map[string]any{"error": "timeout"}),
		usageEvent(5, "turn.settled", "kowal", nil),
	}
	rows := UsageFor(events, members)
	matt := rows[0]
	if matt.Turns != 2 {
		t.Errorf("matt turns = %d, want 2", matt.Turns)
	}
	if len(rows) != 2 || rows[1].Handle != "kowal" || rows[1].Turns != 1 {
		t.Fatalf("rows = %+v, want a kowal row with one turn", rows)
	}
}

func TestUsageFor_IgnoresUserTurnsAndUnknownActors(t *testing.T) {
	members := RosterFor([]string{"matt"})
	events := []Event{
		{Kind: "message.user", Seq: 1, Payload: map[string]any{"text": "hej", "usage": map[string]any{"input_tokens": 99.0}}},
		{Kind: "turn.started", Seq: 2, Actor: Actor{Kind: "gateway", ID: "install:abc"}},
	}
	rows := UsageFor(events, members)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want only the roster member", rows)
	}
	if rows[0].Handle != "matt" || rows[0].Turns != 0 || rows[0].InputTokens != 0 {
		t.Errorf("row = %+v, want an empty matt row", rows[0])
	}
}

func TestUsageFor_KeepsActorsOutsideTheRosterSorted(t *testing.T) {
	members := RosterFor([]string{"matt"})
	events := []Event{
		{Kind: "message.member", Seq: 1, Actor: Actor{Kind: "member", Profile: "zoe"}},
		{Kind: "message.member", Seq: 2, Actor: Actor{Kind: "member", Profile: "adam"}},
	}
	rows := UsageFor(events, members)
	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want roster plus two unknown members", rows)
	}
	if rows[0].Handle != "matt" || rows[1].Handle != "adam" || rows[2].Handle != "zoe" {
		t.Errorf("order = %s,%s,%s; want matt then adam then zoe", rows[0].Handle, rows[1].Handle, rows[2].Handle)
	}
}

func TestUsageTotals(t *testing.T) {
	rows := []MemberUsage{
		{Handle: "matt", Turns: 2, Messages: 3, Usage: Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15, CostUSD: 0.01, HasCost: true}},
		{Handle: "kowal", Turns: 1, Messages: 1, Usage: Usage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3}},
	}
	total := UsageTotals(rows)
	if total.Turns != 3 || total.Messages != 4 || total.TotalTokens != 18 || total.InputTokens != 12 {
		t.Fatalf("totals = %+v", total)
	}
	if !total.HasCost || total.CostUSD != 0.01 {
		t.Errorf("cost total = %+v, want 0.01", total.Usage)
	}
}

func TestFormatUsage_IsDeterministicAndHonestAboutMissingCost(t *testing.T) {
	rows := []MemberUsage{
		{Handle: "matt", Profile: "matt", Turns: 1, Messages: 1, Usage: Usage{InputTokens: 1200, OutputTokens: 300, TotalTokens: 1500, CostUSD: 0.0123, HasCost: true}},
		{Handle: "kowal", Profile: "kowal", Turns: 1, Messages: 0},
	}
	first := FormatUsage(rows)
	if first != FormatUsage(rows) {
		t.Fatal("FormatUsage is not deterministic")
	}
	for _, want := range []string{"@matt", "1500", "$0.0123", "@kowal", "—"} {
		if !strings.Contains(first, want) {
			t.Errorf("table missing %q:\n%s", want, first)
		}
	}
	if !strings.Contains(first, "\nTOTAL") {
		t.Errorf("a multi-member report should carry a total:\n%s", first)
	}

	single := FormatUsage(rows[:1])
	if strings.Contains(single, "\nTOTAL") {
		t.Errorf("a single row needs no total:\n%s", single)
	}

	if got := FormatUsage(nil); !strings.Contains(got, "no usage") {
		t.Errorf("empty report = %q", got)
	}
}
