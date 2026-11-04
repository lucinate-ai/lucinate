package rooms

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// W1 — the edge cases. Each test here names the behaviour a caller depends on
// at the boundary, and the comment says what the boundary actually is, because
// several of these are consequences of the gateway's grammar rather than
// choices this client makes.

// ── (a) mentions ─────────────────────────────────────────────────────────────

func TestW1_MentionGrammarEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{name: "mid-sentence", text: "hej @matt sprawdz to", want: []string{"matt"}},
		{name: "two different handles", text: "@matt @kowal oboje proszeni", want: []string{"matt", "kowal"}},
		{name: "same handle twice", text: "@matt @matt", want: []string{"matt", "matt"}},
		{name: "longer word than the handle", text: "@mattxyz hej", want: []string{"mattxyz"}},
		{name: "typo", text: "@mat hej", want: []string{"mat"}},
		{name: "trailing punctuation", text: "@matt, prosze", want: []string{"matt"}},
		{name: "wrapped in brackets", text: "(@matt)", want: []string{"matt"}},
		{name: "dotted handle", text: "@hermes.lead ok", want: []string{"hermes.lead"}},
		{name: "colon handle", text: "@agent:2 ok", want: []string{"agent:2"}},
		// The grammar stops at the first character outside [A-Za-z0-9._:-],
		// so a unicode handle is truncated to its ASCII prefix. Nothing local
		// can fix that: the same truncation decides routing on the gateway.
		{name: "unicode handle truncates", text: "@młody hej", want: []string{"m"}},
		{name: "bare at", text: "hej @ ty", want: nil},
		{name: "second at is glued to the first", text: "@matt@kowal", want: []string{"matt"}},
		{name: "email address", text: "napisz na kowal@example.com", want: nil},
		{name: "leading underscore is not a mention", text: "@_matt hej", want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseMentions(tc.text)
			if len(got) != len(tc.want) {
				t.Fatalf("ParseMentions(%q) = %+v, want %d mention(s) %v", tc.text, got, len(tc.want), tc.want)
			}
			for i, want := range tc.want {
				if got[i].Handle != want {
					t.Errorf("mention %d = %q, want %q", i, got[i].Handle, want)
				}
			}
		})
	}
}

// A message that is nothing but a mention still routes, and still reaches the
// gateway as the user wrote it.
func TestW1_MessageThatIsOnlyAMentionStillRoutes(t *testing.T) {
	roster := RosterFor([]string{"matt", "kowal"})
	got, _ := Route("@matt", roster, RouteRoundRobin, "", 0)
	if !got.Explicit || got.Text != "@matt" {
		t.Fatalf("decision = %+v, want the mention sent as written", got)
	}
	if len(got.Targets) != 1 || got.Targets[0].Profile != "matt" {
		t.Fatalf("targets = %+v, want matt only", got.Targets)
	}
}

// Two handles in one message address both members: the room answers both, and
// the mode is not applied on top. This is the documented answer to "which one
// wins" — neither, both are mentioned.
func TestW1_TwoHandlesAddressBothMembers(t *testing.T) {
	roster := RosterFor([]string{"matt", "kowal", "olesno"})
	got, next := Route("@matt @kowal raport?", roster, RouteRoundRobin, "", 2)

	if len(got.Targets) != 2 {
		t.Fatalf("targets = %+v, want both mentioned members", got.Targets)
	}
	if got.Targets[0].Profile != "matt" || got.Targets[1].Profile != "kowal" {
		t.Errorf("targets = %+v, want matt then kowal in the order written", got.Targets)
	}
	if got.Text != "@matt @kowal raport?" {
		t.Errorf("text = %q, want it unchanged", got.Text)
	}
	if next != 2 {
		t.Errorf("round-robin cursor moved to %d on an explicit mention, want it untouched", next)
	}
	if !strings.Contains(got.Note, "addressed to @matt @kowal") {
		t.Errorf("note = %q, want both handles named", got.Note)
	}
}

