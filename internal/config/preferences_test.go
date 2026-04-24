package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultPreferences(t *testing.T) {
	p := DefaultPreferences()
	if !p.CompletionBell {
		t.Error("expected CompletionBell to default to true")
	}
	if p.HistoryLimit != DefaultHistoryLimit {
		t.Errorf("expected HistoryLimit %d, got %d", DefaultHistoryLimit, p.HistoryLimit)
	}
}

func TestSaveAndLoadPreferences(t *testing.T) {
	// Use a temp dir to avoid touching the real config.
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", dir)
	defer func() { _ = os.Setenv("HOME", origHome) }()

	p := Preferences{CompletionBell: false, HistoryLimit: 100}
	if err := SavePreferences(p); err != nil {
		t.Fatalf("SavePreferences: %v", err)
	}

	// Verify file exists.
	path := filepath.Join(dir, ".lucinate", "config.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file not created: %v", err)
	}

	loaded := LoadPreferences()
	if loaded.CompletionBell != false {
		t.Error("expected CompletionBell to be false after save/load")
	}
	if loaded.HistoryLimit != 100 {
		t.Errorf("expected HistoryLimit 100, got %d", loaded.HistoryLimit)
	}
}

func TestLoadPreferences_MissingHistoryLimit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	// Write a config file without historyLimit (simulates upgrade from old version).
	configDir := filepath.Join(dir, ".lucinate")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(`{"completionBell":true}`), 0644); err != nil {
		t.Fatal(err)
	}

	loaded := LoadPreferences()
	if loaded.HistoryLimit != DefaultHistoryLimit {
		t.Errorf("expected default HistoryLimit %d for old config, got %d", DefaultHistoryLimit, loaded.HistoryLimit)
	}
}

func TestLoadPreferences_MissingFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	p := LoadPreferences()
	if !p.CompletionBell {
		t.Error("expected defaults when file is missing")
	}
}
