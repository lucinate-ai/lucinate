package rooms

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// ResolveInstallHome returns the Hermes *install* home — the directory
// that holds `profiles/` — as opposed to a profile's own home.
//
// The distinction matters: when a profile is running, Hermes sets
// HERMES_HOME to that profile's home (`<install>/profiles/<name>`), and
// reading `profiles/` under it finds nothing. The gateway only accepts
// room members that are local to it, so resolving this wrongly leaves
// the invite list empty.
func ResolveInstallHome() string {
	candidates := []string{}
	if env := strings.TrimSpace(os.Getenv("HERMES_HOME")); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates, platformDefaultHome())

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if home, ok := installHomeFrom(candidate); ok {
			return home
		}
	}
	for _, candidate := range candidates {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

// installHomeFrom recognises the three shapes a candidate can take:
// the install home itself, a profile home under `<install>/profiles/`,
// or the `profiles` directory.
func installHomeFrom(candidate string) (string, bool) {
	if isDir(filepath.Join(candidate, "profiles")) {
		return candidate, true
	}
	parent := filepath.Dir(candidate)
	if filepath.Base(parent) == "profiles" {
		install := filepath.Dir(parent)
		if isDir(install) {
			return install, true
		}
	}
	if filepath.Base(candidate) == "profiles" {
		install := parent
		if isDir(install) {
			return install, true
		}
	}
	return "", false
}

func platformDefaultHome() string {
	if runtime.GOOS == "windows" {
		if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
			return filepath.Join(local, "hermes")
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".hermes")
	}
	return ""
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// profileIdentityMarkers mirrors hermes_constants._PROFILE_IDENTITY_MARKERS.
// Runtime side-effects (cron heartbeats, caches) and pre-tombstone ghost
// shells leave directories that carry none of these; listing one would put
// a name in the invite menu that the gateway refuses to seat.
var profileIdentityMarkers = []string{
	"config.yaml", ".env", "SOUL.md", "profile.yaml", "auth.json", "state.db",
}

// profileIDPattern mirrors hermes_constants.PROFILE_ID_RE.
var profileIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// deletedProfilesDir mirrors hermes_constants._DELETED_PROFILES_DIR: a
// deleted profile's directory is tombstoned here and must not resurface.
const deletedProfilesDir = ".deleted"

// DiscoverProfiles lists the Hermes profiles a room hosted by this
// machine can invite: the launch home ("default") plus every named
// profile directory under <hermesHome>/profiles.
//
// The filter mirrors hermes_cli.profiles._iter_named_profile_dirs exactly
// — a valid id, an identity marker, not tombstoned — so this list agrees
// with `hermes profile list`. A directory scan alone is not enough: this
// host carries `daily2.bak-…` and `research.pre-research-zip`, which are
// directories but not profiles.
//
// The gateway only accepts members that are local to it, so this — not
// the saved-connections list — is the invite menu's source of truth.
func DiscoverProfiles(hermesHome string) ([]string, error) {
	out := []string{"default"}
	if strings.TrimSpace(hermesHome) == "" {
		return out, nil
	}
	profilesDir := filepath.Join(hermesHome, "profiles")
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if name == "default" || !profileIDPattern.MatchString(name) {
			continue
		}
		dir := filepath.Join(profilesDir, name)
		// Stat rather than trust DirEntry.IsDir(): a profile can be a
		// symlink into a project checkout, which IsDir() reports false.
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		if !hasProfileIdentity(dir) {
			continue
		}
		if _, err := os.Stat(filepath.Join(profilesDir, deletedProfilesDir, name)); err == nil {
			continue // tombstoned
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return append(out, names...), nil
}

// hasProfileIdentity reports whether dir identifies itself as a profile.
// A dangling symlinked marker still counts: it is an identity claim left
// by a clone or migration, and Python's is_file()/is_symlink() pair — the
// reference behaviour — treats it the same way.
func hasProfileIdentity(dir string) bool {
	for _, marker := range profileIdentityMarkers {
		info, err := os.Lstat(filepath.Join(dir, marker))
		if err != nil {
			continue
		}
		if info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

// DiscoverLocalProfiles is DiscoverProfiles against the resolved Hermes
// install home.
func DiscoverLocalProfiles() ([]string, error) {
	return DiscoverProfiles(ResolveInstallHome())
}
