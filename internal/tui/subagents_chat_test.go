package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/client"
)

func newSubagentsTestModel() (*chatModel, *fakeBackend) {
	m := newSlashTestModel()
	m.sessionKey = "main"
	m.agentID = "agent-A"
	m.subagents = newSubagentTracker()
	fake, _ := m.backend.(*fakeBackend)
	return m, fake
}

func TestSubagentTracker_UpsertReplaceRemove(t *testing.T) {
	tr := newSubagentTracker()
	tr.upsert(backend.SubagentInfo{SessionKey: "child-1", Status: "running", Label: "first"})
	tr.upsert(backend.SubagentInfo{SessionKey: "child-1", Status: "completed"})
	snap := tr.snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 child, got %d", len(snap))
	}
	if snap[0].Status != "completed" {
		t.Errorf("status not updated: %s", snap[0].Status)
	}
	if snap[0].Label != "first" {
		t.Errorf("label clobbered by partial upsert: %s", snap[0].Label)
	}

	tr.replace([]backend.SubagentInfo{{SessionKey: "child-2", Status: "running"}})
	if got := tr.snapshot(); len(got) != 1 || got[0].SessionKey != "child-2" {
		t.Fatalf("replace did not narrow set to canonical list: %+v", got)
	}

	tr.remove("child-2")
	if tr.len() != 0 {
		t.Fatal("remove did not delete row")
	}
}

func TestSubagentTracker_ObserveToolStartAndResult(t *testing.T) {
	tr := newSubagentTracker()
	args := json.RawMessage(`{"agentId":"agent-B","task":"summarise","label":"summariser"}`)
	tr.observeToolStart("main", "tool-1", args)

	snap := tr.snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 placeholder, got %d", len(snap))
	}
	if snap[0].Status != "running" {
		t.Errorf("expected running, got %s", snap[0].Status)
	}
	if snap[0].Label != "summariser" {
		t.Errorf("expected label summariser, got %s", snap[0].Label)
	}

	tr.observeToolResult("tool-1", false, "")
	snap = tr.snapshot()
	if snap[0].Status != "completed" {
		t.Errorf("expected completed, got %s", snap[0].Status)
	}

	tr.observeToolStart("main", "tool-2", json.RawMessage(`{"goal":"audit"}`))
	tr.observeToolResult("tool-2", true, "bad input")
	for _, info := range tr.snapshot() {
		if info.SessionKey == "tool:tool-2" {
			if info.Status != "failed" {
				t.Errorf("expected failed for error result, got %s", info.Status)
			}
			if info.LastMessage != "bad input" {
				t.Errorf("expected error text in last message, got %s", info.LastMessage)
			}
		}
	}
}

func TestIsSubagentToolName(t *testing.T) {
	cases := map[string]bool{
		"sessions_spawn": true,
		"sessions.spawn": true,
		"sessions-spawn": true,
		"delegate_task":  true,
		"delegate":       true,
		"subagents":      true,
		"Read":           false,
		"":               false,
	}
	for name, want := range cases {
		if got := isSubagentToolName(name); got != want {
			t.Errorf("isSubagentToolName(%q)=%v, want %v", name, got, want)
		}
	}
}

func TestSlashCommand_Subagents_NotAvailable(t *testing.T) {
	m := newSlashTestModel()
	// noSubagentBackend implements only backend.Backend — no SubagentBackend.
	m.backend = newNoSubagentBackend()
	handled, cmd := m.handleSlashCommand("/subagents")
	if !handled {
		t.Fatal("expected /subagents to be handled")
	}
	if cmd != nil {
		t.Errorf("expected no cmd when capability missing, got %v", cmd)
	}
	last := m.messages[len(m.messages)-1]
	if last.role != "system" || !strings.Contains(last.errMsg, "not available") {
		t.Errorf("expected not-available system error, got: %+v", last)
	}
}

