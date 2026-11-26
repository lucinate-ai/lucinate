package rooms

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A path produced by the Orca host is not necessarily a path this machine can
// use: on WSL the host is Windows, so ORCA_WORKTREE_ID carries a
// \\wsl.localhost\... UNC. Handing that to os.MkdirAll creates a directory
// whose name contains backslashes (observed: internal/tui/\\wsl.localhost\...),
// and telling a member to write there names a path that does not exist.
func TestNormaliseProjectPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"wsl localhost becomes posix", `\\wsl.localhost\Ubuntu\home\hermes\projects\app`, "/home/hermes/projects/app"},
		{"wsl dollar becomes posix", `\\wsl$\Ubuntu\home\hermes\projects\app`, "/home/hermes/projects/app"},
		{"wsl path keeps mnt", `\\wsl.localhost\Ubuntu\mnt\c\Users\me`, "/mnt/c/Users/me"},
		{"quoted value is unwrapped", `"\\wsl.localhost\Ubuntu\home\x"`, "/home/x"},
		{"posix path unchanged", "/home/hermes/projects/app", "/home/hermes/projects/app"},
		{"relative path unchanged", "src/app", "src/app"},
		{"empty stays empty", "", ""},
		{"other unc is unknown on linux", `\\server\share\dir`, ""},
		{"unknown drive is unknown on linux", `Z:\nope\dir`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormaliseProjectPath(tc.in)
			if runtime.GOOS == "windows" && strings.Contains(tc.in, `\`) {
				// On Windows these paths are natively meaningful; the
				// normaliser must not mangle them.
				if got == "" {
					t.Fatalf("NormaliseProjectPath(%q) = empty on Windows", tc.in)
				}
				return
			}
			if got != tc.want {
				t.Errorf("NormaliseProjectPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestOrientationFromEnv_NormalisesTheWSLPath(t *testing.T) {
	raw := `id:6d1af8c2::\\wsl.localhost\Ubuntu\home\hermes\projects\lucinate`
	t.Setenv("ORCA_WORKTREE_ID", raw)

	o := OrientationFromEnv()
	if o.Path != "/home/hermes/projects/lucinate" {
		t.Errorf("Path = %q, want the POSIX path", o.Path)
	}
	if o.Project != "lucinate" {
		t.Errorf("Project = %q, want lucinate", o.Project)
	}
	// The selector is an Orca argument, not a filesystem path: it travels
	// through unchanged so an orca_* tool still recognises the worktree.
	if !strings.Contains(o.Worktree, `\\wsl.localhost\Ubuntu`) {
		t.Errorf("Worktree = %q, want the original selector", o.Worktree)
	}
}

func TestOrientationFromWorktree_NormalisesTheProjectPath(t *testing.T) {
	o := OrientationFromWorktree(`id:repo::\\wsl.localhost\Ubuntu\home\hermes\projects\app`)
	if o.Path != "/home/hermes/projects/app" {
		t.Errorf("Path = %q, want the POSIX path", o.Path)
	}
	if o.Project != "app" {
		t.Errorf("Project = %q, want app", o.Project)
	}
}

func TestRoomsBase_UsesANormalisedWSLPath(t *testing.T) {
	dir := t.TempDir()
	unc := `\\wsl.localhost\Ubuntu` + strings.ReplaceAll(dir, "/", `\`)
	t.Setenv("ORCA_WORKTREE_ID", "id:repo::"+unc)

	base, err := RoomsBase()
	if err != nil {
		t.Fatalf("RoomsBase: %v", err)
	}
	if filepath.Clean(base) != filepath.Clean(dir) {
		t.Errorf("base = %q, want the normalised worktree %q", base, dir)
	}
}

func TestRoomsBase_FallsBackWhenTheWorktreeIsNotLocal(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("ORCA_WORKTREE_ID", `id:repo::\\wsl.localhost\Ubuntu\no\such\dir`)

	base, err := RoomsBase()
	if err != nil {
		t.Fatalf("RoomsBase: %v", err)
	}
	if filepath.Clean(base) != filepath.Clean(cwd) {
		t.Errorf("base = %q, want the working directory %q", base, cwd)
	}
}

// The bug's other half: the raw string must never be enough to create a
// directory. A base with a backslash in it is not a path on this host.
func TestEnsureRoomDir_RefusesABackslashBase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a backslash base is a valid path on Windows")
	}
	cwd := t.TempDir()
	t.Chdir(cwd)

	if _, err := EnsureRoomDir(`\\wsl.localhost\Ubuntu\home\x`, "sztab"); err == nil {
		t.Fatal("a backslash base should be refused, not turned into a directory name")
	}
	entries, err := os.ReadDir(cwd)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), `\`) {
			t.Fatalf("created a directory whose name contains a backslash: %q", e.Name())
		}
	}
}

func TestEnsureDefaultRoomDir_FallsBackForAnUnknownWorktree(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("ORCA_WORKTREE_ID", `id:repo::C:\Users\me\project`)

	dir, err := EnsureDefaultRoomDir("sztab")
	if err != nil {
		t.Fatalf("EnsureDefaultRoomDir: %v", err)
	}
	if want := filepath.Join(cwd, RoomsDirName, "sztab"); filepath.Clean(dir) != filepath.Clean(want) {
		t.Errorf("dir = %q, want %q", dir, want)
	}
}
