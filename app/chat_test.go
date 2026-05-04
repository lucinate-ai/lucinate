package app

import (
	"strings"
	"testing"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// makeStore builds a Connections store with the supplied entries and
// no DefaultID. Tests that need a default set it after the fact.
func makeStore(t *testing.T, conns ...config.Connection) Connections {
	t.Helper()
	out := Connections{Connections: conns}
	return out
}

func TestResolveChatRunOptions_PlumbsExplicitConnection(t *testing.T) {
	a := config.Connection{ID: "id-a", Name: "alpha", Type: config.ConnTypeOpenClaw, URL: "ws://a"}
	b := config.Connection{ID: "id-b", Name: "beta", Type: config.ConnTypeOpenClaw, URL: "ws://b"}
	store := makeStore(t, a, b)

	opts, err := resolveChatRunOptions(ChatOptions{
		Connection:       "alpha",
		ConnectionsStore: &store,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if opts.Initial == nil {
		t.Fatal("Initial nil; want alpha")
	}
	if opts.Initial.ID != "id-a" {
		t.Fatalf("Initial.ID = %q, want id-a", opts.Initial.ID)
	}
}

func TestResolveChatRunOptions_MatchesByIDAndCaseInsensitiveName(t *testing.T) {
	a := config.Connection{ID: "id-A", Name: "Alpha", Type: config.ConnTypeOpenClaw, URL: "ws://a"}
	store := makeStore(t, a)

	cases := []struct{ name, query string }{
		{"by ID", "id-A"},
		{"by lowercase name", "alpha"},
		{"by mixed-case name", "ALPHA"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := store
			opts, err := resolveChatRunOptions(ChatOptions{
				Connection:       tc.query,
				ConnectionsStore: &s,
			})
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if opts.Initial == nil || opts.Initial.ID != "id-A" {
				t.Fatalf("Initial = %+v, want id-A", opts.Initial)
			}
		})
	}
}

func TestResolveChatRunOptions_AutoPicksSingleConnectionWhenUnspecified(t *testing.T) {
	t.Setenv("OPENCLAW_GATEWAY_URL", "")
	t.Setenv("LUCINATE_OPENAI_BASE_URL", "")
	t.Setenv(config.DataDirEnvVar, t.TempDir())

	// ResolveEntryConnection reads from disk via LoadConnections, so
	// seed the on-disk store explicitly.
	a := config.Connection{ID: "id-a", Name: "alpha", Type: config.ConnTypeOpenClaw, URL: "ws://a"}
	if err := config.SaveConnections(Connections{Connections: []config.Connection{a}}); err != nil {
		t.Fatalf("SaveConnections: %v", err)
	}

	opts, err := resolveChatRunOptions(ChatOptions{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if opts.Initial == nil || opts.Initial.ID != "id-a" {
		t.Fatalf("Initial = %+v, want id-a", opts.Initial)
	}
	if opts.Store == nil {
		t.Fatal("Store nil; managed mode requires a store")
	}
}

func TestResolveChatRunOptions_LeavesInitialNil_ForPickerCase(t *testing.T) {
	t.Setenv("OPENCLAW_GATEWAY_URL", "")
	t.Setenv("LUCINATE_OPENAI_BASE_URL", "")
	t.Setenv(config.DataDirEnvVar, t.TempDir())

	// Two connections, no DefaultID — ResolveEntryConnection returns
	// ShowPicker and we expect Chat to leave Initial nil so the TUI
	// renders the connections picker.
	a := config.Connection{ID: "id-a", Name: "alpha", Type: config.ConnTypeOpenClaw, URL: "ws://a"}
	b := config.Connection{ID: "id-b", Name: "beta", Type: config.ConnTypeOpenClaw, URL: "ws://b"}
	if err := config.SaveConnections(Connections{Connections: []config.Connection{a, b}}); err != nil {
		t.Fatalf("SaveConnections: %v", err)
	}

	opts, err := resolveChatRunOptions(ChatOptions{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if opts.Initial != nil {
		t.Fatalf("Initial = %+v, want nil for picker", opts.Initial)
	}
	if opts.Store == nil {
		t.Fatal("Store nil; picker requires a store")
	}
}

func TestResolveChatRunOptions_UnknownConnectionErrors(t *testing.T) {
	a := config.Connection{ID: "id-a", Name: "alpha", Type: config.ConnTypeOpenClaw, URL: "ws://a"}
	store := makeStore(t, a)

	_, err := resolveChatRunOptions(ChatOptions{
		Connection:       "bogus",
		ConnectionsStore: &store,
	})
	if err == nil {
		t.Fatal("expected error for unknown connection, got nil")
	}
	if !strings.Contains(err.Error(), `"bogus"`) {
		t.Fatalf("error %q should name the missing connection", err)
	}
}

func TestResolveChatRunOptions_EmptyStoreErrorIsHelpful(t *testing.T) {
	store := Connections{}
	_, err := resolveChatRunOptions(ChatOptions{
		Connection:       "anything",
		ConnectionsStore: &store,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// First-run hint: tell the user how to populate the store.
	if !strings.Contains(err.Error(), "no saved connections") {
		t.Fatalf("error %q should hint at empty-store first-run", err)
	}
}

func TestResolveChatRunOptions_PlumbsAgentSessionMessage(t *testing.T) {
	a := config.Connection{ID: "id-a", Name: "alpha", Type: config.ConnTypeOpenClaw, URL: "ws://a"}
	store := makeStore(t, a)

	opts, err := resolveChatRunOptions(ChatOptions{
		Connection:       "alpha",
		Agent:            "  scout  ",
		Session:          "  S1  ",
		Message:          "hello",
		ConnectionsStore: &store,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if opts.InitialAgent != "scout" {
		t.Fatalf("InitialAgent = %q, want %q (trimmed)", opts.InitialAgent, "scout")
	}
	if opts.InitialSession != "S1" {
		t.Fatalf("InitialSession = %q, want %q (trimmed)", opts.InitialSession, "S1")
	}
	// Message is intentionally not trimmed — leading/trailing
	// whitespace is part of what the user typed and the TUI's
	// regular send path would preserve it.
	if opts.InitialMessage != "hello" {
		t.Fatalf("InitialMessage = %q, want %q", opts.InitialMessage, "hello")
	}
	if opts.BackendFactory == nil {
		t.Fatal("BackendFactory nil; should default to DefaultBackendFactory")
	}
	if opts.OnConnectionsChanged == nil {
		t.Fatal("OnConnectionsChanged nil; CLI relies on it for persistence")
	}
}
