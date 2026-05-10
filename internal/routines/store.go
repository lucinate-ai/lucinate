package routines

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// ErrNotFound is returned by Load when no routine directory exists for the
// requested name.
var ErrNotFound = errors.New("routine not found")

// Dir returns the routines root directory (<data-dir>/routines). It is not
// created here — callers that mutate the tree must MkdirAll themselves.
func Dir() (string, error) {
	root, err := config.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "routines"), nil
}

// List returns every routine present on disk, sorted by name. Routines whose
// STEPS.md is missing or unparseable are skipped silently — listing should
// surface what works rather than refuse on one bad entry.
func List() ([]Routine, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Routine
	for _, entry := range entries {
		info, err := os.Stat(filepath.Join(dir, entry.Name()))
		if err != nil || !info.IsDir() {
			continue
		}
		r, err := Load(entry.Name())
		if err != nil {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Load reads and parses a single routine by name. Returns ErrNotFound if the
// directory doesn't exist or contains no STEPS.md.
func Load(name string) (Routine, error) {
	if !validName(name) {
		return Routine{}, fmt.Errorf("invalid routine name %q", name)
	}
	dir, err := Dir()
	if err != nil {
		return Routine{}, err
	}
	path := filepath.Join(dir, name, "STEPS.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Routine{}, ErrNotFound
		}
		return Routine{}, err
	}
	return Parse(string(data), name, path)
}

// Save writes the routine to disk. The directory <routines>/<name>/ is
// created when missing. Existing STEPS.md is overwritten atomically.
//
// Save enforces kebab-case (see IsValidKebab) so any new or renamed
// routine lands on disk with a typeable, lowercase identifier. Load
// and Delete remain lenient — non-kebab directories left over from
// earlier versions continue to work until they're edited, at which
// point the rename in submitForm forces them onto a kebab name.
func Save(r Routine) error {
	if !IsValidKebab(r.Name) {
		return fmt.Errorf("invalid routine name %q: use kebab-case (lowercase letters, digits, hyphens)", r.Name)
	}
	root, err := Dir()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, r.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	target := filepath.Join(dir, "STEPS.md")
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, []byte(Format(r)), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// Delete removes the routine directory and everything beneath it.
func Delete(name string) error {
	if !validName(name) {
		return fmt.Errorf("invalid routine name %q", name)
	}
	root, err := Dir()
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(root, name))
}

// validName accepts directory-safe routine names: non-empty, no path
// separators, no leading dot. Mirrors the conservative shape we already
// enforce on agent identifiers.
func validName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if name[0] == '.' {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '/' || c == '\\' || c == 0:
			return false
		}
	}
	return true
}
