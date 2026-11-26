package rooms

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lucinate-ai/lucinate/internal/config"
)

func TestPrefsStore_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rooms-prefs.json")
	store := PrefsStore{}
	store.Set("sztab", RoomPrefs{
		Mode:            string(RouteRoundRobin),
		Moderator:       "matt",
		RoundRobinIndex: 2,
		Colours:         map[string]string{"matt": "#FF0000"},
		CompactKeep:     4,
	})
	if err := SavePrefs(path, store); err != nil {
		t.Fatalf("SavePrefs: %v", err)
	}

	got, err := LoadPrefs(path)
	if err != nil {
		t.Fatalf("LoadPrefs: %v", err)
	}
	p := got.Get("sztab")
	if p.Mode != string(RouteRoundRobin) || p.Moderator != "matt" || p.RoundRobinIndex != 2 {
		t.Errorf("prefs = %+v, want the round-robin cursor and moderator preserved", p)
	}
	if p.Colours["matt"] != "#FF0000" || p.CompactKeep != 4 {
		t.Errorf("prefs = %+v, want colours and keep window preserved", p)
	}
}

func TestLoadPrefs_MissingOrCorruptFileIsAnEmptyStore(t *testing.T) {
	dir := t.TempDir()

	if got, err := LoadPrefs(filepath.Join(dir, "nope.json")); err != nil || len(got.Rooms) != 0 {
		t.Errorf("missing file: store=%+v err=%v, want empty and no error", got, err)
	}

	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadPrefs(broken); err != nil || len(got.Rooms) != 0 {
		t.Errorf("corrupt file: store=%+v err=%v, want empty and no error", got, err)
	}
}

func TestSavePrefs_UsesRestrictivePermissions(t *testing.T) {
	requirePosixFileModes(t)
	dir := filepath.Join(t.TempDir(), "nested")
	path := filepath.Join(dir, "rooms-prefs.json")
	store := PrefsStore{}
	store.Set("sztab", RoomPrefs{Mode: string(RouteModerator)})
	if err := SavePrefs(path, store); err != nil {
		t.Fatalf("SavePrefs: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 600", perm)
	}
}

func TestPrefsStore_UpdateMutatesInPlace(t *testing.T) {
	store := PrefsStore{}
	store.Update("sztab", func(p *RoomPrefs) {
		p.Mode = string(RouteRoundRobin)
		p.RoundRobinIndex = 3
	})
	if got := store.Get("sztab"); got.Mode != string(RouteRoundRobin) || got.RoundRobinIndex != 3 {
		t.Fatalf("prefs = %+v, want the mutation applied", got)
	}
	// A nil-map store must not panic on the first write.
	store.Update("nowy", func(p *RoomPrefs) { p.Colours = map[string]string{"matt": "#00FF00"} })
	if got := store.Get("nowy").Colours["matt"]; got != "#00FF00" {
		t.Errorf("colours = %v, want the new entry", store.Get("nowy").Colours)
	}
}

func TestRoomPrefs_KeepLastDefaultsAndBounds(t *testing.T) {
	tests := []struct {
		in   int
		want int
	}{
		{in: 0, want: DefaultCompactKeep},
		{in: -3, want: DefaultCompactKeep},
		{in: 2, want: 2},
		{in: 500, want: MaxCompactKeep},
	}
	for _, tc := range tests {
		if got := (RoomPrefs{CompactKeep: tc.in}).KeepLast(); got != tc.want {
			t.Errorf("KeepLast(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestRoomPrefs_RoutingModeFallsBackToBroadcast(t *testing.T) {
	tests := []struct {
		in   string
		want RoutingMode
	}{
		{in: "", want: RouteBroadcast},
		{in: "moderator", want: RouteModerator},
		{in: "round-robin", want: RouteRoundRobin},
		{in: "dyktator", want: RouteBroadcast},
	}
	for _, tc := range tests {
		if got := (RoomPrefs{Mode: tc.in}).RoutingMode(); got != tc.want {
			t.Errorf("RoutingMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDefaultPrefsPath_LivesInTheDataDir(t *testing.T) {
	dir := t.TempDir()
	config.SetDataDir(dir)
	t.Cleanup(func() { config.SetDataDir("") })

	path, err := DefaultPrefsPath()
	if err != nil {
		t.Fatalf("DefaultPrefsPath: %v", err)
	}
	if want := filepath.Join(dir, "rooms-prefs.json"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if path == presetsPathForTest(t) {
		t.Error("prefs must not share a file with the presets store — a corrupt prefs write would lose the rooms")
	}
}

func presetsPathForTest(t *testing.T) string {
	t.Helper()
	path, err := DefaultPresetsPath()
	if err != nil {
		t.Fatalf("DefaultPresetsPath: %v", err)
	}
	return path
}
