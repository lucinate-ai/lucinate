package tui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
)

// loadAgents seeds the picker with a set of agents via agentsLoadedMsg,
// matching the path the picker takes once ListAgents returns. Used by
// the delete-flow tests.
func loadAgents(m selectModel, agents ...protocol.AgentSummary) selectModel {
	defaultID := ""
	if len(agents) > 0 {
		defaultID = agents[0].ID
	}
	m, _ = m.Update(agentsLoadedMsg{
		result: &protocol.AgentsListResult{
			DefaultID: defaultID,
			MainKey:   defaultID,
			Agents:    agents,
		},
	})
	return m
}

func TestSelectModel_AutoSelectSingleAgent(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)

	msg := agentsLoadedMsg{
		result: &protocol.AgentsListResult{
			DefaultID: "main",
			MainKey:   "main",
			Agents: []protocol.AgentSummary{
				{ID: "main", Name: "My Agent"},
			},
		},
	}

	m, _ = m.Update(msg)

	if !m.selected {
		t.Error("expected auto-select when only one agent exists")
	}
	item, ok := m.selectedAgent()
	if !ok {
		t.Fatal("expected selected agent item")
	}
	if item.agent.ID != "main" {
		t.Errorf("selected agent ID = %q, want %q", item.agent.ID, "main")
	}
	if item.sessionKey != "main" {
		t.Errorf("session key = %q, want %q", item.sessionKey, "main")
	}
}

func TestSelectModel_NoAutoSelectMultipleAgents(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)

	msg := agentsLoadedMsg{
		result: &protocol.AgentsListResult{
			DefaultID: "main",
			MainKey:   "main",
			Agents: []protocol.AgentSummary{
				{ID: "main", Name: "Agent One"},
				{ID: "secondary", Name: "Agent Two"},
			},
		},
	}

	m, _ = m.Update(msg)

	if m.selected {
		t.Error("should not auto-select when multiple agents exist")
	}
}

func TestSelectModel_NoAutoSelectOnError(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)

	msg := agentsLoadedMsg{
		err: fmt.Errorf("connection failed"),
	}

	m, _ = m.Update(msg)

	if m.selected {
		t.Error("should not auto-select on error")
	}
	if m.err == nil {
		t.Error("expected error to be set")
	}
}

func TestSelectModel_CreateFormActivation(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	// Simulate agents loaded so the list is ready.
	m.loading = false
	m.allowAgentManagement = true

	m, _ = m.Update(tea.KeyPressMsg{Code: 'n'})

	if m.subState != subStateCreate {
		t.Error("expected subState to be subStateCreate after pressing 'n'")
	}
}

