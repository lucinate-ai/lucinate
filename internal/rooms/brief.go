package rooms

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// A brief is the project state a room's members would otherwise rediscover
// from scratch. It is generated once per room from the repo, written to the
// room's shared directory, and pointed at from the room's first message.
//
// The rendered Markdown is the contract: it must be stable — same repo state,
// same bytes, same digest — because the digest is what the first message
// quotes as the pointer's fingerprint.

const (
	// maxBriefBytes keeps the whole file inside one comfortable paste. A
	// brief larger than this stops being a summary.
	maxBriefBytes = 6000
	// section limits, applied before the byte budget.
	briefRulesMax     = 12
	briefReadmeLines  = 40
	briefReadmeBytes  = 1500
	briefTasksMax     = 15
	briefHandoffBytes = 1500
	// briefGitTimeout bounds all git probes together, not each one: the call
	// happens on the send path, so a broken repo must not stall the room.
	briefGitTimeout = 5 * time.Second
	// truncatedNote marks a section cut to fit the budget.
	truncatedNote = "... (truncated)\n"
)

// Brief is the shared project state written to <room>/brief.md.
type Brief struct {
	Project string // filepath.Base(repo root)
	Path    string // absolute repo root
	Branch  string
	Head    string   // short sha, 7-12 chars
	Dirty   int      // count of `git status --porcelain` lines
	Rules   []string // first 12 non-empty bullet lines of AGENTS.md, if present
	Readme  string   // first 40 lines of README.md / README, trimmed to 1500 bytes
	Tasks   []string // unchecked "- [ ]" items from openspec/changes/*/tasks.md, max 15
	Handoff string   // newest file under docs/handoff/, first 1500 bytes, if present
}

// BuildBrief reads the project state under repoPath. Missing files are normal,
// and a git failure is not fatal: a brief with only a README is still useful,
// so the git-derived fields fall back to empty values.
func BuildBrief(repoPath string) (Brief, error) {
	if strings.TrimSpace(repoPath) == "" {
		return Brief{}, fmt.Errorf("brief: repo path is required")
	}
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return Brief{}, fmt.Errorf("brief: repo path %s: %w", repoPath, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), briefGitTimeout)
	defer cancel()

	root := abs
	if top, ok := runGit(ctx, abs, "rev-parse", "--show-toplevel"); ok {
		if top = strings.TrimSpace(top); top != "" {
			if resolved, err := filepath.Abs(top); err == nil {
				root = resolved
			}
		}
	}

	b := Brief{
		Project: filepath.Base(root),
		Path:    root,
	}
	if branch, ok := runGit(ctx, root, "rev-parse", "--abbrev-ref", "HEAD"); ok {
		b.Branch = strings.TrimSpace(branch)
	}
	if head, ok := runGit(ctx, root, "rev-parse", "--short=12", "HEAD"); ok {
		b.Head = strings.TrimSpace(head)
		if len(b.Head) > 12 {
			b.Head = b.Head[:12]
		}
	}
	if status, ok := runGit(ctx, root, "status", "--porcelain"); ok {
		for _, line := range strings.Split(status, "\n") {
			if strings.TrimSpace(line) != "" {
				b.Dirty++
			}
		}
	}

	b.Rules = readRules(filepath.Join(root, "AGENTS.md"))
	b.Readme = readReadme(root)
	b.Tasks = readTasks(filepath.Join(root, "openspec", "changes"))
	b.Handoff = readHandoff(filepath.Join(root, "docs", "handoff"))
	return b, nil
}

// runGit runs one git command against repoPath. ok is false on any failure —
// no caller distinguishes which failure, because every git failure degrades
// to the same empty field.
func runGit(ctx context.Context, repoPath string, args ...string) (string, bool) {
	full := append([]string{"-C", repoPath}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// readRules keeps the first bullet lines, already trimmed. A rules file that
// is prose-only yields nothing rather than the prose.
func readRules(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range splitLines(string(data)) {
		t := strings.TrimSpace(line)
		if !isBullet(t) {
			continue
		}
		out = append(out, t)
		if len(out) == briefRulesMax {
			break
		}
	}
	return out
}

func isBullet(line string) bool {
	if len(line) < 2 {
		return false
	}
	switch line[0] {
	case '-', '*', '+':
		return line[1] == ' ' || line[1] == '\t'
	}
	return false
}

// readReadme prefers README.md, then README, then README.txt. Absence is not
// an error.
func readReadme(root string) string {
	for _, name := range []string{"README.md", "README", "README.txt"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		lines := splitLines(string(data))
		if len(lines) > briefReadmeLines {
			lines = lines[:briefReadmeLines]
		}
		return strings.TrimRight(truncateBytes(strings.Join(lines, "\n"), briefReadmeBytes), "\n ")
	}
	return ""
}

// readTasks collects unchecked items from openspec/changes/*/tasks.md, sorted
// by source path so the list does not depend on directory iteration order.
func readTasks(changesDir string) []string {
	files, err := filepath.Glob(filepath.Join(changesDir, "*", "tasks.md"))
	if err != nil || len(files) == 0 {
		return nil
	}
	sort.Strings(files)
	var out []string
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		for _, line := range splitLines(string(data)) {
			t := strings.TrimSpace(line)
			if !strings.HasPrefix(t, "- [ ]") {
				continue
			}
			item := strings.TrimSpace(strings.TrimPrefix(t, "- [ ]"))
			if item == "" {
				continue
			}
			out = append(out, item)
			if len(out) == briefTasksMax {
				return out
			}
		}
	}
	return out
}

// readHandoff picks the newest file under docs/handoff/ and keeps its first
// 1500 bytes. Ties on mtime break on name so the result is deterministic.
func readHandoff(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	type candidate struct {
		path    string
		modTime time.Time
	}
	var files []candidate
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, candidate{path: filepath.Join(dir, entry.Name()), modTime: info.ModTime()})
	}
	if len(files) == 0 {
		return ""
	}
	sort.Slice(files, func(i, j int) bool {
		if !files[i].modTime.Equal(files[j].modTime) {
			return files[i].modTime.After(files[j].modTime)
		}
		return files[i].path < files[j].path
	})
	data, err := os.ReadFile(files[0].path)
	if err != nil {
		return ""
	}
	return strings.TrimRight(truncateBytes(string(data), briefHandoffBytes), "\n ")
}

