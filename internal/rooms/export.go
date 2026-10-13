package rooms

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// Transcript export.
//
// A room lives on the gateway and is shared by every client, so the only way
// to hand a transcript to someone else — a colleague, a ticket, an archive —
// is to write it out. Markdown is for a human reader; JSON is for a script
// that wants the events themselves.

// ExportOptions is the room metadata an export carries. The events alone do
// not say which room or roster they belong to once the file has left lucinate.
type ExportOptions struct {
	RoomID    string
	RoomName  string
	Members   []Member
	Generated time.Time
}

// ExportMarkdown renders the transcript as a markdown document.
//
// Times are UTC, not local: an exported document is read on machines in other
// time zones, and a bare local time would be ambiguous.
func ExportMarkdown(events []Event, opts ExportOptions) string {
	generated := opts.Generated.UTC().Format(time.RFC3339)
	title := strings.TrimSpace(opts.RoomName)
	if title == "" {
		title = strings.TrimSpace(opts.RoomID)
	}
	if title == "" {
		title = "Room"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	if opts.RoomID != "" {
		fmt.Fprintf(&b, "Room ID: %s\n", opts.RoomID)
	}
	fmt.Fprintf(&b, "Generated: %s\n", generated)

	b.WriteString("\n## Roster\n")
	if len(opts.Members) == 0 {
		b.WriteString("_no members_\n")
	}
	for _, m := range opts.Members {
		handle := m.Handle
		if handle == "" {
			handle = HandleFor(m.Profile)
		}
		fmt.Fprintf(&b, "- @%s — %s\n", handle, m.Profile)
	}

	b.WriteString("\n## Transcript\n")
	if len(events) == 0 {
		b.WriteString("_no events_\n")
		return b.String()
	}
	for _, ev := range events {
		writeMarkdownEvent(&b, ev)
	}
	return b.String()
}

// writeMarkdownEvent renders one transcript row.
//
// Message text is escaped (a `|` would start a table cell and a backslash
// would escape the next character) and continuation lines are indented so a
// multi-line message stays one markdown list item instead of splitting the
// list.
func writeMarkdownEvent(b *strings.Builder, ev Event) {
	stamp := time.Unix(int64(ev.CreatedAt), 0).UTC().Format("15:04:05")
	label := exportLabel(ev)
	body := escapeExportText(ev.Text())

	switch ev.Kind {
	case "message.user":
		fmt.Fprintf(b, "- **%s** you: %s\n", stamp, indentContinuation(body))
	case "message.member":
		fmt.Fprintf(b, "- **%s** @%s: %s\n", stamp, ev.Speaker(), indentContinuation(body))
	case "turn.started":
		fmt.Fprintf(b, "- **%s** **%s is thinking…**\n", stamp, label)
	case "turn.settled":
		if passed, _ := ev.Payload["passed"].(bool); passed {
			fmt.Fprintf(b, "- **%s** **%s passed**\n", stamp, label)
		} else {
			fmt.Fprintf(b, "- **%s** **%s finished**\n", stamp, label)
		}
	case "turn.failed":
		fmt.Fprintf(b, "- **%s** **%s failed: %v**\n", stamp, label, ev.Payload["error"])
	case "turn.deferred":
		fmt.Fprintf(b, "- **%s** **%s deferred: %v**\n", stamp, label, ev.Payload["reason"])
	case "room.activity":
		fmt.Fprintf(b, "- _room.activity %s_\n", label)
	case "member.unavailable":
		fmt.Fprintf(b, "- _member.unavailable %s_\n", label)
	default:
		fmt.Fprintf(b, "- _%s %s_\n", ev.Kind, label)
	}
}

// exportLabel names the actor of a non-message event. DisplayName is preferred
// so a transcript reads with the names the user saw on screen, the profile is
// the stable identity behind it, and the actor kind is the last resort for
// gateway-side events that have neither.
func exportLabel(ev Event) string {
	if ev.Actor.DisplayName != "" {
		return ev.Actor.DisplayName
	}
	if ev.Actor.Profile != "" {
		return ev.Actor.Profile
	}
	if ev.Actor.Kind != "" {
		return ev.Actor.Kind
	}
	return "gateway"
}

// escapeExportText neutralises the two markdown characters that would change
// the shape of the document rather than its meaning.
func escapeExportText(s string) string {
	if s == "" {
		return "_(empty)_"
	}
	s = strings.ReplaceAll(s, "\\", "\\\\")
	return strings.ReplaceAll(s, "|", "\\|")
}

// indentContinuation indents every line but the first, so a multi-line message
// remains a single markdown list item.
func indentContinuation(s string) string {
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = "  " + lines[i]
	}
	return strings.Join(lines, "\n")
}