func TestSlashCommand_Subagents_OpensBrowser(t *testing.T) {
	m, _ := newSubagentsTestModel()
	handled, cmd := m.handleSlashCommand("/subagents")
	if !handled {
		t.Fatal("expected /subagents to be handled")
	}
	if cmd == nil {
		t.Fatal("expected showSubagentsMsg cmd")
	}
	msg := cmd()
	show, ok := msg.(showSubagentsMsg)
	if !ok {
		t.Fatalf("expected showSubagentsMsg, got %T", msg)
	}
	if show.parentSessionKey != "main" || show.parentAgentID != "agent-A" {
		t.Errorf("unexpected scope: %+v", show)
	}
	if show.initialSpawn != nil {
		t.Errorf("bare /subagents should not preload a spawn: %+v", show.initialSpawn)
	}
}

func TestSlashCommand_Subagents_SpawnPreloadsBrowser(t *testing.T) {
	m, _ := newSubagentsTestModel()
	handled, cmd := m.handleSlashCommand("/subagents spawn refactor the auth module")
	if !handled || cmd == nil {
		t.Fatal("expected /subagents spawn to dispatch")
	}
	show, ok := cmd().(showSubagentsMsg)
	if !ok {
		t.Fatalf("expected showSubagentsMsg, got %T", cmd())
	}
	if show.initialSpawn == nil {
		t.Fatal("expected initialSpawn to be populated")
	}
	if show.initialSpawn.Task != "refactor the auth module" {
		t.Errorf("task not propagated: %q", show.initialSpawn.Task)
	}
	if show.initialSpawn.AgentID != "agent-A" {
		t.Errorf("agent fallback not applied: %q", show.initialSpawn.AgentID)
	}
}

func TestSlashCommand_Subagents_SpawnEmptyError(t *testing.T) {
	m, _ := newSubagentsTestModel()
	handled, cmd := m.handleSlashCommand("/subagents spawn")
	if !handled || cmd != nil {
		t.Fatalf("expected inline error, got cmd=%v", cmd)
	}
	last := m.messages[len(m.messages)-1]
	if last.errMsg == "" {
		t.Errorf("expected error message, got %+v", last)
	}
}

func TestSlashCommand_Subagents_List_UsesTrackerWhenPopulated(t *testing.T) {
	m, _ := newSubagentsTestModel()
	m.subagents.upsert(backend.SubagentInfo{SessionKey: "child-1", Status: "running", Label: "x"})
	_, cmd := m.handleSlashCommand("/subagents list")
	if cmd == nil {
		t.Fatal("expected cmd")
	}
	msg := cmd()
	loaded, ok := msg.(subagentsLoadedMsg)
	if !ok {
		t.Fatalf("expected subagentsLoadedMsg, got %T", msg)
	}
	if loaded.err != nil {
		t.Fatalf("unexpected err: %v", loaded.err)
	}
	if len(loaded.items) != 1 || loaded.items[0].SessionKey != "child-1" {
		t.Errorf("tracker not used: %+v", loaded.items)
	}
}

func TestSlashCommand_Subagents_List_FallsBackToRPC(t *testing.T) {
	m, fake := newSubagentsTestModel()
	fake.subagentList = []backend.SubagentInfo{
		{SessionKey: "child-2", Status: "completed", Label: "from-rpc"},
	}
	_, cmd := m.handleSlashCommand("/subagents list")
	loaded, ok := cmd().(subagentsLoadedMsg)
	if !ok {
		t.Fatalf("expected subagentsLoadedMsg, got %T", cmd())
	}
	if loaded.err != nil {
		t.Fatalf("unexpected err: %v", loaded.err)
	}
	if len(loaded.items) != 1 || loaded.items[0].SessionKey != "child-2" {
		t.Errorf("expected RPC fallback, got %+v", loaded.items)
	}
	if m.subagents.len() != 1 {
		t.Errorf("expected tracker to be primed from fallback, len=%d", m.subagents.len())
	}
}

