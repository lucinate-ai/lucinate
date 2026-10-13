package rooms

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// ── wspólny katalog pokoju ───────────────────────────────────────────────────
//
// Problem, który ten plik rozwiązuje: członkowie pokoju zapisywali pliki
// względem katalogu SESJI (albo TUI, albo worktree), więc drugi członek ich
// nie znajdował — katalog po prostu nie istniał po jego stronie. Pokój
// dostaje więc jeden, wspólny, absolutny katalog na dysku, tworzony przy
// starcie pokoju i podawany wszystkim członkom w pierwszej wiadomości.
//
// Katalog nie ma rejestru ani koordynatora: ścieżka jest deterministyczna
// (<baza>/.rooms/<id-pokoju>), więc każdy klient, który rozwiąże pokój tak
// sam, trafi w to samo miejsce. Stąd też reguła, że identyfikator pokoju nie
// może wyprowadzić ścieżki poza ten katalog.

// RoomsDirName is the directory every room's shared workspace lives in,
// directly under the base (project root / worktree).
const RoomsDirName = ".rooms"

// RoomsBase returns the root room directories are created under: the Orca
// worktree named by ORCA_WORKTREE_ID when it carries a path, else the
// process working directory. Both are absolute, so the path a member is
// handed can be used verbatim.
func RoomsBase() (string, error) {
	if p := strings.TrimSpace(OrientationFromEnv().Path); p != "" {
		return filepath.Abs(p)
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("rooms base: %w", err)
	}
	return filepath.Abs(wd)
}

// roomDirLeaf maps a room id (or name) onto one path-safe directory name.
// Letters and digits survive in any script — a Polish room name stays
// readable — while separators, ".." and other hostile input collapse to
// '-' and are trimmed from both ends, so the id can never climb out of
// .rooms/. Two ids that differ only in punctuation may share a directory;
// the gateway's own ids are slugs and never do.
func roomDirLeaf(id string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.TrimSpace(id) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '.', r == '_', r == '-':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "room"
	}
	return out
}

// RoomDir resolves a room's shared directory under base without touching
// the disk — the pure half of EnsureRoomDir, and the one both the writer
// and the reader must agree on.
func RoomDir(base, roomID string) string {
	return filepath.Join(strings.TrimSpace(base), RoomsDirName, roomDirLeaf(roomID))
}

