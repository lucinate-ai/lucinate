package openclaw

import (
	"strings"
	"testing"
)

func TestCapabilities_SubagentsAdvertised(t *testing.T) {
	b := New(nil)
	if !b.Capabilities().Subagents {
		t.Error("OpenClaw backend should advertise Subagents=true")
	}
}

func TestSubagentListEntry_InfoProjection(t *testing.T) {
	entry := subagentListEntry{
		Key:                "agent:A:subagent:abc",
		AgentID:            "A",
		Model:              "claude-sonnet-4-6",
		DerivedTitle:       "Review the diff",
		Label:              "reviewer",
		LastMessagePreview: "Looking at file foo.go...",
		CreatedAt:          1700000000000,
		UpdatedAt:          1700000005000,
		SpawnedBy:          "main",
		SpawnDepth:         1,
		Status:             "running",
	}
	got := entry.info()
	if got.SessionKey != entry.Key {
		t.Errorf("session key: %q", got.SessionKey)
	}
	if got.Label != "reviewer" {
		t.Errorf("label should prefer explicit Label over DerivedTitle: %q", got.Label)
	}
	if got.ParentKey != "main" {
		t.Errorf("parent key: %q", got.ParentKey)
	}
	if got.Status != "running" {
		t.Errorf("status: %q", got.Status)
	}
	if got.AgentID != "A" || got.Model == "" {
		t.Errorf("agent/model not projected: %+v", got)
	}
	if got.SpawnDepth != 1 {
		t.Errorf("depth: %d", got.SpawnDepth)
	}
	if got.LastMessage != entry.LastMessagePreview {
		t.Errorf("last message: %q", got.LastMessage)
	}
}

func TestSubagentListEntry_InfoFallsBackToDerivedTitle(t *testing.T) {
	entry := subagentListEntry{
		Key:          "agent:A:subagent:xyz",
		DerivedTitle: "Summarise design doc",
	}
	got := entry.info()
	if got.Label != "Summarise design doc" {
		t.Errorf("expected derivedTitle fallback, got %q", got.Label)
	}
	if got.Status != "unknown" {
		t.Errorf("expected status fallback, got %q", got.Status)
	}
}

func TestSubagentListEntry_InfoTrimsWhitespace(t *testing.T) {
	entry := subagentListEntry{
		Key:    "agent:A:subagent:xyz",
		Label:  "   ",
		Status: "  ",
	}
	got := entry.info()
	if strings.TrimSpace(got.Label) != "" {
		t.Errorf("expected blank label to remain blank, got %q", got.Label)
	}
	if got.Status != "unknown" {
		t.Errorf("expected blank status to fall back to 'unknown', got %q", got.Status)
	}
}
