package rooms

import (
	"fmt"
	"strings"
)

// EvidenceStatus is what a member reply proved about the file it claims to
// have written. "none" is a declaration without a read-back, not an error.
type EvidenceStatus string

const (
	EvidenceNone      EvidenceStatus = "none"
	EvidenceVerified  EvidenceStatus = "verified"
	EvidenceFailed    EvidenceStatus = "failed"
	EvidenceDuplicate EvidenceStatus = "duplicate"
)

// ReplyEvidence is the verdict on one member reply, plus the one-line reason
// the transcript shows next to it.
type ReplyEvidence struct {
	Status    EvidenceStatus
	Receipts  []HandoffReceipt
	Duplicate *Claim
	Detail    string
}

// VerifyReply checks a member's reply against the room directory: every
// HANDOFF receipt is read back and compared, and an artifact already finished
// by another owner is a duplicate even before the file is touched.
//
// A reply with no receipt is "none" — the contract calls that a declaration,
// not a proven write, and the transcript says so. Empty dir or empty text
// cannot prove anything either and is also "none".
func VerifyReply(dir, owner, text string) ReplyEvidence {
	if strings.TrimSpace(dir) == "" || strings.TrimSpace(text) == "" {
		return ReplyEvidence{Status: EvidenceNone, Detail: "brak potwierdzenia zapisu"}
	}
	receipts := ParseHandoffReceipts(text)
	if len(receipts) == 0 {
		return ReplyEvidence{Status: EvidenceNone, Detail: "brak potwierdzenia zapisu"}
	}

	// Duplicate check runs first: a file already delivered by someone else
	// is the reader's problem even when the bytes happen to check out.
	claims, _ := ReadLedger(dir)
	for _, r := range receipts {
		if dup := DuplicateOf(claims, owner, r.Path); dup != nil {
			return ReplyEvidence{
				Status:    EvidenceDuplicate,
				Receipts:  receipts,
				Duplicate: dup,
				Detail:    fmt.Sprintf("duplikat: %s zrobione przez @%s", dup.Artifact, cleanOwner(dup.Owner)),
			}
		}
	}

	if err := VerifyHandoffs(dir, receipts); err != nil {
		return ReplyEvidence{Status: EvidenceFailed, Receipts: receipts, Detail: err.Error()}
	}

	r := receipts[0]
	return ReplyEvidence{
		Status:   EvidenceVerified,
		Receipts: receipts,
		Detail:   fmt.Sprintf("zapis potwierdzony: %s (%d B)", r.Path, r.Bytes),
	}
}
