package rooms

import (
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// ── ścieżki projektowe z obcego hosta ────────────────────────────────────────
//
// Orca runs on the host the user sees (often Windows), while a Hermes gateway
// and its room members may run in WSL. ORCA_WORKTREE_ID carries the host's own
// spelling of the project path, so a WSL session can receive
// `\\wsl.localhost\Ubuntu\home\…`. Using that string as a filesystem path is
// how a room once created `<cwd>/\\wsl.localhost\Ubuntu\home\…\.rooms/<id>` —
// a directory no member could open — instead of the project's own `.rooms/`.

var (
	wslUNCPrefix = regexp.MustCompile(`(?i)^\\\\wsl(?:\.localhost|\$)\\[^\\]+\\?(.*)$`)
	drivePrefix  = regexp.MustCompile(`^([A-Za-z]):[\\/](.*)$`)
)

// NormaliseProjectPath maps a project path produced by the Orca host onto this
// machine's filesystem. It returns "" when the path cannot be resolved here —
// callers must treat that as "no project path", never as a relative one.
//
//   - `\\wsl.localhost\<distro>\a\b` and `\\wsl$\<distro>\a\b` become `/a/b`
//     on a POSIX host, and pass through untouched on Windows (where the UNC
//     is itself valid).
//   - `C:\a\b` becomes `/mnt/c/a/b` on Linux only when that directory exists;
//     otherwise it is unknown. On Windows it passes through.
//   - any other backslash path (a non-WSL UNC, a Windows-relative path) is
//     unknown on a POSIX host.
//   - POSIX and relative paths are returned unchanged.
func NormaliseProjectPath(raw string) string {
	p := strings.TrimSpace(raw)
	p = strings.Trim(p, `"'`)
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		return p
	}

	if m := wslUNCPrefix.FindStringSubmatch(p); m != nil {
		rest := strings.TrimPrefix(strings.ReplaceAll(m[1], `\`, "/"), "/")
		if rest == "" {
			return "/"
		}
		return "/" + rest
	}

	if m := drivePrefix.FindStringSubmatch(p); m != nil {
		rest := strings.ReplaceAll(m[2], `\`, "/")
		candidate := "/mnt/" + strings.ToLower(m[1]) + "/" + rest
		if isDir(candidate) {
			return candidate
		}
		return ""
	}

	if strings.Contains(p, `\`) {
		return ""
	}
	return p
}

// LocalProjectPath reports the project path the room can actually use: the
// normalised path when it names an existing directory, else "". The room's
// shared directory must never be created from a path that does not exist —
// the members would be handed a directory they cannot open.
func LocalProjectPath(raw string) string {
	p := NormaliseProjectPath(raw)
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil || !isDir(abs) {
		return ""
	}
	return abs
}
