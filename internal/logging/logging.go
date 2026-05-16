// Package logging configures the process-wide slog.Default logger from
// LUCINATE_LOG_* environment variables.
//
// Defaults are tuned for a TUI: when the application takes over the
// terminal, log output never goes to stdout/stderr (which would corrupt
// the rendered frame); instead it lands in a side file inside the OS
// temp dir (e.g. /tmp/lucinate-events.log on Linux, %TEMP% on Windows).
// Non-TUI subcommands log to stderr. Both can be redirected with
// LUCINATE_LOG_FILE.
//
// Recognised env vars:
//
//	LUCINATE_LOG_LEVEL   debug|info|warn|error  (default warn)
//	LUCINATE_LOG_FILE    path to log file       (default depends on mode)
//	LUCINATE_LOG_FORMAT  text|json              (default text)
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	EnvLevel  = "LUCINATE_LOG_LEVEL"
	EnvFile   = "LUCINATE_LOG_FILE"
	EnvFormat = "LUCINATE_LOG_FORMAT"

	// defaultTUIFileName is joined onto os.TempDir to produce the
	// default destination when the TUI is active and no explicit
	// LUCINATE_LOG_FILE is set.
	defaultTUIFileName = "lucinate-events.log"
)

// DefaultTUIFile returns the destination used when the TUI is active and
// no explicit LUCINATE_LOG_FILE is set. Resolved against the OS temp
// directory so it stays sensible across platforms. Truncated on each
// start so the file always reflects the current session.
func DefaultTUIFile() string {
	return filepath.Join(os.TempDir(), defaultTUIFileName)
}

// Options influence the default destination when LUCINATE_LOG_FILE is
// unset.
type Options struct {
	// TUI is true when the caller is about to take over the terminal.
	// In that mode the default destination is a side file rather than
	// stderr, to avoid corrupting the rendered frame.
	TUI bool
}

// currentFile tracks any file opened by Init so a subsequent Init call
// (e.g. in tests) can close the previous one rather than leaking it.
var currentFile *os.File

// Init configures slog.Default from the environment and opts. It is
// safe to call multiple times; the most recent call wins.
func Init(opts Options) error {
	level := parseLevel(os.Getenv(EnvLevel))
	format := parseFormat(os.Getenv(EnvFormat))

	dest, file, err := openDestination(os.Getenv(EnvFile), opts.TUI)
	if err != nil {
		return err
	}

	if currentFile != nil {
		_ = currentFile.Close()
	}
	currentFile = file

	handlerOpts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	switch format {
	case "json":
		handler = slog.NewJSONHandler(dest, handlerOpts)
	default:
		handler = slog.NewTextHandler(dest, handlerOpts)
	}
	slog.SetDefault(slog.New(handler))
	return nil
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "error":
		return slog.LevelError
	case "warn", "warning", "":
		return slog.LevelWarn
	}
	return slog.LevelWarn
}

func parseFormat(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "json":
		return "json"
	}
	return "text"
}

// closeForTest closes any open log file and clears the package handle.
// Test-only helper so a test can read back what it just wrote without a
// follow-up Init re-truncating the same path.
func closeForTest() {
	if currentFile != nil {
		_ = currentFile.Close()
		currentFile = nil
	}
}

// openDestination resolves the writer slog should write to. An explicit
// path always wins; otherwise TUI callers get a truncated side file and
// non-TUI callers get stderr.
func openDestination(path string, tui bool) (io.Writer, *os.File, error) {
	if path == "" {
		if !tui {
			return os.Stderr, nil, nil
		}
		path = DefaultTUIFile()
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file %q: %w", path, err)
	}
	return f, f, nil
}
