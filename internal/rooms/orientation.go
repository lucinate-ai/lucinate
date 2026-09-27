package rooms

import (
	"fmt"
	"os"
	"strings"
)

// Orientation is what a room's members need to know about the project they
// were seated for. A member agent runs inside the gateway process, not in
// an Orca terminal, so it inherits no ORCA_* environment and cannot
// discover the project on its own — it has to be told.
//
// The gateway's room payload carries exactly {text, thread_id}
// (hosted_room_discussion.validate_user_payload), so there is no system
// channel: the context rides the room's first user message, which also
// keeps it visible to the human reading the transcript.
type Orientation struct {
	// Worktree is an Orca worktree selector (id:<repoId>::<path>,
	// name:<displayName>, path:<path>, active/current).
	Worktree string
	// Path is the project's filesystem path, when known.
	Path string
	// Project is a human label (usually the worktree display name).
	Project string
}

// Empty reports whether there is nothing to say.
func (o Orientation) Empty() bool {
	return strings.TrimSpace(o.Worktree) == "" && strings.TrimSpace(o.Path) == ""
}

// Marker opens the block. It is deliberately loud: a member that answers
// the header instead of the message wastes a turn.
const OrientationMarker = "[kontekst projektu — to nie jest pytanie, nie odpowiadaj na tę linię]"

// Block renders the context header prepended to a room's first message.
//
// The Orca half is spelled out as a tool sequence rather than left to
// discovery: the browser tools need a page id, and an agent that does not
// know to create a tab first will report "browser unavailable" and stop.
func (o Orientation) Block() string {
	if o.Empty() {
		return ""
	}
	var b strings.Builder
	b.WriteString(OrientationMarker + "\n")
	label := strings.TrimSpace(o.Project)
	if label == "" && o.Path != "" {
		label = o.Path
	}
	if label != "" {
		fmt.Fprintf(&b, "projekt: %s\n", label)
	}
	if p := strings.TrimSpace(o.Path); p != "" {
		fmt.Fprintf(&b, "katalog: %s\n", p)
	}
	if w := strings.TrimSpace(o.Worktree); w != "" {
		fmt.Fprintf(&b, "worktree Orca: %s\n", w)
	}
	b.WriteString("praca z Orca: orca_worktree_current (potwierdź projekt) → orca_tab_list → " +
		"orca_tab_create url=<adres> → weź browserPageId z wyniku → orca_browser_goto page=<browserPageId> " +
		"→ orca_browser_snapshot (dowód, nie deklaracja); dokument dla użytkownika: orca_file_open path=<plik>\n")
	b.WriteString("każde narzędzie orca_* przyjmuje worktree=<selektor powyżej>, gdy pracujesz poza aktywnym worktree")
	return b.String()
}

// OrientationFromEnv derives the project context from the ORCA_* variables
// Orca sets in a terminal it owns, so a room seated from inside an Orca
// terminal inherits the right project without a flag.
//
// ORCA_WORKTREE_ID has the form "<repoId>::<path>".
func OrientationFromEnv() Orientation {
	raw := strings.TrimSpace(os.Getenv("ORCA_WORKTREE_ID"))
	if raw == "" {
		return Orientation{}
	}
	o := Orientation{Worktree: "id:" + raw}
	if i := strings.LastIndex(raw, "::"); i >= 0 {
		o.Path = raw[i+2:]
		o.Project = baseName(o.Path)
	}
	return o
}

// OrientationFromWorktree parses an Orca worktree selector into project
// context. Selectors are `id:<repoId>::<path>`, `path:<path>`,
// `name:<displayName>`, `branch:<b>`, or `active`/`current`.
func OrientationFromWorktree(selector string) Orientation {
	sel := strings.TrimSpace(selector)
	if sel == "" {
		return Orientation{}
	}
	o := Orientation{Worktree: sel}
	switch {
	case strings.HasPrefix(sel, "id:"):
		if i := strings.LastIndex(sel, "::"); i >= 0 {
			o.Path = sel[i+2:]
		}
	case strings.HasPrefix(sel, "path:"):
		o.Path = strings.TrimPrefix(sel, "path:")
	}
	if o.Path != "" {
		o.Project = baseName(o.Path)
	}
	if o.Project == "" && strings.HasPrefix(sel, "name:") {
		o.Project = strings.TrimPrefix(sel, "name:")
	}
	return o
}

func baseName(path string) string {
	path = strings.TrimRight(strings.ReplaceAll(path, "\\", "/"), "/")
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}