// A typo or an unknown handle never silently broadens the message to the whole
// roster: it goes out as written and the user is told.
func TestW1_TypoAndUnknownHandlesAreReportedNotSwallowed(t *testing.T) {
	roster := RosterFor([]string{"matt", "kowal"})
	for _, text := range []string{"@mat hej", "@ghost hej", "@mattxyz hej"} {
		got, _ := Route(text, roster, RouteBroadcast, "", 0)
		if got.Text != text {
			t.Errorf("%q: text = %q, want it untouched", text, got.Text)
		}
		if len(got.Targets) != 0 {
			t.Errorf("%q: targets = %+v, want none resolved", text, got.Targets)
		}
		if len(got.Targets) == 0 && !strings.Contains(got.Note, "unknown handle") {
			t.Errorf("%q: note = %q, want the unknown handle named", text, got.Note)
		}
		if got.Mode != RouteBroadcast {
			t.Errorf("%q: mode = %q, want the room's mode reported", text, got.Mode)
		}
	}
}

// The roster's own handles never look like a truncated unicode mention, so the
// truncation cannot address the wrong member by accident.
func TestW1_DerivedHandlesAreNeverATruncatedUnicodePrefix(t *testing.T) {
	handle := HandleFor("młody")
	if handle == "m" {
		t.Fatal("HandleFor produced a bare ASCII prefix — a truncated unicode mention would hit the wrong member")
	}
	roster := RosterFor([]string{"młody", "matt"})
	mentions := ParseMentions("@młody hej")
	if len(mentions) != 1 || mentions[0].Handle != "m" {
		t.Fatalf("mentions = %+v, want the documented truncation to %q", mentions, "m")
	}
	if FindMember(roster, "m") != nil {
		t.Error("the truncated mention resolved to a member; it must be reported as unknown instead")
	}
}

// ── (b) streaming ────────────────────────────────────────────────────────────

// Two members answering at once: their fragments must not cross, and both
// turns are live until each one settles.
func TestW1_InterleavedStreamsStayPerMember(t *testing.T) {
	events := []Event{
		stEvent(1, "turn.started", "matt", nil),
		stEvent(2, "turn.started", "kowal", nil),
		stEvent(3, "message.member.delta", "matt", map[string]any{"text": "matt: raz"}),
		stEvent(4, "message.member.delta", "kowal", map[string]any{"text": "kowal: raz"}),
		stEvent(5, "message.member.delta", "matt", map[string]any{"text": "matt: raz dwa"}),
		stEvent(6, "message.member.delta", "kowal", map[string]any{"text": "kowal: raz dwa"}),
	}
	streams := StreamsFor(events)
	if len(streams) != 2 {
		t.Fatalf("streams = %+v, want one per member", streams)
	}
	byMember := map[string]Stream{}
	for _, s := range streams {
		byMember[s.Member] = s
	}
	if byMember["matt"].Text != "matt: raz dwa" {
		t.Errorf("matt text = %q, want only his own fragments", byMember["matt"].Text)
	}
	if byMember["kowal"].Text != "kowal: raz dwa" {
		t.Errorf("kowal text = %q, want only his own fragments", byMember["kowal"].Text)
	}
	if len(ActiveStreams(events)) != 2 {
		t.Errorf("active = %+v, want both members still answering", ActiveStreams(events))
	}

	// One settles: the other keeps streaming.
	settled := append(events, stEvent(7, "message.member", "kowal", map[string]any{"text": "kowal: gotowe"}))
	active := ActiveStreams(settled)
	if len(active) != 1 || active[0].Member != "matt" {
		t.Fatalf("active = %+v, want only matt in flight", active)
	}
}

