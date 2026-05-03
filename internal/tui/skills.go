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

// expandSkillReferences scans text for /<skill-name> tokens (preceded by BOF
// or whitespace) that match a known skill, and returns a rewritten payload
// containing a <local-agent-skill> envelope followed by the prose with each
// matched token replaced.
//
// If text is exactly "/<skill>" (after trimming), the prose collapses to
// `use the "<name>" skill above immediately`. Otherwise each match is
// replaced with `the "<name>" skill above`.
//
// Returns (text, false) if no skill ref was matched.
func expandSkillReferences(text string, skills []agentSkill) (string, bool) {
	if len(skills) == 0 || text == "" {
		return text, false
	}

	byName := make(map[string]*agentSkill, len(skills))
	for i := range skills {
		byName[strings.ToLower(skills[i].Name)] = &skills[i]
	}

	var matched []*agentSkill
	seen := make(map[string]bool)
	register := func(s *agentSkill) {
		if seen[s.Name] {
			return
		}
		seen[s.Name] = true
		matched = append(matched, s)
	}

	// Bare-command short-circuit: entire trimmed message is a single skill ref.
	trimmed := strings.TrimSpace(text)
	var rewritten string
	if strings.HasPrefix(trimmed, "/") {
		bareName := strings.ToLower(trimmed[1:])
		if s, ok := byName[bareName]; ok && bareName != "" && isSlashTokenName(bareName) {
			register(s)
			rewritten = fmt.Sprintf("use the %q skill above immediately", s.Name)
		}
	}

	if rewritten == "" {
		var out strings.Builder
		out.Grow(len(text))
		i := 0
		for i < len(text) {
			c := text[i]
			if c == '/' && (i == 0 || isSpaceByte(text[i-1])) {
				j := i + 1
				for j < len(text) && isSlashTokenByte(text[j]) {
					j++
				}
				name := strings.ToLower(text[i+1 : j])
				if s, ok := byName[name]; ok && name != "" {
					register(s)
					fmt.Fprintf(&out, "the %q skill above", s.Name)
					i = j
					continue
				}
			}
			out.WriteByte(c)
			i++
		}
		rewritten = out.String()
	}

	if len(matched) == 0 {
		return text, false
	}

	var prefix strings.Builder
	if len(matched) == 1 {
		prefix.WriteString("Please use the following skill:\n\n")
	} else {
		prefix.WriteString("Please use the following skills:\n\n")
	}
	for idx, s := range matched {
		fmt.Fprintf(&prefix, "<local-agent-skill name=%q>\n", s.Name)
		if s.Body != "" {
			prefix.WriteString(s.Body)
			if !strings.HasSuffix(s.Body, "\n") {
				prefix.WriteString("\n")
			}
		}
		prefix.WriteString("</local-agent-skill>")
		if idx < len(matched)-1 {
			prefix.WriteString("\n\n")
		}
	}
	prefix.WriteString("\n\n")
	prefix.WriteString(rewritten)
	return prefix.String(), true
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}

func isSlashTokenByte(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_' || b == '-'
}

func isSlashTokenName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isSlashTokenByte(s[i]) {
			return false
		}
	}
	return true
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
