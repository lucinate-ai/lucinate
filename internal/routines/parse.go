package routines

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Parse reads a STEPS.md document and returns the parsed Routine.
//
// File grammar (in order):
//   - Optional YAML frontmatter delimited by lines containing exactly "---".
//   - One or more step bodies separated by lines containing exactly "---".
//
// A frontmatter block is recognised only when the very first non-empty line
// is "---"; otherwise the whole document is treated as steps. Step bodies
// preserve internal blank lines, but leading/trailing whitespace around the
// body is trimmed. Empty bodies (consecutive separators) are dropped.
//
// The provided name becomes Routine.Name regardless of any frontmatter.name
// value — the on-disk directory is the source of truth for identity.
func Parse(content, name, path string) (Routine, error) {
	r := Routine{Name: name, Path: path}

	body := content
	if fm, rest, ok := splitFrontmatter(content); ok {
		var parsed Frontmatter
		if err := yaml.Unmarshal([]byte(fm), &parsed); err != nil {
			return Routine{}, fmt.Errorf("invalid frontmatter YAML: %w", err)
		}
		r.Frontmatter = parsed
		body = rest
	}

	r.Steps = splitSteps(body)
	return r, nil
}

// splitFrontmatter detects a leading YAML frontmatter block and returns
// the YAML body and the post-frontmatter remainder. The first non-blank
// line must be "---" and a subsequent "---" line closes the block. Returns
// ok=false when no frontmatter is present.
func splitFrontmatter(content string) (yamlBody, rest string, ok bool) {
	lines := strings.Split(content, "\n")

	// Skip leading blank lines.
	first := 0
	for first < len(lines) && strings.TrimSpace(lines[first]) == "" {
		first++
	}
	if first >= len(lines) || strings.TrimRight(lines[first], " \t\r") != "---" {
		return "", "", false
	}

	// Find the closing "---".
	for i := first + 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t\r") != "---" {
			continue
		}
		yamlBody = strings.Join(lines[first+1:i], "\n")
		rest = strings.Join(lines[i+1:], "\n")
		return yamlBody, rest, true
	}
	return "", "", false
}

// SplitSteps splits a body (with no frontmatter) on lines containing
// exactly "---", trims each chunk, and drops empty chunks. Useful when
// turning editor-form contents into a step list before saving.
func SplitSteps(body string) []string { return splitSteps(body) }

// splitSteps splits a body on lines containing exactly "---" (ignoring
// trailing whitespace), trims each chunk, and drops empty chunks.
func splitSteps(body string) []string {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	lines := strings.Split(body, "\n")
	var (
		steps []string
		cur   []string
	)
	flush := func() {
		joined := strings.TrimSpace(strings.Join(cur, "\n"))
		if joined != "" {
			steps = append(steps, joined)
		}
		cur = cur[:0]
	}
	for _, line := range lines {
		if strings.TrimRight(line, " \t\r") == "---" {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return steps
}

// Format renders a Routine back to a STEPS.md document. Frontmatter is
// included only when at least one field is non-empty. Used by the editor
// UI for round-tripping.
func Format(r Routine) string {
	var b strings.Builder
	if hasFrontmatter(r.Frontmatter) {
		b.WriteString("---\n")
		if r.Frontmatter.Name != "" {
			fmt.Fprintf(&b, "name: %s\n", r.Frontmatter.Name)
		}
		if r.Frontmatter.Mode != "" {
			fmt.Fprintf(&b, "mode: %s\n", r.Frontmatter.Mode)
		}
		if r.Frontmatter.Log != "" {
			fmt.Fprintf(&b, "log: %s\n", r.Frontmatter.Log)
		}
		b.WriteString("---\n\n")
	}
	for i, step := range r.Steps {
		if i > 0 {
			b.WriteString("\n---\n\n")
		}
		b.WriteString(strings.TrimSpace(step))
		b.WriteString("\n")
	}
	return b.String()
}

func hasFrontmatter(f Frontmatter) bool {
	return f.Name != "" || f.Mode != "" || f.Log != ""
}
