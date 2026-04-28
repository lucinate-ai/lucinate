package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
)

func TestComputeWantsInput(t *testing.T) {
	cases := []struct {
		name     string
		state    viewState
		subState selectSubState
		want     bool
	}{
		{"select list — navigation only", viewSelect, subStateList, false},
		{"select create-form — typing required", viewSelect, subStateCreate, true},
		{"chat — textarea always focused", viewChat, subStateList, true},
		{"sessions — list navigation", viewSessions, subStateList, false},
		{"config — toggle navigation", viewConfig, subStateList, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := AppModel{state: tc.state}
			m.selectModel.subState = tc.subState
			if got := m.computeWantsInput(); got != tc.want {
				t.Fatalf("wants=%v, got=%v", tc.want, got)
			}
		})
	}
}

func TestMaybeNotifyInputFocus_FiresOnChange(t *testing.T) {
	var got []bool
	m := AppModel{
		state:               viewSelect,
		onInputFocusChanged: func(b bool) { got = append(got, b) },
	}

	// First call must fire with the initial state so embedders need not
	// assume a default.
	_, cmd := m.maybeNotifyInputFocus(nil)
	runCmd(t, cmd)
	if len(got) != 1 || got[0] != false {
		t.Fatalf("initial fire: got=%v", got)
	}

	// Mark reported as the previous call's returned model would have.
	m.inputFocusReported = true
	m.lastWantsInput = false

	// No state change — callback must not fire again.
	_, cmd = m.maybeNotifyInputFocus(nil)
	runCmd(t, cmd)
	if len(got) != 1 {
		t.Fatalf("unchanged state should not refire: got=%v", got)
	}

	// Transition into chat — fire with true.
	m.state = viewChat
	_, cmd = m.maybeNotifyInputFocus(nil)
	runCmd(t, cmd)
	if len(got) != 2 || got[1] != true {
		t.Fatalf("chat fire: got=%v", got)
	}
	m.lastWantsInput = true

	// Transition into sessions — fire with false.
	m.state = viewSessions
	_, cmd = m.maybeNotifyInputFocus(nil)
	runCmd(t, cmd)
	if len(got) != 3 || got[2] != false {
		t.Fatalf("sessions fire: got=%v", got)
	}
	m.lastWantsInput = false

	// Enter the create-agent form from the agent list — fire with true.
	m.state = viewSelect
	m.selectModel.subState = subStateCreate
	_, cmd = m.maybeNotifyInputFocus(nil)
	runCmd(t, cmd)
	if len(got) != 4 || got[3] != true {
		t.Fatalf("create-form fire: got=%v", got)
	}
}

func TestMaybeNotifyInputFocus_NoCallbackIsNoop(t *testing.T) {
	m := AppModel{state: viewChat}
	_, cmd := m.maybeNotifyInputFocus(nil)
	if cmd != nil {
		t.Fatalf("no callback should yield no notify cmd, got %T", cmd)
	}
}

func TestMaybeNotifyInputFocus_PreservesIncomingCmd(t *testing.T) {
	var got []bool
	var inner bool
	innerCmd := func() tea.Msg { inner = true; return nil }
	m := AppModel{
		state:               viewSelect,
		onInputFocusChanged: func(b bool) { got = append(got, b) },
	}
	_, cmd := m.maybeNotifyInputFocus(innerCmd)
	runCmd(t, cmd)
	if !inner {
		t.Fatal("inner cmd was dropped")
	}
	if len(got) != 1 || got[0] != false {
		t.Fatalf("notify cmd did not fire: got=%v", got)
	}
}

// TestNewApp_LegacyClientStartsAtSelect: pre-connected client (native
// platform embedder pattern) skips the connections picker.
func TestNewApp_LegacyClientStartsAtSelect(t *testing.T) {
	m := NewApp(nil, AppOptions{})
	if m.state != viewSelect {
		t.Errorf("legacy client should start at viewSelect, got %v", m.state)
	}
}

// TestNewApp_ManagedNoInitialStartsAtConnections: managed mode with no
// Initial drops the user into the picker.
func TestNewApp_ManagedNoInitialStartsAtConnections(t *testing.T) {
	store := &config.Connections{}
	m := NewApp(nil, AppOptions{
		Store:         store,
		ClientFactory: func(*config.Connection) (*client.Client, error) { return nil, nil },
	})
	if m.state != viewConnections {
		t.Errorf("managed-no-initial should start at viewConnections, got %v", m.state)
	}
}

// TestNewApp_ManagedWithInitialStartsAtConnecting: managed mode with an
// Initial connection jumps straight into the connecting state.
func TestNewApp_ManagedWithInitialStartsAtConnecting(t *testing.T) {
	store := &config.Connections{}
	conn, _ := store.Add("home", config.ConnTypeOpenClaw, "https://home.example.com")
	m := NewApp(nil, AppOptions{
		Store:         store,
		Initial:       conn,
		ClientFactory: func(*config.Connection) (*client.Client, error) { return nil, nil },
	})
	if m.state != viewConnecting {
		t.Errorf("managed-with-initial should start at viewConnecting, got %v", m.state)
	}
}

// runCmd drains a Cmd by invoking it and recursing into any BatchMsg it
// returns. Tests use it to flush the focus-notify cmd produced by
// maybeNotifyInputFocus alongside any inner cmd it was batched with.
func runCmd(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			runCmd(t, c)
		}
	}
}
