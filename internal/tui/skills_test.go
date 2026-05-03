package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandSkillReferences(t *testing.T) {
	skills := []agentSkill{
		{Name: "foo", Body: "foo instructions"},
		{Name: "bar", Body: "bar instructions"},
		{Name: "commit-message", Body: "use conventional commits"},
		{Name: "Capital", Body: "capital body"},
	}

	t.Run("empty input", func(t *testing.T) {
		got, ok := expandSkillReferences("", skills)
		if got != "" || ok {
			t.Errorf("got (%q, %v), want (\"\", false)", got, ok)
		}
	})

	t.Run("no slash", func(t *testing.T) {
		got, ok := expandSkillReferences("hello world", skills)
		if got != "hello world" || ok {
			t.Errorf("got (%q, %v), want unchanged false", got, ok)
		}
	})

	t.Run("bare command", func(t *testing.T) {
		got, ok := expandSkillReferences("/foo", skills)
		if !ok {
			t.Fatal("expected expansion")
		}
		if !strings.Contains(got, "Please use the following skill:") {
			t.Errorf("missing singular preamble in: %s", got)
		}
		if !strings.Contains(got, "<local-agent-skill name=\"foo\">") {
			t.Errorf("missing envelope tag in: %s", got)
		}
		if !strings.Contains(got, "foo instructions") {
			t.Errorf("missing body in: %s", got)
		}
		if !strings.Contains(got, "use the \"foo\" skill above immediately") {
			t.Errorf("missing bare-prose in: %s", got)
		}
	})

	t.Run("bare command with surrounding whitespace", func(t *testing.T) {
		got, ok := expandSkillReferences("  /foo  ", skills)
		if !ok || !strings.Contains(got, "use the \"foo\" skill above immediately") {
			t.Errorf("expected bare-form expansion, got ok=%v body=%s", ok, got)
		}
	})

	t.Run("mid-message", func(t *testing.T) {
		got, ok := expandSkillReferences("use /foo on x", skills)
		if !ok {
			t.Fatal("expected expansion")
		}
		if !strings.Contains(got, "Please use the following skill:") {
			t.Errorf("missing singular preamble: %s", got)
		}
		if !strings.HasSuffix(got, "use the \"foo\" skill above on x") {
			t.Errorf("expected trailing prose, got: %s", got)
		}
	})

	t.Run("repeated reference", func(t *testing.T) {
		got, ok := expandSkillReferences("/foo and /foo again", skills)
		if !ok {
			t.Fatal("expected expansion")
		}
		if c := strings.Count(got, "<local-agent-skill"); c != 1 {
			t.Errorf("expected one envelope, got %d in: %s", c, got)
		}
		if c := strings.Count(got, "the \"foo\" skill above"); c != 2 {
			t.Errorf("expected two substitutions, got %d in: %s", c, got)
		}
	})

	t.Run("multiple skills", func(t *testing.T) {
		got, ok := expandSkillReferences("use /foo and /bar on x", skills)
		if !ok {
			t.Fatal("expected expansion")
		}
		if !strings.Contains(got, "Please use the following skills:") {
			t.Errorf("missing plural preamble: %s", got)
		}
		fooIdx := strings.Index(got, "name=\"foo\"")
		barIdx := strings.Index(got, "name=\"bar\"")
		if fooIdx < 0 || barIdx < 0 || fooIdx >= barIdx {
			t.Errorf("expected foo before bar in envelope, got: %s", got)
		}
		if !strings.HasSuffix(got, "use the \"foo\" skill above and the \"bar\" skill above on x") {
			t.Errorf("unexpected prose, got: %s", got)
		}
	})

	t.Run("unknown ref unchanged", func(t *testing.T) {
		got, ok := expandSkillReferences("/baz", skills)
		if got != "/baz" || ok {
			t.Errorf("got (%q, %v), want unchanged false", got, ok)
		}
	})

	t.Run("punctuation after token", func(t *testing.T) {
		got, ok := expandSkillReferences("use /foo.", skills)
		if !ok {
			t.Fatal("expected expansion")
		}
		if !strings.HasSuffix(got, "use the \"foo\" skill above.") {
			t.Errorf("punctuation not preserved: %s", got)
		}
	})

	t.Run("mid-word slash unchanged", func(t *testing.T) {
		got, ok := expandSkillReferences("a/foo", skills)
		if got != "a/foo" || ok {
			t.Errorf("got (%q, %v), want unchanged false", got, ok)
		}
	})

	t.Run("after newline", func(t *testing.T) {
		got, ok := expandSkillReferences("a\n/foo b", skills)
		if !ok {
			t.Fatal("expected expansion")
		}
		if !strings.HasSuffix(got, "a\nthe \"foo\" skill above b") {
			t.Errorf("unexpected output: %s", got)
		}
	})

	t.Run("hyphenated name", func(t *testing.T) {
		got, ok := expandSkillReferences("run /commit-message now", skills)
		if !ok {
			t.Fatal("expected expansion")
		}
		if !strings.HasSuffix(got, "run the \"commit-message\" skill above now") {
			t.Errorf("unexpected: %s", got)
		}
	})

	t.Run("canonical-name preserved", func(t *testing.T) {
		got, ok := expandSkillReferences("hi /capital there", skills)
		if !ok {
			t.Fatal("expected expansion")
		}
		if !strings.Contains(got, "the \"Capital\" skill above") {
			t.Errorf("expected canonical 'Capital', got: %s", got)
		}
	})
}

