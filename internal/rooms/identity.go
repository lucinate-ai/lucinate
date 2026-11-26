package rooms

import "strings"

// MemberHandle resolves the roster member an event refers to and returns the
// mention handle the transcript should label the row with.
//
// A member message names its author in the actor. Every turn event
// (`turn.started`, `turn.settled`, `turn.failed`, `turn.deferred`,
// `turn.cancelled`) names the member in its payload — `member_id`, with
// `member_index` as the fallback the gateway also sets — while the actor is
// the gateway that scheduled or settled the turn. The roster turns the id into
// the handle other members type.
//
// It returns "" when neither the payload nor the roster can place the member,
// so callers fall back to the actor rather than inventing a name.
func MemberHandle(ev Event, roster []Member) string {
	if h := memberHandleFromPayload(ev, roster); h != "" {
		return h
	}
	return handleForActor(ev.Actor)
}

// memberHandleFromPayload places a turn's member on the roster: by
// `member_id` first (stable across reconnects and roster edits), then by
// `member_index` into the slice.
func memberHandleFromPayload(ev Event, roster []Member) string {
	if id, ok := ev.Payload["member_id"].(string); ok && strings.TrimSpace(id) != "" {
		for _, m := range roster {
			if m.MemberID == id {
				return handleOfMember(m)
			}
		}
	}
	if idx := ev.MemberIndex(); idx >= 0 && idx < len(roster) {
		return handleOfMember(roster[idx])
	}
	return ""
}

// handleOfMember is the one place a roster row becomes a handle.
func handleOfMember(m Member) string {
	if strings.TrimSpace(m.Handle) != "" {
		return m.Handle
	}
	if strings.TrimSpace(m.Profile) != "" {
		return HandleFor(m.Profile)
	}
	return ""
}

// handleForActor is the fallback for a member message, whose actor is the
// member itself. A gateway actor has no profile and no handle, so it resolves
// to "" instead of letting an installation id masquerade as a member.
func handleForActor(a Actor) string {
	if a.Profile != "" {
		return HandleFor(a.Profile)
	}
	if a.Kind != "member" {
		return ""
	}
	if a.DisplayName != "" {
		return HandleFor(a.DisplayName)
	}
	return ""
}
