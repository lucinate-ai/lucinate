package rooms

import (
	"fmt"
	"strings"
	"time"
)

// /compact support for long rooms.
//
// The gateway's transcript is append-only and shared by every client, so a
// client cannot rewrite history. What it *can* do is stop rendering the part
// the room has already absorbed and show a brief in its place: the brief is
// produced by the room itself (a member answers the request), and everything
// after it stays verbatim. The result is the reading experience /compact
// promises without mutating anyone else's copy of the room.
const (
	// DefaultCompactKeep is how many trailing messages /compact leaves in
	// full when the user does not say otherwise. Six is roughly one
	// exchange per member in a small room — enough to keep the thread of
	// the conversation.
	DefaultCompactKeep = 6
	// MaxCompactKeep bounds the window: a transcript that keeps 100
	// messages verbatim has not been compacted.
	MaxCompactKeep = 100
)

// Compaction is one room's compaction.
type Compaction struct {
	// ThroughSeq is the newest transcript seq the brief covers. Every
	// event up to and including it is replaced by the brief.
	ThroughSeq int `json:"through_seq"`
	// PromptSeq is the /compact request itself, hidden with the part it
	// summarises — the instruction is bookkeeping, not conversation.
	PromptSeq int `json:"prompt_seq,omitempty"`
	// Summary is the brief.
	Summary string `json:"summary"`
	// KeepLast is how many trailing messages were left verbatim.
	KeepLast int `json:"keep_last"`
	// CreatedAt is when the compaction was made (unix seconds).
	CreatedAt float64 `json:"created_at"`
	// Source records where the brief came from: "roster" when a member
	// wrote it, "local" for the offline extractive fallback.
	Source string `json:"source,omitempty"`
}

// NormalizeKeepLast bounds a requested window.
func NormalizeKeepLast(n int) int {
	if n <= 0 {
		return DefaultCompactKeep
	}
	if n > MaxCompactKeep {
		return MaxCompactKeep
	}
	return n
}

// ValidateKeepWindow checks an explicitly requested window.
//
// It differs from NormalizeKeepLast on purpose: a stored preference is bounded
// silently (a hand-edited file must not lock /compact out), but a window the
// user typed is either honoured or refused. Compacting with a different N than
// asked — or with the default because the argument was ignored — silently
// discards history the user meant to keep.
func ValidateKeepWindow(n int) (int, error) {
	if n < 0 {
		return 0, fmt.Errorf("keep window must be zero or more messages, got %d — /compact [N] keeps the last N messages in full", n)
	}
	if n > MaxCompactKeep {
		return MaxCompactKeep, nil
	}
	return n, nil
}

// CompactSplit divides a transcript around its last keepLast messages.
//
// The cut lands on a message boundary — never between a message and the turn
// events that belong to it — and throughSeq reports the newest summarised
// event so the caller can record exactly what the brief replaced.
func CompactSplit(events []Event, keepLast int) (older, recent []Event, throughSeq int) {
	keepLast = NormalizeKeepLast(keepLast)

	// Indices of message events; those are what the window counts.
	var msgs []int
	for i, ev := range events {
		switch ev.Kind {
		case "message.user", "message.member":
			msgs = append(msgs, i)
		}
	}
	if len(msgs) <= keepLast {
		return nil, events, 0
	}
	cut := msgs[len(msgs)-keepLast]
	older, recent = events[:cut], events[cut:]
	if cut > 0 {
		throughSeq = events[cut-1].Seq
	}
	return older, recent, throughSeq
}

// CompactPrompt is the request the room is sent to produce its brief.
//
// It carries no transcript: every member of a hosted room already has the
// conversation in its own context, so re-sending the text would cost tokens
// (and a window) for nothing.
func CompactPrompt(older []Event, keepLast int) string {
	if len(older) == 0 {
		return ""
	}
	keepLast = NormalizeKeepLast(keepLast)
	var b strings.Builder
	b.WriteString("Summarise the conversation so far as a short brief: decisions made, open questions, and who owns what. ")
	b.WriteString("Keep it under 200 words and reply with the brief only — no preamble. ")
	b.WriteString("It replaces the earlier transcript for anyone catching up; the last ")
	b.WriteString(itoaSmall(keepLast))
	b.WriteString(" messages stay below it in full.")
	return b.String()
}

// LocalSummary is the offline fallback: a deterministic extractive brief,
// used when the room cannot answer (no reachable roster) and in tests. It is
// honest about being mechanical — it quotes rather than interprets.
func LocalSummary(older []Event, maxItems int) string {
	if maxItems <= 0 {
		maxItems = 8
	}
	var items []string
	messages := 0
	for _, ev := range older {
		text := strings.TrimSpace(ev.Text())
		switch ev.Kind {
		case "message.user":
			if text == "" {
				continue
			}
			messages++
			items = append(items, "you: "+oneLine(text, 100))
		case "message.member":
			if text == "" {
				continue
			}
			messages++
			items = append(items, "@"+streamMember(ev)+": "+oneLine(text, 100))
		}
		if len(items) >= maxItems {
			break
		}
	}
	header := "Local summary (" + itoaSmall(len(older)) + " events, " + itoaSmall(messages) + " messages)"
	if len(items) == 0 {
		return header + ": nothing worth keeping."
	}
	return header + ":\n- " + strings.Join(items, "\n- ")
}

// ApplyCompaction returns the brief to show and the events that stay visible.
// A nil compaction passes everything through.
//
// The brief replaces everything up to ThroughSeq. The request that produced it
// is hidden separately, by seq: it was posted after the tail the user chose to
// keep, so folding it into the cut would swallow that tail.
func ApplyCompaction(events []Event, c *Compaction) (summary string, visible []Event) {
	if c == nil {
		return "", events
	}
	visible = make([]Event, 0, len(events))
	for _, ev := range events {
		if ev.Seq <= c.ThroughSeq {
			continue
		}
		if c.PromptSeq != 0 && ev.Seq == c.PromptSeq {
			continue
		}
		visible = append(visible, ev)
	}
	return c.Summary, visible
}

// NewCompaction stamps a compaction, so callers cannot forget the timestamp
// the status line reads back.
func NewCompaction(throughSeq, promptSeq int, summary, source string, keepLast int, now time.Time) *Compaction {
	return &Compaction{
		ThroughSeq: throughSeq,
		PromptSeq:  promptSeq,
		Summary:    strings.TrimSpace(summary),
		KeepLast:   NormalizeKeepLast(keepLast),
		CreatedAt:  float64(now.Unix()),
		Source:     source,
	}
}

// oneLine flattens and truncates text for a summary bullet.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max-1]) + "…"
	}
	return s
}
