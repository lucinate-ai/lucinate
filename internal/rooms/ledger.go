package rooms

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A ledger is the room's shared work ledger: one JSON line per claim, done
// marker or blocker, written to <room>/work.jsonl. Members read it before
// starting work so two of them do not claim the same task and a finished
// task keeps its owner.
//
// There is no lock: the file is appended to by the one process that owns
// the room directory, and a single O_APPEND write of one line is atomic
// enough for that. flock would cost the Windows build for no gain.

// Claim is one row of the work ledger.
type Claim struct {
	Task     string    `json:"task"`     // short slug, what is being done
	Owner    string    `json:"owner"`    // member handle, without @
	Status   string    `json:"status"`   // "claim" | "done" | "blocked"
	Artifact string    `json:"artifact"` // path relative to the room dir, or absolute
	Bytes    int64     `json:"bytes"`    // 0 while merely claimed
	Note     string    `json:"note,omitempty"`
	At       time.Time `json:"at"`
}

// LedgerPath is the file a room's claims live in.
func LedgerPath(dir string) string {
	return filepath.Join(dir, "work.jsonl")
}

// EnsureLedger makes sure <dir>/work.jsonl exists so a member following the
// brief finds a real file instead of a promise. It never truncates.
func EnsureLedger(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("ledger: directory is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ledger: directory %s: %w", dir, err)
	}
	f, err := os.OpenFile(LedgerPath(dir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("ledger: open %s: %w", LedgerPath(dir), err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("ledger: close %s: %w", LedgerPath(dir), err)
	}
	return nil
}

// AppendClaim adds one line to the ledger, creating the room directory if
// needed. Owner loses a leading @ and At defaults to now, so the stored row
// is the canonical one — the returned Claim is what a caller should show.
func AppendClaim(dir string, c Claim) (Claim, error) {
	if strings.TrimSpace(dir) == "" {
		return c, fmt.Errorf("ledger: directory is required")
	}
	c.Task = strings.TrimSpace(c.Task)
	c.Owner = cleanOwner(c.Owner)
	if c.Task == "" {
		return c, fmt.Errorf("ledger: task is required")
	}
	if c.Owner == "" {
		return c, fmt.Errorf("ledger: owner is required")
	}
	if c.At.IsZero() {
		c.At = time.Now()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return c, fmt.Errorf("ledger: directory %s: %w", dir, err)
	}
	line, err := json.Marshal(c)
	if err != nil {
		return c, fmt.Errorf("ledger: encode %s: %w", c.Task, err)
	}
	f, err := os.OpenFile(LedgerPath(dir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return c, fmt.Errorf("ledger: open %s: %w", LedgerPath(dir), err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return c, fmt.Errorf("ledger: write %s: %w", LedgerPath(dir), err)
	}
	if err := f.Close(); err != nil {
		return c, fmt.Errorf("ledger: close %s: %w", LedgerPath(dir), err)
	}
	return c, nil
}

// ReadLedger returns every well-formed claim, in file order. A missing file
// is an empty ledger; a malformed line is skipped rather than fatal, because
// a half-written line must not blind the whole room.
func ReadLedger(dir string) ([]Claim, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(LedgerPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("ledger: read %s: %w", LedgerPath(dir), err)
	}
	var claims []Claim
	for _, line := range splitLines(string(data)) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		c, ok := parseLedgerLine(line)
		if !ok {
			continue
		}
		claims = append(claims, c)
	}
	return claims, nil
}

// ledgerLine is the union of the two shapes members write. The canonical row
// is AppendClaim's, but a member in a live room produced
// {"claim","file","ts"} instead — a ledger that silently dropped those rows
// hid every owner from Board and DuplicateOf.
type ledgerLine struct {
	Task     string          `json:"task"`
	Owner    string          `json:"owner"`
	Status   string          `json:"status"`
	Artifact string          `json:"artifact"`
	Bytes    int64           `json:"bytes"`
	Note     string          `json:"note,omitempty"`
	At       json.RawMessage `json:"at"`

	Claim string `json:"claim"`
	File  string `json:"file"`
	TS    string `json:"ts"`
}

// parseLedgerLine maps one JSON line onto a Claim, accepting either shape.
// ok is false for a line that is not JSON at all.
func parseLedgerLine(line string) (Claim, bool) {
	var raw ledgerLine
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return Claim{}, false
	}
	c := Claim{
		Task:     firstNonEmpty(raw.Task, raw.Claim),
		Owner:    raw.Owner,
		Status:   raw.Status,
		Artifact: firstNonEmpty(raw.Artifact, raw.File),
		Bytes:    raw.Bytes,
		Note:     raw.Note,
		At:       firstTime(raw.At, raw.TS),
	}
	return c, true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// firstTime reads the timestamp from either the canonical `at` (an RFC 3339
// string or a time.Time marshal) or the member's date-only `ts`.
func firstTime(raw json.RawMessage, ts string) time.Time {
	if len(raw) > 0 {
		var t time.Time
		if err := json.Unmarshal(raw, &t); err == nil && !t.IsZero() {
			return t
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil && s != "" {
			if parsed, ok := parseTimeString(s); ok {
				return parsed
			}
		}
	}
	if ts != "" {
		if parsed, ok := parseTimeString(ts); ok {
			return parsed
		}
	}
	return time.Time{}
}

func parseTimeString(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// Board renders the ledger for the room transcript: at most 12 lines, the
// most recent claims kept, grouped by status. An empty ledger renders "" so
// the caller can skip the block entirely.
func Board(claims []Claim) string {
	var lines []string
	for _, c := range claims {
		task := strings.TrimSpace(c.Task)
		if task == "" {
			continue
		}
		owner := cleanOwner(c.Owner)
		switch c.Status {
		case "done":
			lines = append(lines, fmt.Sprintf("zrobione: %s (@%s, %d B)", task, owner, c.Bytes))
		case "blocked":
			lines = append(lines, fmt.Sprintf("zablokowane: %s (@%s)", task, owner))
		case "claim":
			lines = append(lines, fmt.Sprintf("w toku: %s (@%s)", task, owner))
		}
	}
	if len(lines) > 12 {
		lines = lines[len(lines)-12:]
	}
	return strings.Join(lines, "\n")
}

// DuplicateOf reports a claim that already finished the same artifact for a
// different owner. It returns the claim, so the caller can name who did it;
// a same-owner re-claim is not a duplicate.
func DuplicateOf(claims []Claim, owner string, artifact string) *Claim {
	target := cleanArtifact(artifact)
	if target == "" {
		return nil
	}
	owner = cleanOwner(owner)
	for i := range claims {
		c := claims[i]
		if c.Status != "done" {
			continue
		}
		if cleanOwner(c.Owner) == owner {
			continue
		}
		if cleanArtifact(c.Artifact) == target {
			return &c
		}
	}
	return nil
}

func cleanOwner(owner string) string {
	return strings.TrimPrefix(strings.TrimSpace(owner), "@")
}

func cleanArtifact(artifact string) string {
	artifact = strings.TrimSpace(artifact)
	if artifact == "" {
		return ""
	}
	return filepath.Clean(filepath.FromSlash(artifact))
}