// A stale delta after the final must not resurrect the turn — the gateway
// emits these, and a placeholder that comes back locks the composer.
func TestW1_DeltaAfterFinalDoesNotResurrectTheTurn(t *testing.T) {
	events := []Event{
		stEvent(1, "turn.started", "matt", nil),
		stEvent(2, "message.member", "matt", map[string]any{"text": "gotowe"}),
		stEvent(3, "message.member.delta", "matt", map[string]any{"text": "gotowe"}),
	}
	if active := ActiveStreams(events); len(active) != 0 {
		t.Fatalf("active = %+v, want none — the turn already settled", active)
	}
	streams := StreamsFor(events)
	if len(streams) != 1 || !streams[0].Settled || streams[0].Empty {
		t.Fatalf("streams = %+v, want one settled, non-empty stream", streams)
	}
}

func TestW1_FinalWithoutAnyDeltaStillSettlesWithItsText(t *testing.T) {
	events := []Event{stEvent(1, "message.member", "matt", map[string]any{"text": "bez streamu"})}
	streams := StreamsFor(events)
	if len(streams) != 1 {
		t.Fatalf("streams = %+v, want one", streams)
	}
	if streams[0].Text != "bez streamu" || !streams[0].Settled || streams[0].Empty {
		t.Errorf("stream = %+v, want the text and a settled, non-empty turn", streams[0])
	}
	if len(ActiveStreams(events)) != 0 {
		t.Error("a member that never streamed is not in flight")
	}
}

// ── (c) compaction windows ───────────────────────────────────────────────────

func TestW1_CompactWindowEdgeCases(t *testing.T) {
	t.Run("zero means the stored default", func(t *testing.T) {
		if got := NormalizeKeepLast(0); got != DefaultCompactKeep {
			t.Errorf("NormalizeKeepLast(0) = %d, want %d", got, DefaultCompactKeep)
		}
		older, recent, through := CompactSplit(compactConversation(10), 0)
		if len(older) != 10-DefaultCompactKeep || len(recent) != DefaultCompactKeep || through == 0 {
			t.Errorf("older=%d recent=%d through=%d, want the default window applied", len(older), len(recent), through)
		}
	})

	t.Run("window equal to the transcript compacts nothing", func(t *testing.T) {
		events := compactConversation(6)
		older, recent, through := CompactSplit(events, 6)
		if len(older) != 0 || len(recent) != 6 || through != 0 {
			t.Errorf("older=%d recent=%d through=%d, want nothing compacted", len(older), len(recent), through)
		}
	})

	t.Run("transcript shorter than the window compacts nothing", func(t *testing.T) {
		events := compactConversation(3)
		older, recent, through := CompactSplit(events, 50)
		if len(older) != 0 || len(recent) != 3 || through != 0 {
			t.Errorf("older=%d recent=%d through=%d, want nothing compacted", len(older), len(recent), through)
		}
		if prompt := CompactPrompt(older, 50); prompt != "" {
			t.Errorf("prompt = %q, want no brief asked for when there is nothing to summarise", prompt)
		}
	})

	t.Run("negative window is refused, not clamped", func(t *testing.T) {
		n, err := ValidateKeepWindow(-1)
		if err == nil {
			t.Fatalf("ValidateKeepWindow(-1) = %d, want an error", n)
		}
		if !strings.Contains(err.Error(), "/compact") {
			t.Errorf("error = %v, want it to name the command", err)
		}
		// Zero is legal: it means "use the room's stored window".
		if n, err := ValidateKeepWindow(0); err != nil || n != 0 {
			t.Errorf("ValidateKeepWindow(0) = %d, %v; want 0 and no error", n, err)
		}
	})

	t.Run("a huge window is bounded", func(t *testing.T) {
		if n, err := ValidateKeepWindow(9999); err != nil || n != MaxCompactKeep {
			t.Errorf("ValidateKeepWindow(9999) = %d, %v; want %d", n, err, MaxCompactKeep)
		}
	})
}

func TestW1_LocalSummaryOfANonConversationSaysSo(t *testing.T) {
	events := []Event{
		stEvent(1, "room.activity", "", map[string]any{"status": "driver restarted"}),
		stEvent(2, "turn.started", "matt", nil),
	}
	got := LocalSummary(events, 5)
	if !strings.Contains(got, "nothing worth keeping") {
		t.Errorf("summary = %q, want it to say there was no conversation", got)
	}
}

