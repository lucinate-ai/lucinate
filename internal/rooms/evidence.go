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

// claimNegations are the phrases a reply uses to deny writing. They are
// checked first: "nie zapisałem pliku report.md" mentions a marker and a path,
// but it is a denial, not a claim.
var claimNegations = []string{
	"nie zapisa", "nie utworz", "nie wdroż", "bez zapisu", "nic nie zapisa",
	"not saved", "no file written", "didn't write",
}

// claimDeliveries are the first-person verbs that name a delivery even
// without a file extension ("zapisałem raport").
var claimDeliveries = []string{
	"zapisałem", "utworzyłem", "wdrożyłem", "wygenerowałem",
}

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
//
// A claim needs both halves: a write marker AND a target — a deliverable file
// extension or an explicit first-person delivery. A bare path is a reference
// ("the branch is at docs/x.md"), and a denial ("nie zapisałem") is not a
// claim at all. Keeping the row off for those is the point: an evidence column
// that accuses every mention of a file teaches readers to ignore it.
func claimsWrite(text string) bool {
	lower := strings.ToLower(text)
	for _, neg := range claimNegations {
		if strings.Contains(lower, neg) {
			return false
		}
	}
	marked := false
	for _, marker := range claimMarkers {
		if strings.Contains(lower, marker) {
			marked = true
			break
		}
	}
	if !marked {
		return false
	}
	if claimFilePattern.MatchString(text) {
		return true
	}
	for _, verb := range claimDeliveries {
		if strings.Contains(lower, verb) {
			return true
		}
	}
	return false
}
