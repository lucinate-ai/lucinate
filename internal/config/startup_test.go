package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveEntryConnection_EmptyStoreNoEnv(t *testing.T) {
	withHomeDir(t)
	t.Setenv("OPENCLAW_GATEWAY_URL", "")
	t.Setenv("LUCINATE_OPENAI_BASE_URL", "")

	got := ResolveEntryConnection()
	if !got.ShowPicker {
		t.Fatalf("expected ShowPicker=true, got %+v", got)
	}
	if got.Connection != nil {
		t.Errorf("expected nil Connection, got %+v", got.Connection)
	}
}

func TestResolveEntryConnection_EnvMatchesExisting(t *testing.T) {
	withHomeDir(t)
	t.Setenv("LUCINATE_OPENAI_BASE_URL", "")

	var seed Connections
	seed.Add(ConnectionFields{Name: "home", Type: ConnTypeOpenClaw, URL: "https://gw.example.com"})
	if err := SaveConnections(seed); err != nil {
		t.Fatal(err)
	}

	t.Setenv("OPENCLAW_GATEWAY_URL", "https://gw.example.com/")
	got := ResolveEntryConnection()
	if got.ShowPicker || got.Connection == nil {
		t.Fatalf("expected matched connection, got %+v", got)
	}
	if got.Connection.URL != "https://gw.example.com" {
		t.Errorf("matched URL = %q", got.Connection.URL)
	}
}

func TestResolveEntryConnection_EnvAutoAdds(t *testing.T) {
	withHomeDir(t)
	t.Setenv("LUCINATE_OPENAI_BASE_URL", "")
	t.Setenv("OPENCLAW_GATEWAY_URL", "https://newgw.example.com")

	got := ResolveEntryConnection()
	if got.ShowPicker || got.Connection == nil {
		t.Fatalf("expected auto-added connection, got %+v", got)
	}
	if got.Connection.URL != "https://newgw.example.com" {
		t.Errorf("auto-added URL = %q", got.Connection.URL)
	}
	if got.Connection.Name != "newgw.example.com" {
		t.Errorf("auto-added Name = %q", got.Connection.Name)
	}
	if len(got.Store.Connections) != 1 {
		t.Errorf("store should have 1 connection, has %d", len(got.Store.Connections))
	}
}

func TestResolveEntryConnection_EnvAutoAddNotPersisted(t *testing.T) {
	home := withHomeDir(t)
	t.Setenv("LUCINATE_OPENAI_BASE_URL", "")
	t.Setenv("OPENCLAW_GATEWAY_URL", "https://newgw.example.com")

	_ = ResolveEntryConnection()

	if _, err := os.Stat(filepath.Join(home, ".lucinate", "connections.json")); !os.IsNotExist(err) {
		t.Errorf("auto-add should not persist before successful connect, got %v", err)
	}
}

func TestResolveEntryConnection_DefaultID(t *testing.T) {
	withHomeDir(t)
	t.Setenv("OPENCLAW_GATEWAY_URL", "")
	t.Setenv("LUCINATE_OPENAI_BASE_URL", "")

	var seed Connections
	a, _ := seed.Add(ConnectionFields{Name: "a", Type: ConnTypeOpenClaw, URL: "https://a.example.com"})
	seed.Add(ConnectionFields{Name: "b", Type: ConnTypeOpenClaw, URL: "https://b.example.com"})
	seed.MarkUsed(a.ID)
	if err := SaveConnections(seed); err != nil {
		t.Fatal(err)
	}

	got := ResolveEntryConnection()
	if got.ShowPicker || got.Connection == nil {
		t.Fatalf("expected default connection, got %+v", got)
	}
	if got.Connection.ID != a.ID {
		t.Errorf("Connection.ID = %q want %q", got.Connection.ID, a.ID)
	}
}