// ── (d) export ───────────────────────────────────────────────────────────────

func TestW1_ExportOfAnEmptyConversationIsStillWellFormed(t *testing.T) {
	opts := exportOpts("r1", "Sztab", nil, time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC))
	md := ExportMarkdown(nil, opts)
	if !strings.Contains(md, "# Sztab") || !strings.Contains(md, "## Transcript") {
		t.Errorf("markdown = %q, want a document with the empty transcript section", md)
	}
	if !strings.Contains(md, "_no events_") || !strings.Contains(md, "_no members_") {
		t.Errorf("markdown = %q, want explicit empty markers, not silence", md)
	}

	raw, err := ExportJSON(nil, opts)
	if err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// A nil slice marshals to an explicit null, which is what lets a consumer
	// tell "no events" from "an empty list" with a plain nil check.
	if v, ok := doc["events"]; !ok || v != nil {
		t.Errorf("events = %v (present=%v), want an explicit null for an empty transcript", v, ok)
	}
	if v, ok := doc["members"]; !ok || v != nil {
		t.Errorf("members = %v (present=%v), want an explicit null for a room with no roster recorded", v, ok)
	}
}

func TestW1_ExportPreservesPolishTextAndEmoji(t *testing.T) {
	const text = "Zażółć gęślą jaźń — 🚀 100% ✓ „cudzysłów”"
	events := []Event{cpMsg(1, "message.member", "matt", text)}
	opts := exportOpts("r1", "Sztab", []Member{mp("m1", "matt", "matt")}, time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC))

	md := ExportMarkdown(events, opts)
	if !strings.Contains(md, text) {
		t.Errorf("markdown lost the text:\n%s", md)
	}

	raw, err := ExportJSON(events, opts)
	if err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}
	var doc struct {
		Events []Event `json:"events"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Events) != 1 || doc.Events[0].Text() != text {
		t.Fatalf("json text = %q, want it byte-identical", doc.Events[0].Text())
	}
	if !utf8.Valid(raw) {
		t.Error("the JSON document is not valid UTF-8")
	}
}

func TestW1_ExportIsDeterministicForAFixedTimestamp(t *testing.T) {
	events := compactConversation(6)
	opts := exportOpts("r1", "Sztab", []Member{mp("m1", "matt", "matt")}, time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC))

	a, err := ExportJSON(events, opts)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ExportJSON(events, opts)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Error("two JSON exports of the same transcript differ")
	}
	if ExportMarkdown(events, opts) != ExportMarkdown(events, opts) {
		t.Error("two markdown exports of the same transcript differ")
	}
}

// ── (e) reconnect windows ────────────────────────────────────────────────────

// A gateway that never comes back must not spin silently: the schedule caps,
// and it tells the caller what it is doing.
func TestW1_BackoffCapsAndReportsItsState(t *testing.T) {
	b := NewBackoff()
	b.Base = 100 * time.Millisecond
	b.Max = time.Second

	var last time.Duration
	for i := 0; i < 50; i++ {
		last = b.Next()
		if last > b.Max {
			t.Fatalf("attempt %d waited %v, past the %v cap", i+1, last, b.Max)
		}
	}
	if last != b.Max {
		t.Errorf("after 50 attempts the delay = %v, want the cap", last)
	}
	if got := b.Peek(); got != b.Max {
		t.Errorf("Peek() = %v, want the cap", got)
	}
	describe := b.Describe()
	if !strings.Contains(describe, "50") || !strings.Contains(describe, b.Max.String()) {
		t.Errorf("Describe() = %q, want the attempt count and the delay so a status line can show it", describe)
	}

	b.Reset()
	if b.Attempt() != 0 {
		t.Errorf("Attempt() after Reset = %d, want 0", b.Attempt())
	}
	if got := b.Next(); got != b.Base {
		t.Errorf("first delay after Reset = %v, want the base %v", got, b.Base)
	}
}
