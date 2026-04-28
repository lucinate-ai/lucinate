package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lucinate-ai/lucinate/internal/config"
)

func newSeededStore(t *testing.T) *config.Connections {
	t.Helper()
	store := &config.Connections{}
	if _, err := store.Add(config.ConnectionFields{Name: "home", Type: config.ConnTypeOpenClaw, URL: "https://home.example.com"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := store.Add(config.ConnectionFields{Name: "work", Type: config.ConnTypeOpenClaw, URL: "https://work.example.com"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return store
}

func TestConnectionsModel_RendersEmptyState(t *testing.T) {
	m := newConnectionsModel(&config.Connections{}, false)
	out := m.View()
	if !strings.Contains(out, "No connections yet") {
		t.Errorf("expected empty-state hint in view, got:\n%s", out)
	}
}

func TestConnectionsModel_RendersListItems(t *testing.T) {
	store := newSeededStore(t)
	m := newConnectionsModel(store, false)
	m.setSize(80, 20)
	m.rebuildItems()
	out := m.View()
	if !strings.Contains(out, "home") || !strings.Contains(out, "work") {
		t.Errorf("expected both connection names in view, got:\n%s", out)
	}
	if !strings.Contains(out, "https://home.example.com") {
		t.Errorf("expected URL in view, got:\n%s", out)
	}
}

func TestConnectionsModel_DefaultBadge(t *testing.T) {
	store := newSeededStore(t)
	store.MarkUsed(store.Connections[0].ID)
	m := newConnectionsModel(store, false)
	m.setSize(80, 20)
	m.rebuildItems()
	if !strings.Contains(m.View(), "(default)") {
		t.Errorf("expected (default) badge for promoted connection, got:\n%s", m.View())
	}
}

func TestConnectionsModel_NewConnectionFlow(t *testing.T) {
	store := &config.Connections{}
	m := newConnectionsModel(store, false)
	m.setSize(80, 20)

	// Trigger 'n' (new).
	m, _ = m.TriggerAction("new-connection")
	if m.subState != subStateConnForm {
		t.Fatalf("subState = %v, want form", m.subState)
	}

	m.nameInput.SetValue("home")
	m.urlInput.SetValue("https://home.example.com")

	m, cmd := m.submitForm()
	if m.formErr != "" {
		t.Fatalf("submit form error: %s", m.formErr)
	}
	if cmd == nil {
		t.Fatal("expected connectionsChangedMsg cmd from successful submit")
	}
	if got := cmd(); got == nil {
		t.Fatal("cmd returned nil msg")
	}
	if len(store.Connections) != 1 {
		t.Errorf("expected 1 connection in store, got %d", len(store.Connections))
	}
	if store.Connections[0].URL != "https://home.example.com" {
		t.Errorf("URL = %q", store.Connections[0].URL)
	}
}

func TestConnectionsModel_NewConnectionRejectsInvalidURL(t *testing.T) {
	store := &config.Connections{}
	m := newConnectionsModel(store, false)
	m, _ = m.TriggerAction("new-connection")

	m.nameInput.SetValue("bad")
	m.urlInput.SetValue("ftp://nope.example.com")

	m, _ = m.submitForm()
	if m.formErr == "" {
		t.Error("expected form error for invalid URL")
	}
	if len(store.Connections) != 0 {
		t.Errorf("invalid URL should not be added, got %d entries", len(store.Connections))
	}
	if m.subState != subStateConnForm {
		t.Errorf("should still be in form sub-state, got %v", m.subState)
	}
}

func TestConnectionsModel_DeleteConfirmFlow(t *testing.T) {
	store := newSeededStore(t)
	m := newConnectionsModel(store, false)
	m.setSize(80, 20)
	m.rebuildItems()

	// Highlight first item.
	m.list.Select(0)

	m, _ = m.TriggerAction("delete-connection")
	if m.subState != subStateConnDeleteConfirm {
		t.Fatalf("subState = %v, want delete-confirm", m.subState)
	}
	if !strings.Contains(m.View(), "Delete connection") {
		t.Errorf("confirm view missing prompt: %s", m.View())
	}

	// Cancel returns to list.
	m, _ = m.TriggerAction("delete-cancel")
	if m.subState != subStateConnList {
		t.Errorf("cancel should return to list, got %v", m.subState)
	}
	if len(store.Connections) != 2 {
		t.Errorf("cancel should not delete, got %d entries", len(store.Connections))
	}

	// Re-enter confirm and accept.
	m, _ = m.TriggerAction("delete-connection")
	m, cmd := m.confirmDelete()
	if m.subState != subStateConnList {
		t.Errorf("confirm should return to list, got %v", m.subState)
	}
	if len(store.Connections) != 1 {
		t.Errorf("expected 1 entry after delete, got %d", len(store.Connections))
	}
	if cmd == nil {
		t.Error("expected connectionsChangedMsg cmd after delete")
	}
}

func TestConnectionsModel_EnterEmitsPickedMsg(t *testing.T) {
	store := newSeededStore(t)
	m := newConnectionsModel(store, false)
	m.setSize(80, 20)
	m.rebuildItems()
	m.list.Select(0)

	_, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected pick cmd")
	}
	msg := cmd()
	picked, ok := msg.(connectionPickedMsg)
	if !ok {
		t.Fatalf("expected connectionPickedMsg, got %T", msg)
	}
	if picked.connection == nil || picked.connection.ID != store.Connections[0].ID {
		t.Errorf("picked the wrong connection: %+v", picked.connection)
	}
}

func TestConnectionsModel_TypeCycleUpdatesPreset(t *testing.T) {
	store := &config.Connections{}
	m := newConnectionsModel(store, false)
	m, _ = m.TriggerAction("new-connection")

	if m.formPreset != presetOpenClaw {
		t.Fatalf("expected initial preset OpenClaw, got %v", m.formPreset)
	}

	// Right cycles OpenClaw → OpenAI.
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.formPreset != presetOpenAI {
		t.Errorf("expected OpenAI after Right, got %v", m.formPreset)
	}

	// Right again cycles OpenAI → Ollama, prefilling the URL and name.
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.formPreset != presetOllama {
		t.Errorf("expected Ollama after second Right, got %v", m.formPreset)
	}
	if m.urlInput.Value() != "http://localhost:11434/v1" {
		t.Errorf("Ollama preset did not prefill URL: %q", m.urlInput.Value())
	}
	if m.nameInput.Value() != "ollama" {
		t.Errorf("Ollama preset did not prefill name: %q", m.nameInput.Value())
	}

	// Right again wraps back to OpenClaw and clears the Ollama
	// prefill so the user isn't stuck with localhost in a gateway
	// URL field.
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.formPreset != presetOpenClaw {
		t.Errorf("expected wrap to OpenClaw, got %v", m.formPreset)
	}
	if m.urlInput.Value() != "" {
		t.Errorf("expected URL cleared on switch away from Ollama, got %q", m.urlInput.Value())
	}
	if m.nameInput.Value() != "" {
		t.Errorf("expected name cleared on switch away from Ollama, got %q", m.nameInput.Value())
	}
}

func TestConnectionsModel_OllamaPresetPersistsAsOpenAI(t *testing.T) {
	store := &config.Connections{}
	m := newConnectionsModel(store, false)
	m, _ = m.TriggerAction("new-connection")

	// OpenClaw → OpenAI → Ollama.
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.formPreset != presetOllama {
		t.Fatalf("expected Ollama preset, got %v", m.formPreset)
	}

	m.modelInput.SetValue("qwen2.5:0.5b")
	m, _ = m.submitForm()
	if m.formErr != "" {
		t.Fatalf("submit error: %s", m.formErr)
	}
	if len(store.Connections) != 1 {
		t.Fatalf("expected 1 connection, got %d", len(store.Connections))
	}
	got := store.Connections[0]
	if got.Type != config.ConnTypeOpenAI {
		t.Errorf("Ollama preset should persist as OpenAI, got %q", got.Type)
	}
	if got.URL != "http://localhost:11434/v1" {
		t.Errorf("URL = %q", got.URL)
	}
	if got.Name != "ollama" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.DefaultModel != "qwen2.5:0.5b" {
		t.Errorf("DefaultModel = %q", got.DefaultModel)
	}
}

func TestConnectionsModel_OpenAITabOrderIncludesModel(t *testing.T) {
	store := &config.Connections{}
	m := newConnectionsModel(store, false)
	m, _ = m.TriggerAction("new-connection")

	// Switch to OpenAI so the model field joins the tab order.
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})

	steps := []formField{formFieldName, formFieldURL, formFieldModel, formFieldType}
	for i, want := range steps {
		m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
		if got := m.currentField(); got != want {
			t.Errorf("tab %d: got %v want %v", i+1, got, want)
		}
	}
}

func TestConnectionsModel_EditFormSkipsTypeRadio(t *testing.T) {
	store := newSeededStore(t)
	m := newConnectionsModel(store, false)
	m.setSize(80, 20)
	m.rebuildItems()
	m.list.Select(0)

	m, _ = m.TriggerAction("edit-connection")
	if got := m.currentField(); got != formFieldName {
		t.Errorf("edit form should start on name (type is read-only), got %v", got)
	}
	// Tab cycles between name and url only — no type field, no model
	// field for an OpenClaw connection.
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := m.currentField(); got != formFieldURL {
		t.Errorf("expected URL, got %v", got)
	}
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := m.currentField(); got != formFieldName {
		t.Errorf("expected wrap to name, got %v", got)
	}
}

func TestConnectionsModel_NewOpenAIConnectionPersistsModel(t *testing.T) {
	store := &config.Connections{}
	m := newConnectionsModel(store, false)
	m, _ = m.TriggerAction("new-connection")

	// Switch type to OpenAI.
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})

	m.nameInput.SetValue("local llm")
	m.urlInput.SetValue("http://localhost:11434/v1")
	m.modelInput.SetValue("llama3.2")

	m, _ = m.submitForm()
	if m.formErr != "" {
		t.Fatalf("submit error: %s", m.formErr)
	}
	if len(store.Connections) != 1 {
		t.Fatalf("expected 1 connection, got %d", len(store.Connections))
	}
	got := store.Connections[0]
	if got.Type != config.ConnTypeOpenAI {
		t.Errorf("Type = %q", got.Type)
	}
	if got.DefaultModel != "llama3.2" {
		t.Errorf("DefaultModel = %q", got.DefaultModel)
	}
}

