package routines

import (
	"strings"
	"testing"
)

func TestParse_FullFrontmatter(t *testing.T) {
	in := `---
name: demo
mode: auto
log: ./demo.log
---

first step

It can have multiple lines

---
second step

---

third step
`
	r, err := Parse(in, "demo", "/x/STEPS.md")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.Name != "demo" {
		t.Errorf("Name = %q, want demo", r.Name)
	}
	if r.Frontmatter.Mode != "auto" {
		t.Errorf("Mode = %q, want auto", r.Frontmatter.Mode)
	}
	if r.Frontmatter.Log != "./demo.log" {
		t.Errorf("Log = %q, want ./demo.log", r.Frontmatter.Log)
	}
	if len(r.Steps) != 3 {
		t.Fatalf("len(Steps) = %d, want 3", len(r.Steps))
	}
	if !strings.HasPrefix(r.Steps[0], "first step") {
		t.Errorf("Step[0] = %q, want prefix 'first step'", r.Steps[0])
	}
	if !strings.Contains(r.Steps[0], "It can have multiple lines") {
		t.Errorf("Step[0] missing second line: %q", r.Steps[0])
	}
}

func TestParse_NoFrontmatter(t *testing.T) {
	in := `first step

---

second step
`
	r, err := Parse(in, "x", "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.ResolvedMode() != ModeManual {
		t.Errorf("ResolvedMode = %q, want manual", r.ResolvedMode())
	}
	if len(r.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want 2", len(r.Steps))
	}
	if r.Steps[0] != "first step" {
		t.Errorf("Step[0] = %q, want 'first step'", r.Steps[0])
	}
}

func TestParse_PreservesInternalBlankLines(t *testing.T) {
	in := `---
name: x
---
para one

para two

para three
`
	r, err := Parse(in, "x", "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(r.Steps) != 1 {
		t.Fatalf("len(Steps) = %d, want 1", len(r.Steps))
	}
	got := r.Steps[0]
	want := "para one\n\npara two\n\npara three"
	if got != want {
		t.Errorf("Step[0] = %q, want %q", got, want)
	}
}

func TestParse_EmptySeparatorsDropped(t *testing.T) {
	in := `step one
---
---
step two
`
	r, err := Parse(in, "x", "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(r.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want 2 (got %v)", len(r.Steps), r.Steps)
	}
}

func TestParse_ResolvedModeDefaults(t *testing.T) {
	cases := []struct {
		mode string
		want Mode
	}{
		{"", ModeManual},
		{"manual", ModeManual},
		{"auto", ModeAuto},
		{"bogus", ModeManual},
	}
	for _, tc := range cases {
		r := Routine{Frontmatter: Frontmatter{Mode: tc.mode}}
		if got := r.ResolvedMode(); got != tc.want {
			t.Errorf("Mode=%q ResolvedMode=%q want %q", tc.mode, got, tc.want)
		}
	}
}

func TestFormatRoundTrip(t *testing.T) {
	r1 := Routine{
		Name: "demo",
		Frontmatter: Frontmatter{
			Name: "demo",
			Mode: "auto",
			Log:  "./out.log",
		},
		Steps: []string{
			"first step\n\nwith two paragraphs",
			"second step",
		},
	}
	s := Format(r1)
	r2, err := Parse(s, "demo", "")
	if err != nil {
		t.Fatalf("Parse(Format()): %v", err)
	}
	if r2.Frontmatter != r1.Frontmatter {
		t.Errorf("frontmatter mismatch: got %+v want %+v", r2.Frontmatter, r1.Frontmatter)
	}
	if len(r2.Steps) != len(r1.Steps) {
		t.Fatalf("len(Steps) = %d, want %d", len(r2.Steps), len(r1.Steps))
	}
	for i := range r1.Steps {
		if r2.Steps[i] != r1.Steps[i] {
			t.Errorf("Steps[%d]: got %q want %q", i, r2.Steps[i], r1.Steps[i])
		}
	}
}

func TestFormat_NoFrontmatterWhenEmpty(t *testing.T) {
	r := Routine{Steps: []string{"a", "b"}}
	s := Format(r)
	if strings.HasPrefix(s, "---") {
		t.Errorf("Format emitted frontmatter block for empty Frontmatter: %q", s)
	}
	if !strings.Contains(s, "\n---\n") {
		t.Errorf("Format missing step separator: %q", s)
	}
}
