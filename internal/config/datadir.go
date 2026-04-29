package config

import (
	"os"
	"path/filepath"
)

// DataDirEnvVar is the environment variable that overrides the
// default lucinate state directory. Embedders whose host platform
// doesn't expose a writable user home directory — typically
// native-platform hosts whose process inherits a read-only bundle
// path from os.UserHomeDir() — set this to a writable sandboxed
// location before the program starts. The CLI leaves it unset and
// falls back to the platform's home directory.
const DataDirEnvVar = "LUCINATE_DATA_DIR"

// DataDir returns the root directory for all on-disk lucinate state
// (connections, secrets, identity, agents, preferences, skill cache).
// Resolution order:
//
//  1. The LUCINATE_DATA_DIR environment variable, if set and non-empty.
//  2. <user-home>/.lucinate.
//
// The returned path is not created here — each caller is responsible
// for provisioning the subdirectory layout it needs (with whatever
// permission mode the data warrants).
func DataDir() (string, error) {
	if override := os.Getenv(DataDirEnvVar); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lucinate"), nil
}
