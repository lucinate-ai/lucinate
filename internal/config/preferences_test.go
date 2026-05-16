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

func TestDefaultPreferences_ConnectTimeout(t *testing.T) {
	p := DefaultPreferences()
	if p.ConnectTimeoutSeconds != DefaultConnectTimeoutSeconds {
		t.Errorf("expected ConnectTimeoutSeconds %d, got %d", DefaultConnectTimeoutSeconds, p.ConnectTimeoutSeconds)
	}
}

func TestLoadPreferences_MissingConnectTimeout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	configDir := filepath.Join(dir, ".lucinate")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(`{"completionBell":true,"historyLimit":50}`), 0644); err != nil {
		t.Fatal(err)
	}

	loaded := LoadPreferences()
	if loaded.ConnectTimeoutSeconds != DefaultConnectTimeoutSeconds {
		t.Errorf("expected default ConnectTimeoutSeconds %d for old config, got %d", DefaultConnectTimeoutSeconds, loaded.ConnectTimeoutSeconds)
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
	if !p.UpdateChecksEnabled() {
		t.Error("expected update checks to default to enabled when file is missing")
	}
}

func TestLoadPreferences_MissingCheckForUpdates(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	configDir := filepath.Join(dir, ".lucinate")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	// Older config: lacks checkForUpdates entirely. A naive bool field
	// would unmarshal to false and silently disable update checks for
	// every existing user.
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(`{"completionBell":true,"historyLimit":50}`), 0644); err != nil {
		t.Fatal(err)
	}

	loaded := LoadPreferences()
	if !loaded.UpdateChecksEnabled() {
		t.Error("expected UpdateChecksEnabled to default to true for old config")
	}
	if loaded.CheckForUpdates != nil {
		t.Errorf("expected CheckForUpdates to remain nil after loading legacy config, got %v", *loaded.CheckForUpdates)
	}
}

func TestLoadPreferences_ExplicitlyDisabledUpdateChecks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	configDir := filepath.Join(dir, ".lucinate")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(`{"checkForUpdates":false}`), 0644); err != nil {
		t.Fatal(err)
	}

	loaded := LoadPreferences()
	if loaded.UpdateChecksEnabled() {
		t.Error("expected UpdateChecksEnabled to be false when user has explicitly disabled it")
	}
}

func TestSaveAndLoad_UpdateCheckFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	disabled := false
	p := DefaultPreferences()
	p.CheckForUpdates = &disabled
	p.LastUpdateCheck = 1700000000
	p.LatestSeenVersion = "v1.2.3"
	if err := SavePreferences(p); err != nil {
		t.Fatalf("SavePreferences: %v", err)
	}

	loaded := LoadPreferences()
	if loaded.UpdateChecksEnabled() {
		t.Error("expected UpdateChecksEnabled to round-trip as false")
	}
	if loaded.LastUpdateCheck != 1700000000 {
		t.Errorf("LastUpdateCheck round-trip: got %d", loaded.LastUpdateCheck)
	}
	if loaded.LatestSeenVersion != "v1.2.3" {
		t.Errorf("LatestSeenVersion round-trip: got %q", loaded.LatestSeenVersion)
	}
}

func TestNormalizeHexColor(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"#aabbcc", "#AABBCC"},
		{"aabbcc", "#AABBCC"},
		{"#ABC", "#AABBCC"},
		{"abc", "#AABBCC"},
		{"  #112233  ", "#112233"},
		{"#FfEeDd", "#FFEEDD"},
	}
	for _, c := range cases {
		got, err := NormalizeHexColor(c.in)
		if err != nil {
			t.Errorf("NormalizeHexColor(%q) returned error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeHexColor(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	bad := []string{"", "#", "#1", "#12", "#12345", "#1234567", "ghijkl", "#xyzxyz", "red"}
	for _, in := range bad {
		if _, err := NormalizeHexColor(in); err == nil {
			t.Errorf("NormalizeHexColor(%q) expected error, got nil", in)
		}
	}
}

func TestSaveAndLoadPreferences_HeaderColors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	p := DefaultPreferences()
	p.SetHeaderColor("agent-one", "#4FC3F7")
	p.SetHeaderColor("agent-two", "#FF6B6B")
	if err := SavePreferences(p); err != nil {
		t.Fatalf("SavePreferences: %v", err)
	}
	loaded := LoadPreferences()
	if got := loaded.HeaderColorFor("agent-one"); got != "#4FC3F7" {
		t.Errorf("HeaderColorFor(agent-one) = %q, want %q", got, "#4FC3F7")
	}
	if got := loaded.HeaderColorFor("agent-two"); got != "#FF6B6B" {
		t.Errorf("HeaderColorFor(agent-two) = %q, want %q", got, "#FF6B6B")
	}
	if got := loaded.HeaderColorFor("agent-missing"); got != "" {
		t.Errorf("HeaderColorFor(missing) = %q, want empty", got)
	}
}

func TestSetHeaderColor_ClearRemovesEntry(t *testing.T) {
	p := DefaultPreferences()
	p.SetHeaderColor("agent-one", "#112233")
	p.SetHeaderColor("agent-two", "#445566")
	p.SetHeaderColor("agent-one", "")
	if _, ok := p.Agents["agent-one"]; ok {
		t.Error("expected agent-one entry to be removed after clearing")
	}
	if got := p.HeaderColorFor("agent-two"); got != "#445566" {
		t.Errorf("HeaderColorFor(agent-two) = %q, want %q", got, "#445566")
	}
	p.SetHeaderColor("agent-two", "")
	if p.Agents != nil {
		t.Errorf("expected Agents map to be nil after clearing the last entry, got %v", p.Agents)
	}
}

func TestSetHeaderColor_EmptyAgentIDIsNoop(t *testing.T) {
	p := DefaultPreferences()
	p.SetHeaderColor("", "#112233")
	if p.Agents != nil {
		t.Errorf("expected Agents to stay nil for empty agentID, got %v", p.Agents)
	}
	if got := p.HeaderColorFor(""); got != "" {
		t.Errorf("HeaderColorFor(\"\") = %q, want empty", got)
	}
}

func TestLoadPreferences_FutureTimestampReset(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	configDir := filepath.Join(dir, ".lucinate")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(`{"lastUpdateCheck":99999999999}`), 0644); err != nil {
		t.Fatal(err)
	}

	loaded := LoadPreferences()
	if loaded.LastUpdateCheck != 0 {
		t.Errorf("expected future timestamp to be reset to 0, got %d", loaded.LastUpdateCheck)
	}
}
