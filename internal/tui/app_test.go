package tui

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/config"
)

func TestRunConnect_RoutesNotPairedToPairingModal(t *testing.T) {
	conn := &config.Connection{Name: "home"}
	fake := newFakeBackend()
	fake.connectErr = errors.New("connect: hello: connect rejected: NOT_PAIRED: pairing required")

	res := runConnect(conn, fake, time.Second)
	if res.authNeed != authRecoveryNotPaired {
		t.Fatalf("authNeed = %v, want authRecoveryNotPaired", res.authNeed)
	}
	if res.backend != fake {
		t.Error("expected backend to be retained on NOT_PAIRED so retry can reuse it")
	}
}

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
		Store:          store,
		BackendFactory: func(*config.Connection) (backend.Backend, error) { return nil, nil },
	})
	if m.state != viewConnections {
		t.Errorf("managed-no-initial should start at viewConnections, got %v", m.state)
	}
}

// TestNewApp_ManagedWithInitialStartsAtConnecting: managed mode with an
// Initial connection jumps straight into the connecting state.
func TestNewApp_ManagedWithInitialStartsAtConnecting(t *testing.T) {
	store := &config.Connections{}
	conn, _ := store.Add(config.ConnectionFields{Name: "home", Type: config.ConnTypeOpenClaw, URL: "https://home.example.com"})
	m := NewApp(nil, AppOptions{
		Store:          store,
		Initial:        conn,
		BackendFactory: func(*config.Connection) (backend.Backend, error) { return nil, nil },
	})
	if m.state != viewConnecting {
		t.Errorf("managed-with-initial should start at viewConnecting, got %v", m.state)
	}
}

// TestAppModel_DisableExitKeysSwallowsCtrlC: when an embedded host can't
// be dismissed by terminating the process, ctrl+c at the app level must
// not return tea.Quit — it would only stop the TUI loop while the host
// view stays mounted, leaving a dead Go session behind a frozen UI.
func TestAppModel_DisableExitKeysSwallowsCtrlC(t *testing.T) {
	store := &config.Connections{}

	// Default behaviour: ctrl+c quits.
	cli := NewApp(nil, AppOptions{
		Store:          store,
		BackendFactory: func(*config.Connection) (backend.Backend, error) { return nil, nil },
	})
	_, cmd := cli.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("expected ctrl+c to return a tea.Quit command on the CLI")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("expected non-nil quit message from ctrl+c")
	} else if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg, got %T", msg)
	}

	// DisableExitKeys=true: ctrl+c is a no-op.
	embedded := NewApp(nil, AppOptions{
		Store:           store,
		BackendFactory:  func(*config.Connection) (backend.Backend, error) { return nil, nil },
		DisableExitKeys: true,
	})
	_, cmd = embedded.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd != nil {
		if msg := cmd(); msg != nil {
			if _, ok := msg.(tea.QuitMsg); ok {
				t.Fatal("DisableExitKeys=true should suppress tea.Quit on ctrl+c")
			}
		}
	}
}

// TestAppModel_TabAdvancesFocusInConnectionsForm: end-to-end check
// that Tab in the connections form actually advances focus when
// routed through AppModel.Update. This guards against the value-vs-
// pointer-receiver bug where mutations got lost on the way back up
// the call chain.
func TestAppModel_TabAdvancesFocusInConnectionsForm(t *testing.T) {
	store := &config.Connections{}
	m := NewApp(nil, AppOptions{
		Store:          store,
		BackendFactory: func(*config.Connection) (backend.Backend, error) { return nil, nil },
	})
	if m.state != viewConnections {
		t.Fatalf("expected viewConnections, got %v", m.state)
	}

	// Open the new-connection form via the action mechanism so the
	// path matches what the inline-help "n" key triggers.
	next, _ := m.TriggerAction("new-connection")
	m = next
	if m.connectionsModel.subState != subStateConnForm {
		t.Fatalf("expected form sub-state, got %v", m.connectionsModel.subState)
	}
	if got := m.connectionsModel.currentField(); got != formFieldName {
		t.Fatalf("expected initial focus on name input, got %v", got)
	}

	// One Tab advances to URL, two to the type radio — through
	// AppModel.Update.
	for _, want := range []formField{formFieldURL, formFieldType} {
		updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		m = updated.(AppModel)
		if got := m.connectionsModel.currentField(); got != want {
			t.Fatalf("Tab through AppModel.Update did not advance focus: got %v want %v", got, want)
		}
	}
}

