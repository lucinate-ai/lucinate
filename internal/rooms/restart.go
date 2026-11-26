package rooms

import (
	"os"
	"strings"
)

// MaxRestartSummaryLines bounds the hand-over block /restart with-summary
// posts. The first message of a new room is context, not conversation: it has
// to be read in one screenful, because everything below it starts the room
// working.
const MaxRestartSummaryLines = 30

// restartMarker opens the block, and tells the new room what kind of message
// it is reading: background to absorb, not a question to answer.
const restartMarker = "[przeniesienie pokoju — kontekst do przeczytania, nie pytanie; odpowiedzi pod tym blokiem]"

// RestartSummary is the block /restart with-summary posts as the new room's
// first message: which room the roster came from, where that room's shared
// directory is (the state members own — INDEX, ledger, files), what was in the
// directory, and a short digest of the old transcript.
//
// The digest is LocalSummary — extractive, so it quotes what was said instead
// of interpreting it. A new room cannot verify an interpretation of history it
// has not seen; a quote it can re-read from the directory it was pointed at.
func RestartSummary(oldRoomID, oldName, oldDir string, events []Event) string {
	head := []string{restartMarker}
	if name := strings.TrimSpace(oldName); name == "" || name == oldRoomID {
		head = append(head, "pokój źródłowy: "+oldRoomID)
	} else {
		head = append(head, "pokój źródłowy: "+oldRoomID+" ("+name+")")
	}
	if dir := strings.TrimSpace(oldDir); dir != "" {
		head = append(head, "katalog starego pokoju (stan: INDEX/ledger/pliki): "+dir)
		head = append(head, roomDirLines(dir)...)
	}
	head = append(head, "skrót transcriptu starego pokoju:")

	// Whatever the header cost, the digest gets the lines that are left and
	// never more than the budget as a whole.
	budget := MaxRestartSummaryLines - len(head)
	if budget < 2 {
		budget = 2
	}
	lines := append(append([]string{}, head...), strings.Split(LocalSummary(events, budget-1), "\n")...)
	if len(lines) > MaxRestartSummaryLines {
		lines = append(lines[:MaxRestartSummaryLines-1], "…")
	}
	return strings.Join(lines, "\n")
}

// roomDirLines lists what a room's directory currently holds — the concrete
// state the summary promises. Directories first, files with their size, and
// never more than a handful: it is a pointer, not an inventory.
func roomDirLines(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for i, entry := range entries {
		if i >= 5 {
			out = append(out, "…")
			break
		}
		if entry.IsDir() {
			out = append(out, "- "+entry.Name()+"/")
			continue
		}
		line := "- " + entry.Name()
		if info, err := entry.Info(); err == nil {
			line += " (" + itoaSmall(int(info.Size())) + " B)"
		}
		out = append(out, line)
	}
	return out
}