// EnsureRoomDir creates the room's shared directory (idempotent — a room
// can be re-created while it already exists) and returns its absolute
// path. It refuses to guess: an empty base or room id is an error, because
// the path it returns is what every member will be told to use.
func EnsureRoomDir(base, roomID string) (string, error) {
	if strings.TrimSpace(base) == "" {
		return "", fmt.Errorf("room directory: base is required")
	}
	if strings.TrimSpace(roomID) == "" {
		return "", fmt.Errorf("room directory: room id is required")
	}
	dir := RoomDir(base, roomID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("room directory %s: %w", dir, err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("room directory %s: %w", dir, err)
	}
	return abs, nil
}

// EnsureDefaultRoomDir creates the room's directory under RoomsBase() —
// for the moment a room is seated, when there is no project context to
// resolve yet beyond the worktree the client itself runs in.
func EnsureDefaultRoomDir(roomID string) (string, error) {
	base, err := RoomsBase()
	if err != nil {
		return "", err
	}
	return EnsureRoomDir(base, roomID)
}

// ── kontrakt handoff ─────────────────────────────────────────────────────────
//
// Sam katalog nie wystarcza: plik był „zapisany", a drugi członek go nie
// widział. Kontrakt domyka to po obu stronach — writer czyta plik z powrotem
// i cytuje rozmiar, reader przy braku pliku odpowiada fail-closed, nigdy nie
// zgaduje. Blok jedzie pierwszą wiadomością pokoju razem z adresem katalogu.

// HandoffReceiptPrefix opens every read-back confirmation a writer posts
// after reading its own file back. The shape is deliberately rigid —
//
//	HANDOFF <ścieżka> bytes=<rozmiar>
//
// — bo od tego wiersza zależy, czy da się potwierdzić zapis bez ufania
// deklaracji.
const HandoffReceiptPrefix = "HANDOFF"

// handoffReceiptRe matches the receipt shape above. The path is a single
// whitespace-free token (absolute paths the members are told to use contain
// none), the size must be a plain non-negative integer; the spaces around
// '=' are tolerated because an agent writing by hand will add them.
var handoffReceiptRe = regexp.MustCompile(`(?i)\b` + HandoffReceiptPrefix + `\s+(\S+)\s+bytes\s*=\s*(\d+)`)

// HandoffReceipt is one read-back confirmation: the file the writer claims
// to have read, and the byte count it quoted from that read.
type HandoffReceipt struct {
	Path  string
	Bytes int64
}

// HandoffBlock renders the contract a room's members get on the first
// message, together with the directory it governs. Empty dir → empty block:
// announcing a contract over a directory that was never resolved would be
// worse than saying nothing.
func HandoffBlock(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("[kontrakt handoff pokoju — wiąże wszystkich członków]\n")
	fmt.Fprintf(&b, "katalog pokoju (wspólny, jeden na pokój, absolutny): %s\n", dir)
	fmt.Fprintf(&b, "1. zapis: write_file do tego katalogu; używaj ścieżki bezwzględnej zaczynającej się od %s\n", dir)
	b.WriteString("2. po zapisie writer ODCZYTUJE plik z powrotem i cytuje potwierdzenie w odpowiedzi: " +
		HandoffReceiptPrefix + " <ścieżka> bytes=<rozmiar w bajtach>\n")
	b.WriteString("3. deklaracja zapisu bez odczytu zwrotnego to błąd — odpowiedź bez potwierdzenia " +
		HandoffReceiptPrefix + " … bytes= nie jest dowodem\n")
	b.WriteString("4. reader: czytaj tylko z tego katalogu; przy braku pliku odpowiada fail-closed " +
		"(„brak pliku: <ścieżka>”), nigdy nie zgaduje zawartości\n")
	return b.String()
}

// ParseHandoffReceipts pulls every receipt out of a member's reply. No
// receipt means no evidence: the caller treats an empty result as a
// declaration without a read-back.
func ParseHandoffReceipts(text string) []HandoffReceipt {
	matches := handoffReceiptRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]HandoffReceipt, 0, len(matches))
	for _, m := range matches {
		var n int64
		for _, c := range m[2] {
			n = n*10 + int64(c-'0')
		}
		out = append(out, HandoffReceipt{Path: m[1], Bytes: n})
	}
	return out
}

// VerifyHandoffs re-reads every receipt's file inside dir and compares the
// size on disk with the size the writer quoted. It fails closed: a missing
// file, an unreadable one, a size that moved, or a path that leaves the room
// directory are all errors, and the error names what failed.
func VerifyHandoffs(dir string, receipts []HandoffReceipt) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return fmt.Errorf("handoff: no room directory to verify against")
	}
	if len(receipts) == 0 {
		return fmt.Errorf("handoff: no %s receipt — a write declared without a read-back is an error", HandoffReceiptPrefix)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("handoff: room directory %s: %w", dir, err)
	}
	var errs []string
	for _, r := range receipts {
		full := r.Path
		if !filepath.IsAbs(full) {
			full = filepath.Join(absDir, filepath.FromSlash(full))
		}
		if !withinDir(absDir, full) {
			errs = append(errs, fmt.Sprintf("%s: path leaves the room directory", r.Path))
			continue
		}
		body, err := os.ReadFile(full)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", r.Path, err))
			continue
		}
		if int64(len(body)) != r.Bytes {
			errs = append(errs, fmt.Sprintf("%s: read back %d bytes, receipt says %d", r.Path, len(body), r.Bytes))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("handoff: %s", strings.Join(errs, "; "))
	}
	return nil
}

// VerifyHandoffReport applies the whole contract to one member reply: the
// reply must carry at least one receipt, and every receipt must check out.
// This is the call a reader (or a test) makes on what a member actually
// said — an answer with no receipt at all fails the same way a wrong one does.
func VerifyHandoffReport(dir, text string) error {
	return VerifyHandoffs(dir, ParseHandoffReceipts(text))
}

// withinDir reports whether candidate resolves inside dir. Both must be
// absolute for the comparison to mean anything; a different volume makes
// filepath.Rel fail, which counts as outside.
func withinDir(dir, candidate string) bool {
	rel, err := filepath.Rel(dir, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	return true
}
