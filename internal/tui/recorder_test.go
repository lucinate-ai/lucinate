package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// withTempDataDir points config.DataDir at a fresh temp directory for
// the duration of the test, restoring the previous override on
// cleanup. Using SetDataDir keeps the helper agnostic to whether the
// surrounding test set LUCINATE_DATA_DIR explicitly.
func withTempDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	config.SetDataDir(dir)
	t.Cleanup(func() { config.SetDataDir("") })
	return dir
}

func TestNewTranscriptRecorder_WritesHeader(t *testing.T) {
	dir := withTempDataDir(t)
	now := time.Date(2026, 5, 10, 14, 33, 21, 0, time.UTC)
	rec, err := newTranscriptRecorder("main", "alice", "claude-opus-4-7", "local", now)
	if err != nil {
		t.Fatalf("newTranscriptRecorder: %v", err)
	}
	if !strings.HasPrefix(rec.path, filepath.Join(dir, "transcripts")) {
		t.Errorf("path %q not under data dir %q", rec.path, dir)
	}
	if !strings.HasSuffix(rec.path, ".md") {
		t.Errorf("expected .md suffix on %q", rec.path)
	}
	if err := rec.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	body, err := os.ReadFile(rec.path)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	got := string(body)
	for _, want := range []string{
		"# Lucinate transcript",
		"- agent: alice",
		"- model: claude-opus-4-7",
		"- session: main",
		"- recorded:",
		"---",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript missing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestTranscriptRecorder_WriteNew_DeduplicatesAcrossRefreshes(t *testing.T) {
	withTempDataDir(t)
	now := time.Date(2026, 5, 10, 14, 33, 21, 0, time.UTC)
	rec, err := newTranscriptRecorder("s", "a", "m", "c", now)
	if err != nil {
		t.Fatalf("newTranscriptRecorder: %v", err)
	}
	defer rec.close()

	first := []chatMessage{
		{role: "user", content: "hello", timestampMs: 1_000},
		{role: "assistant", content: "hi", timestampMs: 1_500},
	}
	if err := rec.writeNew(first); err != nil {
		t.Fatalf("writeNew first: %v", err)
	}
	// Second refresh resends the same two and adds one more.
	second := []chatMessage{
		{role: "user", content: "hello", timestampMs: 1_000},
		{role: "assistant", content: "hi", timestampMs: 1_500},
		{role: "user", content: "follow-up", timestampMs: 2_000},
	}
	if err := rec.writeNew(second); err != nil {
		t.Fatalf("writeNew second: %v", err)
	}
	rec.close()

	body, err := os.ReadFile(rec.path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(body)
	if c := strings.Count(got, "hello"); c != 1 {
		t.Errorf("expected exactly one 'hello' in transcript, got %d\n%s", c, got)
	}
	if c := strings.Count(got, "follow-up"); c != 1 {
		t.Errorf("expected one 'follow-up', got %d\n%s", c, got)
	}
	if c := strings.Count(got, "## User"); c != 2 {
		t.Errorf("expected two user headings, got %d", c)
	}
	if c := strings.Count(got, "## Assistant"); c != 1 {
		t.Errorf("expected one assistant heading, got %d", c)
	}
}

func TestTranscriptRecorder_WriteNew_SkipsNonCanonicalRows(t *testing.T) {
	withTempDataDir(t)
	rec, err := newTranscriptRecorder("s", "a", "m", "c", time.Now())
	if err != nil {
		t.Fatalf("newTranscriptRecorder: %v", err)
	}
	defer rec.close()

	msgs := []chatMessage{
		{role: "system", content: "compacted"},
		{role: "separator", timestampMs: 1},
		{role: "tool", content: "tool stuff"},
		{role: "user", content: ""}, // empty content
		{role: "assistant", content: "kept", timestampMs: 5},
	}
	if err := rec.writeNew(msgs); err != nil {
		t.Fatalf("writeNew: %v", err)
	}
	rec.close()

	body, err := os.ReadFile(rec.path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(body)
	if strings.Contains(got, "compacted") {
		t.Errorf("system rows must not be recorded:\n%s", got)
	}
	if strings.Contains(got, "tool stuff") {
		t.Errorf("tool rows must not be recorded:\n%s", got)
	}
	if !strings.Contains(got, "kept") {
		t.Errorf("expected assistant row to be recorded:\n%s", got)
	}
}

func TestTranscriptRecorder_WriteNew_PrefersRawWhenRendered(t *testing.T) {
	withTempDataDir(t)
	rec, err := newTranscriptRecorder("s", "a", "m", "c", time.Now())
	if err != nil {
		t.Fatalf("newTranscriptRecorder: %v", err)
	}
	defer rec.close()

	msgs := []chatMessage{{
		role:        "assistant",
		content:     "ANSI escapes here",
		raw:         "**markdown** source",
		rendered:    true,
		timestampMs: 7,
	}}
	if err := rec.writeNew(msgs); err != nil {
		t.Fatalf("writeNew: %v", err)
	}
	rec.close()

	body, err := os.ReadFile(rec.path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "**markdown** source") {
		t.Errorf("expected raw markdown source in transcript:\n%s", got)
	}
	if strings.Contains(got, "ANSI escapes here") {
		t.Errorf("rendered ANSI form leaked into transcript:\n%s", got)
	}
}

func TestExportTranscript_WritesFullCanonicalSlice(t *testing.T) {
	dir := withTempDataDir(t)
	now := time.Date(2026, 5, 10, 14, 33, 21, 0, time.UTC)
	msgs := []chatMessage{
		{role: "user", content: "first", timestampMs: 1_000},
		{role: "assistant", content: "reply", timestampMs: 1_500, thinking: "internal"},
		{role: "system", content: "noise"}, // excluded
		{role: "assistant", content: "still streaming", streaming: true},
		{role: "user", content: "second", timestampMs: 2_500},
	}
	path, err := exportTranscript(msgs, "main", "alice", "claude-opus-4-7", "local", now)
	if err != nil {
		t.Fatalf("exportTranscript: %v", err)
	}
	if !strings.HasPrefix(path, filepath.Join(dir, "transcripts")) {
		t.Errorf("path %q not under data dir", path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "Lucinate transcript export") {
		t.Errorf("expected export header in:\n%s", got)
	}
	for _, want := range []string{"first", "reply", "second", "internal"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "noise") {
		t.Errorf("system row leaked into export:\n%s", got)
	}
	if strings.Contains(got, "still streaming") {
		t.Errorf("streaming row leaked into export:\n%s", got)
	}
}

func TestExtractUserPromptsForRoutine(t *testing.T) {
	msgs := []chatMessage{
		{role: "user", content: "first"},
		{role: "assistant", content: "reply"},
		{role: "user", content: "  spaced  "},
		{role: "user", content: ""},
		{role: "system", content: "ignored"},
		{role: "user", content: "last", raw: "raw last", rendered: true},
	}
	got := extractUserPromptsForRoutine(msgs)
	want := []string{"first", "spaced", "raw last"}
	if len(got) != len(want) {
		t.Fatalf("expected %d steps, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("step %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestSanitiseForPath(t *testing.T) {
	cases := map[string]string{
		"":              "",
		"  ":            "",
		"main":          "main",
		"hello world":   "hello-world",
		"a/b\\c":        "a-b-c",
		"with:colons?":  "with-colons",
		"---a---":       "a",
		"abc.123":       "abc-123",
		"alice@example": "alice-example",
	}
	for in, want := range cases {
		if got := sanitiseForPath(in); got != want {
			t.Errorf("sanitiseForPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTranscriptFilename_StableShape(t *testing.T) {
	now := time.Date(2026, 5, 10, 14, 33, 21, 0, time.UTC)
	got := transcriptFilename("main session", "Agent Name", "record", now)
	if !strings.HasPrefix(got, "record-") {
		t.Errorf("filename %q missing record- prefix", got)
	}
	if !strings.Contains(got, "Agent-Name") {
		t.Errorf("filename %q missing sanitised agent name", got)
	}
	if !strings.Contains(got, "main-session") {
		t.Errorf("filename %q missing sanitised session", got)
	}
	if !strings.HasSuffix(got, "-20260510T143321.md") {
		t.Errorf("filename %q missing timestamp/extension", got)
	}
}

func TestMessageSignature_DistinguishesRoleAndTimestamp(t *testing.T) {
	a := messageSignature("user", 1_000, "hi")
	b := messageSignature("assistant", 1_000, "hi")
	c := messageSignature("user", 2_000, "hi")
	d := messageSignature("user", 1_000, "hi")
	if a == b {
		t.Error("role must affect signature")
	}
	if a == c {
		t.Error("timestamp must affect signature")
	}
	if a != d {
		t.Error("identical inputs must hash equal")
	}
}
