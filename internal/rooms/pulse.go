package rooms

import "strings"

// A pulse is the client moderating the room. The gateway runs one member
// turn at a time, so a round opened by the first speaker is a queue of
// monologues: the later speakers never saw the earlier ones. Only the client
// can post a "user" message, so it publishes the board and a short opinion
// prompt while the room is idle — that opens a NEW discussion (the gateway's
// round budget is per discussion) and the members then see each other.

// pulseAsk closes every pulse. It is the same three questions each round so
// the replies line up, and it forbids doing work so a pulse cannot collide
// with a claim already on the board.
const pulseAsk = "Odpowiedz w maksymalnie 120 slowach: (1) twoje stanowisko, (2) czego nie wiesz, (3) co bierzesz. Nie wykonuj pracy w tej rundzie i nie powtarzaj zadan, ktore maja juz wlasciciela na tablicy."

// PulseText renders the moderator message. An empty goal falls back to
// "co dalej"; an empty board omits the header so a fresh room is not told
// about an empty table. Nothing here embeds a timestamp, so equal inputs
// produce equal bytes.
func PulseText(goal string, board string) string {
	return PulseTextWithBrief(goal, board, "", "")
}

// PulseTextWithBrief is PulseText plus the one line that tells the room its
// brief was regenerated. digest is the brief just written, quoted the digest
// the room's first message carried. The line appears only when both are known
// and differ — a pulse that always claimed a refresh would teach members to
// ignore the claim.
func PulseTextWithBrief(goal, board, digest, quoted string) string {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		goal = "co dalej"
	}
	var sb strings.Builder
	sb.WriteString("[Puls pokoju] ")
	sb.WriteString(goal)
	sb.WriteString("\n\n")
	if board != "" {
		sb.WriteString("Tablica robot:\n")
		sb.WriteString(board)
		sb.WriteString("\n\n")
	}
	if line := BriefRefreshLine(digest, quoted); line != "" {
		sb.WriteString(line)
		sb.WriteString("\n\n")
	}
	sb.WriteString(pulseAsk)
	return sb.String()
}

// BriefRefreshLine reports the line a pulse carries when the brief on disk no
// longer matches the digest the room was pointed at. Empty when the two agree
// or either is unknown.
func BriefRefreshLine(digest, quoted string) string {
	digest = strings.TrimSpace(digest)
	quoted = strings.TrimSpace(quoted)
	if digest == "" || digest == quoted {
		return ""
	}
	return "brief sha=" + digest + " — odświeżony, przeczytaj ponownie"
}

// ShouldPulse reports whether the room has earned another pulse: it must be
// idle, and something must have happened since the previous one. Without the
// second condition a silent room would be pulsed forever.
func ShouldPulse(idle bool, lastSeq, lastPulseSeq int) bool {
	return idle && lastSeq > lastPulseSeq
}
