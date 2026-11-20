package rooms

import "strings"

// installationPrefix marks a gateway actor whose id is the installation, not a
// person and not a roster member.
const installationPrefix = "install:"

// IsInstallationID reports whether a label is the gateway's own installation
// identifier. That id must never be shown as if it were a member.
func IsInstallationID(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), installationPrefix)
}

// ControlLabel returns the human wording for a room control event — a
// gateway-owned receipt with no member behind it — and whether the kind is one.
func ControlLabel(kind string) (string, bool) {
	switch kind {
	case "room.created":
		return "room created", true
	case "room.members_changed":
		return "roster changed", true
	case "room.stop_requested":
		return "stop requested", true
	}
	return "", false
}

// EventLabel is the one place a row that is not a member action becomes a
// label: the control wording when the kind is one, the kind itself otherwise,
// plus the speaker only when that speaker is a person or a member. A gateway's
// installation id is never appended, so `room.stop_requested install:<hash>`
// cannot reach the screen.
func EventLabel(ev Event) string {
	label, ok := ControlLabel(ev.Kind)
	if !ok {
		label = ev.Kind
	}
	speaker := ev.Speaker()
	if speaker == "" || IsInstallationID(speaker) {
		return label
	}
	return label + " " + speaker
}
