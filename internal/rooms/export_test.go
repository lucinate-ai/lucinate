package rooms

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucinate-ai/lucinate/internal/config"
)

func exportOpts(roomID, roomName string, members []Member, generated time.Time) ExportOptions {
	return ExportOptions{
		RoomID:    roomID,
		RoomName:  roomName,
		Members:   members,
		Generated: generated,
	}
}

func mp(memberID, profile, handle string) Member {
	return Member{MemberID: memberID, Profile: profile, Handle: handle}
}

// empty events
func TestExportMarkdown_EmptyTranscript(t *testing.T) {
	md := ExportMarkdown(nil, exportOpts("r1", "Sztab", nil, time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)))
	wantPrefix := []string{
		"# Sztab",
		"Room ID: r1",
		"Generated: 2024-01-02T03:04:05Z",
		"## Roster",
		"## Transcript",
	}
	for _, want := range wantPrefix {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestExportJSON_EmptyTranscript(t *testing.T) {
	raw, err := ExportJSON(nil, exportOpts("r1", "Sztab", nil, time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		RoomID      string   `json:"room_id"`
		RoomName    string   `json:"room_name"`
		GeneratedAt string   `json:"generated_at"`
		Members     []Member `json:"members"`
		Events      []Event  `json:"events"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.RoomID != "r1" || doc.RoomName != "Sztab" || doc.GeneratedAt != "2024-01-02T03:04:05Z" {
		t.Fatalf("doc = %+v", doc)
	}
	if doc.Members != nil || doc.Events != nil {
		t.Fatalf("expected nil members/events, got %+v", doc)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Fatal("JSON must end with a newline")
	}
}

func TestParseExportFormat_Normalises(t *testing.T) {
	cases := map[string]string{
		"":         "md",
		"md":       "md",
		"markdown": "md",
		"MD":       "md",
		"json":     "json",
		"both":     "both",
	}
	for in, want := range cases {
		got, err := ParseExportFormat(in)
		if err != nil {
			t.Fatalf("ParseExportFormat(%q) error: %v", in, err)
		}
		if got != want {
			t.Fatalf("ParseExportFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseExportFormat_Rejects(t *testing.T) {
	got, err := ParseExportFormat("txt")
	if err == nil {
		t.Fatalf("ParseExportFormat(txt) = %q, want error", got)
	}
	if !strings.Contains(err.Error(), "txt") {
		t.Fatalf("error = %q, want it to mention txt", err)
	}
}

// full transcript
func TestExportMarkdown_FullTranscript(t *testing.T) {
	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	events := []Event{
		{
			RoomID:    "r1",
			Seq:       1,
			EventID:   "e1",
			Kind:      "message.user",
			Actor:     Actor{Kind: "user", ID: "desktop"},
			Payload:   map[string]any{"text": "hej"},
			CreatedAt: 1704160000,
		},
		{
			RoomID:    "r1",
			Seq:       2,
			EventID:   "e2",
			Kind:      "message.member",
			Actor:     Actor{Kind: "member", ID: "m1", Profile: "matt", DisplayName: "Matt"},
			Payload:   map[string]any{"text": "oraz @matt", "member_id": "m1"},
			CreatedAt: 1704160060,
		},
		{
			RoomID:    "r1",
			Seq:       3,
			EventID:   "e3",
			Kind:      "message.member",
			Actor:     Actor{Kind: "member", ID: "m2", Profile: "kowal"},
			Payload:   map[string]any{"text": "a|b\\c"},
			CreatedAt: 1704160120,
		},
		{
			RoomID:    "r1",
			Seq:       4,
			EventID:   "e4",
			Kind:      "message.member",
			Actor:     Actor{Kind: "member", ID: "m1", Profile: "matt"},
			Payload:   map[string]any{"text": "pierwsza\ndruga\ntrzecia"},
			CreatedAt: 1704160180,
		},
		{
			RoomID:    "r1",
			Seq:       5,
			EventID:   "e5",
			Kind:      "turn.started",
			Actor:     Actor{Kind: "member", ID: "m1", Profile: "matt", DisplayName: "Matt"},
			Payload:   map[string]any{"member_index": 0},
			CreatedAt: 1704160240,
		},
		{
			RoomID:    "r1",
			Seq:       6,
			EventID:   "e6",
			Kind:      "turn.settled",
			Actor:     Actor{Kind: "member", ID: "m1", Profile: "matt", DisplayName: "Matt"},
			Payload:   map[string]any{"member_index": 0, "passed": true},
			CreatedAt: 1704160300,
		},
		{
			RoomID:    "r1",
			Seq:       7,
			EventID:   "e7",
			Kind:      "turn.settled",
			Actor:     Actor{Kind: "member", ID: "m2", Profile: "kowal"},
			Payload:   map[string]any{"member_index": 1, "passed": false},
			CreatedAt: 1704160360,
		},
		{
			RoomID:    "r1",
			Seq:       8,
			EventID:   "e8",
			Kind:      "turn.failed",
			Actor:     Actor{Kind: "member", ID: "m2", Profile: "kowal"},
			Payload:   map[string]any{"member_index": 1, "error": "timeout"},
			CreatedAt: 1704160420,
		},
		{
			RoomID:    "r1",
			Seq:       9,
			EventID:   "e9",
			Kind:      "turn.deferred",
			Actor:     Actor{Kind: "member", ID: "m1", Profile: "matt"},
			Payload:   map[string]any{"member_index": 0, "reason": "waiting on dependency"},
			CreatedAt: 1704160480,
		},
		{
			RoomID:    "r1",
			Seq:       10,
			EventID:   "e10",
			Kind:      "room.activity",
			Actor:     Actor{Kind: "gateway", ID: "install:abc"},
			Payload:   map[string]any{"status": "driver restarted"},
			CreatedAt: 1704160540,
		},
		{
			RoomID:    "r1",
			Seq:       11,
			EventID:   "e11",
			Kind:      "member.unavailable",
			Actor:     Actor{Kind: "member", ID: "m2", Profile: "kowal"},
			Payload:   map[string]any{"member_id": "m2"},
			CreatedAt: 1704160600,
		},
		{
			RoomID:    "r1",
			Seq:       12,
			EventID:   "e12",
			Kind:      "message.user",
			Actor:     Actor{Kind: "user", ID: "desktop"},
			Payload:   map[string]any{},
			CreatedAt: 1704160660,
		},
	}
	opts := exportOpts("r1", "Sztab", []Member{mp("m1", "matt", "matt"), mp("m2", "kowal", "kowal")}, now)
	md := ExportMarkdown(events, opts)

	if !strings.Contains(md, "# Sztab") {
		t.Fatalf("missing title:\n%s", md)
	}
	if !strings.Contains(md, "Room ID: r1") {
		t.Fatalf("missing room id:\n%s", md)
	}
	if !strings.Contains(md, "Generated: 2024-01-02T03:04:05Z") {
		t.Fatalf("missing generated:\n%s", md)
	}

	if !strings.Contains(md, "@matt — matt") {
		t.Fatalf("missing roster row for matt:\n%s", md)
	}
	if !strings.Contains(md, "@kowal — kowal") {
		t.Fatalf("missing roster row for kowal:\n%s", md)
	}

	// message.user → "you". Times render in UTC: an exported document is read
	// on machines in other zones, so a bare local time would be ambiguous.
	stamp := time.Unix(1704160000, 0).UTC().Format("15:04:05")
	if !strings.Contains(md, "- **"+stamp+"** you: hej") {
		t.Fatalf("missing you line (want %s):\n%s", stamp, md)
	}
	// message.member → "@Matt: ..."
	if !strings.Contains(md, "@Matt: oraz @matt") {
		t.Fatalf("missing @handle line:\n%s", md)
	}
	// escape pipe + backslash
	if strings.Contains(md, "a|b\\c") {
		t.Fatalf("pipe and backslash not escaped:\n%s", md)
	}
	if !strings.Contains(md, "a\\|b\\\\c") {
		t.Fatalf("expected escaped pipe+backslash:\n%s", md)
	}
	// multiline indent
	if !strings.Contains(md, "pierwsza\n  druga\n  trzecia") {
		t.Fatalf("multiline not indented:\n%s", md)
	}
	// turn.started bold
	if !strings.Contains(md, "**Matt is thinking…**") {
		t.Fatalf("missing turn.started:\n%s", md)
	}
	// turn.settled passed
	if !strings.Contains(md, "**Matt passed**") {
		t.Fatalf("missing turn.settled passed:\n%s", md)
	}
	// turn.settled finished
	if !strings.Contains(md, "**kowal finished**") {
		t.Fatalf("missing turn.settled finished:\n%s", md)
	}
	// turn.failed
	if !strings.Contains(md, "**kowal failed: timeout**") {
		t.Fatalf("missing turn.failed:\n%s", md)
	}
	// turn.deferred
	if !strings.Contains(md, "**matt deferred: waiting on dependency**") {
		t.Fatalf("missing turn.deferred:\n%s", md)
	}
	// room.activity italic
	if !strings.Contains(md, "_room.activity gateway_") {
		t.Fatalf("missing room.activity:\n%s", md)
	}
	// member.unavailable italic
	if !strings.Contains(md, "_member.unavailable kowal_") {
		t.Fatalf("missing member.unavailable:\n%s", md)
	}
	// empty text
	if !strings.Contains(md, "_(empty)_") {
		t.Fatalf("missing empty-text marker:\n%s", md)
	}
}

func TestExportMarkdown_Deterministic(t *testing.T) {
	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	events := []Event{{
		RoomID:    "r1",
		Seq:       1,
		EventID:   "e1",
		Kind:      "message.user",
		Actor:     Actor{Kind: "user", ID: "desktop"},
		Payload:   map[string]any{"text": "hej"},
		CreatedAt: 1704160000,
	}}
	opts := exportOpts("r1", "Sztab", []Member{mp("m1", "matt", "matt")}, now)
	a := ExportMarkdown(events, opts)
	b := ExportMarkdown(events, opts)
	if a != b {
		t.Fatalf("markdown not deterministic:\na---\n%s\nb---\n%s", a, b)
	}
}

func TestExportJSON_Fields(t *testing.T) {
	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	events := []Event{{
		RoomID:    "r1",
		Seq:       7,
		EventID:   "e7",
		Kind:      "message.user",
		Actor:     Actor{Kind: "user", ID: "desktop", Profile: "matt"},
		Payload:   map[string]any{"text": "hej"},
		CreatedAt: 1704160000,
	}}
	opts := exportOpts("r1", "Sztab", []Member{mp("m1", "matt", "matt")}, now)
	raw, err := ExportJSON(events, opts)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		RoomID      string `json:"room_id"`
		RoomName    string `json:"room_name"`
		GeneratedAt string `json:"generated_at"`
		Members     []struct {
			MemberID    string `json:"member_id"`
			Profile     string `json:"profile"`
			Handle      string `json:"handle"`
			DisplayName string `json:"display_name,omitempty"`
		} `json:"members"`
		Events []struct {
			Seq       int            `json:"seq"`
			EventID   string         `json:"event_id"`
			Kind      string         `json:"kind"`
			Actor     Actor          `json:"actor"`
			Payload   map[string]any `json:"payload"`
			CreatedAt float64        `json:"created_at"`
		} `json:"events"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.RoomID != "r1" || doc.RoomName != "Sztab" || doc.GeneratedAt != "2024-01-02T03:04:05Z" {
		t.Fatalf("doc header = %+v", doc)
	}
	if len(doc.Members) != 1 || doc.Members[0].Profile != "matt" || doc.Members[0].Handle != "matt" {
		t.Fatalf("doc.members = %+v", doc.Members)
	}
	if len(doc.Events) != 1 || doc.Events[0].Seq != 7 || doc.Events[0].Kind != "message.user" {
		t.Fatalf("doc.events = %+v", doc.Events)
	}
	if doc.Events[0].Payload["text"] != "hej" {
		t.Fatalf("event payload = %+v", doc.Events[0].Payload)
	}
}

// WriteExport
func TestWriteExport_MD(t *testing.T) {
	dir := t.TempDir()
	config.SetDataDir(dir)
	t.Cleanup(func() { config.SetDataDir("") })

	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	events := []Event{{
		RoomID:    "r1",
		Seq:       1,
		EventID:   "e1",
		Kind:      "message.user",
		Actor:     Actor{Kind: "user", ID: "desktop"},
		Payload:   map[string]any{"text": "hej"},
		CreatedAt: 1704160000,
	}}
	opts := exportOpts("r1", "Sztab", []Member{mp("m1", "matt", "matt")}, now)
	paths, err := WriteExport("Sztab Orca", events, opts, "md")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("paths = %d, want 1", len(paths))
	}
	p := paths[0]
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	requirePosixFileModes(t)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o, want 0o600", info.Mode().Perm())
	}
	content, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "# Sztab") {
		t.Fatalf("file content missing title:\n%s", content)
	}
	// ExportDir must be <datadir>/exports
	exportsDir, err := ExportDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(p) != exportsDir {
		t.Fatalf("file dir %s != exports dir %s", filepath.Dir(p), exportsDir)
	}
}

