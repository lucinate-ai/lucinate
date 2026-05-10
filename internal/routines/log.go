package routines

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Logger appends a routine run to a file. The file is opened on Open and
// kept open for the duration of the run; Close releases it. All operations
// are best-effort — a failing write should not break the user's session,
// so callers ignore returned errors except at Open.
type Logger struct {
	f       *os.File
	now     func() time.Time
	rootDir string
}

// Open opens (or creates) the log file at path and writes a run header.
// Relative paths resolve against rootDir (typically os.Getwd()). Returns
// nil, nil when path is empty so callers can unconditionally invoke it.
func Open(path, rootDir, routineName string) (*Logger, error) {
	if path == "" {
		return nil, nil
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(rootDir, abs)
	}
	if dir := filepath.Dir(abs); dir != "" {
		_ = os.MkdirAll(dir, 0o700)
	}
	f, err := os.OpenFile(abs, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open routine log: %w", err)
	}
	l := &Logger{f: f, now: time.Now, rootDir: rootDir}
	header := fmt.Sprintf("--- routine: %s started %s ---\n",
		routineName, l.now().UTC().Format(time.RFC3339))
	_, _ = f.WriteString(header)
	return l, nil
}

// WriteUser appends a user message to the log.
func (l *Logger) WriteUser(text string) {
	l.write("user", text)
}

// WriteAssistant appends an assistant message to the log.
func (l *Logger) WriteAssistant(text string) {
	l.write("assistant", text)
}

// Close flushes and closes the underlying file. Safe on a nil receiver.
func (l *Logger) Close() {
	if l == nil || l.f == nil {
		return
	}
	_ = l.f.Close()
	l.f = nil
}

func (l *Logger) write(role, text string) {
	if l == nil || l.f == nil {
		return
	}
	ts := l.now().UTC().Format(time.RFC3339)
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s: %s\n", ts, role, lines[0])
	for _, line := range lines[1:] {
		b.WriteString(line)
		b.WriteString("\n")
	}
	_, _ = l.f.WriteString(b.String())
}
