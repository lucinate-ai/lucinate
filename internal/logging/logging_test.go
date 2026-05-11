package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"":        slog.LevelWarn,
		"debug":   slog.LevelDebug,
		"DEBUG":   slog.LevelDebug,
		" info ":  slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"bogus":   slog.LevelWarn,
	}
	for in, want := range cases {
		if got := parseLevel(in); got != want {
			t.Errorf("parseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseFormat(t *testing.T) {
	cases := map[string]string{
		"":      "text",
		"text":  "text",
		"json":  "json",
		"JSON":  "json",
		"bogus": "text",
	}
	for in, want := range cases {
		if got := parseFormat(in); got != want {
			t.Errorf("parseFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestInit_DefaultsTUI confirms a TUI invocation with no env vars writes
// debug-or-above output to the default side file.
func TestInit_DefaultsTUI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tui.log")
	t.Setenv(EnvLevel, "debug")
	t.Setenv(EnvFile, path)
	t.Setenv(EnvFormat, "text")

	if err := Init(Options{TUI: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(closeForTest)
	slog.Debug("hello", "k", "v")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(data), "hello") || !strings.Contains(string(data), "k=v") {
		t.Errorf("log file missing expected content; got %q", string(data))
	}
}

// TestInit_NonTUIDefaultsToStderr confirms that without LUCINATE_LOG_FILE
// a non-TUI Init does not touch the filesystem.
func TestInit_NonTUIDefaultsToStderr(t *testing.T) {
	t.Setenv(EnvLevel, "warn")
	t.Setenv(EnvFile, "")
	t.Setenv(EnvFormat, "text")

	if err := Init(Options{TUI: false}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(closeForTest)
	if currentFile != nil {
		t.Errorf("expected no file, got %v", currentFile.Name())
	}
}

// TestInit_LevelFiltersBelow confirms entries below the configured level
// are dropped before reaching the destination.
func TestInit_LevelFiltersBelow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "filter.log")
	t.Setenv(EnvLevel, "warn")
	t.Setenv(EnvFile, path)

	if err := Init(Options{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(closeForTest)
	slog.Debug("debug-line")
	slog.Info("info-line")
	slog.Warn("warn-line")
	slog.Error("error-line")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	out := string(data)
	if strings.Contains(out, "debug-line") || strings.Contains(out, "info-line") {
		t.Errorf("expected debug/info filtered, got %q", out)
	}
	if !strings.Contains(out, "warn-line") || !strings.Contains(out, "error-line") {
		t.Errorf("expected warn/error retained, got %q", out)
	}
}

// TestInit_JSONFormat confirms JSON handler emits structured output.
func TestInit_JSONFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "json.log")
	t.Setenv(EnvLevel, "info")
	t.Setenv(EnvFile, path)
	t.Setenv(EnvFormat, "json")

	if err := Init(Options{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(closeForTest)
	slog.Info("structured", "shape", "json")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, `"msg":"structured"`) || !strings.Contains(out, `"shape":"json"`) {
		t.Errorf("expected JSON output, got %q", out)
	}
}
