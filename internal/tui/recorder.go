package tui

import (
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// transcriptRecorder appends canonical conversation messages to a
// markdown file. Lives on chatModel for the duration of a /record on
// session; closed on /record off or when the chat model is torn down
// (session switch, view change, quit).
//
// The recorder consumes the same []chatMessage slices the chat view
// receives from fetchHistory — i.e. only canonical user/assistant rows
// with text content, never streaming deltas, tool cards, or system
// notes. Each refresh delivers the entire history (capped by
// historyLimit), so seenSig dedups against rows already written.
type transcriptRecorder struct {
	w       io.WriteCloser
	path    string
	seenSig map[string]bool
}

// newTranscriptRecorder opens the target file (creating parent dirs)
// and writes the markdown header. Returns the recorder + the resolved
// path so the caller can surface it to the user.
func newTranscriptRecorder(sessionKey, agentName, modelID, connName string, now time.Time) (*transcriptRecorder, error) {
	dir, err := transcriptsDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, transcriptFilename(sessionKey, agentName, "record", now))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	r := &transcriptRecorder{w: f, path: path, seenSig: make(map[string]bool)}
	if err := writeTranscriptHeader(f, sessionKey, agentName, modelID, connName, "record", now); err != nil {
		_ = f.Close()
		return nil, err
	}
	return r, nil
}

// writeNew appends every message in msgs whose signature has not been
// recorded yet. Errors are returned so the chat view can surface them
// once; subsequent failures on the same recorder are silently dropped
// (the file is broken, recording is effectively over).
func (r *transcriptRecorder) writeNew(msgs []chatMessage) error {
	if r == nil || r.w == nil {
		return nil
	}
	for _, msg := range msgs {
		if !isRecordableRole(msg.role) {
			continue
		}
		text := messageSourceText(msg)
		if text == "" {
			continue
		}
		sig := messageSignature(msg.role, msg.timestampMs, text)
		if r.seenSig[sig] {
			continue
		}
		r.seenSig[sig] = true
		if err := writeTranscriptEntry(r.w, msg.role, msg.timestampMs, text, msg.thinking); err != nil {
			return err
		}
	}
	return nil
}

// close flushes and closes the underlying writer. Safe to call on a
// nil recorder.
func (r *transcriptRecorder) close() error {
	if r == nil || r.w == nil {
		return nil
	}
	err := r.w.Close()
	r.w = nil
	return err
}

// transcriptsDir returns the on-disk root for transcript files
// (<dataDir>/transcripts).
func transcriptsDir() (string, error) {
	root, err := config.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "transcripts"), nil
}

// transcriptFilename builds a filesystem-safe filename for a transcript.
// kind is "record" or "export" so the two flows are distinguishable on
// disk; the timestamp resolves to the second.
func transcriptFilename(sessionKey, agentName, kind string, now time.Time) string {
	parts := []string{kind}
	if a := sanitiseForPath(agentName); a != "" {
		parts = append(parts, a)
	}
	if s := sanitiseForPath(sessionKey); s != "" {
		parts = append(parts, s)
	}
	parts = append(parts, now.Format("20060102T150405"))
	return strings.Join(parts, "-") + ".md"
}

// sanitiseForPath replaces filesystem-unsafe characters with '-' and
// trims any leading/trailing separators. Empty input yields empty
// output.
func sanitiseForPath(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// writeTranscriptHeader emits the markdown front-matter for a
// transcript file: title, key/value preamble, and a horizontal rule.
func writeTranscriptHeader(w io.Writer, sessionKey, agentName, modelID, connName, kind string, now time.Time) error {
	var sb strings.Builder
	switch kind {
	case "export":
		sb.WriteString("# Lucinate transcript export\n\n")
	default:
		sb.WriteString("# Lucinate transcript\n\n")
	}
	if connName != "" {
		fmt.Fprintf(&sb, "- connection: %s\n", connName)
	}
	if agentName != "" {
		fmt.Fprintf(&sb, "- agent: %s\n", agentName)
	}
	if modelID != "" {
		fmt.Fprintf(&sb, "- model: %s\n", modelID)
	}
	if sessionKey != "" {
		fmt.Fprintf(&sb, "- session: %s\n", sessionKey)
	}
	fmt.Fprintf(&sb, "- %s: %s\n\n---\n\n", kind+"ed", now.Format(time.RFC3339))
	_, err := io.WriteString(w, sb.String())
	return err
}

// writeTranscriptEntry emits one role-prefixed message block. Thinking
// content is rendered as a fenced thought block above the visible
// reply so the file is self-contained.
func writeTranscriptEntry(w io.Writer, role string, timestampMs int64, content, thinking string) error {
	heading := strings.ToUpper(role[:1]) + role[1:]
	stamp := ""
	if timestampMs > 0 {
		stamp = " · " + time.UnixMilli(timestampMs).Format(time.RFC3339)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "## %s%s\n\n", heading, stamp)
	if t := strings.TrimSpace(thinking); t != "" {
		sb.WriteString("> _thinking_\n")
		for _, line := range strings.Split(t, "\n") {
			sb.WriteString("> ")
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
		sb.WriteByte('\n')
	}
	sb.WriteString(strings.TrimRight(content, "\n"))
	sb.WriteString("\n\n")
	_, err := io.WriteString(w, sb.String())
	return err
}

// isRecordableRole reports whether a chatMessage role belongs in the
// transcript. System rows, separators, tool cards, and streaming
// placeholders are excluded — the canonical history fetch only
// surfaces user/assistant anyway, but this guard keeps the intent
// explicit and protects against future changes upstream.
func isRecordableRole(role string) bool {
	return role == "user" || role == "assistant"
}

// messageSourceText returns the markdown source for a message,
// preferring raw (the pre-glamour text) over content (the rendered
// ANSI form). fetchHistory sets raw on assistant messages it ran
// through the renderer; user messages and unrendered assistant
// messages fall back to content.
func messageSourceText(msg chatMessage) string {
	if msg.rendered && msg.raw != "" {
		return msg.raw
	}
	return msg.content
}

// messageSignature is the dedup key for the recorder. Combines role
// (so a user/assistant collision on identical content is impossible),
// timestampMs (when the gateway provides one — disambiguates a
// repeated user message), and an FNV-64 hash of the source text
// (cheap, collision-resistant enough for this purpose).
func messageSignature(role string, timestampMs int64, text string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(text))
	return fmt.Sprintf("%s|%d|%016x", role, timestampMs, h.Sum64())
}

// extractUserPromptsForRoutine walks msgs in order and returns the
// non-empty user-message texts as routine step candidates. Source
// text is preferred (so glamour rendering doesn't bleed ANSI into
// step bodies); whitespace is trimmed per step.
func extractUserPromptsForRoutine(msgs []chatMessage) []string {
	var out []string
	for _, msg := range msgs {
		if msg.role != "user" {
			continue
		}
		text := strings.TrimSpace(messageSourceText(msg))
		if text == "" {
			continue
		}
		out = append(out, text)
	}
	return out
}
