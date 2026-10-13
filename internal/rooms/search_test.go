package rooms

import (
	"strings"
	"testing"
	"time"
)

func TestFind(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC).Unix()
	events := []Event{
		{
			Seq:       1,
			Kind:      "message.user",
			Actor:     Actor{DisplayName: "Alice"},
			Payload:   map[string]any{"text": "Hello world"},
			CreatedAt: float64(now),
		},
		{
			Seq:       2,
			Kind:      "message.user",
			Actor:     Actor{DisplayName: "Bob"},
			Payload:   map[string]any{"text": "hello again"},
			CreatedAt: float64(now),
		},
		{
			Seq:       3,
			Kind:      "turn.started",
			Actor:     Actor{},
			Payload:   map[string]any{},
			CreatedAt: float64(now),
		},
	}

	t.Run("single match in the middle", func(t *testing.T) {
		m := Find(events, "world")
		if l := len(m); l != 1 {
			t.Fatalf("expected 1 match, got %d", l)
		}
		got := m[0]
		if got.Seq != 1 {
			t.Errorf("Seq = %d, want 1", got.Seq)
		}
		if got.Kind != "message.user" {
			t.Errorf("Kind = %q, want %q", got.Kind, "message.user")
		}
		if got.Speaker != "Alice" {
			t.Errorf("Speaker = %q, want %q", got.Speaker, "Alice")
		}
		if got.Index != 6 {
			t.Errorf("Index = %d, want 6", got.Index)
		}
		if !strings.Contains(got.Snippet, "world") {
			t.Errorf("Snippet should contain the hit: %q", got.Snippet)
		}
		if got.CreatedAt != float64(now) {
			t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, float64(now))
		}
	})

	t.Run("case-insensitive match", func(t *testing.T) {
		m := Find(events, "WORLD")
		if l := len(m); l != 1 {
			t.Fatalf("expected 1 match, got %d", l)
		}
	})

	t.Run("match at the start has no leading ellipsis", func(t *testing.T) {
		events2 := []Event{
			{
				Seq:     1,
				Kind:    "message.user",
				Actor:   Actor{DisplayName: "Zoe"},
				Payload: map[string]any{"text": "alpha beta"},
			},
		}
		m := Find(events2, "alpha")
		if l := len(m); l != 1 {
			t.Fatalf("expected 1 match, got %d", l)
		}
		if strings.HasPrefix(m[0].Snippet, "…") {
			t.Errorf("leading ellipsis expected for start-of-string hit, got %q", m[0].Snippet)
		}
		if m[0].Index != 0 {
			t.Errorf("Index = %d, want 0", m[0].Index)
		}
	})

	t.Run("three hits in one event at increasing indices", func(t *testing.T) {
		events2 := []Event{
			{
				Seq:     1,
				Kind:    "message.user",
				Actor:   Actor{DisplayName: "Zoe"},
				Payload: map[string]any{"text": "la la la"},
			},
		}
		m := Find(events2, "la")
		if l := len(m); l != 3 {
			t.Fatalf("expected 3 matches, got %d", l)
		}
		want := []int{0, 3, 6}
		for i, exp := range want {
			if m[i].Index != exp {
				t.Errorf("match[%d].Index = %d, want %d", i, m[i].Index, exp)
			}
		}
		if m[0].Seq != 1 || m[1].Seq != 1 || m[2].Seq != 1 {
			t.Errorf("all three matches should share Seq=1")
		}
	})

	t.Run("no matches returns nil", func(t *testing.T) {
		m := Find(events, "xyz")
		if m != nil {
			t.Errorf("expected nil for no matches, got %v", m)
		}
	})

	t.Run("empty query returns nil", func(t *testing.T) {
		for _, q := range []string{"", "   ", "\t"} {
			m := Find(events, q)
			if m != nil {
				t.Errorf("expected nil for query %q, got %v", q, m)
			}
		}
	})

	t.Run("event without text is skipped", func(t *testing.T) {
		m := Find(events, "turn")
		if m != nil {
			t.Errorf("expected nil; event without text should be skipped, got %v", m)
		}
	})

	t.Run("ordered by Seq ascending", func(t *testing.T) {
		events2 := []Event{
			{Seq: 3, Kind: "message.user", Actor: Actor{DisplayName: "C"}, Payload: map[string]any{"text": "x"}},
			{Seq: 1, Kind: "message.user", Actor: Actor{DisplayName: "A"}, Payload: map[string]any{"text": "x"}},
			{Seq: 2, Kind: "message.user", Actor: Actor{DisplayName: "B"}, Payload: map[string]any{"text": "x"}},
		}
		m := Find(events2, "x")
		if l := len(m); l != 3 {
			t.Fatalf("expected 3 matches, got %d", l)
		}
		wantSeq := []int{1, 2, 3}
		for i, seq := range wantSeq {
			if m[i].Seq != seq {
				t.Errorf("m[%d].Seq = %d, want %d", i, m[i].Seq, seq)
			}
		}
	})

	t.Run("within one event ordered by Index ascending", func(t *testing.T) {
		events2 := []Event{
			{
				Seq:     1,
				Kind:    "message.user",
				Actor:   Actor{DisplayName: "Zoe"},
				Payload: map[string]any{"text": "la la la"},
			},
		}
		m := Find(events2, "la")
		if l := len(m); l != 3 {
			t.Fatalf("expected 3 matches, got %d", l)
		}
		idxs := []int{m[0].Index, m[1].Index, m[2].Index}
		if !isSorted(idxs) {
			t.Errorf("indices within one event not ascending: %v", idxs)
		}
	})

	t.Run("multiline snippet replaces newlines with spaces", func(t *testing.T) {
		events2 := []Event{
			{
				Seq:     1,
				Kind:    "message.user",
				Actor:   Actor{DisplayName: "Zoe"},
				Payload: map[string]any{"text": "first line\nsecond line\nthird line"},
			},
		}
		m := Find(events2, "second")
		if l := len(m); l != 1 {
			t.Fatalf("expected 1 match, got %d", l)
		}
		s := m[0].Snippet
		if strings.Contains(s, "\n") {
			t.Errorf("snippet must not contain literal newlines: %q", s)
		}
	})

	t.Run("unicode multi-byte rune does not shift the window", func(t *testing.T) {
		// Use a string with multi-byte runes so byte offset ≠ rune count.
		text := "αβγδε hello world ℤ"
		events2 := []Event{
			{Seq: 1, Kind: "message.user", Actor: Actor{DisplayName: "Zoe"}, Payload: map[string]any{"text": text}},
		}
		m := Find(events2, "hello")
		if l := len(m); l != 1 {
			t.Fatalf("expected 1 match, got %d", l)
		}
		// The byte offset of "hello" must be reported correctly.
		expectedIdx := strings.Index(text, "hello")
		if m[0].Index != expectedIdx {
			t.Errorf("Index = %d, want %d (byte offset of %q in %q)", m[0].Index, expectedIdx, "hello", text)
		}
		if !strings.Contains(m[0].Snippet, "hello") {
			t.Errorf("snippet should contain the hit: %q", m[0].Snippet)
		}
	})
}