func TestConnectionsModel_NewFormRendersOnlyRelevantFields(t *testing.T) {
	store := &config.Connections{}
	m := newConnectionsModel(store, false)
	m, _ = m.TriggerAction("new-connection")

	// OpenClaw form: no model field rendered.
	out := m.viewForm()
	if strings.Contains(out, "Default model") {
		t.Errorf("OpenClaw form should not render model field, got:\n%s", out)
	}
	if !strings.Contains(out, "Gateway URL") {
		t.Errorf("OpenClaw form should label URL as Gateway URL, got:\n%s", out)
	}

	// Switch to OpenAI: model field appears, URL relabels.
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	out = m.viewForm()
	if !strings.Contains(out, "Default model") {
		t.Errorf("OpenAI form missing model field, got:\n%s", out)
	}
	if !strings.Contains(out, "Base URL") {
		t.Errorf("OpenAI form missing Base URL label, got:\n%s", out)
	}
	if strings.Contains(out, "Gateway URL") {
		t.Errorf("OpenAI form should not show Gateway URL label, got:\n%s", out)
	}
}

func TestConnectionsModel_TabAdvancesFocus(t *testing.T) {
	store := &config.Connections{}
	m := newConnectionsModel(store, false)
	m, _ = m.TriggerAction("new-connection")

	// New form starts focused on the type radio (index 0).
	if m.focusedField != 0 || m.currentField() != formFieldType {
		t.Fatalf("expected initial focus on type, got field=%v idx=%d", m.currentField(), m.focusedField)
	}

	// Tab advances through type → name → url; for OpenClaw there is
	// no model field so the next Tab wraps back to type.
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.currentField() != formFieldName {
		t.Errorf("after first tab: %v", m.currentField())
	}
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.currentField() != formFieldURL {
		t.Errorf("after second tab: %v", m.currentField())
	}
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.currentField() != formFieldType {
		t.Errorf("expected wrap back to type, got %v", m.currentField())
	}
}