// Markdown renders the brief file. Every list and file read above is already
// ordered, and nothing here embeds a timestamp or a temp path, so equal repo
// state produces equal bytes.
func (b Brief) Markdown() string {
	var sb strings.Builder
	sb.WriteString("# Brief projektu\n\n")
	if b.Project != "" {
		fmt.Fprintf(&sb, "- projekt: %s\n", b.Project)
	}
	if b.Path != "" {
		fmt.Fprintf(&sb, "- ścieżka: %s\n", b.Path)
	}
	if b.Branch != "" {
		fmt.Fprintf(&sb, "- gałąź: %s\n", b.Branch)
	}
	if b.Head != "" {
		fmt.Fprintf(&sb, "- head: %s\n", b.Head)
	}
	fmt.Fprintf(&sb, "- brudne pliki: %d\n", b.Dirty)

	sb.WriteString("\n## Zasady pracy (pokój)\n")
	sb.WriteString("- przed pracą: sprawdź <roomdir>/work.jsonl, nie powtarzaj zadania, które ma już właściciela; nowe zadanie dopisz jako claim\n")
	sb.WriteString("- po zapisie pliku: w odpowiedzi podaj `HANDOFF <ścieżka> bytes=<rozmiar>`\n")
	sb.WriteString("- nie deklaruj zapisu bez odczytu zwrotnego\n")

	if len(b.Rules) > 0 {
		var body strings.Builder
		for _, rule := range b.Rules {
			body.WriteString(rule)
			body.WriteString("\n")
		}
		appendSection(&sb, "\n## Zasady (AGENTS.md)\n", body.String())
	}
	if strings.TrimSpace(b.Readme) != "" {
		appendSection(&sb, "\n## README\n", ensureTrailingNewline(b.Readme))
	}
	if len(b.Tasks) > 0 {
		var body strings.Builder
		for _, task := range b.Tasks {
			body.WriteString("- [ ] ")
			body.WriteString(task)
			body.WriteString("\n")
		}
		appendSection(&sb, "\n## Zadania (openspec)\n", body.String())
	}
	if strings.TrimSpace(b.Handoff) != "" {
		appendSection(&sb, "\n## Handoff\n", ensureTrailingNewline(b.Handoff))
	}
	return sb.String()
}

// Digest is the pointer's fingerprint: the first 12 hex chars of the
// Markdown's sha256. A member can re-run it to tell whether its brief is
// current.
func (b Brief) Digest() string {
	return digestMarkdown(b.Markdown())
}

func digestMarkdown(markdown string) string {
	sum := sha256.Sum256([]byte(markdown))
	return hex.EncodeToString(sum[:])[:12]
}

// WriteBrief writes <dir>/brief.md atomically and returns its path and digest.
func WriteBrief(dir string, b Brief) (path string, digest string, err error) {
	if strings.TrimSpace(dir) == "" {
		return "", "", fmt.Errorf("brief: directory is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("brief: directory %s: %w", dir, err)
	}
	markdown := b.Markdown()
	digest = digestMarkdown(markdown)
	path = filepath.Join(dir, "brief.md")

	tmp, err := os.CreateTemp(dir, ".brief-*.tmp")
	if err != nil {
		return "", "", fmt.Errorf("brief: temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(markdown); err != nil {
		tmp.Close()
		return "", "", fmt.Errorf("brief: write %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return "", "", fmt.Errorf("brief: chmod %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return "", "", fmt.Errorf("brief: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", "", fmt.Errorf("brief: rename to %s: %w", path, err)
	}
	return path, digest, nil
}

// appendSection adds header+body, cutting the body (on a line boundary when
// possible) so the file never exceeds maxBriefBytes.
func appendSection(sb *strings.Builder, header, body string) {
	section := header + body
	if sb.Len()+len(section) <= maxBriefBytes {
		sb.WriteString(section)
		return
	}
	remaining := maxBriefBytes - sb.Len() - len(truncatedNote)
	if remaining <= 0 {
		return
	}
	sb.WriteString(truncateAtLine(section, remaining))
	sb.WriteString(truncatedNote)
}

func truncateAtLine(s string, n int) string {
	if n >= len(s) {
		return s
	}
	cut := truncateBytes(s, n)
	if i := strings.LastIndexByte(cut, '\n'); i >= 0 {
		return cut[:i+1]
	}
	return cut
}

// truncateBytes cuts to at most n bytes without splitting a UTF-8 rune.
func truncateBytes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func ensureTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// splitLines normalises CRLF so a Windows checkout renders the same bytes as
// a Linux one.
func splitLines(s string) []string {
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}
