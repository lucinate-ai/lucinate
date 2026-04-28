package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecrets_RoundTrip(t *testing.T) {
	withHomeDir(t)
	if err := SetAPIKey("conn-a", "secret-1"); err != nil {
		t.Fatal(err)
	}
	if got := GetAPIKey("conn-a"); got != "secret-1" {
		t.Errorf("GetAPIKey = %q", got)
	}
}

func TestSecrets_MissingReturnsEmpty(t *testing.T) {
	withHomeDir(t)
	if got := GetAPIKey("ghost"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestSecrets_OverwriteReplaces(t *testing.T) {
	withHomeDir(t)
	_ = SetAPIKey("conn-a", "first")
	_ = SetAPIKey("conn-a", "second")
	if got := GetAPIKey("conn-a"); got != "second" {
		t.Errorf("GetAPIKey = %q", got)
	}
}

func TestSecrets_EmptyKeyDeletes(t *testing.T) {
	withHomeDir(t)
	_ = SetAPIKey("conn-a", "secret")
	_ = SetAPIKey("conn-a", "")
	if got := GetAPIKey("conn-a"); got != "" {
		t.Errorf("expected key cleared, got %q", got)
	}
}

func TestSecrets_FileMode(t *testing.T) {
	home := withHomeDir(t)
	if err := SetAPIKey("conn-a", "secret"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(home, ".lucinate", "secrets", "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("file mode = %v want 0600", mode)
	}
}

func TestSecrets_RequiresConnectionID(t *testing.T) {
	if err := SetAPIKey("", "x"); err == nil {
		t.Error("expected error for empty connection id")
	}
}
