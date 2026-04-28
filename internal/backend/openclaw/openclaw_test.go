package openclaw

import (
	"strings"
	"testing"

	"github.com/lucinate-ai/lucinate/internal/backend"
)

// TestTakePendingCatalog_OnlyOnFirstTurn covers the gateway's
// expectation that the System:-prefixed catalog block is delivered
// once per session — the gateway parses it into a server-side system
// block on the first turn and retains it across subsequent turns.
func TestTakePendingCatalog_OnlyOnFirstTurn(t *testing.T) {
	b := New(nil)
	skills := []backend.SkillCatalogEntry{
		{Name: "review", Description: "Code review"},
		{Name: "commit", Description: "Write a commit message"},
	}

	first := b.takePendingCatalog("session-1", skills)
	if first == "" {
		t.Fatal("first call should return the catalog block")
	}
	if !strings.Contains(first, "System: Available agent skills") {
		t.Errorf("first catalog block missing header line:\n%s", first)
	}
	if !strings.Contains(first, "System:   - review:") || !strings.Contains(first, "System:   - commit:") {
		t.Errorf("first catalog block missing skill entries:\n%s", first)
	}

	second := b.takePendingCatalog("session-1", skills)
	if second != "" {
		t.Errorf("second call should return empty string (catalog already sent), got:\n%s", second)
	}

	// A different session is independent.
	other := b.takePendingCatalog("session-2", skills)
	if other == "" {
		t.Error("a fresh session should still receive the catalog")
	}
}

func TestTakePendingCatalog_EmptySkillsNoBlock(t *testing.T) {
	b := New(nil)
	if got := b.takePendingCatalog("s", nil); got != "" {
		t.Errorf("expected empty for nil skills, got %q", got)
	}
	if got := b.takePendingCatalog("s", []backend.SkillCatalogEntry{}); got != "" {
		t.Errorf("expected empty for empty slice, got %q", got)
	}
	// All-blank entries also produce an empty block.
	got := b.takePendingCatalog("s", []backend.SkillCatalogEntry{{Name: ""}})
	if got != "" {
		t.Errorf("expected empty when all skills lack names, got %q", got)
	}
}
