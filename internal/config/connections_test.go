package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// withHomeDir points HOME at a temporary directory for the duration of
// the test so ConnectionsPath, LoadConnections and SaveConnections
// don't touch the real ~/.lucinate.
func withHomeDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestConnections_AddFindUpdateDelete(t *testing.T) {
	withHomeDir(t)

	var c Connections
	conn, err := c.Add(ConnectionFields{Name: "home", Type: ConnTypeOpenClaw, URL: "https://gw.example.com"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if conn.ID == "" {
		t.Fatal("Add did not assign an ID")
	}
	if conn.Type != ConnTypeOpenClaw {
		t.Errorf("Type = %q", conn.Type)
	}
	if conn.CreatedAt.IsZero() {
		t.Error("CreatedAt was not set")
	}

	if got := c.Find(conn.ID); got == nil || got.Name != "home" {
		t.Errorf("Find(%q) returned %+v", conn.ID, got)
	}

	if got := c.FindByURL(ConnTypeOpenClaw, "https://gw.example.com/"); got == nil {
		t.Error("FindByURL did not match URL with trailing slash")
	}
	if got := c.FindByURL(ConnTypeOpenClaw, "HTTPS://GW.EXAMPLE.COM"); got == nil {
		t.Error("FindByURL did not match case-insensitive host")
	}

	if err := c.Update(conn.ID, ConnectionFields{Name: "renamed", Type: ConnTypeOpenClaw, URL: "https://other.example.com"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := c.Find(conn.ID); got.Name != "renamed" || got.URL != "https://other.example.com" {
		t.Errorf("Update did not persist: %+v", got)
	}

	if err := c.Delete(conn.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := c.Find(conn.ID); got != nil {
		t.Error("Delete did not remove the entry")
	}
}

func TestConnections_AddRejectsInvalidURL(t *testing.T) {
	var c Connections
	if _, err := c.Add(ConnectionFields{Name: "bad", Type: ConnTypeOpenClaw, URL: "ftp://nope"}); err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
	if _, err := c.Add(ConnectionFields{Name: "blank", Type: ConnTypeOpenClaw, URL: ""}); err == nil {
		t.Fatal("expected error for empty URL")
	}
	if _, err := c.Add(ConnectionFields{Name: "", Type: ConnTypeOpenClaw, URL: "https://ok.example.com"}); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestConnections_DeleteClearsDefault(t *testing.T) {
	var c Connections
	conn, _ := c.Add(ConnectionFields{Name: "a", Type: ConnTypeOpenClaw, URL: "https://a.example.com"})
	c.MarkUsed(conn.ID)
	if c.DefaultID != conn.ID {
		t.Fatalf("MarkUsed did not set default: %q", c.DefaultID)
	}
	_ = c.Delete(conn.ID)
	if c.DefaultID != "" {
		t.Errorf("Delete did not clear DefaultID: %q", c.DefaultID)
	}
}

func TestConnections_MarkUsedSetsTimestampAndDefault(t *testing.T) {
	var c Connections
	a, _ := c.Add(ConnectionFields{Name: "a", Type: ConnTypeOpenClaw, URL: "https://a.example.com"})
	b, _ := c.Add(ConnectionFields{Name: "b", Type: ConnTypeOpenClaw, URL: "https://b.example.com"})

	c.MarkUsed(a.ID)
	if c.DefaultID != a.ID {
		t.Errorf("DefaultID = %q after MarkUsed(a)", c.DefaultID)
	}
	if c.Find(a.ID).LastUsed.IsZero() {
		t.Error("MarkUsed did not stamp LastUsed")
	}

	c.MarkUsed(b.ID)
	if c.DefaultID != b.ID {
		t.Errorf("DefaultID = %q after MarkUsed(b)", c.DefaultID)
	}
}

func TestSaveAndLoadConnections_RoundTrip(t *testing.T) {
	withHomeDir(t)

	var saved Connections
	conn, _ := saved.Add(ConnectionFields{Name: "home", Type: ConnTypeOpenClaw, URL: "https://home.example.com"})
	saved.MarkUsed(conn.ID)

	if err := SaveConnections(saved); err != nil {
		t.Fatalf("SaveConnections: %v", err)
	}

	loaded := LoadConnections()
	if len(loaded.Connections) != 1 {
		t.Fatalf("loaded %d connections", len(loaded.Connections))
	}
	if loaded.DefaultID != conn.ID {
		t.Errorf("DefaultID = %q want %q", loaded.DefaultID, conn.ID)
	}
	if loaded.Connections[0].Name != "home" {
		t.Errorf("Name = %q", loaded.Connections[0].Name)
	}
}

func TestLoadConnections_MissingFileReturnsEmpty(t *testing.T) {
	withHomeDir(t)

	loaded := LoadConnections()
	if len(loaded.Connections) != 0 || loaded.DefaultID != "" {
		t.Errorf("expected empty store, got %+v", loaded)
	}
}

func TestLoadConnections_MalformedFileReturnsEmpty(t *testing.T) {
	home := withHomeDir(t)
	path := filepath.Join(home, ".lucinate", "connections.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	loaded := LoadConnections()
	if len(loaded.Connections) != 0 {
		t.Errorf("expected empty store for malformed file, got %+v", loaded)
	}
}

func TestSaveConnections_FileMode(t *testing.T) {
	home := withHomeDir(t)
	if err := SaveConnections(Connections{}); err != nil {
		t.Fatalf("SaveConnections: %v", err)
	}
	info, err := os.Stat(filepath.Join(home, ".lucinate", "connections.json"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("file mode = %v, want 0600", mode)
	}
}

func TestSaveConnections_WritesValidJSON(t *testing.T) {
	home := withHomeDir(t)
	var c Connections
	_, _ = c.Add(ConnectionFields{Name: "a", Type: ConnTypeOpenClaw, URL: "https://a.example.com"})
	if err := SaveConnections(c); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".lucinate", "connections.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got Connections
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("file is not valid JSON: %v", err)
	}
}

func TestAutoNameForURL(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"https://raspberrypi.tail4388db.ts.net", "raspberrypi.tail4388db.ts.net"},
		{"http://localhost:18789", "localhost"},
		{"wss://gateway.example.com:8443/ws", "gateway.example.com"},
	}
	for _, tt := range tests {
		if got := AutoNameForURL(tt.in); got != tt.want {
			t.Errorf("AutoNameForURL(%q) = %q want %q", tt.in, got, tt.want)
		}
	}
}
