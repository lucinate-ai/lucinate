package rooms

import (
	"strings"
	"testing"
)

func cpMsg(seq int, kind, profile, text string) Event {
	return Event{
		RoomID: "sztab", Seq: seq, Kind: kind,
		Actor:   Actor{Kind: "member", Profile: profile},
		Payload: map[string]any{"text": text},
	}
}

// compactConversation builds a transcript of n message events so the split
// point is unambiguous: message 1 is seq 1, message 2 is seq 2, ...
func compactConversation(n int) []Event {
	events := make([]Event, 0, n)
	for i := 1; i <= n; i++ {
		if i%2 == 1 {
			events = append(events, cpMsg(i, "message.user", "", "pytanie "+itoa(i)))
			continue
		}
		events = append(events, cpMsg(i, "message.member", "matt", "odpowiedz "+itoa(i)))
	}
	return events
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestCompactSplit_KeepsTheLastVerbatimMessages(t *testing.T) {
	events := compactConversation(10)
	older, recent, through := CompactSplit(events, 3)

	if len(older) != 7 || len(recent) != 3 {
		t.Fatalf("older=%d recent=%d, want 7/3", len(older), len(recent))
	}
	if len(older)+len(recent) != len(events) {
		t.Fatalf("split lost events: %d + %d != %d", len(older), len(recent), len(events))
	}
	if through != older[len(older)-1].Seq {
		t.Errorf("through seq = %d, want the newest summarised event %d", through, older[len(older)-1].Seq)
	}
	if recent[0].Seq != 8 {
		t.Errorf("recent starts at seq %d, want 8", recent[0].Seq)
	}
}

func TestCompactSplit_ShortTranscriptHasNothingToCompact(t *testing.T) {
	events := compactConversation(4)
	older, recent, through := CompactSplit(events, 6)
	if len(older) != 0 {
		t.Errorf("older = %d events, want none", len(older))
	}
	if len(recent) != len(events) {
		t.Errorf("recent = %d events, want all %d", len(recent), len(events))
	}
	if through != 0 {
		t.Errorf("through seq = %d, want 0", through)
	}
}

func TestCompactSplit_NormalisesTheKeepWindow(t *testing.T) {
	for _, tc := range []struct{ in, want int }{
		{in: 0, want: DefaultCompactKeep},
		{in: -5, want: DefaultCompactKeep},
		{in: 999, want: MaxCompactKeep},
		{in: 4, want: 4},
	} {
		if got := NormalizeKeepLast(tc.in); got != tc.want {
			t.Errorf("NormalizeKeepLast(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
	older, recent, _ := CompactSplit(compactConversation(120), 999)
	if len(recent) != MaxCompactKeep || len(older) != 120-MaxCompactKeep {
		t.Errorf("older=%d recent=%d, want %d/%d", len(older), len(recent), 120-MaxCompactKeep, MaxCompactKeep)
	}
}

// The cut lands on a message boundary, so the window always holds exactly the
// messages the user asked to keep; activity events fall on whichever side of
// that boundary they sit.
func TestCompactSplit_CutsOnAMessageBoundary(t *testing.T) {
	events := []Event{
		cpMsg(1, "message.user", "", "pytanie"),
		stEvent(2, "turn.started", "matt", nil),
		cpMsg(3, "message.member", "matt", "odpowiedz 1"),
		stEvent(4, "turn.settled", "matt", nil),
		cpMsg(5, "message.user", "", "pytanie 2"),
		stEvent(6, "turn.started", "matt", nil),
		cpMsg(7, "message.member", "matt", "odpowiedz 2"),
	}
	older, recent, through := CompactSplit(events, 2)
	if len(older)+len(recent) != len(events) {
		t.Fatalf("split lost events: %d + %d != %d", len(older), len(recent), len(events))
	}
	if got := countMessages(recent); got != 2 {
		t.Fatalf("recent holds %d message(s), want exactly the 2 kept ones", got)
	}
	if recent[0].Seq != 5 {
		t.Errorf("recent starts at seq %d, want 5 (the first kept message)", recent[0].Seq)
	}
	if through != older[len(older)-1].Seq {
		t.Errorf("through = %d, want the newest summarised event %d", through, older[len(older)-1].Seq)
	}
	// Every summarised event must sort before every kept one, or the rendered
	// transcript would interleave.
	for _, ev := range older {
		if ev.Seq > through {
			t.Errorf("older holds seq %d, which is past the cut %d", ev.Seq, through)
		}
	}
}

func countMessages(events []Event) int {
	n := 0
	for _, ev := range events {
		if ev.Kind == "message.user" || ev.Kind == "message.member" {
			n++
		}
	}
	return n
}

func TestCompactPrompt_AsksForABriefAndCarriesNoTranscript(t *testing.T) {
	events := compactConversation(10)
	older, _, _ := CompactSplit(events, 2)
	prompt := CompactPrompt(older, 2)
	if prompt == "" {
		t.Fatal("CompactPrompt returned nothing for a compactable transcript")
	}
	low := strings.ToLower(prompt)
	if !strings.Contains(low, "brief") {
		t.Errorf("prompt = %q, want it to ask for a brief", prompt)
	}
	if strings.Contains(prompt, "odpowiedz 2") || strings.Contains(prompt, "pytanie 1") {
		t.Errorf("prompt = %q, want no transcript content — the members already have the room in context", prompt)
	}
}

func TestCompactPrompt_EmptyTranscriptNeedsNoBrief(t *testing.T) {
	if got := CompactPrompt(nil, 4); got != "" {
		t.Errorf("CompactPrompt(nil) = %q, want empty", got)
	}
}

func TestLocalSummary_IsDeterministicAndNamesSpeakers(t *testing.T) {
	events := compactConversation(10)
	older, _, _ := CompactSplit(events, 2)
	first := LocalSummary(older, 8)
	second := LocalSummary(older, 8)
	if first != second {
		t.Fatalf("LocalSummary is not deterministic:\n%s\n---\n%s", first, second)
	}
	if !strings.Contains(first, "@matt") || !strings.Contains(first, "you") {
		t.Errorf("summary = %q, want both speakers named", first)
	}
	if strings.Count(first, "\n- ") > 8 {
		t.Errorf("summary kept more than the 8 items asked for:\n%s", first)
	}
}

func TestLocalSummary_EmptyTranscriptIsExplicit(t *testing.T) {
	if got := LocalSummary(nil, 4); !strings.Contains(got, "0") {
		t.Errorf("LocalSummary(nil) = %q, want it to state there was nothing", got)
	}
}

func TestApplyCompaction_HidesTheSummarisedPartAndThePrompt(t *testing.T) {
	events := compactConversation(10)
	// The run kept messages 9-10 and asked for a brief at seq 8 in a
	// transcript where the request itself was posted later.
	events = append(events, Event{Kind: "message.user", Seq: 40, Payload: map[string]any{"text": "brief the room"}})
	events = append(events, Event{Kind: "message.member", Seq: 41, Actor: Actor{Profile: "matt"}, Payload: map[string]any{"text": "BRIEF"}})
	c := &Compaction{ThroughSeq: 5, PromptSeq: 40, Summary: "BRIEF: ustaliliśmy X", KeepLast: 2}
	summary, visible := ApplyCompaction(events, c)

	if summary != "BRIEF: ustaliliśmy X" {
		t.Errorf("summary = %q", summary)
	}
	var seqs []int
	for _, ev := range visible {
		seqs = append(seqs, ev.Seq)
	}
	want := []int{6, 7, 8, 9, 10, 41}
	if len(seqs) != len(want) {
		t.Fatalf("visible seqs = %v, want %v (the request at 40 hidden, the kept tail intact)", seqs, want)
	}
	for i, w := range want {
		if seqs[i] != w {
			t.Fatalf("visible seqs = %v, want %v", seqs, want)
		}
	}
}

func TestApplyCompaction_NoCompactionShowsEverything(t *testing.T) {
	events := compactConversation(6)
	summary, visible := ApplyCompaction(events, nil)
	if summary != "" {
		t.Errorf("summary = %q, want empty", summary)
	}
	if len(visible) != len(events) {
		t.Errorf("visible = %d events, want all %d", len(visible), len(events))
	}
}
