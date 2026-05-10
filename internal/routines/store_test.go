package routines

import (
	"path/filepath"
	"testing"

	"github.com/lucinate-ai/lucinate/internal/config"
)

func TestStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	config.SetDataDir(dir)
	t.Cleanup(func() { config.SetDataDir("") })

	in := Routine{
		Name: "demo",
		Frontmatter: Frontmatter{
			Name: "demo",
			Mode: string(ModeAuto),
			Log:  "./demo.log",
		},
		Steps: []string{
			"first step\n\nwith two paragraphs",
			"second step",
		},
	}
	if err := Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out, err := Load("demo")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Name != "demo" {
		t.Errorf("Name = %q", out.Name)
	}
	if out.ResolvedMode() != ModeAuto {
		t.Errorf("ResolvedMode = %q", out.ResolvedMode())
	}
	if len(out.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want 2", len(out.Steps))
	}
	if out.Steps[0] != in.Steps[0] {
		t.Errorf("Steps[0] = %q, want %q", out.Steps[0], in.Steps[0])
	}
	if out.Path != filepath.Join(dir, "routines", "demo", "STEPS.md") {
		t.Errorf("Path = %q", out.Path)
	}

	listed, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0].Name != "demo" {
		t.Errorf("List = %+v, want [demo]", listed)
	}

	if err := Delete("demo"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := Load("demo"); err != ErrNotFound {
		t.Errorf("Load after delete = %v, want ErrNotFound", err)
	}
}

func TestStore_RejectsInvalidName(t *testing.T) {
	// Path-unsafe and non-kebab names are both rejected by Save.
	// validName (path safety) is unchanged; IsValidKebab adds the
	// stricter form rule on top, so any name that fails either gate
	// is rejected here.
	cases := []string{
		"", ".", "..", ".hidden", "with/slash", "with\\back",
		"Mixed-Case", "with space", "snake_case", "trailing-",
		"-leading", "double--hyphen",
	}
	for _, name := range cases {
		if err := Save(Routine{Name: name, Steps: []string{"x"}}); err == nil {
			t.Errorf("Save(%q) = nil, want error", name)
		}
	}
}
