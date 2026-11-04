package rooms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyReply_NoReceiptIsNone(t *testing.T) {
	dir := t.TempDir()
	ev := VerifyReply(dir, "a", "zrobilem zadanie, wszystko gotowe")
	if ev.Status != EvidenceNone {
		t.Errorf("Status = %q, want none", ev.Status)
	}
	if ev.Detail != "brak potwierdzenia zapisu" {
		t.Errorf("Detail = %q", ev.Detail)
	}
	if len(ev.Receipts) != 0 {
		t.Errorf("Receipts = %+v, want none", ev.Receipts)
	}
}

func TestVerifyReply_EmptyDirOrTextIsNone(t *testing.T) {
	for _, tc := range []struct{ dir, text string }{
		{"", "HANDOFF out.md bytes=5"},
		{t.TempDir(), ""},
	} {
		if ev := VerifyReply(tc.dir, "a", tc.text); ev.Status != EvidenceNone {
			t.Errorf("VerifyReply(%q, %q) = %q, want none", tc.dir, tc.text, ev.Status)
		}
	}
}

func TestVerifyReply_VerifiedByReadBack(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "out.md"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ev := VerifyReply(dir, "a", "gotowe\nHANDOFF out.md bytes=5")
	if ev.Status != EvidenceVerified {
		t.Fatalf("Status = %q, want verified (%s)", ev.Status, ev.Detail)
	}
	if !strings.Contains(ev.Detail, "out.md") || !strings.Contains(ev.Detail, "5 B") {
		t.Errorf("Detail = %q, want the path and size", ev.Detail)
	}
	if len(ev.Receipts) != 1 || ev.Receipts[0].Bytes != 5 {
		t.Errorf("Receipts = %+v", ev.Receipts)
	}
}

func TestVerifyReply_WrongByteCountFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "out.md"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ev := VerifyReply(dir, "a", "HANDOFF out.md bytes=99")
	if ev.Status != EvidenceFailed {
		t.Fatalf("Status = %q, want failed", ev.Status)
	}
	if ev.Detail == "" || !strings.Contains(ev.Detail, "99") {
		t.Errorf("Detail = %q, want the verifier's mismatch", ev.Detail)
	}
}

func TestVerifyReply_DuplicateOfAnotherOwnersDoneArtifact(t *testing.T) {
	dir := t.TempDir()
	if _, err := AppendClaim(dir, Claim{
		Task: "api", Owner: "a", Status: "done", Artifact: "out.md", Bytes: 5,
	}); err != nil {
		t.Fatalf("AppendClaim: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "out.md"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ev := VerifyReply(dir, "b", "tez to zrobilem\nHANDOFF out.md bytes=5")
	if ev.Status != EvidenceDuplicate {
		t.Fatalf("Status = %q, want duplicate (%s)", ev.Status, ev.Detail)
	}
	if ev.Duplicate == nil || ev.Duplicate.Owner != "a" {
		t.Errorf("Duplicate = %+v, want @a's claim", ev.Duplicate)
	}
	if !strings.Contains(ev.Detail, "duplikat: out.md") || !strings.Contains(ev.Detail, "@a") {
		t.Errorf("Detail = %q", ev.Detail)
	}
}
