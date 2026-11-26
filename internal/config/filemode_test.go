package config

import (
	"runtime"
	"testing"
)

// requirePosixFileModes skips a test that asserts a POSIX permission
// mode.
//
// Windows has no POSIX modes: os.Chmod only toggles the read-only bit, so
// a file the code writes with 0600 stats as 0666 and every such assertion
// fails for a reason that has nothing to do with the code under test. The
// production code still asks for 0600 — this only stops the host from
// being read as a regression.
func requirePosixFileModes(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX file modes — os.Chmod only toggles the read-only bit")
	}
}
