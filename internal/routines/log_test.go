package routines

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogger_Format(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.log")

	l, err := Open(path, dir, "demo")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Pin the clock so the assertion is stable.
	fixed := time.Date(2026, 5, 9, 14, 30, 5, 0, time.UTC)
	l.now = func() time.Time { return fixed }

	l.WriteUser("hello")
	l.WriteAssistant("hi there\nover two lines")
	l.Close()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(got)

	if !strings.Contains(s, "--- routine: demo started") {
		t.Errorf("missing run header: %q", s)
	}
	if !strings.Contains(s, "[2026-05-09T14:30:05Z] user: hello") {
		t.Errorf("missing user line: %q", s)
	}
	if !strings.Contains(s, "[2026-05-09T14:30:05Z] assistant: hi there\nover two lines") {
		t.Errorf("missing multi-line assistant message: %q", s)
	}
}

func TestLogger_AppendsAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.log")

	for i := 0; i < 2; i++ {
		l, err := Open(path, dir, "demo")
		if err != nil {
			t.Fatalf("Open #%d: %v", i, err)
		}
		l.WriteUser("turn")
		l.Close()
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Count(string(got), "--- routine: demo started") != 2 {
		t.Errorf("expected 2 run headers, got: %q", string(got))
	}
}

func TestLogger_NilSafe(t *testing.T) {
	l, err := Open("", "/tmp", "x")
	if err != nil {
		t.Fatalf("Open(empty path): %v", err)
	}
	if l != nil {
		t.Errorf("Open(empty path) returned non-nil logger")
	}
	// Methods on nil receiver must not panic.
	l.WriteUser("x")
	l.WriteAssistant("y")
	l.Close()
}