func TestFindSlashTokenAt(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		cursor     int
		wantStart  int
		wantPrefix string
		wantOK     bool
	}{
		{"slash only", "/", 1, 0, "/", true},
		{"slash at start, end of token", "/foo", 4, 0, "/foo", true},
		{"after space", "use /foo", 8, 4, "/foo", true},
		{"end-of-token before space", "use /foo bar", 8, 4, "/foo", true},
		{"cursor mid-token", "use /foo bar", 6, 0, "", false},
		{"no whitespace before /", "a/foo", 5, 0, "", false},
		{"empty input", "", 0, 0, "", false},
		{"multi-line", "hello\n/foo", 10, 6, "/foo", true},
		{"emoji before slash", "👋 /foo", len("👋 /foo"), len("👋 "), "/foo", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, prefix, ok := findSlashTokenAt(tt.value, tt.cursor)
			if ok != tt.wantOK || prefix != tt.wantPrefix || (ok && start != tt.wantStart) {
				t.Errorf("findSlashTokenAt(%q, %d) = (%d, %q, %v), want (%d, %q, %v)",
					tt.value, tt.cursor, start, prefix, ok, tt.wantStart, tt.wantPrefix, tt.wantOK)
			}
		})
	}
}

func TestParseSkillFile(t *testing.T) {
	content := `---
name: code-review
description: Reviews code for best practices
---

Review the code for:
- Security issues
- Performance problems
`
	skill, err := parseSkillFile(content, "/tmp/skills/code-review")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skill.Name != "code-review" {
		t.Errorf("name = %q, want %q", skill.Name, "code-review")
	}
	if skill.Description != "Reviews code for best practices" {
		t.Errorf("description = %q, want %q", skill.Description, "Reviews code for best practices")
	}
	if skill.Body == "" {
		t.Error("expected non-empty body")
	}
	if skill.Dir != "/tmp/skills/code-review" {
		t.Errorf("dir = %q, want %q", skill.Dir, "/tmp/skills/code-review")
	}
}

func TestParseSkillFile_NoFrontmatter(t *testing.T) {
	_, err := parseSkillFile("just some text", "/tmp")
	if err == nil {
		t.Error("expected error for missing frontmatter")
	}
}

func TestParseSkillFile_UnclosedFrontmatter(t *testing.T) {
	_, err := parseSkillFile("---\nname: test\n", "/tmp")
	if err == nil {
		t.Error("expected error for unclosed frontmatter")
	}
}

func TestSkillCatalogBlock(t *testing.T) {
	skills := []agentSkill{
		{Name: "review", Description: "Code review"},
		{Name: "test", Description: "Write tests"},
	}
	block := skillCatalogBlock(skills)
	if block == "" {
		t.Fatal("expected non-empty catalog block")
	}
	if !contains(block, "review") || !contains(block, "test") {
		t.Error("catalog block should contain skill names")
	}
	if !contains(block, "System:") {
		t.Error("catalog block should have System: prefix")
	}
}

func TestSkillCatalogBlock_Empty(t *testing.T) {
	if block := skillCatalogBlock(nil); block != "" {
		t.Errorf("expected empty block for no skills, got %q", block)
	}
}

