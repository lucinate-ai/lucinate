package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lucinate-ai/lucinate/internal/config"
)

func TestSelectModel_Actions(t *testing.T) {
	cases := []struct {
		name     string
		subState selectSubState
		loading  bool
		err      error
		wantIDs  []string
	}{
		{"list, ready", subStateList, false, nil, []string{"new-agent"}},
		{"list, loading", subStateList, true, nil, nil},
		{"list, error", subStateList, false, errors.New("boom"), []string{"retry"}},
		{"create form exposes nothing", subStateCreate, false, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := selectModel{subState: tc.subState, loading: tc.loading, err: tc.err, allowAgentManagement: true}
			got := actionIDs(m.Actions())
			if !equalStrings(got, tc.wantIDs) {
				t.Fatalf("ids=%v, want=%v", got, tc.wantIDs)
			}
		})
	}
}

func TestSelectModel_TriggerAction_NewAgent(t *testing.T) {
	m := selectModel{subState: subStateList, allowAgentManagement: true}
	next, cmd := m.TriggerAction("new-agent")
	if next.subState != subStateCreate {
		t.Fatalf("expected create sub-state, got %v", next.subState)
	}
	if cmd == nil {
		t.Fatal("expected an init cmd from initCreateForm")
	}
}

func TestSelectModel_KeyDelegatesToTriggerAction(t *testing.T) {
	// Regression: pressing 'n' on the agent list still opens the form
	// even though the dispatch now goes through Actions().
	m := selectModel{subState: subStateList, allowAgentManagement: true}
	next, _ := m.handleKey(tea.KeyPressMsg{Code: 'n'})
	if next.subState != subStateCreate {
		t.Fatalf("key 'n' should open create form via TriggerAction, got %v", next.subState)
	}
}

func TestSessionsModel_Actions(t *testing.T) {
	cases := []struct {
		name    string
		loading bool
		err     error
		wantIDs []string
	}{
		{"ready", false, nil, []string{"new-session", "back"}},
		{"loading", true, nil, []string{"back"}},
		{"error", false, errors.New("x"), []string{"back", "retry"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := sessionsModel{loading: tc.loading, err: tc.err}
			got := actionIDs(m.Actions())
			if !equalStrings(got, tc.wantIDs) {
				t.Fatalf("ids=%v, want=%v", got, tc.wantIDs)
			}
		})
	}
}

func TestConfigModel_Actions(t *testing.T) {
	cases := []struct {
		name    string
		items   []configItem
		cursor  int
		wantIDs []string
	}{
		{"empty (defensive)", nil, 0, []string{"back"}},
		{"cursor on bool item", []configItem{{kind: configItemBool}}, 0, []string{"toggle", "back"}},
		{"cursor on int item", []configItem{{kind: configItemBool}, {kind: configItemInt}}, 1, []string{"back"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := configModel{items: tc.items, cursor: tc.cursor}
			got := actionIDs(m.Actions())
			if !equalStrings(got, tc.wantIDs) {
				t.Fatalf("ids=%v, want=%v", got, tc.wantIDs)
			}
		})
	}
}

// TestHideActionHints_Suppresses verifies the inline help line is
// emitted by default and omitted when the embedder has signalled that
// it surfaces the same actions itself. We assert against the rendered
// hint substring rather than the entire view so style changes elsewhere
// don't make the test brittle.
func TestHideActionHints_Suppresses(t *testing.T) {
	t.Run("select", func(t *testing.T) {
		shown := newSelectModel(nil, false, false, nil, false, "")
		shown.loading = false
		shown.allowAgentManagement = true
		hidden := newSelectModel(nil, true, false, nil, false, "")
		hidden.loading = false
		hidden.allowAgentManagement = true
		if !strings.Contains(shown.View(), "n: new agent") {
			t.Fatalf("expected hint with hideHints=false, got: %q", shown.View())
		}
		if strings.Contains(hidden.View(), "n: new agent") {
			t.Fatalf("expected no hint with hideHints=true, got: %q", hidden.View())
		}
	})
	t.Run("sessions", func(t *testing.T) {
		shown := newSessionsModel(nil, "a", "A", "m", "k", false, nil, false)
		shown.loading = false
		hidden := newSessionsModel(nil, "a", "A", "m", "k", true, nil, false)
		hidden.loading = false
		if !strings.Contains(shown.View(), "esc: back") {
			t.Fatalf("expected hint with hideHints=false, got: %q", shown.View())
		}
		if strings.Contains(hidden.View(), "esc: back") {
			t.Fatalf("expected no hint with hideHints=true, got: %q", hidden.View())
		}
	})
	t.Run("config", func(t *testing.T) {
		prefs := config.DefaultPreferences()
		shown := newConfigModel(prefs, false)
		hidden := newConfigModel(prefs, true)
		if !strings.Contains(shown.View(), "space: toggle") {
			t.Fatalf("expected hint with hideHints=false, got: %q", shown.View())
		}
		if strings.Contains(hidden.View(), "space: toggle") {
			t.Fatalf("expected no hint with hideHints=true, got: %q", hidden.View())
		}
		if strings.Contains(hidden.View(), "←/→: adjust") {
			t.Fatalf("expected ←/→ adjust hint also suppressed, got: %q", hidden.View())
		}
	})
}

