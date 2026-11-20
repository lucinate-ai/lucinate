package rooms

import "strings"

// Streaming state for a room transcript.
//
// A hosted room's members reply through the gateway's log, and long answers
// arrive as a run of partial events before the full message lands. The TUI
// needs to know, from the log alone, which members are mid-answer, what they
// have produced so far, and when a turn ended without producing anything —
// an empty reply is a real outcome and its placeholder has to go, or the
// room looks like it is still thinking.

// StreamDeltaKind is the event kind a gateway uses for a partial member
// reply. Payloads may also mark a `message.member` partial with a flag
// instead (see IsStreamDelta), so both shapes are understood.
const StreamDeltaKind = "message.member.delta"

// Stream is one member's reply as far as the transcript shows it.
type Stream struct {
	// Member is the mention handle, the stable key a reader sees.
	Member string
	// Profile is the Hermes profile behind the handle, when known.
	Profile string
	// Text is the reply so far — the newest cumulative delta, or the
	// finished message once it arrives.
	Text string
	// Started is true once the member's turn began.
	Started bool
	// Settled is true once the turn ended (settled, failed, deferred or
	// the member turned out unavailable), or once the finished message
	// arrived.
	Settled bool
	// Empty is true when the turn ended without producing text: the row
	// that showed a spinner must be removed rather than kept.
	Empty bool
}

// Label is what the transcript renders the row under.
func (s Stream) Label() string {
	if s.Member == "" {
		return "@?"
	}
	return "@" + s.Member
}

// IsStreamDelta reports whether an event carries a partial reply.
//
// The kind is the documented shape; the flags cover gateways that reuse
// `message.member` and mark partials in the payload, which is how the chat
// backend's SSE feed behaves.
func IsStreamDelta(ev Event) bool {
	if ev.Kind == StreamDeltaKind {
		return true
	}
	if ev.Kind != "message.member" {
		return false
	}
	return boolPayload(ev.Payload, "partial") || boolPayload(ev.Payload, "streaming")
}

// StreamsFor folds a transcript into one stream per member, in the order
// each member was first seen.
//
// Deltas are treated as cumulative — each carries the full text so far, the
// same contract the chat backend's deltas use — unless the payload asks to
// append, which a gateway that streams tokens may do instead.
func StreamsFor(events []Event) []Stream {
	var order []string
	index := map[string]int{}
	streams := []Stream{}

	get := func(ev Event) *Stream {
		key := streamMember(ev)
		if i, ok := index[key]; ok {
			return &streams[i]
		}
		index[key] = len(streams)
		order = append(order, key)
		streams = append(streams, Stream{Member: key, Profile: streamProfile(ev)})
		return &streams[len(streams)-1]
	}

	for _, ev := range events {
		switch {
		case ev.Kind == "message.user":
			continue
		case ev.Kind == "turn.started":
			// A new turn resets the text: the previous answer is done, and
			// carrying it over would mix two answers in one row.
			s := get(ev)
			s.Text = ""
			s.Started = true
			s.Settled = false
			s.Empty = false
		case IsStreamDelta(ev):
			s := get(ev)
			s.Started = true
			if boolPayload(ev.Payload, "append") {
				s.Text += ev.Text()
			} else {
				s.Text = ev.Text()
			}
		case ev.Kind == "message.member":
			s := get(ev)
			s.Text = ev.Text()
			s.Settled = true
			s.Empty = strings.TrimSpace(s.Text) == ""
		case ev.Kind == "turn.settled", ev.Kind == "turn.failed", ev.Kind == "turn.deferred",
			ev.Kind == "member.unavailable":
			s := get(ev)
			s.Settled = true
			if strings.TrimSpace(s.Text) == "" {
				s.Empty = true
			}
		}
	}
	return streams
}

// ActiveStreams are the members still answering — the rows that animate.
func ActiveStreams(events []Event) []Stream {
	var out []Stream
	for _, s := range StreamsFor(events) {
		if s.Started && !s.Settled {
			out = append(out, s)
		}
	}
	return out
}

// streamMember picks the key a stream is tracked under, preferring the
// gateway's own handle field and falling back to the same derivation the
// roster uses so a partial reply still lands on the right member.
func streamMember(ev Event) string {
	if h, _ := ev.Payload["handle"].(string); strings.TrimSpace(h) != "" {
		return strings.ToLower(strings.TrimSpace(h))
	}
	if ev.Actor.Profile != "" {
		return HandleFor(ev.Actor.Profile)
	}
	if ev.Actor.DisplayName != "" {
		return HandleFor(ev.Actor.DisplayName)
	}
	if ev.Actor.ID != "" {
		return HandleFor(ev.Actor.ID)
	}
	return HandleFor(ev.Speaker())
}

func streamProfile(ev Event) string {
	if p, _ := ev.Payload["profile"].(string); strings.TrimSpace(p) != "" {
		return p
	}
	return ev.Actor.Profile
}

func boolPayload(payload map[string]any, key string) bool {
	v, ok := payload[key]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}