func TestMatchLine(t *testing.T) {
	t.Run("formats time and speaker", func(t *testing.T) {
		now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC).Unix()
		m := Match{
			Speaker:   "Alice",
			Snippet:   "Hello world",
			CreatedAt: float64(now),
		}
		want := "03:04:05 @Alice: Hello world"
		if got := MatchLine(m); got != want {
			t.Errorf("MatchLine() = %q, want %q", got, want)
		}
	})

	t.Run("zero CreatedAt shows ???:??:??", func(t *testing.T) {
		m := Match{
			Speaker:   "Bob",
			Snippet:   "bye",
			CreatedAt: 0,
		}
		want := "???:??:?? @Bob: bye"
		if got := MatchLine(m); got != want {
			t.Errorf("MatchLine() = %q, want %q", got, want)
		}
	})

	t.Run("snippet gets ellipsis on both sides when in the middle", func(t *testing.T) {
		now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC).Unix()
		text := strings.Repeat("abcdefghij", 20) // 200 chars
		m := Match{
			Speaker:   "Zoe",
			Text:      text,
			Snippet:   snippet(text, 100),
			CreatedAt: float64(now),
		}
		line := MatchLine(m)
		if !strings.HasPrefix(line, "03:04:05 @Zoe: …") {
			t.Errorf("expected leading ellipsis, got %q", line)
		}
		if !strings.HasSuffix(line, "…") {
			t.Errorf("expected trailing ellipsis, got %q", line)
		}
	})
}

// isSorted reports whether ints are in non-decreasing order.
func isSorted(ints []int) bool {
	for i := 1; i < len(ints); i++ {
		if ints[i] < ints[i-1] {
			return false
		}
	}
	return true
}
