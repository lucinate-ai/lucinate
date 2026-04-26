package tui

import (
	"os"
	"path/filepath"
	"testing"
)

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
	hint := m.slashCommandHint("/rev")
	if hint != "iew" {
		t.Errorf("slashCommandHint(/rev) = %q, want %q", hint, "iew")
	}
}

func TestSkillActivation(t *testing.T) {
	m := newSlashTestModel()
	m.skills = []agentSkill{
		{Name: "review", Description: "Code review", Body: "Review the code carefully."},
	}

	handled, cmd := m.handleSlashCommand("/review")
	if !handled {
		t.Fatal("expected /review to be handled")
	}
	if cmd == nil {
		t.Fatal("expected a send cmd")
	}
	// Verify the user message was added to display.
	last := m.messages[len(m.messages)-1]
	if last.role != "user" || last.content != "/review" {
		t.Errorf("expected user message '/review', got role=%q content=%q", last.role, last.content)
	}
	if !m.sending {
		t.Error("expected sending to be true after skill activation")
	}
}

func TestWithSkillCatalog(t *testing.T) {
	m := newSlashTestModel()
	m.skills = []agentSkill{
		{Name: "review", Description: "Code review"},
	}

	// First call should prepend catalog.
	result := m.withSkillCatalog("hello")
	if !contains(result, "System:") {
		t.Error("first message should contain skill catalog")
	}
	if !contains(result, "hello") {
		t.Error("first message should contain original text")
	}

	// Second call should not prepend.
	result2 := m.withSkillCatalog("world")
	if contains(result2, "System:") {
		t.Error("second message should not contain skill catalog")
	}
	if result2 != "world" {
		t.Errorf("second message = %q, want %q", result2, "world")
	}
}

func TestWithSkillCatalog_NoSkills(t *testing.T) {
	m := newSlashTestModel()
	result := m.withSkillCatalog("hello")
	if result != "hello" {
		t.Errorf("expected unmodified text with no skills, got %q", result)
	}
}

func TestWithSkillCatalog_EmptyCatalog(t *testing.T) {
	m := newSlashTestModel()
	// Skills with empty names produce an empty catalog block.
	m.skills = []agentSkill{{Name: "", Description: "no name"}}
	result := m.withSkillCatalog("hello")
	if result != "hello" {
		t.Errorf("expected unmodified text when catalog is empty, got %q", result)
	}
	if m.skillCatalogSent {
		t.Error("skillCatalogSent should not be set when catalog is empty")
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
	if !handled {
		t.Fatal("expected /review to match skill 'Review'")
	}
	if cmd == nil {
		t.Fatal("expected a send cmd")
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