func TestSlashCommand_Subagents_Info_ByIndex(t *testing.T) {
	m, fake := newSubagentsTestModel()
	m.subagents.upsert(backend.SubagentInfo{SessionKey: "child-A", Status: "running", Label: "alpha"})
	fake.subagentInfoHook = func(ctx context.Context, sessionKey string) (*backend.SubagentInfo, error) {
		return &backend.SubagentInfo{SessionKey: sessionKey, Status: "running", Label: "alpha"}, nil
	}
	_, cmd := m.handleSlashCommand("/subagents info 1")
	got, ok := cmd().(subagentInfoLoadedMsg)
	if !ok {
		t.Fatalf("expected subagentInfoLoadedMsg, got %T", cmd())
	}
	if got.err != nil || got.info == nil || got.info.SessionKey != "child-A" {
		t.Errorf("unexpected info result: %+v", got)
	}
}

func TestSlashCommand_Subagents_Info_BadIndex(t *testing.T) {
	m, _ := newSubagentsTestModel()
	_, cmd := m.handleSlashCommand("/subagents info 99")
	if cmd != nil {
		t.Errorf("expected nil cmd for bad index, got %v", cmd)
	}
	last := m.messages[len(m.messages)-1]
	if !strings.Contains(last.errMsg, "no subagent") {
		t.Errorf("expected lookup error, got %+v", last)
	}
}

func TestSlashCommand_Subagents_Kill_All(t *testing.T) {
	m, fake := newSubagentsTestModel()
	m.subagents.upsert(backend.SubagentInfo{SessionKey: "child-a", Status: "running", CreatedAtMs: 2})
	m.subagents.upsert(backend.SubagentInfo{SessionKey: "child-b", Status: "running", CreatedAtMs: 1})
	_, cmd := m.handleSlashCommand("/subagents kill all")
	if cmd == nil {
		t.Fatal("expected batch cmd")
	}
	// Batch cmds run their children asynchronously; for the fake all
	// kills are synchronous so executing the batch settles immediately.
	// teatest harness is overkill — invoke the batch and drain msgs.
	// tea.Batch returns a tea.Cmd whose msg is tea.BatchMsg; simplest
	// path is to invoke and check that the fake recorded both kills.
	executeBatch(t, cmd)
	if len(fake.subagentKilled) != 2 {
		t.Errorf("expected 2 kills, got %v", fake.subagentKilled)
	}
}

func TestSlashCommand_Subagents_Kill_BySessionKey(t *testing.T) {
	m, fake := newSubagentsTestModel()
	m.subagents.upsert(backend.SubagentInfo{SessionKey: "child-x", Status: "running"})
	_, cmd := m.handleSlashCommand("/subagents kill child-x")
	if cmd == nil {
		t.Fatal("expected cmd")
	}
	msg := cmd()
	got, ok := msg.(subagentKilledMsg)
	if !ok {
		t.Fatalf("expected subagentKilledMsg, got %T", msg)
	}
	if got.err != nil || got.sessionKey != "child-x" {
		t.Errorf("unexpected kill result: %+v", got)
	}
	if len(fake.subagentKilled) != 1 || fake.subagentKilled[0] != "child-x" {
		t.Errorf("kill not dispatched: %v", fake.subagentKilled)
	}
}

func TestSlashCommand_Subagents_Kill_PropagatesError(t *testing.T) {
	m, fake := newSubagentsTestModel()
	m.subagents.upsert(backend.SubagentInfo{SessionKey: "child-x", Status: "running"})
	fake.subagentKillErr = errors.New("nope")
	_, cmd := m.handleSlashCommand("/subagents kill child-x")
	got := cmd().(subagentKilledMsg)
	if got.err == nil {
		t.Fatal("expected error to surface")
	}
}

func TestFormatSubagentList_EmptyAndError(t *testing.T) {
	if s := formatSubagentList(nil, nil); !strings.Contains(s, "No subagents") {
		t.Errorf("empty rendering: %q", s)
	}
	if s := formatSubagentList(nil, errors.New("rpc broke")); !strings.Contains(s, "rpc broke") {
		t.Errorf("error rendering: %q", s)
	}
}

func TestFormatSubagentInfo_NilAndError(t *testing.T) {
	if s := formatSubagentInfo(nil, errors.New("nope")); !strings.Contains(s, "nope") {
		t.Errorf("error rendering: %q", s)
	}
	if s := formatSubagentInfo(nil, nil); !strings.Contains(s, "not found") {
		t.Errorf("nil rendering: %q", s)
	}
}

