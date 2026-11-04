package rooms

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The hosted room is a bounded discussion, and the gateway is the only
// authority on how far it has run. These two constants are not client
// policy: they mirror the gateway's own `MAX_DISCUSSION_ROUNDS` and
// `MAX_DISCUSSION_MESSAGES`, which is why the header can state a budget
// without owning one.
const (
	MaxDiscussionRounds   = 3
	MaxDiscussionMessages = 10
)

// EndReason is how a discussion stopped, in the gateway's vocabulary.
// EndReasonIdle is different from the others: nothing has opened a
// discussion yet, so there is nothing to report as ended.
type EndReason string

const (
	EndReasonNone               EndReason = ""
	EndReasonIdle               EndReason = "idle"
	EndReasonSettledSilentRound EndReason = "settled/silent_round"
	EndReasonBoundedMaxRounds   EndReason = "bounded/max_rounds"
	EndReasonBoundedMaxMessages EndReason = "bounded/max_messages"
)

// DiscussionBudget is the position of the open discussion against the
// gateway's budget, derived only from events the room log already holds.
//
// It is a value, computed on demand: a reconnect replays the log and the
// numbers are recomputed from the same events the gateway replays, so a
// counter can never drift the way an optimistically incremented one would.
type DiscussionBudget struct {
	// HasDiscussion is false when no user message has opened one yet.
	HasDiscussion bool
	// CurrentRound is the 1-based round the discussion is in (round 0 is the
	// user's message, so an open discussion starts at 1). It is clamped to
	// MaxDiscussionRounds.
	CurrentRound int
	// RoundUnknown is true when an event carries a round_index that could not
	// be read. The header then shows "?" instead of guessing a number.
	RoundUnknown bool
	// RoundsUsed is how many distinct rounds the log records.
	RoundsUsed int
	// MessagesUsed is how many member messages the discussion has spent.
	MessagesUsed int
	// Ended says the discussion is over; EndReason then says why.
	Ended     bool
	EndReason EndReason
}

// RoundIndex reports the round an event belongs to. The gateway writes the
// 0-based index onto message.member and turn.* events; anything else (or a
// value that is not a whole number) reports false rather than a made-up 0.
func (e Event) RoundIndex() (int, bool) {
	v, ok := e.Payload["round_index"]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) {
			return 0, false
		}
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	}
	return 0, false
}

// DiscussionBudgetFor derives the discussion budget from a room log.
//
// A discussion starts at the last message.user: a new user message is what
// opens the next one, so the counts of an earlier discussion are simply not
// in scope any more. Events with an unreadable round_index still count
// towards messages, and mark RoundUnknown instead of failing.
func DiscussionBudgetFor(events []Event) DiscussionBudget {
	start := -1
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == "message.user" {
			start = i
			break
		}
	}
	if start < 0 {
		return DiscussionBudget{EndReason: EndReasonIdle}
	}

	b := DiscussionBudget{HasDiscussion: true, CurrentRound: 1}
	maxRound := -1
	rounds := map[int]bool{}
	messagesInRound := map[int]bool{}
	speechInRound := map[int]bool{}
	passedInRound := map[int]bool{}
	terminalInRound := map[int]bool{}
	openTurns := 0

	for _, ev := range events[start+1:] {
		idx, hasRound := ev.RoundIndex()
		if _, present := ev.Payload["round_index"]; present && !hasRound {
			b.RoundUnknown = true
		}
		if hasRound {
			rounds[idx] = true
			if idx > maxRound {
				maxRound = idx
			}
			if idx < 0 {
				// A negative round is not a round the gateway can mean.
				b.RoundUnknown = true
			}
		}
		switch ev.Kind {
		case "turn.started":
			openTurns++
		case "turn.settled":
			if openTurns > 0 {
				openTurns--
			}
			if hasRound {
				terminalInRound[idx] = true
				passed, _ := ev.Payload["passed"].(bool)
				if passed {
					passedInRound[idx] = true
				} else {
					speechInRound[idx] = true
				}
			}
		case "turn.failed", "turn.deferred":
			if openTurns > 0 {
				openTurns--
			}
			if hasRound {
				terminalInRound[idx] = true
			}
		case "message.member":
			b.MessagesUsed++
			if hasRound {
				messagesInRound[idx] = true
				speechInRound[idx] = true
			}
		}
	}

	if maxRound >= 0 {
		b.CurrentRound = maxRound + 1
	}
	if b.CurrentRound > MaxDiscussionRounds {
		b.CurrentRound = MaxDiscussionRounds
	}
	b.RoundsUsed = len(rounds)
	if b.RoundsUsed > MaxDiscussionRounds {
		b.RoundsUsed = MaxDiscussionRounds
	}
	if b.MessagesUsed > MaxDiscussionMessages {
		b.MessagesUsed = MaxDiscussionMessages
	}

	last := maxRound
	lastSilent := last >= 0 && terminalInRound[last] && !speechInRound[last] && passedInRound[last]
	switch {
	case openTurns > 0:
		// Work is still running; the gateway has not settled anything.
	case b.MessagesUsed >= MaxDiscussionMessages:
		b.Ended, b.EndReason = true, EndReasonBoundedMaxMessages
	case b.CurrentRound >= MaxDiscussionRounds:
		b.Ended, b.EndReason = true, EndReasonBoundedMaxRounds
	case lastSilent:
		b.Ended, b.EndReason = true, EndReasonSettledSilentRound
	default:
		b.EndReason = EndReasonNone
	}
	return b
}

