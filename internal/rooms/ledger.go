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
		var c Claim
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			continue
		}
		claims = append(claims, c)
	}
	return claims, nil
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