// exportDocument is the JSON shape. Members and Events are left nil when there
// is nothing to export, so a consumer can tell "no members" from "empty list"
// with a plain nil check.
type exportDocument struct {
	RoomID      string   `json:"room_id"`
	RoomName    string   `json:"room_name"`
	GeneratedAt string   `json:"generated_at"`
	Members     []Member `json:"members"`
	Events      []Event  `json:"events"`
}

// ExportJSON renders the transcript as a JSON document.
func ExportJSON(events []Event, opts ExportOptions) ([]byte, error) {
	doc := exportDocument{
		RoomID:      opts.RoomID,
		RoomName:    opts.RoomName,
		GeneratedAt: opts.Generated.UTC().Format(time.RFC3339),
	}
	if len(opts.Members) > 0 {
		doc.Members = opts.Members
	}
	if len(events) > 0 {
		doc.Events = events
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// ParseExportFormat normalises the format argument: an empty argument means
// markdown, which is what a human wants when they do not say.
func ParseExportFormat(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "md", "markdown":
		return "md", nil
	case "json":
		return "json", nil
	case "both", "all":
		return "both", nil
	default:
		return "", fmt.Errorf("unknown export format %q — use md, json or both", strings.TrimSpace(s))
	}
}

// ExportDir is <lucinate data dir>/exports, created 0700.
func ExportDir() (string, error) {
	dir, err := config.DataDir()
	if err != nil {
		return "", err
	}
	out := filepath.Join(dir, "exports")
	if err := os.MkdirAll(out, 0o700); err != nil {
		return "", err
	}
	return out, nil
}

// ExportPath names one transcript file: <room>-<date>-<time>.<ext>, so two
// exports of the same room never overwrite each other.
func ExportPath(roomID, format string, now time.Time) (string, error) {
	dir, err := ExportDir()
	if err != nil {
		return "", err
	}
	ext := "md"
	if format == "json" {
		ext = "json"
	}
	name := fmt.Sprintf("%s-%s.%s", Slug(roomID), now.UTC().Format("20060102-150405"), ext)
	return filepath.Join(dir, name), nil
}

// WriteExport writes the transcript in the requested format and returns the
// paths it wrote, in the order md, json.
func WriteExport(roomID string, events []Event, opts ExportOptions, format string) ([]string, error) {
	parsed, err := ParseExportFormat(format)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(opts.RoomID) == "" {
		opts.RoomID = roomID
	}
	if strings.TrimSpace(opts.RoomName) == "" {
		opts.RoomName = roomID
	}
	// One timestamp for the whole export, so the markdown's header, the JSON's
	// generated_at and both file names agree.
	now := time.Now()

	var paths []string
	write := func(ext string, data []byte) error {
		path, err := ExportPath(roomID, ext, now)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return err
		}
		paths = append(paths, path)
		return nil
	}

	if parsed == "md" || parsed == "both" {
		if err := write("md", []byte(ExportMarkdown(events, opts))); err != nil {
			return nil, err
		}
	}
	if parsed == "json" || parsed == "both" {
		raw, err := ExportJSON(events, opts)
		if err != nil {
			return nil, err
		}
		if err := write("json", raw); err != nil {
			return nil, err
		}
	}
	return paths, nil
}
