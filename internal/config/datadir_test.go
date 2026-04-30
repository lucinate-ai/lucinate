package config

import (
	"path/filepath"
	"testing"
)

func TestDataDir_HomeFallback(t *testing.T) {
	t.Setenv(DataDirEnvVar, "")
	SetDataDir("")
	t.Cleanup(func() { SetDataDir("") })

	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	if want := filepath.Join(home, ".lucinate"); got != want {
		t.Errorf("DataDir = %q, want %q", got, want)
	}
}

func TestDataDir_EnvVarOverridesHome(t *testing.T) {
	SetDataDir("")
	t.Cleanup(func() { SetDataDir("") })

	t.Setenv("HOME", t.TempDir())
	envDir := t.TempDir()
	t.Setenv(DataDirEnvVar, envDir)

	got, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	if got != envDir {
		t.Errorf("DataDir = %q, want %q", got, envDir)
	}
}

func TestDataDir_SetDataDirOverridesEnv(t *testing.T) {
	t.Cleanup(func() { SetDataDir("") })

	envDir := t.TempDir()
	t.Setenv(DataDirEnvVar, envDir)

	progDir := t.TempDir()
	SetDataDir(progDir)

	got, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	if got != progDir {
		t.Errorf("DataDir = %q, want %q (env was %q)", got, progDir, envDir)
	}
}

func TestDataDir_SetDataDirEmptyClearsOverride(t *testing.T) {
	t.Cleanup(func() { SetDataDir("") })

	envDir := t.TempDir()
	t.Setenv(DataDirEnvVar, envDir)

	SetDataDir(t.TempDir())
	SetDataDir("")

	got, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	if got != envDir {
		t.Errorf("DataDir = %q, want env fallback %q", got, envDir)
	}
}

func TestDataDir_NoDotfilePrefixOnOverride(t *testing.T) {
	t.Cleanup(func() { SetDataDir("") })

	// Mirrors the embedder use case: a sandbox-conventional path with
	// no leading-dot component. DataDir must return it verbatim so
	// downstream subdirectories (identity/, secrets/, agents/) sit
	// directly under the embedder-chosen root.
	override := filepath.Join(t.TempDir(), "Application Support", "Lucinate")
	SetDataDir(override)

	got, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	if got != override {
		t.Errorf("DataDir = %q, want %q (no dotfile prefix should be added)", got, override)
	}
}
