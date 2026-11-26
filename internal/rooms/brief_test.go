package rooms

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func initBriefRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

func writeBriefTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func commitAll(t *testing.T, dir, message string) {
	t.Helper()
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", message)
}

// setupBriefRepo builds the full fixture: a committed repo with README,
// AGENTS.md and one unchecked openspec task.
func setupBriefRepo(t *testing.T) string {
	t.Helper()
	dir := initBriefRepo(t)
	writeBriefTestFile(t, filepath.Join(dir, "README.md"), "# Demo\n\nOpis projektu.\n")
	writeBriefTestFile(t, filepath.Join(dir, "AGENTS.md"),
		"# Zasady\n\n- pierwsza zasada\n- druga zasada\n\nzwykły akapit\n")
	writeBriefTestFile(t, filepath.Join(dir, "openspec", "changes", "feat-x", "tasks.md"),
		"# Zadania\n\n- [ ] zrobić X\n- [x] już zrobione\n")
	commitAll(t, dir, "content")
	return dir
}

func setupNoReadmeRepo(t *testing.T) string {
	t.Helper()
	dir := initBriefRepo(t)
	writeBriefTestFile(t, filepath.Join(dir, "AGENTS.md"), "- tylko zasada\n")
	commitAll(t, dir, "rules")
	return dir
}

func setupPlainDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeBriefTestFile(t, filepath.Join(dir, "README.md"), "# Sam katalog\n")
	return dir
}

func resolvedPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks %s: %v", path, err)
	}
	return resolved
}

var (
	digestRe = regexp.MustCompile(`^[0-9a-f]{12}$`)
	headRe   = regexp.MustCompile(`^[0-9a-f]{7,12}$`)
)

// ── section presence, table-driven ───────────────────────────────────────────

func TestBuildBrief_Sections(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(t *testing.T) string
		wantReadme bool
		wantRules  bool
		wantTasks  bool
		wantGit    bool
	}{
		{"full repo", setupBriefRepo, true, true, true, true},
		{"missing README", setupNoReadmeRepo, false, true, false, true},
		{"not a git repo", setupPlainDir, true, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.setup(t)
			b, err := BuildBrief(dir)
			if err != nil {
				t.Fatalf("BuildBrief: %v", err)
			}

			md := b.Markdown()
			if got := strings.Contains(md, "## README"); got != tc.wantReadme {
				t.Errorf("README section = %v, want %v:\n%s", got, tc.wantReadme, md)
			}
			if got := strings.Contains(md, "## Zasady (AGENTS.md)"); got != tc.wantRules {
				t.Errorf("rules section = %v, want %v:\n%s", got, tc.wantRules, md)
			}
			if got := strings.Contains(md, "## Zadania (openspec)"); got != tc.wantTasks {
				t.Errorf("tasks section = %v, want %v:\n%s", got, tc.wantTasks, md)
			}
			if len(md) >= maxBriefBytes {
				t.Errorf("Markdown is %d bytes, budget is %d", len(md), maxBriefBytes)
			}
			if !digestRe.MatchString(b.Digest()) {
				t.Errorf("Digest = %q, want 12 hex chars", b.Digest())
			}
			if !filepath.IsAbs(b.Path) {
				t.Errorf("Path = %q, want absolute", b.Path)
			}

			if tc.wantGit {
				if b.Branch == "" {
					t.Errorf("Branch is empty in a git repo")
				}
				if !headRe.MatchString(b.Head) {
					t.Errorf("Head = %q, want 7-12 hex chars", b.Head)
				}
				if b.Dirty != 0 {
					t.Errorf("Dirty = %d, want 0 on a clean repo", b.Dirty)
				}
			} else if b.Branch != "" || b.Head != "" {
				t.Errorf("non-git dir gave Branch=%q Head=%q, want empty", b.Branch, b.Head)
			}
		})
	}
}

func TestBuildBrief_FullSectionsCarryTheirContent(t *testing.T) {
	dir := setupBriefRepo(t)
	b, err := BuildBrief(dir)
	if err != nil {
		t.Fatalf("BuildBrief: %v", err)
	}
	root := resolvedPath(t, dir)
	if b.Project != filepath.Base(root) {
		t.Errorf("Project = %q, want %q", b.Project, filepath.Base(root))
	}
	if resolvedPath(t, b.Path) != root {
		t.Errorf("Path = %q, want %q", b.Path, root)
	}
	if len(b.Rules) != 2 || b.Rules[0] != "- pierwsza zasada" {
		t.Errorf("Rules = %v, want the two bullets from AGENTS.md", b.Rules)
	}
	if !strings.Contains(b.Readme, "Opis projektu.") {
		t.Errorf("Readme = %q, want the README body", b.Readme)
	}
	if len(b.Tasks) != 1 || b.Tasks[0] != "zrobić X" {
		t.Errorf("Tasks = %v, want the one unchecked item", b.Tasks)
	}
}