func TestResolveEntryConnection_SingleConnectionAutoPick(t *testing.T) {
	withHomeDir(t)
	t.Setenv("OPENCLAW_GATEWAY_URL", "")
	t.Setenv("LUCINATE_OPENAI_BASE_URL", "")

	var seed Connections
	conn, _ := seed.Add(ConnectionFields{Name: "only", Type: ConnTypeOpenClaw, URL: "https://only.example.com"})
	if err := SaveConnections(seed); err != nil {
		t.Fatal(err)
	}

	got := ResolveEntryConnection()
	if got.ShowPicker || got.Connection == nil {
		t.Fatalf("expected single-connection auto-pick, got %+v", got)
	}
	if got.Connection.ID != conn.ID {
		t.Errorf("Connection.ID = %q want %q", got.Connection.ID, conn.ID)
	}
}

func TestResolveEntryConnection_MultipleNoDefaultShowsPicker(t *testing.T) {
	withHomeDir(t)
	t.Setenv("OPENCLAW_GATEWAY_URL", "")
	t.Setenv("LUCINATE_OPENAI_BASE_URL", "")

	var seed Connections
	seed.Add(ConnectionFields{Name: "a", Type: ConnTypeOpenClaw, URL: "https://a.example.com"})
	seed.Add(ConnectionFields{Name: "b", Type: ConnTypeOpenClaw, URL: "https://b.example.com"})
	if err := SaveConnections(seed); err != nil {
		t.Fatal(err)
	}

	got := ResolveEntryConnection()
	if !got.ShowPicker {
		t.Fatalf("expected ShowPicker=true, got %+v", got)
	}
}

func TestResolveEntryConnection_StaleDefaultIDFallsThrough(t *testing.T) {
	withHomeDir(t)
	t.Setenv("OPENCLAW_GATEWAY_URL", "")
	t.Setenv("LUCINATE_OPENAI_BASE_URL", "")

	var seed Connections
	seed.Add(ConnectionFields{Name: "a", Type: ConnTypeOpenClaw, URL: "https://a.example.com"})
	seed.Add(ConnectionFields{Name: "b", Type: ConnTypeOpenClaw, URL: "https://b.example.com"})
	seed.DefaultID = "stale-id"
	if err := SaveConnections(seed); err != nil {
		t.Fatal(err)
	}

	got := ResolveEntryConnection()
	if !got.ShowPicker {
		t.Fatalf("expected ShowPicker=true with stale defaultId and >1 entries, got %+v", got)
	}
}

func TestResolveEntryConnection_OpenAIEnvAutoAdds(t *testing.T) {
	withHomeDir(t)
	t.Setenv("OPENCLAW_GATEWAY_URL", "")
	t.Setenv("LUCINATE_OPENAI_BASE_URL", "http://localhost:11434/v1")
	t.Setenv("LUCINATE_OPENAI_DEFAULT_MODEL", "llama3.2")

	got := ResolveEntryConnection()
	if got.ShowPicker || got.Connection == nil {
		t.Fatalf("expected auto-added OpenAI connection, got %+v", got)
	}
	if got.Connection.Type != ConnTypeOpenAI {
		t.Errorf("Type = %q want %q", got.Connection.Type, ConnTypeOpenAI)
	}
	if got.Connection.DefaultModel != "llama3.2" {
		t.Errorf("DefaultModel = %q", got.Connection.DefaultModel)
	}
}

func TestResolveEntryConnection_InvalidEnvURLFallsThrough(t *testing.T) {
	withHomeDir(t)
	t.Setenv("OPENCLAW_GATEWAY_URL", "ftp://nope.example.com")

	var seed Connections
	conn, _ := seed.Add(ConnectionFields{Name: "only", Type: ConnTypeOpenClaw, URL: "https://only.example.com"})
	if err := SaveConnections(seed); err != nil {
		t.Fatal(err)
	}

	got := ResolveEntryConnection()
	if got.ShowPicker || got.Connection == nil {
		t.Fatalf("expected fallback to single-connection auto-pick, got %+v", got)
	}
	if got.Connection.ID != conn.ID {
		t.Errorf("fell back to wrong connection: %+v", got.Connection)
	}
}