// EndText translates the gateway's reason into the words the room header
// shows. An idle budget has no line at all.
func (b DiscussionBudget) EndText() string {
	switch b.EndReason {
	case EndReasonSettledSilentRound:
		return "settled — silent round"
	case EndReasonBoundedMaxRounds:
		return "bounded — max rounds"
	case EndReasonBoundedMaxMessages:
		return "bounded — max messages"
	}
	return ""
}

// HeaderText is the header's budget label:
//
//	round 2/3 · 7/10 messages
//
// It degrades to `2/3 · 7/10` when the full label does not fit the width it
// is handed, appends the end reason when the discussion is over, and returns
// "" when nothing fits — the caller can never be handed a label wider than
// the pane. A width of 0 or less means "unmeasured": the full label is
// returned, which is what the pure callers and tests want.
func (b DiscussionBudget) HeaderText(width int) string {
	if !b.HasDiscussion {
		return ""
	}
	round := strconv.Itoa(b.CurrentRound)
	if b.RoundUnknown {
		round = "?"
	}
	full := fmt.Sprintf("round %s/%d · %d/%d messages",
		round, MaxDiscussionRounds, b.MessagesUsed, MaxDiscussionMessages)
	short := fmt.Sprintf("%s/%d · %d/%d",
		round, MaxDiscussionRounds, b.MessagesUsed, MaxDiscussionMessages)

	var candidates []string
	if end := b.EndText(); end != "" {
		candidates = []string{full + " · " + end, short + " · " + end, end}
	} else {
		candidates = []string{full, short}
	}
	for _, c := range candidates {
		if width <= 0 || displayCols(c) <= width {
			return c
		}
	}
	return ""
}

// displayCols is the printed width of a label. These labels are Latin text
// plus “·” and “—”, each one column, so rune count is the display width;
// nothing here is byte-sliced.
func displayCols(s string) int { return utf8.RuneCountInString(s) }

// PeerPull is a member reply that cites one or more peers, which is what
// makes the gateway open the next round.
type PeerPull struct {
	// Event is the citing message itself: the transcript annotates that row,
	// because that is where the user can act on it.
	Event Event
	// Citer is the citing member's handle (no '@').
	Citer string
	// Handles are the cited peers, lowercased and deduplicated, in the order
	// they appear in the reply.
	Handles []string
	// Round is the citing message's 0-based round, -1 when the log did not
	// say.
	Round int
}

// Label renders the annotation the transcript shows under the citing row:
// `@a pulled @b into round 2`. The display is 1-based, so a reply in round 0
// pulls into round 2 as the user counts it.
func (p PeerPull) Label() string {
	if len(p.Handles) == 0 {
		return ""
	}
	cited := make([]string, 0, len(p.Handles))
	for _, h := range p.Handles {
		cited = append(cited, "@"+h)
	}
	// "the next round" rather than a number: the header counts rounds that
	// have already produced messages, so a numbered pull ("round 2") reads as
	// a contradiction while the pulled member is still working in round 1.
	// The engine's own wording is the same phrase, which keeps UI and prompt
	// agreeing.
	return fmt.Sprintf("@%s pulled %s into the next round", p.Citer, joinAnd(cited))
}

// joinAnd joins one, two or more labels the way prose does.
func joinAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
}

// PeerPulls finds every member reply that cites a peer the gateway can pull
// into the next round.
//
// Only handles the roster knows count: an unknown token is a typo, `@all`
// addresses the room rather than one member, and a member cannot pull
// themselves. A peer who already spoke (or passed) in this discussion is not
// pulled again, and neither is anyone cited from the final allowed round or
// after the message budget is spent — there is no next round to open.
func PeerPulls(events []Event, members []Member) []PeerPull {
	byHandle := make(map[string]Member, len(members))
	for _, m := range members {
		if h := strings.ToLower(memberHandle(m)); h != "" {
			byHandle[h] = m
		}
	}

	var out []PeerPull
	heard := map[string]bool{}
	messages := 0
	round := -1

	for _, ev := range events {
		if ev.Kind == "message.user" {
			heard = map[string]bool{}
			messages = 0
			round = -1
			continue
		}
		citer := memberHandleOf(ev)
		switch ev.Kind {
		case "turn.started", "turn.settled", "turn.failed", "turn.deferred":
			if citer != "" {
				heard[citer] = true
			}
			continue
		case "message.member":
		default:
			continue
		}

		messages++
		if idx, ok := ev.RoundIndex(); ok {
			round = idx
		}
		var pulled []string
		seen := map[string]bool{}
		if messages < MaxDiscussionMessages && round < MaxDiscussionRounds-1 {
			for _, mention := range ParseMentions(ev.Text()) {
				if mention.All || seen[mention.Handle] || mention.Handle == citer {
					continue
				}
				if _, ok := byHandle[mention.Handle]; !ok {
					continue
				}
				if heard[mention.Handle] {
					continue
				}
				seen[mention.Handle] = true
				pulled = append(pulled, mention.Handle)
			}
		}
		if len(pulled) > 0 {
			out = append(out, PeerPull{Event: ev, Citer: citer, Handles: pulled, Round: round})
		}
		if citer != "" {
			heard[citer] = true
		}
	}
	return out
}

// memberHandleOf is the handle an event's actor speaks as, empty when the
// event has no profile to derive one from.
func memberHandleOf(ev Event) string {
	if p := strings.TrimSpace(ev.Actor.Profile); p != "" {
		return strings.ToLower(HandleFor(p))
	}
	if s := strings.TrimSpace(ev.Speaker()); s != "" {
		return strings.ToLower(HandleFor(s))
	}
	return ""
}
