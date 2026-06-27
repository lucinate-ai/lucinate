package config

import (
	"os"
	"testing"
)

func TestLoadSubagentDefaults_MissingFile(t *testing.T) {
	SetDataDir(t.TempDir())
	t.Cleanup(func() { SetDataDir("") })

	got := LoadSubagentDefaults()
	if got != (SubagentDefaults{}) {
		t.Errorf("expected zero value, got %+v", got)
	}
}

func TestSaveAndLoadSubagentDefaults_Roundtrip(t *testing.T) {
	SetDataDir(t.TempDir())
	t.Cleanup(func() { SetDataDir("") })

	want := SubagentDefaults{
		Model:          "claude-sonnet-4-6",
		Label:          "scout",
		AgentID:        "secondary",
		TimeoutSeconds: 300,
	}
	if err := SaveSubagentDefaults(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := LoadSubagentDefaults()
	if got != want {
		t.Errorf("roundtrip: got %+v, want %+v", got, want)
	}
}

func TestLoadSubagentDefaults_MalformedJSONFallsBack(t *testing.T) {
	SetDataDir(t.TempDir())
	t.Cleanup(func() { SetDataDir("") })

	path, err := SubagentDefaultsPath()
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got := LoadSubagentDefaults()
	if got != (SubagentDefaults{}) {
		t.Errorf("expected zero value on parse failure, got %+v", got)
	}
}