func TestRenderActionHints(t *testing.T) {
	got := renderActionHints([]Action{
		{ID: "new-agent", Label: "New agent", Key: "n"},
		{ID: "retry", Label: "Retry", Key: "r"},
	})
	want := "  n: new agent · r: retry"
	if got != want {
		t.Fatalf("hint=%q, want=%q", got, want)
	}
	if renderActionHints(nil) != "" {
		t.Fatal("nil actions should render the empty string")
	}
}

func TestActionsEqual(t *testing.T) {
	a := []Action{{ID: "x", Label: "X", Key: "x"}}
	b := []Action{{ID: "x", Label: "X", Key: "x"}}
	if !actionsEqual(a, b) {
		t.Fatal("identical lists should compare equal")
	}
	c := []Action{{ID: "x", Label: "X", Key: "y"}}
	if actionsEqual(a, c) {
		t.Fatal("different keys should not compare equal")
	}
	if actionsEqual(a, nil) {
		t.Fatal("len mismatch should not compare equal")
	}
}

func TestMaybeNotifyActions_FiresOnChange(t *testing.T) {
	var got [][]string
	m := AppModel{
		state:            viewSelect,
		selectModel:      selectModel{allowAgentManagement: true},
		onActionsChanged: func(as []Action) { got = append(got, actionIDs(as)) },
	}

	// First call always fires so embedders see the startup state.
	next, cmd := m.maybeNotifyActions(nil)
	runCmd(t, cmd)
	if len(got) != 1 || !equalStrings(got[0], []string{"new-agent"}) {
		t.Fatalf("initial fire: %v", got)
	}
	m = next.(AppModel)

	// Same state — no refire.
	next, cmd = m.maybeNotifyActions(nil)
	runCmd(t, cmd)
	if len(got) != 1 {
		t.Fatalf("unchanged should not refire: %v", got)
	}
	m = next.(AppModel)

	// Transition: error appears, retry action joins the list.
	m.selectModel.err = errors.New("boom")
	next, cmd = m.maybeNotifyActions(nil)
	runCmd(t, cmd)
	if len(got) != 2 || !equalStrings(got[1], []string{"retry"}) {
		t.Fatalf("error transition: %v", got)
	}
	m = next.(AppModel)

	// Move to sessions view — list changes again.
	m.state = viewSessions
	next, cmd = m.maybeNotifyActions(nil)
	runCmd(t, cmd)
	if len(got) != 3 || !equalStrings(got[2], []string{"new-session", "back"}) {
		t.Fatalf("sessions transition: %v", got)
	}
}

func TestMaybeNotifyActions_NoCallbackIsNoop(t *testing.T) {
	m := AppModel{state: viewSelect}
	_, cmd := m.maybeNotifyActions(nil)
	if cmd != nil {
		t.Fatalf("no callback should yield no notify cmd, got %T", cmd)
	}
}

func TestAppModel_TriggerActionRoutesToActiveView(t *testing.T) {
	m := AppModel{state: viewSelect, selectModel: selectModel{allowAgentManagement: true}}
	next, cmd := m.TriggerAction("new-agent")
	if next.selectModel.subState != subStateCreate {
		t.Fatalf("expected create form via routed TriggerAction, got %v", next.selectModel.subState)
	}
	if cmd == nil {
		t.Fatal("expected initCreateForm cmd to be returned")
	}
}

func actionIDs(as []Action) []string {
	if len(as) == 0 {
		return nil
	}
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.ID
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
