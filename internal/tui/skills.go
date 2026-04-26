package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// agentSkill represents a discovered agent skill from a SKILL.md file.
type agentSkill struct {
	Name        string // from frontmatter
	Description string // from frontmatter
	Body        string // markdown body after frontmatter
	Dir         string // directory containing the SKILL.md
}

// skillFrontmatter is the YAML frontmatter of a SKILL.md file.
type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// discoverSkills scans well-known directories for SKILL.md files and returns
// the discovered skills. Directories searched:
//   - <cwd>/.agents/skills/*/SKILL.md
//   - <cwd>/.agent/skills/*/SKILL.md
//   - ~/.agents/skills/*/SKILL.md
//   - ~/.agent/skills/*/SKILL.md
//
// CWD skills take precedence over home directory skills with the same name.
// Symlinked skill directories are followed.
func discoverSkills() []agentSkill {
	var dirs []string

	// CWD first (higher priority).
	if cwd, err := os.Getwd(); err == nil {
		dirs = append(dirs, filepath.Join(cwd, ".agents", "skills"))
		dirs = append(dirs, filepath.Join(cwd, ".agent", "skills"))
	}

	// Home directory.
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".agents", "skills"))
		dirs = append(dirs, filepath.Join(home, ".agent", "skills"))
	}

	seen := make(map[string]bool)
	var skills []agentSkill

	for _, base := range dirs {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			skillDir := filepath.Join(base, entry.Name())

			// Resolve symlinks — entry.IsDir() returns false for symlinks.
			info, err := os.Stat(skillDir)
			if err != nil || !info.IsDir() {
				continue
			}

			skillFile := filepath.Join(skillDir, "SKILL.md")
			data, err := os.ReadFile(skillFile)
			if err != nil {
				continue
			}

			skill, err := parseSkillFile(string(data), skillDir)
			if err != nil || skill.Name == "" {
				continue
			}

			if seen[skill.Name] {
				continue
			}
			seen[skill.Name] = true
			skills = append(skills, skill)
		}
	}
	return skills
}

// parseSkillFile parses a SKILL.md file with YAML frontmatter delimited by
// "---" lines.
func parseSkillFile(content, dir string) (agentSkill, error) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return agentSkill{}, fmt.Errorf("missing frontmatter")
	}

	// Find closing "---".
	rest := content[3:]
	rest = strings.TrimLeft(rest, "\r\n")
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return agentSkill{}, fmt.Errorf("unclosed frontmatter")
	}

	fmRaw := rest[:idx]
	body := strings.TrimSpace(rest[idx+4:]) // skip past "\n---"

	var fm skillFrontmatter
	if err := yaml.Unmarshal([]byte(fmRaw), &fm); err != nil {
		return agentSkill{}, fmt.Errorf("invalid frontmatter YAML: %w", err)
	}

	return agentSkill{
		Name:        fm.Name,
		Description: fm.Description,
		Body:        body,
		Dir:         dir,
	}, nil
}

// prefixAllLines prepends "System: " to every line of the text so that
// stripSystemLines removes the entire block from display after history refresh.
func prefixAllLines(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = "System: " + line
	}
	return strings.Join(lines, "\n")
}

// skillCatalogBlock returns a System: prefixed block listing available skills.
// This is prepended to the first user message so the agent knows what's available.
// Every line is prefixed with "System:" so that stripSystemLines removes the
// entire block from display after a history refresh.
func skillCatalogBlock(skills []agentSkill) string {
	var entries strings.Builder
	for _, s := range skills {
		if s.Name != "" {
			entries.WriteString(fmt.Sprintf("  - %s: %s\n", s.Name, s.Description))
		}
	}
	if entries.Len() == 0 {
		return ""
	}
	return prefixAllLines("Available agent skills (activate with /skill-name):\n" + entries.String())
}
