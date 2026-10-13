// Package rooms ... (see rooms.go)
//
// Search lets a TUI or log viewer scan a transcript for a substring and
// surface each hit as a one-line summary.
package rooms

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Match is one occurrence of a query inside an event's text.
type Match struct {
	Seq       int     // event sequence number
	Kind      string  // event kind
	Speaker   string  // label to render under
	Text      string  // full event text
	Snippet   string  // ±40 chars around the hit
	Index     int     // byte offset of the first hit inside Text
	CreatedAt float64 // event timestamp (unix seconds); 0 when unknown
}

// Find returns every occurrence of query (case-insensitive substring) in
// events, ordered by Seq then by Index within an event. A query that is
// empty after trimming returns nil; an event with no text is skipped.
func Find(events []Event, query string) []Match {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	qLower := strings.ToLower(q)
	out := make([]Match, 0, len(events)*2)
	for _, e := range events {
		text := e.Text()
		if text == "" {
			continue
		}
		textLower := strings.ToLower(text)
		baseOffset := 0
	search:
		for {
			idx := strings.Index(textLower, qLower)
			if idx < 0 {
				break search
			}
			abs := baseOffset + idx
			out = append(out, Match{
				Seq:       e.Seq,
				Kind:      e.Kind,
				Speaker:   e.Speaker(),
				Text:      text,
				Snippet:   snippet(text, abs),
				Index:     abs,
				CreatedAt: e.CreatedAt,
			})
			advance := idx + len(qLower)
			baseOffset += advance
			textLower = textLower[advance:]
			text = text[advance:]
		}
	}
	if len(out) == 0 {
		// A caller checks `== nil` for "nothing found"; an empty non-nil
		// slice would read as a hit list with no hits.
		return nil
	}
	// The transcript is walked in the order the caller passed it, which is
	// not necessarily sequenced (a compaction view slices it, a merged page
	// may arrive out of order), so the hits are put in transcript order here.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Seq != out[j].Seq {
			return out[i].Seq < out[j].Seq
		}
		return out[i].Index < out[j].Index
	})
	return out
}

// snippet returns ±40 Unicode code points around pos in text, with newlines
// replaced by spaces and "…" on either truncated edge. pos is a byte offset
// into text; we convert to runes for the window.
func snippet(text string, pos int) string {
	s := strings.ReplaceAll(text, "\n", " ")
	// Convert byte offset into rune offset so the window is measured in
	// code points, not raw bytes (multi-byte runes would otherwise shift).
	runePos := 0
	for i := 0; i < pos && i < len(text); {
		_, size := utf8.DecodeRuneInString(text[i:])
		if size == 0 {
			break
		}
		i += size
		runePos++
	}
	r := []rune(s)
	if runePos > len(r) {
		runePos = len(r)
	}
	const half = 40
	start := runePos - half
	if start < 0 {
		start = 0
	}
	end := runePos + half + 1 // +1 to include the matched rune
	if end > len(r) {
		end = len(r)
	}
	win := string(r[start:end])
	var b strings.Builder
	if start > 0 {
		b.WriteString("…")
	}
	b.WriteString(win)
	if end < len(r) {
		b.WriteString("…")
	}
	return b.String()
}

// MatchLine renders a Match as a single UI line: "HH:MM:SS @handle: snippet".
// The time comes from CreatedAt (unix seconds); a zero timestamp yields
// ???:??:?? so events without a meaningful wall clock still format cleanly.
func MatchLine(m Match) string {
	var ts string
	if m.CreatedAt == 0 {
		ts = "???:??:??"
	} else {
		ts = time.Unix(int64(m.CreatedAt), 0).UTC().Format("15:04:05")
	}
	return fmt.Sprintf("%s @%s: %s", ts, m.Speaker, m.Snippet)
}