// A repo with none of the standard sources must still describe what is there:
// the top-level directories stand in for the README, a loose HANDOFF-*.md in
// docs/ stands in for docs/handoff/, and the dirty paths are named.
func TestBriefFallsBackToDirsAndHandoffFiles(t *testing.T) {
	dir := initBriefRepo(t)
	writeBriefTestFile(t, filepath.Join(dir, "app", "main.go"), "package main\n")
	writeBriefTestFile(t, filepath.Join(dir, "docs", "HANDOFF-x.md"), "# Handoff\n\nstan prac\n")

	b, err := BuildBrief(dir)
	if err != nil {
		t.Fatalf("BuildBrief: %v", err)
	}
	md := b.Markdown()
	for _, section := range []string{"## Co tu jest", "## Handoff", "## Brudne pliki"} {
		if !strings.Contains(md, section) {
			t.Errorf("missing %q section:\n%s", section, md)
		}
	}
	if !strings.Contains(md, "- app/\n- docs/\n") {
		t.Errorf("Co tu jest does not list the top-level dirs in order:\n%s", md)
	}
	if !strings.Contains(md, "HANDOFF-x.md") || !strings.Contains(md, "stan prac") {
		t.Errorf("Handoff does not quote the fallback file:\n%s", md)
	}
	if len(md) > maxBriefBytes {
		t.Errorf("Markdown is %d bytes, budget is %d", len(md), maxBriefBytes)
	}

	again, err := BuildBrief(dir)
	if err != nil {
		t.Fatalf("second BuildBrief: %v", err)
	}
	if again.Markdown() != md {
		t.Errorf("the fallback brief is not byte-stable across two builds")
	}
	if again.Digest() != b.Digest() {
		t.Errorf("Digest changed across two builds: %q vs %q", b.Digest(), again.Digest())
	}
}

// ── stability and digest ─────────────────────────────────────────────────────

func TestBuildBrief_IsStable(t *testing.T) {
	dir := setupBriefRepo(t)
	first, err := BuildBrief(dir)
	if err != nil {
		t.Fatalf("first BuildBrief: %v", err)
	}
	second, err := BuildBrief(dir)
	if err != nil {
		t.Fatalf("second BuildBrief: %v", err)
	}
	if first.Markdown() != second.Markdown() {
		t.Errorf("Markdown is not stable across two builds:\n--- first ---\n%s\n--- second ---\n%s",
			first.Markdown(), second.Markdown())
	}
	if first.Digest() != second.Digest() {
		t.Errorf("Digest changed across two builds: %q vs %q", first.Digest(), second.Digest())
	}
}

func TestBuildBrief_DigestChangesWithRepoState(t *testing.T) {
	dir := setupBriefRepo(t)
	base, err := BuildBrief(dir)
	if err != nil {
		t.Fatalf("initial BuildBrief: %v", err)
	}

	t.Run("new commit", func(t *testing.T) {
		gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "second")
		after, err := BuildBrief(dir)
		if err != nil {
			t.Fatalf("BuildBrief after commit: %v", err)
		}
		if after.Head == base.Head {
			t.Errorf("Head did not move after a commit")
		}
		if after.Digest() == base.Digest() {
			t.Errorf("Digest did not change after a commit (still %q)", after.Digest())
		}
	})

	t.Run("new branch", func(t *testing.T) {
		gitIn(t, dir, "checkout", "-q", "-b", "feature/brief")
		after, err := BuildBrief(dir)
		if err != nil {
			t.Fatalf("BuildBrief after branch: %v", err)
		}
		if after.Branch != "feature/brief" {
			t.Errorf("Branch = %q, want feature/brief", after.Branch)
		}
		if after.Digest() == base.Digest() {
			t.Errorf("Digest did not change after a branch switch (still %q)", after.Digest())
		}
	})
}

// ── writer ───────────────────────────────────────────────────────────────────

func TestWriteBrief_WritesTheExactMarkdownAndDigest(t *testing.T) {
	dir := setupBriefRepo(t)
	b, err := BuildBrief(dir)
	if err != nil {
		t.Fatalf("BuildBrief: %v", err)
	}
	roomDir := t.TempDir()

	path, digest, err := WriteBrief(roomDir, b)
	if err != nil {
		t.Fatalf("WriteBrief: %v", err)
	}
	if path != filepath.Join(roomDir, "brief.md") {
		t.Errorf("path = %q, want %q", path, filepath.Join(roomDir, "brief.md"))
	}
	if digest != b.Digest() {
		t.Errorf("digest = %q, want Digest() = %q", digest, b.Digest())
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(onDisk) != b.Markdown() {
		t.Errorf("file bytes differ from Markdown()")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
}

func TestWriteBrief_RequiresDirectory(t *testing.T) {
	if _, _, err := WriteBrief("", Brief{}); err == nil {
		t.Error("WriteBrief(\"\") = nil error, want an error")
	}
}

// ── the pointer the room's first message carries ─────────────────────────────

func TestBriefPointer_WritesTheBriefAndQuotesIt(t *testing.T) {
	dir := setupBriefRepo(t)
	roomDir := t.TempDir()
	orient := Orientation{Path: dir, RoomDir: roomDir}

	pointer := briefPointer(orient)
	if !strings.Contains(pointer, "brief: ") || !strings.Contains(pointer, " sha=") {
		t.Fatalf("pointer missing path/digest: %q", pointer)
	}
	if !strings.Contains(pointer, "Przeczytaj brief przed odpowiedzią") {
		t.Fatalf("pointer missing the instruction line: %q", pointer)
	}
	if len(pointer) > 300 {
		t.Errorf("pointer is %d bytes, budget is 300: %q", len(pointer), pointer)
	}
	if _, err := os.Stat(filepath.Join(roomDir, "brief.md")); err != nil {
		t.Errorf("brief.md was not written: %v", err)
	}
}

func TestBriefPointer_StaysSilentWithoutPathOrRoomDir(t *testing.T) {
	cases := []Orientation{
		{},
		{Path: t.TempDir()},
		{RoomDir: t.TempDir()},
	}
	for _, orient := range cases {
		if pointer := briefPointer(orient); pointer != "" {
			t.Errorf("briefPointer(%+v) = %q, want empty", orient, pointer)
		}
	}
}
