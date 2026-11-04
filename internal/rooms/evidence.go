package rooms

import (
	"fmt"
	"regexp"
	"strings"
)

// EvidenceStatus is what a member reply proved about the file it claims to
// have written. "none" is an opinion with no write claim at all; "claimed" is
// a write claim with no receipt — a declaration, not a proven write.
type EvidenceStatus string

const (
	EvidenceNone      EvidenceStatus = "none"
	EvidenceClaimed   EvidenceStatus = "claimed"
	EvidenceVerified  EvidenceStatus = "verified"
	EvidenceFailed    EvidenceStatus = "failed"
	EvidenceDuplicate EvidenceStatus = "duplicate"
)

// claimMarkers are the words a reply uses when it says it wrote something. A
// reply matching one without a HANDOFF receipt is "claimed": the room is
// discussing, not delivering, and painting every opinion with a red cross
// taught readers to ignore the column entirely.
var claimMarkers = []string{
	"zapisa", "wdroż", "wdroz", "utworz", "gotow",
}

// claimFilePattern is the other half of a write claim: naming a file with a
// deliverable extension. A discussion that mentions a path is talking about
// one, but "plik X.md" in the same breath as a marker is the claim shape.
var claimFilePattern = regexp.MustCompile(`(?i)\.(md|go|json|html)\b`)

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
// A reply with no receipt is "claimed" when it says it wrote something, and
// "none" when it is an opinion. Empty dir or empty text cannot prove anything
// and is also "none".
func VerifyReply(dir, owner, text string) ReplyEvidence {
	if strings.TrimSpace(dir) == "" || strings.TrimSpace(text) == "" {
		return ReplyEvidence{Status: EvidenceNone, Detail: "brak potwierdzenia zapisu"}
	}
	receipts := ParseHandoffReceipts(text)
	if len(receipts) == 0 {
		if claimsWrite(text) {
			return ReplyEvidence{Status: EvidenceClaimed, Detail: "deklaracja zapisu bez potwierdzenia — brak HANDOFF"}
		}
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

// claimsWrite reports whether a receipt-less reply says it wrote something.
func claimsWrite(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range claimMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return claimFilePattern.MatchString(text)
}
