package rooms

import (
	"strings"
)

// Routing is a client-side decision layered on top of the gateway's own
// routing: the hosted-room protocol keeps mention parsing server-side, so
// the only way a client can steer a message is to add the mention itself.
// These modes exist because "every member answers everything" is expensive
// across a six-profile room, and a user who wants one answer should not
// have to type a handle every time.
//
// An explicit mention in the text always wins: the user typed it, so the
// mode must not swallow it.
type RoutingMode string

const (
	// RouteBroadcast leaves the text alone; the whole roster answers.
	RouteBroadcast RoutingMode = "broadcast"
	// RouteModerator sends every un-mentioned message to one member.
	RouteModerator RoutingMode = "moderator"
	// RouteRoundRobin walks the roster, one member per message.
	RouteRoundRobin RoutingMode = "round-robin"
)

// ParseRoutingMode reads a mode from user input, accepting the aliases the
// command line suggests. An empty string is the default (broadcast).
func ParseRoutingMode(s string) (RoutingMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "broadcast", "all", "everyone":
		return RouteBroadcast, nil
	case "moderator", "mod":
		return RouteModerator, nil
	case "round-robin", "roundrobin", "round_robin", "robin", "rr":
		return RouteRoundRobin, nil
	default:
		return RouteBroadcast, errUnknownRoutingMode(s)
	}
}

type routingModeError string

func (e routingModeError) Error() string {
	return "unknown routing mode \"" + string(e) + "\" — use broadcast, moderator or round-robin"
}

func errUnknownRoutingMode(s string) error { return routingModeError(strings.TrimSpace(s)) }

// Mention is one @handle found in an outgoing message.
type Mention struct {
	// Handle is the mention token, lowercased: the gateway's grammar is
	// case-insensitive and its handles are lowercase by construction.
	Handle string
	// All is true for @all / @everyone, which address the whole roster.
	All bool
	// Offset is the byte index of the '@' in the message.
	Offset int
}

// mentionWordRunes are the characters the gateway's mention grammar allows
// after the first character — the same class HandleFor produces.
func mentionWordRune(r byte) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '.', r == '-', r == '_', r == ':':
		return true
	}
	return false
}

// ParseMentions extracts every @handle in an outgoing message.
//
// It mirrors the gateway's grammar (@([A-Za-z0-9][A-Za-z0-9._:-]*)) rather
// than splitting on whitespace, so a mention followed by punctuation
// (@matt, get to it) is still one handle. An '@' preceded by a handle
// character is not a mention: that keeps email addresses out, which matters
// because "mail kowal@example.com" must not send the room to @example.com.
func ParseMentions(text string) []Mention {
	var out []Mention
	for i := 0; i < len(text); i++ {
		if text[i] != '@' {
			continue
		}
		if i > 0 && mentionWordRune(text[i-1]) {
			continue
		}
		start := i + 1
		if start >= len(text) {
			continue
		}
		if r := text[start]; !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			continue
		}
		end := start
		for end < len(text) && mentionWordRune(text[end]) {
			end++
		}
		handle := strings.ToLower(text[start:end])
		out = append(out, Mention{
			Handle: handle,
			All:    handle == "all" || handle == "everyone",
			Offset: i,
		})
	}
	return out
}

// FindMember resolves a handle to a roster row, accepting the profile name
// as an alias so `@olesno-lead-ops` works whether the room's handle was
// derived or hand-written.
func FindMember(members []Member, handle string) *Member {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return nil
	}
	for i := range members {
		if strings.EqualFold(members[i].Handle, handle) {
			return &members[i]
		}
	}
	for i := range members {
		if strings.EqualFold(members[i].Profile, handle) {
			return &members[i]
		}
	}
	return nil
}

// ResolveMentions binds the handles in a message to roster rows, retaining
// the order the user wrote them. Handles that match nobody are returned
// separately: the message still goes out as written (the gateway is the
// authority on which profiles exist), but the user is told.
func ResolveMentions(text string, members []Member) (targets []Member, unknown []string, all bool) {
	seen := map[string]bool{}
	for _, m := range ParseMentions(text) {
		if m.All {
			all = true
			continue
		}
		if seen[m.Handle] {
			continue
		}
		seen[m.Handle] = true
		if member := FindMember(members, m.Handle); member != nil {
			targets = append(targets, *member)
			continue
		}
		unknown = append(unknown, m.Handle)
	}
	return targets, unknown, all
}