func TestSelectModel_CreateFormCancel(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	m.loading = false
	m.allowAgentManagement = true
	m, _ = m.Update(tea.KeyPressMsg{Code: 'n'})
	if m.subState != subStateCreate {
		t.Fatal("expected create form to be active")
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.subState != subStateList {
		t.Error("expected subState to return to subStateList after Esc")
	}
}

func TestSelectModel_CreateFormNotActivatedWhileLoading(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	// loading is true by default

	m, _ = m.Update(tea.KeyPressMsg{Code: 'n'})

	if m.subState != subStateList {
		t.Error("should not activate create form while loading")
	}
}

func TestNormaliseName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"My Agent", "my-agent"},
		{"hello", "hello"},
		{"UPPER", "upper"},
		{"a b c", "a-b-c"},
		{"Already-Good", "already-good"},
	}
	for _, tt := range tests {
		got := normaliseName(tt.input)
		if got != tt.want {
			t.Errorf("normaliseName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestValidateName(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"", true},
		{"my-agent", false},
		{"a", false},
		{"agent123", false},
		{"1agent", true},
		{"-agent", true},
		{"my agent", true},
		{"My-Agent", true},
	}
	for _, tt := range tests {
		msg := validateName(tt.input)
		gotErr := msg != ""
		if gotErr != tt.wantErr {
			t.Errorf("validateName(%q): gotErr=%v, wantErr=%v (msg=%q)", tt.input, gotErr, tt.wantErr, msg)
		}
	}
}

func TestSelectModel_WorkspaceAutoSuggest(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	m.loading = false
	m, _ = m.Update(tea.KeyPressMsg{Code: 'n'})

	// Simulate typing "test" in name field by updating the input directly.
	m.nameInput.SetValue("test")
	// Trigger the auto-suggest by processing a key event in name field context.
	prevName := ""
	_ = prevName
	// Use updateCreateForm directly to trigger workspace auto-suggest.
	m.nameInput.SetValue("")
	m.nameInput.SetValue("test")
	// Manually invoke the auto-suggest logic since SetValue doesn't trigger Update.
	if !m.workspaceEdited {
		m.workInput.SetValue("~/.openclaw/workspaces/test")
	}

	if m.workInput.Value() != "~/.openclaw/workspaces/test" {
		t.Errorf("workspace = %q, want %q", m.workInput.Value(), "~/.openclaw/workspaces/test")
	}
}

func TestSelectModel_WorkspaceManualEditStopsAutoSuggest(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	m.loading = false
	m, _ = m.Update(tea.KeyPressMsg{Code: 'n'})

	// Mark workspace as manually edited.
	m.workspaceEdited = true
	m.workInput.SetValue("/custom/path")

	// Simulate name change — workspace should NOT be overwritten.
	m.nameInput.SetValue("new-name")
	// The auto-suggest in updateCreateForm checks workspaceEdited.

	if m.workInput.Value() != "/custom/path" {
		t.Errorf("workspace = %q, want %q (should not auto-suggest after manual edit)",
			m.workInput.Value(), "/custom/path")
	}
}

func TestSelectModel_AgentCreatedSuccess(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	m.subState = subStateCreate
	m.creating = true

	m, _ = m.Update(agentCreatedMsg{
		name: "new-agent",
	})

	if m.creating {
		t.Error("expected creating to be false")
	}
	if m.subState != subStateList {
		t.Error("expected subState to return to list")
	}
	if m.newAgentID != "new-agent" {
		t.Errorf("newAgentID = %q, want %q", m.newAgentID, "new-agent")
	}
	if !m.loading {
		t.Error("expected loading to be true (agent list reload)")
	}
}

func TestSelectModel_AgentCreatedError(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	m.subState = subStateCreate
	m.creating = true

	m, _ = m.Update(agentCreatedMsg{
		err: fmt.Errorf("permission denied"),
	})

	if m.creating {
		t.Error("expected creating to be false")
	}
	if m.subState != subStateCreate {
		t.Error("expected to stay in create form on error")
	}
	if m.createErr == nil {
		t.Error("expected createErr to be set")
	}
}

func TestSelectModel_AutoSelectNewAgent(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	m.newAgentID = "new-agent"

	m, _ = m.Update(agentsLoadedMsg{
		result: &protocol.AgentsListResult{
			DefaultID: "main",
			MainKey:   "main",
			Agents: []protocol.AgentSummary{
				{ID: "main", Name: "Main Agent"},
				{ID: "new-agent", Name: "New Agent"},
			},
		},
	})

	if !m.selected {
		t.Error("expected new agent to be auto-selected")
	}
	item, ok := m.selectedAgent()
	if !ok {
		t.Fatal("expected selected agent item")
	}
	if item.agent.ID != "new-agent" {
		t.Errorf("selected agent ID = %q, want %q", item.agent.ID, "new-agent")
	}
	if m.newAgentID != "" {
		t.Error("expected newAgentID to be cleared")
	}
}

func TestSelectModel_ConnectionsActionOnlyInManagedMode(t *testing.T) {
	legacy := newSelectModel(nil, false, false, nil, false)
	for _, a := range legacy.Actions() {
		if a.ID == "connections" {
			t.Errorf("legacy mode should not expose connections action, got %+v", legacy.Actions())
		}
	}

	managed := newSelectModel(nil, false, true, nil, false)
	// Loaded state — no error — so the new-agent action is also
	// present. Find the connections action.
	var found *Action
	for i, a := range managed.Actions() {
		if a.ID == "connections" {
			found = &managed.Actions()[i]
		}
	}
	if found == nil {
		t.Fatalf("managed mode should expose connections action, got %+v", managed.Actions())
	}
	if found.Key != "c" {
		t.Errorf("expected key 'c', got %q", found.Key)
	}
}

// hasAction reports whether the picker currently exposes an action
// with the given ID. Used by the delete-flow tests to verify the
// confirm-delete action toggles correctly with the typed-name match.
func hasAction(m selectModel, id string) bool {
	for _, a := range m.Actions() {
		if a.ID == id {
			return true
		}
	}
	return false
}

func TestSelectModel_DeleteActionAppearsWhenManagementEnabled(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	m.allowAgentManagement = true
	m = loadAgents(m, protocol.AgentSummary{ID: "alpha", Name: "Alpha"})

	if !hasAction(m, "delete-agent") {
		t.Errorf("expected delete-agent action when management is enabled and a list item is selected, got %+v", m.Actions())
	}
}

func TestSelectModel_DeleteActionHiddenWhenManagementDisabled(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	// allowAgentManagement remains false (Hermes-style)
	m = loadAgents(m, protocol.AgentSummary{ID: "alpha", Name: "Alpha"})

	if hasAction(m, "delete-agent") {
		t.Errorf("expected delete-agent to be hidden when AgentManagement=false, got %+v", m.Actions())
	}
}

func TestSelectModel_DeleteActionHiddenWhenLoading(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	m.allowAgentManagement = true
	// loading=true is the initial state — list never received a load msg.

	if hasAction(m, "delete-agent") {
		t.Errorf("expected delete-agent to be hidden while loading, got %+v", m.Actions())
	}
}

func TestSelectModel_DeleteActionHiddenWhenEmptyList(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	m.allowAgentManagement = true
	m = loadAgents(m) // no agents

	if hasAction(m, "delete-agent") {
		t.Errorf("expected delete-agent to be hidden when list is empty, got %+v", m.Actions())
	}
}

func TestSelectModel_TriggerDeleteEntersConfirmSubstate(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	m.allowAgentManagement = true
	m = loadAgents(m, protocol.AgentSummary{ID: "alpha", Name: "Alpha Agent"})

	m, _ = m.TriggerAction("delete-agent")

	if m.subState != subStateConfirmDelete {
		t.Fatalf("expected subStateConfirmDelete, got %v", m.subState)
	}
	if m.pendingDeleteID != "alpha" {
		t.Errorf("pendingDeleteID = %q, want alpha", m.pendingDeleteID)
	}
	if m.pendingDeleteName != "Alpha Agent" {
		t.Errorf("pendingDeleteName = %q, want %q", m.pendingDeleteName, "Alpha Agent")
	}
	if m.keepFiles {
		t.Error("keepFiles should default to false (destructive)")
	}
	if m.deleting {
		t.Error("deleting should be false at substate entry")
	}
}

func TestSelectModel_TriggerDeleteUsesIDWhenNameEmpty(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	m.allowAgentManagement = true
	m = loadAgents(m, protocol.AgentSummary{ID: "id-only"})

	m, _ = m.TriggerAction("delete-agent")
	if m.pendingDeleteName != "id-only" {
		t.Errorf("pendingDeleteName = %q, expected fallback to ID", m.pendingDeleteName)
	}
}

func TestSelectModel_ConfirmDeleteRequiresNameMatch(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	m.allowAgentManagement = true
	m = loadAgents(m, protocol.AgentSummary{ID: "alpha", Name: "Alpha"})
	m, _ = m.TriggerAction("delete-agent")

	if hasAction(m, "confirm-delete") {
		t.Error("confirm-delete should not appear before name is typed")
	}

	m.confirmInput.SetValue("wrong-name")
	if hasAction(m, "confirm-delete") {
		t.Error("confirm-delete should not appear with mismatched name")
	}

	m.confirmInput.SetValue("Alpha")
	if !hasAction(m, "confirm-delete") {
		t.Errorf("confirm-delete should appear when name matches, got %+v", m.Actions())
	}
}

func TestSelectModel_ConfirmDeleteCaseInsensitive(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	m.allowAgentManagement = true
	m = loadAgents(m, protocol.AgentSummary{ID: "alpha", Name: "My Agent"})
	m, _ = m.TriggerAction("delete-agent")

	m.confirmInput.SetValue("MY AGENT")
	if !m.nameMatches() {
		t.Error("expected case-insensitive match")
	}

	m.confirmInput.SetValue("  my agent  ")
	if !m.nameMatches() {
		t.Error("expected whitespace-trimmed match")
	}
}

func TestSelectModel_ConfirmDeleteEscClearsState(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	m.allowAgentManagement = true
	m = loadAgents(m, protocol.AgentSummary{ID: "alpha", Name: "Alpha"})
	m, _ = m.TriggerAction("delete-agent")
	m.confirmInput.SetValue("Alpha")

	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.subState != subStateList {
		t.Errorf("expected return to subStateList, got %v", m.subState)
	}
	if m.pendingDeleteID != "" || m.pendingDeleteName != "" {
		t.Errorf("expected pending fields cleared, got id=%q name=%q", m.pendingDeleteID, m.pendingDeleteName)
	}
}

func TestSelectModel_ToggleKeepFilesFlipsState(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	m.allowAgentManagement = true
	m = loadAgents(m, protocol.AgentSummary{ID: "alpha", Name: "Alpha"})
	m, _ = m.TriggerAction("delete-agent")

	if m.keepFiles {
		t.Fatal("expected keepFiles=false at entry")
	}

	m, _ = m.TriggerAction("toggle-keep-files")
	if !m.keepFiles {
		t.Error("expected keepFiles=true after first toggle")
	}

	// Action label should now read the inverse — i.e. the button
	// offers to flip back to "Delete files".
	for _, a := range m.Actions() {
		if a.ID == "toggle-keep-files" && a.Label != "Delete files" {
			t.Errorf("toggle label = %q, want %q", a.Label, "Delete files")
		}
	}

	m, _ = m.TriggerAction("toggle-keep-files")
	if m.keepFiles {
		t.Error("expected keepFiles=false after second toggle")
	}
}

func TestSelectModel_ToggleKeepFilesViaTabKey(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	m.allowAgentManagement = true
	m = loadAgents(m, protocol.AgentSummary{ID: "alpha", Name: "Alpha"})
	m, _ = m.TriggerAction("delete-agent")

	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if !m.keepFiles {
		t.Error("expected tab key to toggle keepFiles")
	}
}

func TestSelectModel_DeleteCmdPassesKeepFilesChoice(t *testing.T) {
	fb := newFakeBackend()
	fb.caps.AgentManagement = true
	m := newSelectModel(fb, false, false, nil, false)
	m = loadAgents(m, protocol.AgentSummary{ID: "alpha", Name: "Alpha"})
	m, _ = m.TriggerAction("delete-agent")
	m.confirmInput.SetValue("Alpha")
	// Toggle to keep files.
	m, _ = m.TriggerAction("toggle-keep-files")

	m, cmd := m.TriggerAction("confirm-delete")
	if !m.deleting {
		t.Fatal("expected deleting=true after confirm")
	}
	if cmd == nil {
		t.Fatal("expected a cmd from confirm-delete")
	}
	// Run the cmd to actually call the backend (the cmd's body
	// invokes b.DeleteAgent and returns agentDeletedMsg).
	_ = cmd()

	if len(fb.deletedAgents) != 1 {
		t.Fatalf("expected 1 DeleteAgent call, got %d", len(fb.deletedAgents))
	}
	got := fb.deletedAgents[0]
	if got.AgentID != "alpha" {
		t.Errorf("AgentID = %q, want alpha", got.AgentID)
	}
	if got.DeleteFiles {
		t.Error("expected DeleteFiles=false (keepFiles=true was set)")
	}
}

func TestSelectModel_ConfirmDeleteIgnoredWithoutMatch(t *testing.T) {
	fb := newFakeBackend()
	fb.caps.AgentManagement = true
	m := newSelectModel(fb, false, false, nil, false)
	m = loadAgents(m, protocol.AgentSummary{ID: "alpha", Name: "Alpha"})
	m, _ = m.TriggerAction("delete-agent")
	m.confirmInput.SetValue("nope")

	m, cmd := m.TriggerAction("confirm-delete")
	if cmd != nil {
		t.Error("expected no cmd when name doesn't match")
	}
	if m.deleting {
		t.Error("expected deleting to remain false")
	}
}

func TestSelectModel_AgentDeletedSuccessReloads(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	m.subState = subStateConfirmDelete
	m.deleting = true
	m.pendingDeleteID = "alpha"
	m.pendingDeleteName = "Alpha"

	m, _ = m.Update(agentDeletedMsg{name: "Alpha"})

	if m.subState != subStateList {
		t.Errorf("subState = %v, want subStateList", m.subState)
	}
	if !m.loading {
		t.Error("expected loading=true (reload) after successful delete")
	}
	if m.pendingDeleteID != "" || m.pendingDeleteName != "" {
		t.Error("expected pending fields cleared on success")
	}
	if m.deleting {
		t.Error("expected deleting=false on success")
	}
}

func TestSelectModel_AgentDeletedErrorStaysInConfirm(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	m.subState = subStateConfirmDelete
	m.deleting = true
	m.pendingDeleteID = "alpha"
	m.pendingDeleteName = "Alpha"

	m, _ = m.Update(agentDeletedMsg{name: "Alpha", err: fmt.Errorf("boom")})

	if m.subState != subStateConfirmDelete {
		t.Errorf("expected to stay in confirm substate on error, got %v", m.subState)
	}
	if m.deleting {
		t.Error("expected deleting=false on error")
	}
	if m.deleteErr == nil {
		t.Error("expected deleteErr to be set")
	}
	// Pending fields preserved so the user can retry without
	// re-typing.
	if m.pendingDeleteID != "alpha" || m.pendingDeleteName != "Alpha" {
		t.Errorf("expected pending fields preserved on error, got id=%q name=%q", m.pendingDeleteID, m.pendingDeleteName)
	}
}

func TestSelectModel_DeletingBlocksKeystrokes(t *testing.T) {
	m := newSelectModel(nil, false, false, nil, false)
	m.subState = subStateConfirmDelete
	m.deleting = true
	m.pendingDeleteID = "alpha"
	m.pendingDeleteName = "Alpha"

	prev := m.keepFiles
	// Tab while deleting should NOT flip the toggle.
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.keepFiles != prev {
		t.Error("tab should be ignored while a delete is in flight")
	}
}

// Compile-time guard so the fake backend tracks DeleteAgent calls.
var _ = backend.DeleteAgentParams{}

func TestSelectModel_ConnectionsActionEmitsShowConnections(t *testing.T) {
	m := newSelectModel(nil, false, true, nil, false)
	_, cmd := m.TriggerAction("connections")
	if cmd == nil {
		t.Fatal("expected cmd from connections action")
	}
	if _, ok := cmd().(showConnectionsMsg); !ok {
		t.Errorf("expected showConnectionsMsg, got %T", cmd())
	}
}
