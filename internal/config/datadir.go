package config

import (
	"os"
	"path/filepath"
	"sync"
)

// DataDirEnvVar is the environment variable that overrides the
// default lucinate state directory. Set by callers that can't reach
// SetDataDir before the first DataDir() call (typically shell-driven
// CLI invocations and integration tests). Embedders that drive the
// program from in-process Go code should prefer SetDataDir.
const DataDirEnvVar = "LUCINATE_DATA_DIR"

var (
	dataDirMu       sync.RWMutex
	dataDirOverride string
)

// SetDataDir programmatically overrides the default state directory.
// Once set with a non-empty path, subsequent DataDir() calls return
// it verbatim — every persistence helper (preferences, connections,
// secrets, identity, agents) writes inside it, so the embedder's
// chosen path becomes the entire on-disk root.
//
// Embedders whose host platform exposes a sandboxed, platform-
// conventional state directory through host APIs (rather than a
// writable home directory) call this once at startup, before any
// other persistence helper runs. The expected shape is a fully-
// resolved absolute path with no dotfile prefix — the lucinate code
// does not add ".lucinate" or any other leading-dot component on top.
//
// Passing an empty string clears the override and restores fallback
// resolution. Safe to call from any goroutine; in practice the
// override is set once during startup.
func SetDataDir(dir string) {
	dataDirMu.Lock()
	defer dataDirMu.Unlock()
	dataDirOverride = dir
}

// DataDir returns the root directory for all on-disk lucinate state
// (connections, secrets, identity, agents, preferences, skill cache).
// Resolution order:
//
//  1. The most recent SetDataDir call, if non-empty.
//  2. The LUCINATE_DATA_DIR environment variable, if set and non-empty.
//  3. <user-home>/.lucinate (CLI fallback on platforms with a writable
//     user home directory).
//
// The returned path is not created here — each caller is responsible
// for provisioning the subdirectory layout it needs (with whatever
// permission mode the data warrants).
func DataDir() (string, error) {
	dataDirMu.RLock()
	override := dataDirOverride
	dataDirMu.RUnlock()
	if override != "" {
		return override, nil
	}
	if env := os.Getenv(DataDirEnvVar); env != "" {
		return env, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lucinate"), nil
}