func TestUnknownSubagentVerbReportsError(t *testing.T) {
	m, _ := newSubagentsTestModel()
	handled, cmd := m.handleSlashCommand("/subagents bogus")
	if !handled || cmd != nil {
		t.Fatalf("expected inline error, got cmd=%v", cmd)
	}
	last := m.messages[len(m.messages)-1]
	if !strings.Contains(last.errMsg, "unknown /subagents verb") {
		t.Errorf("expected unknown-verb error, got %+v", last)
	}
}

// noSubagentBackend is a backend.Backend stub that deliberately omits
// SubagentBackend methods so the type assertion in
// handleSubagentsCommand fails. Every other RPC delegates to a fresh
// fakeBackend through a wrapper — embedding would promote the
// fakeBackend's SubagentBackend methods through the outer type and
// defeat the purpose, so each method is declared explicitly.
type noSubagentBackend struct {
	inner *fakeBackend
}

func newNoSubagentBackend() *noSubagentBackend {
	return &noSubagentBackend{inner: newFakeBackend()}
}

func (b *noSubagentBackend) Connect(ctx context.Context) error { return b.inner.Connect(ctx) }
func (b *noSubagentBackend) Close() error                      { return b.inner.Close() }
func (b *noSubagentBackend) Events() <-chan protocol.Event     { return b.inner.Events() }
func (b *noSubagentBackend) Supervise(ctx context.Context, notify func(client.ConnState)) {
	b.inner.Supervise(ctx, notify)
}
func (b *noSubagentBackend) ListAgents(ctx context.Context) (*protocol.AgentsListResult, error) {
	return b.inner.ListAgents(ctx)
}
func (b *noSubagentBackend) CreateAgent(ctx context.Context, params backend.CreateAgentParams) error {
	return b.inner.CreateAgent(ctx, params)
}
func (b *noSubagentBackend) DeleteAgent(ctx context.Context, params backend.DeleteAgentParams) error {
	return b.inner.DeleteAgent(ctx, params)
}
func (b *noSubagentBackend) SessionsList(ctx context.Context, agentID string) (json.RawMessage, error) {
	return b.inner.SessionsList(ctx, agentID)
}
func (b *noSubagentBackend) CreateSession(ctx context.Context, agentID, key string) (string, error) {
	return b.inner.CreateSession(ctx, agentID, key)
}
func (b *noSubagentBackend) SessionDelete(ctx context.Context, sessionKey string) error {
	return b.inner.SessionDelete(ctx, sessionKey)
}
func (b *noSubagentBackend) ChatSend(ctx context.Context, sessionKey string, params backend.ChatSendParams) (*protocol.ChatSendResult, error) {
	return b.inner.ChatSend(ctx, sessionKey, params)
}
func (b *noSubagentBackend) ChatAbort(ctx context.Context, sessionKey, runID string) error {
	return b.inner.ChatAbort(ctx, sessionKey, runID)
}
func (b *noSubagentBackend) ChatHistory(ctx context.Context, sessionKey string, limit int) (json.RawMessage, error) {
	return b.inner.ChatHistory(ctx, sessionKey, limit)
}
func (b *noSubagentBackend) ModelsList(ctx context.Context) (*protocol.ModelsListResult, error) {
	return b.inner.ModelsList(ctx)
}
func (b *noSubagentBackend) SessionPatchModel(ctx context.Context, sessionKey, modelID string) error {
	return b.inner.SessionPatchModel(ctx, sessionKey, modelID)
}
func (b *noSubagentBackend) Capabilities() backend.Capabilities { return b.inner.Capabilities() }

// executeBatch unwraps tea.BatchMsg recursively and runs each leaf cmd
// against the fake backend. The fake's RPCs are synchronous so this is
// enough for the kill-all test to settle. Returns nothing — tests
// inspect the fake afterwards.
func executeBatch(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			executeBatch(t, c)
		}
		return
	}
	// Leaf command — already executed, msg holds its result. Nothing
	// else to do.
	_ = msg
}