func TestSkillCatalogBlock_EmptyNames(t *testing.T) {
	skills := []agentSkill{
		{Name: "", Description: "no name"},
		{Name: "", Description: "also no name"},
	}
	if block := skillCatalogBlock(skills); block != "" {
		t.Errorf("expected empty block when all skills have empty names, got %q", block)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestDiscoverSkills_FromDir(t *testing.T) {
	// Create a temp directory with a skill.
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".agents", "skills", "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: my-skill\ndescription: A test skill\n---\n\nDo the thing."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Change to the temp dir so discoverSkills finds it via cwd.
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	skills := discoverSkills()
	found := false
	for _, s := range skills {
		if s.Name == "my-skill" {
			found = true
			if s.Description != "A test skill" {
				t.Errorf("description = %q, want %q", s.Description, "A test skill")
			}
		}
	}
	if !found {
		t.Error("expected to discover my-skill")
	}
}

func TestSkillSlashCommand(t *testing.T) {
	m := newSlashTestModel()
	m.skills = []agentSkill{
		{Name: "review", Description: "Code review", Body: "Review the code."},
	}

	// Tab-complete a skill name.
	got := m.completeSlashCommand("/rev")
	if got != "/review" {
		t.Errorf("completeSlashCommand(/rev) = %q, want /review", got)
	}

	// Hint for skill.
	token, suffix := m.slashCommandHint("/rev", 4)
	if token != "/rev" || suffix != "iew" {
		t.Errorf("slashCommandHint(/rev, 4) = (%q, %q), want (%q, %q)", token, suffix, "/rev", "iew")
	}
}

func TestSkillActivation_DelegatesToSendPath(t *testing.T) {
	m := newSlashTestModel()
	m.skills = []agentSkill{
		{Name: "review", Description: "Code review", Body: "Review the code carefully."},
	}

	// /skill alone: handleSlashCommand returns (false, nil) so the regular
	// send path runs expandSkillReferences and produces the envelope, with
	// a streaming-assistant placeholder appended.
	handled, cmd := m.handleSlashCommand("/review")
	if handled || cmd != nil {
		t.Fatalf("expected /review to be delegated (false, nil), got handled=%v cmd=%v", handled, cmd)
	}

	// /skill with prose: same delegation.
	handled, cmd = m.handleSlashCommand("/review the diff")
	if handled || cmd != nil {
		t.Fatalf("expected /review-with-prose to be delegated, got handled=%v cmd=%v", handled, cmd)
	}
}

func TestSkillActivation_UnknownCommandStillErrors(t *testing.T) {
	m := newSlashTestModel()
	m.skills = []agentSkill{
		{Name: "review", Description: "Code review", Body: "Review the code carefully."},
	}

	handled, cmd := m.handleSlashCommand("/notaskill")
	if !handled || cmd != nil {
		t.Fatalf("expected unknown slash to be handled with no cmd, got handled=%v cmd=%v", handled, cmd)
	}
	if len(m.messages) == 0 {
		t.Fatal("expected an error system message")
	}
	last := m.messages[len(m.messages)-1]
	if last.role != "system" || last.errMsg == "" {
		t.Errorf("expected system error message, got role=%q errMsg=%q", last.role, last.errMsg)
	}
}

func TestCatalogParams_ConvertsSkills(t *testing.T) {
	m := newSlashTestModel()
	m.skills = []agentSkill{
		{Name: "review", Description: "Code review"},
		{Name: "commit", Description: "Write a commit message"},
	}

	got := m.catalogParams()
	if len(got) != 2 {
		t.Fatalf("expected 2 catalog entries, got %d", len(got))
	}
	if got[0].Name != "review" || got[0].Description != "Code review" {
		t.Errorf("first entry: %+v", got[0])
	}
	if got[1].Name != "commit" || got[1].Description != "Write a commit message" {
		t.Errorf("second entry: %+v", got[1])
	}
}

func TestCatalogParams_NoSkillsReturnsNil(t *testing.T) {
	m := newSlashTestModel()
	if got := m.catalogParams(); got != nil {
		t.Errorf("expected nil when no skills loaded, got %+v", got)
	}
}

func TestSkillsDiscoveredMsg_AddsMessage(t *testing.T) {
	m := newSlashTestModel()
	initialCount := len(m.messages)

	updated, _ := m.Update(skillsDiscoveredMsg{skills: []agentSkill{
		{Name: "review", Description: "Code review"},
		{Name: "test", Description: "Write tests"},
	}})

	if len(updated.skills) != 2 {
		t.Errorf("expected 2 skills, got %d", len(updated.skills))
	}
	if len(updated.messages) != initialCount+1 {
		t.Fatalf("expected %d messages, got %d", initialCount+1, len(updated.messages))
	}
	msg := updated.messages[len(updated.messages)-1]
	if msg.role != "system" {
		t.Errorf("expected system message, got %q", msg.role)
	}
	if !contains(msg.content, "2 agent skill(s) loaded") {
		t.Errorf("expected skills count in message, got %q", msg.content)
	}
}

func TestSkillsDiscoveredMsg_NoMessage_WhenEmpty(t *testing.T) {
	m := newSlashTestModel()
	initialCount := len(m.messages)

	updated, _ := m.Update(skillsDiscoveredMsg{skills: nil})

	if len(updated.messages) != initialCount {
		t.Error("expected no message added when no skills discovered")
	}
}

func TestSkillsListCommand(t *testing.T) {
	m := newSlashTestModel()
	m.skills = []agentSkill{
		{Name: "review", Description: "Code review"},
		{Name: "test", Description: "Write tests"},
	}
	initialCount := len(m.messages)

	handled, cmd := m.handleSlashCommand("/skills")
	if !handled {
		t.Fatal("expected /skills to be handled")
	}
	if cmd != nil {
		t.Error("expected nil cmd from /skills")
	}
	if len(m.messages) != initialCount+1 {
		t.Fatalf("expected %d messages, got %d", initialCount+1, len(m.messages))
	}
	msg := m.messages[len(m.messages)-1]
	if !contains(msg.content, "/review") || !contains(msg.content, "/test") {
		t.Errorf("skills list should contain skill names, got %q", msg.content)
	}
}

func TestSkillsListCommand_Empty(t *testing.T) {
	m := newSlashTestModel()
	initialCount := len(m.messages)

	handled, _ := m.handleSlashCommand("/skills")
	if !handled {
		t.Fatal("expected /skills to be handled")
	}
	msg := m.messages[len(m.messages)-1]
	if !contains(msg.content, "No agent skills found") {
		t.Errorf("expected empty skills message, got %q", msg.content)
	}
	if len(m.messages) != initialCount+1 {
		t.Fatalf("expected %d messages, got %d", initialCount+1, len(m.messages))
	}
}

func TestSkillActivation_CaseInsensitive(t *testing.T) {
	m := newSlashTestModel()
	m.skills = []agentSkill{
		{Name: "Review", Description: "Code review", Body: "Review the code."},
	}

	handled, cmd := m.handleSlashCommand("/review")
	if handled || cmd != nil {
		t.Fatalf("expected /review (case-insensitive match for 'Review') to be delegated, got handled=%v cmd=%v", handled, cmd)
	}
}

func TestDiscoverSkills_Symlinks(t *testing.T) {
	dir := t.TempDir()

	// Create actual skill directory.
	realSkillDir := filepath.Join(dir, "real-skills", "my-skill")
	if err := os.MkdirAll(realSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: my-skill\ndescription: A symlinked skill\n---\n\nDo the thing."
	if err := os.WriteFile(filepath.Join(realSkillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create .agents/skills/ and symlink the skill directory into it.
	agentsSkillsDir := filepath.Join(dir, ".agents", "skills")
	if err := os.MkdirAll(agentsSkillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realSkillDir, filepath.Join(agentsSkillsDir, "my-skill")); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	skills := discoverSkills()
	found := false
	for _, s := range skills {
		if s.Name == "my-skill" {
			found = true
		}
	}
	if !found {
		t.Error("expected to discover symlinked skill")
	}
}

func TestDiscoverSkills_SingularDir(t *testing.T) {
	dir := t.TempDir()

	// Use .agent (singular) instead of .agents.
	skillDir := filepath.Join(dir, ".agent", "skills", "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: my-skill\ndescription: Singular dir skill\n---\n\nBody."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	skills := discoverSkills()
	found := false
	for _, s := range skills {
		if s.Name == "my-skill" {
			found = true
		}
	}
	if !found {
		t.Error("expected to discover skill from .agent/ (singular)")
	}
}

func TestDiscoverSkills_Dedup(t *testing.T) {
	dir := t.TempDir()
	content := "---\nname: my-skill\ndescription: A skill\n---\n\nBody."

	// Same skill name in both .agents and .agent.
	for _, parent := range []string{".agents", ".agent"} {
		skillDir := filepath.Join(dir, parent, "skills", "my-skill")
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	skills := discoverSkills()
	count := 0
	for _, s := range skills {
		if s.Name == "my-skill" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 deduplicated skill, got %d", count)
	}
}

func TestStatsTable_IncludesSkills(t *testing.T) {
	m := newSlashTestModel()
	m.skills = []agentSkill{
		{Name: "a", Description: "A"},
		{Name: "b", Description: "B"},
		{Name: "c", Description: "C"},
	}
	m.stats = &sessionStats{}
	table := m.formatStatsTable()
	if !contains(table, "Agent skills: 3 loaded") {
		t.Errorf("stats table should include skills count, got:\n%s", table)
	}
}

func TestStatsTable_NoSkillsLine_WhenEmpty(t *testing.T) {
	m := newSlashTestModel()
	m.stats = &sessionStats{}
	table := m.formatStatsTable()
	if contains(table, "Agent skills") {
		t.Error("stats table should not mention skills when none are loaded")
	}
}