func TestConnectionsModel_EditFlow(t *testing.T) {
	store := newSeededStore(t)
	m := newConnectionsModel(store, false)
	m.setSize(80, 20)
	m.rebuildItems()
	m.list.Select(0)

	m, _ = m.TriggerAction("edit-connection")
	if m.subState != subStateConnForm || m.editingID == "" {
		t.Fatalf("edit didn't enter form: state=%v editing=%q", m.subState, m.editingID)
	}

	m.nameInput.SetValue("renamed")
	m, _ = m.submitForm()
	if m.formErr != "" {
		t.Fatalf("submit error: %s", m.formErr)
	}
	if got := store.Connections[0].Name; got != "renamed" {
		t.Errorf("rename did not persist: %q", got)
	}
}

func TestConnectionsModel_ActionsListReflectsState(t *testing.T) {
	m := newConnectionsModel(&config.Connections{}, false)
	if got := m.Actions(); len(got) != 1 || got[0].ID != "new-connection" {
		t.Errorf("empty store should expose only New, got %+v", got)
	}

	store := newSeededStore(t)
	m = newConnectionsModel(store, false)
	m.rebuildItems()
	m.list.Select(0)
	gotIDs := []string{}
	for _, a := range m.Actions() {
		gotIDs = append(gotIDs, a.ID)
	}
	want := []string{"new-connection", "edit-connection", "delete-connection"}
	if strings.Join(gotIDs, ",") != strings.Join(want, ",") {
		t.Errorf("Actions = %v, want %v", gotIDs, want)
	}

	m.subState = subStateConnDeleteConfirm
	gotIDs = gotIDs[:0]
	for _, a := range m.Actions() {
		gotIDs = append(gotIDs, a.ID)
	}
	wantConfirm := []string{"delete-confirm", "delete-cancel"}
	if strings.Join(gotIDs, ",") != strings.Join(wantConfirm, ",") {
		t.Errorf("delete-confirm Actions = %v, want %v", gotIDs, wantConfirm)
	}
}
