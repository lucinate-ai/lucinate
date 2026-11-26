package rooms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLedger_AppendReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	got, err := AppendClaim(dir, Claim{
		Task:     "api",
		Owner:    "@ania",
		Status:   "claim",
		Artifact: "docs/api.md",
		Bytes:    0,
		Note:     "first pass",
	})
	if err != nil {
		t.Fatalf("AppendClaim: %v", err)
	}
	if got.Owner != "ania" {
		t.Errorf("Owner = %q, want the @ stripped", got.Owner)
	}
	if got.At.IsZero() {
		t.Error("At was left zero")
	}

	claims, err := ReadLedger(dir)
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("ReadLedger returned %d claims, want 1", len(claims))
	}
	c := claims[0]
	if c.Task != "api" || c.Owner != "ania" || c.Status != "claim" || c.Artifact != "docs/api.md" || c.Note != "first pass" {
		t.Fatalf("round trip lost fields: %+v", c)
	}
	if !c.At.Equal(got.At) {
		t.Errorf("At = %v, want %v", c.At, got.At)
	}

	if got, err := AppendClaim(dir, Claim{Task: " "}); err == nil {
		t.Errorf("empty task accepted: %+v", got)
	}
	if _, err := AppendClaim(dir, Claim{Task: "x", Owner: "@"}); err == nil {
		t.Error("empty owner accepted")
	}
}

func TestEnsureLedger_CreatesAndNeverTruncates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "room", "nested")
	if err := EnsureLedger(dir); err != nil {
		t.Fatalf("EnsureLedger: %v", err)
	}
	info, err := os.Stat(LedgerPath(dir))
	if err != nil {
		t.Fatalf("work.jsonl missing after EnsureLedger: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("fresh work.jsonl is %d bytes, want 0", info.Size())
	}

	if _, err := AppendClaim(dir, Claim{Task: "api", Owner: "ania", Status: "claim"}); err != nil {
		t.Fatalf("AppendClaim: %v", err)
	}
	if err := EnsureLedger(dir); err != nil {
		t.Fatalf("EnsureLedger on existing ledger: %v", err)
	}
	claims, err := ReadLedger(dir)
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	if len(claims) != 1 || claims[0].Task != "api" {
		t.Fatalf("EnsureLedger truncated the ledger: %+v", claims)
	}

	if err := EnsureLedger(""); err == nil {
		t.Error("EnsureLedger(\"\") accepted an empty directory")
	}
}

func TestReadLedger_SkipsMalformedLinesAndMissingFile(t *testing.T) {
	dir := t.TempDir()
	body := `{"task":"ok","owner":"a","status":"done","artifact":"a.md","bytes":3}
not json at all
{"task":"also-ok","owner":"b","status":"claim"}
`
	if err := os.WriteFile(LedgerPath(dir), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	claims, err := ReadLedger(dir)
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	if len(claims) != 2 {
		t.Fatalf("ReadLedger kept %d claims, want 2 (malformed line skipped): %+v", len(claims), claims)
	}
	if claims[0].Task != "ok" || claims[1].Task != "also-ok" {
		t.Errorf("claims = %+v", claims)
	}

	empty, err := ReadLedger(t.TempDir())
	if err != nil {
		t.Fatalf("ReadLedger on a missing file: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("missing ledger = %+v, want empty", empty)
	}
}

// A member in a live room wrote the claim/file/ts shape instead of the
// client's task/artifact/at. Dropping those rows hid every owner from the
// board and from duplicate detection.
func TestReadLedgerAcceptsTheMemberShape(t *testing.T) {
	dir := t.TempDir()
	line := `{"claim":"grep maili i sekwencji pod frazy zakazane","owner":"freelancer","status":"done","file":"freelancer-grep-maile-2026-11-04.md","ts":"2026-11-04"}`
	if err := os.WriteFile(LedgerPath(dir), []byte(line+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	claims, err := ReadLedger(dir)
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("ReadLedger returned %d claims, want 1: %+v", len(claims), claims)
	}
	c := claims[0]
	if c.Task != "grep maili i sekwencji pod frazy zakazane" {
		t.Errorf("Task = %q, want the member's claim text", c.Task)
	}
	if c.Owner != "freelancer" || c.Status != "done" {
		t.Errorf("Owner/Status = %q/%q", c.Owner, c.Status)
	}
	if c.Artifact != "freelancer-grep-maile-2026-11-04.md" {
		t.Errorf("Artifact = %q, want the member's file", c.Artifact)
	}
	if c.At.IsZero() || c.At.Format("2006-01-02") != "2026-11-04" {
		t.Errorf("At = %v, want the parsed date-only ts", c.At)
	}

	board := Board(claims)
	if !strings.Contains(board, "zrobione: grep maili") || !strings.Contains(board, "@freelancer") {
		t.Errorf("Board does not show the member's claim:\n%s", board)
	}

	dup := DuplicateOf(claims, "@ania", "freelancer-grep-maile-2026-11-04.md")
	if dup == nil || dup.Owner != "freelancer" {
		t.Errorf("DuplicateOf = %+v, want the member's done claim", dup)
	}
}

func TestDuplicateOf_OnlyCountsAnotherOwnersDoneArtifact(t *testing.T) {
	claims := []Claim{
		{Task: "a", Owner: "a", Status: "done", Artifact: "out.md", Bytes: 5},
		{Task: "b", Owner: "b", Status: "claim", Artifact: "other.md"},
		{Task: "c", Owner: "y", Status: "done", Artifact: "./out.md", Bytes: 5},
	}

	dup := DuplicateOf(claims, "@b", "out.md")
	if dup == nil {
		t.Fatal("DuplicateOf found nothing for @b/out.md")
	}
	if dup.Owner != "a" || dup.Task != "a" {
		t.Errorf("DuplicateOf = %+v, want the first other-owner done claim", dup)
	}

	if dup := DuplicateOf(claims, "a", "out.md"); dup == nil || dup.Owner != "y" {
		t.Errorf("same-owner claim must not count as its own duplicate; got %+v", dup)
	}
	if dup := DuplicateOf([]Claim{claims[0]}, "a", "out.md"); dup != nil {
		t.Errorf("same owner re-claim reported as duplicate: %+v", dup)
	}
	if dup := DuplicateOf(claims, "b", "missing.md"); dup != nil {
		t.Errorf("unknown artifact reported as duplicate: %+v", dup)
	}
}

func TestBoard_EmptyAndBounded(t *testing.T) {
	if got := Board(nil); got != "" {
		t.Errorf("Board(nil) = %q, want empty", got)
	}

	claims := make([]Claim, 0, 30)
	for i := 0; i < 30; i++ {
		status := "claim"
		if i%3 == 0 {
			status = "done"
		}
		claims = append(claims, Claim{Task: "zadanie", Owner: "a", Status: status})
	}
	board := Board(claims)
	if board == "" {
		t.Fatal("Board is empty for 30 claims")
	}
	if n := len(strings.Split(board, "\n")); n > 12 {
		t.Fatalf("Board has %d lines, want <= 12:\n%s", n, board)
	}
	if !strings.Contains(board, "zrobione: ") {
		t.Errorf("Board has no done rows:\n%s", board)
	}
}