func TestWriteExport_JSON(t *testing.T) {
	dir := t.TempDir()
	config.SetDataDir(dir)
	t.Cleanup(func() { config.SetDataDir("") })

	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	events := []Event{{
		RoomID:    "r1",
		Seq:       1,
		EventID:   "e1",
		Kind:      "message.user",
		Actor:     Actor{Kind: "user", ID: "desktop"},
		Payload:   map[string]any{"text": "hej"},
		CreatedAt: 1704160000,
	}}
	opts := exportOpts("r1", "Sztab", []Member{mp("m1", "matt", "matt")}, now)
	paths, err := WriteExport("Sztab Orca", events, opts, "json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("paths = %d, want 1", len(paths))
	}
	content, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		RoomID string `json:"room_id"`
	}
	if err := json.Unmarshal(content, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.RoomID != "r1" {
		t.Fatalf("json room_id = %q", doc.RoomID)
	}
}

func TestWriteExport_Both(t *testing.T) {
	dir := t.TempDir()
	config.SetDataDir(dir)
	t.Cleanup(func() { config.SetDataDir("") })

	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	events := []Event{{
		RoomID:    "r1",
		Seq:       1,
		EventID:   "e1",
		Kind:      "message.user",
		Actor:     Actor{Kind: "user", ID: "desktop"},
		Payload:   map[string]any{"text": "hej"},
		CreatedAt: 1704160000,
	}}
	opts := exportOpts("r1", "Sztab", []Member{mp("m1", "matt", "matt")}, now)
	paths, err := WriteExport("Sztab Orca", events, opts, "both")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %d, want 2", len(paths))
	}
	mdContent, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mdContent), "# Sztab") {
		t.Fatalf("md file missing title:\n%s", mdContent)
	}
	jsonContent, err := os.ReadFile(paths[1])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(jsonContent), `"room_id":"r1"`) {
		t.Fatalf("json file missing room_id:\n%s", jsonContent)
	}
	// both files should have 0o600
	requirePosixFileModes(t)
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("file %s mode = %o, want 0o600", p, info.Mode().Perm())
		}
	}
}

func TestWriteExport_BadFormat(t *testing.T) {
	_, err := WriteExport("r1", nil, exportOpts("r1", "Sztab", nil, time.Now()), "txt")
	if err == nil {
		t.Fatal("WriteExport(txt) = nil error, want bad format error")
	}
}
