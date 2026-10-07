package rooms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── wspólny katalog pokoju ───────────────────────────────────────────────────

func TestRoomsBase_FollowsTheWorktreeThenFallsBackToCwd(t *testing.T) {
	t.Setenv("ORCA_WORKTREE_ID", "id:1c0a9030::E:/orca/workspaces/HermesHarness")
	base, err := RoomsBase()
	if err != nil {
		t.Fatalf("RoomsBase: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(base), "/HermesHarness") {
		t.Errorf("base = %q, want the worktree path from ORCA_WORKTREE_ID", base)
	}

	t.Setenv("ORCA_WORKTREE_ID", "")
	base, err = RoomsBase()
	if err != nil {
		t.Fatalf("RoomsBase without a worktree: %v", err)
	}
	wd, _ := os.Getwd()
	if filepath.Clean(base) != filepath.Clean(wd) {
		t.Errorf("base = %q, want the process working directory %q", base, wd)
	}
}

func TestRoomDir_LandsInsideTheRoomsDir(t *testing.T) {
	dir := RoomDir("E:/proj", "research-archivist-research-miner-research")
	want := filepath.Join("E:/proj", RoomsDirName, "research-archivist-research-miner-research")
	if filepath.ToSlash(dir) != filepath.ToSlash(want) {
		t.Errorf("RoomDir = %q, want %q", dir, want)
	}
}

// A room id is untrusted input: it comes from a name the user typed or from
// the gateway. It must never be able to point the shared directory outside
// <base>/.rooms/ — a separator or ".." in an id would silently hand the room
// somebody else's directory.
func TestRoomDir_CannotEscapeTheRoomsDir(t *testing.T) {
	cases := []struct{ id, wantLeaf string }{
		{"../../etc", "etc"},
		{"a/b/c", "a-b-c"},
		{"..", "room"},
		{"", "room"},
		{"  ...  ", "room"},
		{"pokój: roboczy", "pokój-roboczy"},
		{"C:\\Users\\x", "C-Users-x"},
	}
	for _, tc := range cases {
		dir := RoomDir("E:/proj", tc.id)
		rel, err := filepath.Rel(filepath.Join("E:/proj", RoomsDirName), dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Errorf("RoomDir(%q) = %q escaped the rooms dir (rel %q, err %v)", tc.id, dir, rel, err)
			continue
		}
		if leaf := filepath.Base(dir); leaf != tc.wantLeaf {
			t.Errorf("RoomDir(%q) leaf = %q, want %q", tc.id, leaf, tc.wantLeaf)
		}
	}
}

func TestEnsureRoomDir_CreatesItAndStaysIdempotent(t *testing.T) {
	base := t.TempDir()
	dir, err := EnsureRoomDir(base, "probe-handoff")
	if err != nil {
		t.Fatalf("EnsureRoomDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("room directory not created: %v", err)
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("EnsureRoomDir returned a relative path %q — members need an absolute one", dir)
	}
	// Room start happens more than once in practice (re-create, re-seat),
	// and the directory is shared: it must never be an error to find it there.
	again, err := EnsureRoomDir(base, "probe-handoff")
	if err != nil || again != dir {
		t.Errorf("second EnsureRoomDir = %q, %v; want the same path, nil error", again, err)
	}
}

func TestEnsureRoomDir_RefusesToGuess(t *testing.T) {
	if _, err := EnsureRoomDir("", "r1"); err == nil {
		t.Error("empty base should be an error")
	}
	if _, err := EnsureRoomDir(t.TempDir(), ""); err == nil {
		t.Error("empty room id should be an error")
	}
}

// The directory is created when the room starts — before any project
// context exists — so this is the path the room's creation code takes.
func TestEnsureDefaultRoomDir_FollowsTheWorktreeThenTheWorkingDirectory(t *testing.T) {
	wt := t.TempDir()
	t.Setenv("ORCA_WORKTREE_ID", "id:probe::"+wt)
	dir, err := EnsureDefaultRoomDir("sztab")
	if err != nil {
		t.Fatalf("EnsureDefaultRoomDir: %v", err)
	}
	if want := filepath.Join(wt, RoomsDirName, "sztab"); filepath.Clean(dir) != filepath.Clean(want) {
		t.Errorf("dir = %q, want %q", dir, want)
	}

	t.Setenv("ORCA_WORKTREE_ID", "")
	cwd := t.TempDir()
	t.Chdir(cwd)
	dir, err = EnsureDefaultRoomDir("sztab")
	if err != nil {
		t.Fatalf("EnsureDefaultRoomDir without a worktree: %v", err)
	}
	if want := filepath.Join(cwd, RoomsDirName, "sztab"); filepath.Clean(dir) != filepath.Clean(want) {
		t.Errorf("dir = %q, want the working directory %q", dir, want)
	}
}

// ── kontrakt handoff ─────────────────────────────────────────────────────────

func TestHandoffBlock_CarriesTheDirAndEveryRule(t *testing.T) {
	block := HandoffBlock(`E:\proj\.rooms\sztab`)
	for _, want := range []string{
		`E:\proj\.rooms\sztab`,
		HandoffReceiptPrefix, // the writer's read-back confirmation
		"bytes=",             // size in bytes, quoted from the read-back
		"fail-closed",        // reader never guesses when the file is missing
	} {
		if !strings.Contains(block, want) {
			t.Errorf("handoff contract missing %q:\n%s", want, block)
		}
	}
	// No directory, no contract — see TestHandoffBlock_EmptyDirRendersNothing.
}

func TestHandoffBlock_EmptyDirRendersNothing(t *testing.T) {
	if strings.TrimSpace(HandoffBlock("")) != "" {
		t.Errorf("HandoffBlock(\"\") = %q, want empty", HandoffBlock(""))
	}
}

// The room directory is the one thing every member must agree on, so it is
// carried even by an orientation that has no project context at all.
func TestOrientation_RoomDirAloneStillRendersTheContract(t *testing.T) {
	o := Orientation{RoomDir: `E:\proj\.rooms\sztab`}
	if o.Empty() {
		t.Fatal("an orientation carrying the room directory is not empty")
	}
	block := o.Block()
	for _, want := range []string{OrientationMarker, `E:\proj\.rooms\sztab`, HandoffReceiptPrefix} {
		if !strings.Contains(block, want) {
			t.Errorf("block missing %q:\n%s", want, block)
		}
	}
	// …and without a worktree there is no Orca browser flow to promise.
	if strings.Contains(block, "orca_tab_create") {
		t.Errorf("no project context, yet the block promises Orca browser tools:\n%s", block)
	}
}

func TestOrientationWithRoomDir_ResolvesUnderTheProjectPath(t *testing.T) {
	base := t.TempDir()
	o, err := Orientation{Path: base, Project: "proj"}.WithRoomDir("sztab")
	if err != nil {
		t.Fatalf("WithRoomDir: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(o.RoomDir), "/.rooms/sztab") {
		t.Errorf("RoomDir = %q, want <path>/.rooms/sztab", o.RoomDir)
	}
	if _, err := os.Stat(o.RoomDir); err != nil {
		t.Errorf("WithRoomDir did not create the directory: %v", err)
	}
}

func TestOrientationWithRoomDir_FailsRatherThanAnnouncesNothing(t *testing.T) {
	// A base that cannot hold a directory (a plain file) means the members
	// would be told about a path that cannot exist: that is an error, not a
	// silently dropped contract.
	base := t.TempDir()
	blocker := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (Orientation{Path: blocker}).WithRoomDir("sztab"); err == nil {
		t.Error("uncreatable room directory reported no error")
	}
}

// ── potwierdzenie zapisu (HANDOFF … bytes=N) ─────────────────────────────────

func TestParseHandoffReceipts(t *testing.T) {
	text := "zrobione.\n" +
		"HANDOFF extracts/notatka.md bytes=1234\n" +
		"inhelm nie ma tu nic.\n" +
		"handoff E:/proj/.rooms/sztab/extracts/a.txt bytes = 42\n"
	got := ParseHandoffReceipts(text)
	if len(got) != 2 {
		t.Fatalf("receipts = %d (%+v), want 2", len(got), got)
	}
	if got[0].Path != "extracts/notatka.md" || got[0].Bytes != 1234 {
		t.Errorf("receipt[0] = %+v", got[0])
	}
	if got[1].Bytes != 42 {
		t.Errorf("receipt[1].Bytes = %d, want 42 (spaces around '=' are allowed)", got[1].Bytes)
	}
	if len(ParseHandoffReceipts("zapisane, gotowe")) != 0 {
		t.Error("a claim without a receipt must parse as no receipt at all")
	}
	if len(ParseHandoffReceipts("HANDOFF extracts/x.md bytes=abc")) != 0 {
		t.Error("a non-numeric size is not a receipt")
	}
}

func writeRoomFile(t *testing.T, dir, rel, body string) string {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

func TestVerifyHandoffs_AcceptsAReadBackThatMatches(t *testing.T) {
	dir := t.TempDir()
	full := writeRoomFile(t, dir, "extracts/notatka.md", "helłlo")
	size := int64(len([]byte("helłlo")))
	if err := VerifyHandoffs(dir, []HandoffReceipt{{Path: "extracts/notatka.md", Bytes: size}}); err != nil {
		t.Fatalf("VerifyHandoffs: %v", err)
	}
	// An absolute path inside the room directory is the same file.
	if err := VerifyHandoffs(dir, []HandoffReceipt{{Path: full, Bytes: size}}); err != nil {
		t.Fatalf("VerifyHandoffs with an absolute path inside the room: %v", err)
	}
}

func TestVerifyHandoffs_MissingFileFailsClosed(t *testing.T) {
	dir := t.TempDir()
	err := VerifyHandoffs(dir, []HandoffReceipt{{Path: "extracts/niema.md", Bytes: 10}})
	if err == nil {
		t.Fatal("a declared write with no file behind it must fail closed")
	}
	if !strings.Contains(err.Error(), "extracts/niema.md") {
		t.Errorf("error should name the missing file, got: %v", err)
	}
}

func TestVerifyHandoffs_SizeMustMatchTheReadBack(t *testing.T) {
	dir := t.TempDir()
	writeRoomFile(t, dir, "extracts/x.md", "12345")
	if err := VerifyHandoffs(dir, []HandoffReceipt{{Path: "extracts/x.md", Bytes: 99}}); err == nil {
		t.Fatal("a size that does not match the file on disk must fail")
	}
}

func TestVerifyHandoffReport_AReplyWithoutAReadBackIsAnError(t *testing.T) {
	dir := t.TempDir()
	writeRoomFile(t, dir, "extracts/x.md", "12345")
	// The exact failure the contract exists for: "zapisane" with nothing
	// behind it, and a receipt whose size does not match the file.
	if err := VerifyHandoffReport(dir, "zapisane, gotowe"); err == nil {
		t.Error("a reply with no receipt must fail")
	}
	if err := VerifyHandoffReport(dir, "zapisane\nHANDOFF extracts/x.md bytes=5"); err != nil {
		t.Errorf("a matching receipt should pass: %v", err)
	}
	if err := VerifyHandoffReport(dir, "HANDOFF extracts/niema.md bytes=5"); err == nil {
		t.Error("a receipt for a missing file must fail closed")
	}
}

func TestVerifyHandoffs_RejectsAnythingOutsideTheRoomDir(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(filepath.Dir(dir), "not-in-the-room.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })
	if err := VerifyHandoffs(dir, []HandoffReceipt{{Path: "../not-in-the-room.txt", Bytes: 6}}); err == nil {
		t.Fatal("a receipt pointing outside the room directory must be refused")
	}
}
