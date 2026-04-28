package openai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *AgentStore {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	store, err := NewAgentStore("conn-1")
	if err != nil {
		t.Fatalf("NewAgentStore: %v", err)
	}
	return store
}

func TestAgentStore_CreateRoundTrip(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.Create("My Agent", "ident body", "soul body", "gpt-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if meta.ID != "my-agent" {
		t.Errorf("ID = %q", meta.ID)
	}
	if meta.Model != "gpt-test" {
		t.Errorf("Model = %q", meta.Model)
	}
	if meta.CreatedAt.IsZero() {
		t.Error("CreatedAt not set")
	}

	if got := store.LoadIdentity(meta.ID); got != "ident body" {
		t.Errorf("LoadIdentity = %q", got)
	}
	if got := store.LoadSoul(meta.ID); got != "soul body" {
		t.Errorf("LoadSoul = %q", got)
	}

	loaded, err := store.LoadMeta(meta.ID)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if loaded.Name != "My Agent" {
		t.Errorf("LoadMeta.Name = %q", loaded.Name)
	}
}

func TestAgentStore_CreateDuplicate(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Create("alpha", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("Alpha", "", "", ""); err == nil {
		t.Error("expected duplicate error for same slug")
	}
}

func TestAgentStore_CreateRejectsEmptySlug(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Create("///", "", "", ""); err == nil {
		t.Error("expected error for name producing empty slug")
	}
}

func TestAgentStore_AppendAndLoadHistory(t *testing.T) {
	store := newTestStore(t)
	meta, _ := store.Create("agent", "", "", "")

	if err := store.AppendMessage(meta.ID, Message{Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(meta.ID, Message{Role: "assistant", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	msgs, err := store.LoadHistory(meta.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Content != "hello" || msgs[1].Content != "hi" {
		t.Errorf("messages out of order: %+v", msgs)
	}
}

func TestAgentStore_LoadHistoryLimit(t *testing.T) {
	store := newTestStore(t)
	meta, _ := store.Create("agent", "", "", "")
	for i := 0; i < 5; i++ {
		_ = store.AppendMessage(meta.ID, Message{Role: "user", Content: "msg"})
	}
	msgs, _ := store.LoadHistory(meta.ID, 3)
	if len(msgs) != 3 {
		t.Errorf("expected 3 messages with limit, got %d", len(msgs))
	}
}

func TestAgentStore_SystemPromptComposition(t *testing.T) {
	store := newTestStore(t)
	meta, _ := store.Create("agent", "I am a researcher.", "I am thorough.", "")

	prompt := store.SystemPrompt(meta.ID)
	if !strings.Contains(prompt, "# Identity") || !strings.Contains(prompt, "I am a researcher") {
		t.Errorf("identity missing from prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "# Soul") || !strings.Contains(prompt, "I am thorough") {
		t.Errorf("soul missing from prompt:\n%s", prompt)
	}
}

func TestAgentStore_SystemPromptOmitsEmptyParts(t *testing.T) {
	store := newTestStore(t)
	meta, _ := store.Create("agent", "id only", "", "")

	prompt := store.SystemPrompt(meta.ID)
	if !strings.Contains(prompt, "# Identity") {
		t.Errorf("expected identity heading, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "# Soul") {
		t.Errorf("empty soul should not produce heading, got:\n%s", prompt)
	}
}

func TestAgentStore_ListSortsByUpdatedDesc(t *testing.T) {
	store := newTestStore(t)
	a, _ := store.Create("alpha", "", "", "")
	_, _ = store.Create("bravo", "", "", "")

	// Touch alpha so it becomes most recent.
	if err := store.AppendMessage(a.ID, Message{Role: "user", Content: "ping"}); err != nil {
		t.Fatal(err)
	}

	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(list))
	}
	if list[0].ID != "alpha" {
		t.Errorf("expected alpha first (most recently touched), got %q", list[0].ID)
	}
}

func TestAgentStore_DeleteRemovesDirectory(t *testing.T) {
	store := newTestStore(t)
	meta, _ := store.Create("alpha", "", "", "")

	if err := store.Delete(meta.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.AgentDir(meta.ID)); !os.IsNotExist(err) {
		t.Errorf("agent dir should be removed: %v", err)
	}
}

func TestAgentStore_FilesArePrivate(t *testing.T) {
	store := newTestStore(t)
	meta, _ := store.Create("alpha", "ident", "soul", "")
	_ = store.AppendMessage(meta.ID, Message{Role: "user", Content: "hi"})

	check := func(name string) {
		info, err := os.Stat(filepath.Join(store.AgentDir(meta.ID), name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if mode := info.Mode().Perm(); mode != 0600 {
			t.Errorf("%s mode = %v want 0600", name, mode)
		}
	}
	check("agent.json")
	check("IDENTITY.md")
	check("SOUL.md")
	check("history.jsonl")
}

func TestSlugify(t *testing.T) {
	tests := []struct{ in, want string }{
		{"My Agent", "my-agent"},
		{"  Hello  World  ", "hello-world"},
		{"name_with_underscores", "name-with-underscores"},
		{"Already-Hyphen", "already-hyphen"},
		{"with$special!chars", "withspecialchars"},
		{"---", ""},
	}
	for _, tt := range tests {
		if got := slugify(tt.in); got != tt.want {
			t.Errorf("slugify(%q) = %q want %q", tt.in, got, tt.want)
		}
	}
}