// RouteDecision is where one outgoing message goes, and why. Text is what
// actually reaches the gateway — a mode that picks a member appends that
// member's mention to it.
type RouteDecision struct {
	Mode     RoutingMode
	Targets  []Member
	Text     string
	Explicit bool
	Note     string
}

// Route applies the room's routing mode to a message.
//
// It returns the decision plus the round-robin cursor for the next message
// (unchanged for every other mode).
func Route(text string, members []Member, mode RoutingMode, moderator string, rrIndex int) (RouteDecision, int) {
	targets, unknown, all := ResolveMentions(text, members)

	if all {
		return RouteDecision{
			Mode: mode, Targets: members, Text: text, Explicit: true,
			Note: "addressed to the whole roster (@all)",
		}, rrIndex
	}
	if len(targets) > 0 || len(unknown) > 0 {
		return RouteDecision{
			Mode: mode, Targets: targets, Text: text, Explicit: true,
			Note: explicitNote(targets, unknown),
		}, rrIndex
	}
	if len(members) == 0 {
		return RouteDecision{Mode: mode, Text: text, Note: "no roster — nothing to route to"}, rrIndex
	}

	switch mode {
	case RouteModerator:
		member := FindMember(members, moderator)
		note := "moderator @" + memberHandle(*memberOrFirst(members, member)) + " takes this one"
		if member == nil {
			note = "no member matches moderator \"" + strings.TrimSpace(moderator) + "\" — @" +
				memberHandle(members[0]) + " takes this one"
		}
		picked := *memberOrFirst(members, member)
		return RouteDecision{
			Mode: RouteModerator, Targets: []Member{picked},
			Text: mentionText(picked, text), Note: note,
		}, rrIndex
	case RouteRoundRobin:
		i := rrIndex % len(members)
		if i < 0 {
			i += len(members)
		}
		picked := members[i]
		return RouteDecision{
			Mode: RouteRoundRobin, Targets: []Member{picked},
			Text: mentionText(picked, text),
			Note: "round-robin: @" + memberHandle(picked) + " (" + itoaSmall(i+1) + "/" + itoaSmall(len(members)) + ")",
		}, i + 1
	case RouteBroadcast:
		return RouteDecision{
			Mode: RouteBroadcast, Targets: members, Text: text,
			Note: "broadcast to " + itoaSmall(len(members)) + " member(s)",
		}, rrIndex
	default:
		return RouteDecision{
			Mode: RouteBroadcast, Targets: members, Text: text,
			Note: "unknown routing mode \"" + string(mode) + "\" — broadcast to " + itoaSmall(len(members)) + " member(s)",
		}, rrIndex
	}
}

func memberOrFirst(members []Member, m *Member) *Member {
	if m != nil {
		return m
	}
	if len(members) == 0 {
		return nil
	}
	return &members[0]
}

func memberHandle(m Member) string {
	if m.Handle != "" {
		return m.Handle
	}
	return HandleFor(m.Profile)
}

// mentionText prepends the mention the gateway needs to route to one member.
func mentionText(m Member, text string) string {
	if text == "" {
		return "@" + memberHandle(m)
	}
	return "@" + memberHandle(m) + " " + text
}

func explicitNote(targets []Member, unknown []string) string {
	if len(targets) == 0 {
		return "unknown handle @" + strings.Join(unknown, ", @") + " sent as written — the roster decides"
	}
	handles := make([]string, 0, len(targets))
	for _, m := range targets {
		handles = append(handles, "@"+memberHandle(m))
	}
	note := "addressed to " + strings.Join(handles, " ")
	if len(unknown) > 0 {
		note += "; unknown handle @" + strings.Join(unknown, ", @") + " sent as written"
	}
	return note
}

// itoaSmall avoids pulling strconv in for the handful of counts rendered in
// a status line.
func itoaSmall(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
