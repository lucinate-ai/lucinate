package tui

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// setTestHome points the process home at dir (a fresh temp dir when none
// is given) and returns it.
//
// It sets HOME *and* the variables Go actually reads on Windows:
// os.UserHomeDir uses USERPROFILE (falling back to HOMEDRIVE+HOMEPATH),
// so a test that only sets HOME resolves to the developer's real home.
// That writes stray state into ~/.lucinate and makes any assertion that
// expects a clean store fail on the second run — a host mismatch that
// reads like a regression.
//
// It also blanks LUCINATE_DATA_DIR, which config.DataDir() checks BEFORE the
// home directory: an ambient value (a shell pointed at a live store, a CI job,
// hermes verify inheriting the caller env) would otherwise redirect every write
// and make the suite non-hermetic.
func setTestHome(t *testing.T, dirs ...string) string {
	t.Helper()
	home := ""
	if len(dirs) > 0 {
		home = dirs[0]
	}
	if home == "" {
		home = t.TempDir()
	}
	t.Setenv("LUCINATE_DATA_DIR", "") // config.DataDirEnvVar
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
		vol := filepath.VolumeName(home)
		t.Setenv("HOMEDRIVE", vol)
		t.Setenv("HOMEPATH", strings.TrimPrefix(home, vol))
	}
	return home
}