// TestAppModel_RoutesShowConnectionsMsg: /connections from the chat
// view tears down the active backend and transitions to the picker.
// Mid-session switching is the whole point of the connections feature
// — regressing this would be silent.
func TestAppModel_RoutesShowConnectionsMsg(t *testing.T) {
	store := &config.Connections{}
	conn, _ := store.Add(config.ConnectionFields{Name: "home", Type: config.ConnTypeOpenClaw, URL: "https://home.example.com"})
	store.MarkUsed(conn.ID)

	var publishedNil bool
	m := NewApp(nil, AppOptions{
		Store: store,
		BackendFactory: func(*config.Connection) (backend.Backend, error) {
			return newFakeBackend(), nil
		},
		OnBackendChanged: func(b backend.Backend) {
			if b == nil {
				publishedNil = true
			}
		},
	})
	// Pretend we already connected so showConnectionsMsg has work to
	// do (tear down + close the active backend).
	m.backend = newFakeBackend()
	m.state = viewChat

	updated, _ := m.Update(showConnectionsMsg{})
	m = updated.(AppModel)
	if m.state != viewConnections {
		t.Errorf("expected viewConnections after /connections, got %v", m.state)
	}
	if m.backend != nil {
		t.Errorf("expected backend cleared, got %T", m.backend)
	}
	// The OnBackendChanged callback runs in a goroutine — give it a
	// turn to fire. A short blocking nudge is fine for a unit test.
	for i := 0; i < 50 && !publishedNil; i++ {
		runtime.Gosched()
	}
	if !publishedNil {
		t.Error("expected OnBackendChanged(nil) to publish backend tear-down")
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

// TestAppModel_CreateSessionHonoursRequestTimeout: a stuck CreateSession
// (the symptom seen after first-time pairing, where the freshly
// authenticated connection silently drops the RPC) must surface as a
// timeout error rather than freezing the agent picker. The deadline is
// derived from the user-tunable connect timeout in preferences, so a
// floor on that value also floors the request deadline.
func TestAppModel_CreateSessionHonoursRequestTimeout(t *testing.T) {
	fake := newFakeBackend()
	fake.createSessionHook = func(ctx context.Context, agentID, key string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}

	m := AppModel{
		state:   viewSelect,
		backend: fake,
		prefs:   config.Preferences{ConnectTimeoutSeconds: 1},
	}
	m.selectModel = newSelectModel(fake, true, false, nil, false, "")
	m.selectModel.list.SetItems([]list.Item{
		agentItem{agent: protocol.AgentSummary{ID: "demo", Name: "demo"}, sessionKey: "demo"},
	})
	m.selectModel.loading = false
	m.selectModel.selected = true

	_, cmd := m.update(agentsLoadedMsg{result: &protocol.AgentsListResult{
		Agents: []protocol.AgentSummary{{ID: "demo", Name: "demo"}},
	}})
	if cmd == nil {
		t.Fatal("expected a session-create command after agent selection")
	}

	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	select {
	case msg := <-done:
		created, ok := msg.(sessionCreatedMsg)
		if !ok {
			t.Fatalf("expected sessionCreatedMsg, got %T", msg)
		}
		if !errors.Is(created.err, context.DeadlineExceeded) {
			t.Fatalf("expected DeadlineExceeded, got %v", created.err)
		}
	case <-time.After(2 * time.Second + 500*time.Millisecond):
		t.Fatal("CreateSession command never returned — request deadline is not wired")
	}
}
