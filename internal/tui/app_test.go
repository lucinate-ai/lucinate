package tui

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/viewport"
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

// TestAppModel_OnExitRoutesQuitToHost: when the host supplies OnExit, the
// program must hand quit requests to it rather than calling tea.Quit —
// tea.Quit would freeze the host's view on the last frame. Exercises all
// four quit paths: ctrl+c, q on a navigation screen, /quit, and the Quit
// action.
func TestAppModel_OnExitRoutesQuitToHost(t *testing.T) {
	store := &config.Connections{}

	newModel := func(exited *bool) AppModel {
		return NewApp(nil, AppOptions{
			Store:           store,
			BackendFactory:  func(*config.Connection) (backend.Backend, error) { return nil, nil },
			DisableExitKeys: true, // host can't be dismissed by tea.Quit...
			OnExit:          func() { *exited = true },
		})
	}

	// runExit asserts the cmd fires OnExit and emits no tea.QuitMsg.
	runExit := func(t *testing.T, name string, cmd tea.Cmd, exited *bool) {
		t.Helper()
		if cmd == nil {
			t.Fatalf("%s: expected a command", name)
		}
		if msg := cmd(); msg != nil {
			if _, ok := msg.(tea.QuitMsg); ok {
				t.Fatalf("%s: OnExit hosts must not emit tea.Quit", name)
			}
		}
		if !*exited {
			t.Fatalf("%s: expected OnExit to be invoked", name)
		}
	}

	t.Run("ctrl+c", func(t *testing.T) {
		var exited bool
		m := newModel(&exited)
		_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
		runExit(t, "ctrl+c", cmd, &exited)
	})

	t.Run("q on navigation screen", func(t *testing.T) {
		var exited bool
		m := newModel(&exited)
		// The entry view is the agent/connections picker — a navigation
		// screen with no focused text input, so q quits.
		_, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
		runExit(t, "q", cmd, &exited)
	})

	t.Run("/quit via requestExitMsg", func(t *testing.T) {
		var exited bool
		m := newModel(&exited)
		_, cmd := m.Update(requestExitMsg{})
		runExit(t, "requestExitMsg", cmd, &exited)
	})

	t.Run("Quit action", func(t *testing.T) {
		var exited bool
		m := newModel(&exited)
		_, cmd := m.Update(TriggerActionMsg{ID: "quit"})
		runExit(t, "quit action", cmd, &exited)
	})
}

// TestAppModel_RequestExitMsgRouting: the message /quit emits resolves
// per host. The CLI quits via tea.Quit; a host that can't self-terminate
// gets no command and a user-facing notice instead of a dead loop.
func TestAppModel_RequestExitMsgRouting(t *testing.T) {
	store := &config.Connections{}
	factory := func(*config.Connection) (backend.Backend, error) { return nil, nil }

	// CLI: requestExitMsg becomes tea.Quit.
	cli := NewApp(nil, AppOptions{Store: store, BackendFactory: factory})
	_, cmd := cli.Update(requestExitMsg{})
	if cmd == nil {
		t.Fatal("CLI: expected a quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("CLI: expected tea.QuitMsg, got %T", cmd())
	}

	// No-exit host in chat: no command, but a notice is appended so the
	// user isn't left wondering why /quit did nothing.
	noExit := NewApp(nil, AppOptions{Store: store, BackendFactory: factory, DisableExitKeys: true})
	noExit.state = viewChat
	before := len(noExit.chatModel.messages)
	next, cmd := noExit.Update(requestExitMsg{})
	if cmd != nil {
		if msg := cmd(); msg != nil {
			if _, ok := msg.(tea.QuitMsg); ok {
				t.Fatal("no-exit host must not emit tea.Quit")
			}
		}
	}
	got := next.(AppModel).chatModel.messages
	if len(got) != before+1 {
		t.Fatalf("expected a notice message appended, got %d (was %d)", len(got), before)
	}
	if last := got[len(got)-1]; last.role != "system" || last.content != exitUnavailableNotice {
		t.Fatalf("unexpected notice message: %+v", last)
	}
}

// TestAppModel_QuitActionExposedOnlyForHostDrivenExit: the Quit action is
// an embedder affordance — it should appear when (and only when) the host
// can act on it. The CLI (tea.Quit) and a no-exit host both leave it off.
func TestAppModel_QuitActionExposedOnlyForHostDrivenExit(t *testing.T) {
	store := &config.Connections{}
	factory := func(*config.Connection) (backend.Backend, error) { return nil, nil }

	hasQuit := func(actions []Action) bool {
		for _, a := range actions {
			if a.ID == "quit" {
				return true
			}
		}
		return false
	}

	cli := NewApp(nil, AppOptions{Store: store, BackendFactory: factory})
	if hasQuit(cli.Actions()) {
		t.Error("CLI should not expose a Quit action")
	}

	noExit := NewApp(nil, AppOptions{Store: store, BackendFactory: factory, DisableExitKeys: true})
	if hasQuit(noExit.Actions()) {
		t.Error("a host that can't terminate should not expose a Quit action")
	}

	hostExit := NewApp(nil, AppOptions{Store: store, BackendFactory: factory, DisableExitKeys: true, OnExit: func() {}})
	if !hasQuit(hostExit.Actions()) {
		t.Error("an OnExit host should expose a Quit action on the navigation screen")
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

// TestAppModel_EscFromCrons_RestoresOriginalChat: opening a cron run
// transcript rebuilds chatModel in place, so esc out of the crons list
// must restore the chat the user opened the list from rather than
// stranding them on the transcript.
func TestAppModel_EscFromCrons_RestoresOriginalChat(t *testing.T) {
	fake := newFakeBackend()
	m := AppModel{backend: fake, width: 120, height: 40}
	m.chatModel = newChatModel(fake, "orig-session", "agent-1", "Scout", "", config.Preferences{}, false, "", "", false)
	m.chatModel.setSize(120, 40)
	m.state = viewCrons

	// Open a run transcript: this clobbers the live chat in place.
	updated, _ := m.Update(cronTranscriptMsg{job: sampleJobs()[0], agentName: "Scout"})
	m = updated.(AppModel)
	if !m.cronsReturnValid {
		t.Fatal("expected the original chat to be preserved when a transcript opens")
	}
	if !m.chatModel.transcript {
		t.Fatal("expected the live chat to be the transcript after cronTranscriptMsg")
	}

	// Esc out of the crons list must put the original chat back.
	updated, _ = m.Update(goBackFromCronsMsg{})
	m = updated.(AppModel)
	if m.state != viewChat {
		t.Errorf("expected viewChat after esc from crons, got %v", m.state)
	}
	if m.chatModel.transcript {
		t.Error("expected the original (non-transcript) chat to be restored")
	}
	if m.chatModel.sessionKey != "orig-session" {
		t.Errorf("expected original session restored, got %q", m.chatModel.sessionKey)
	}
	if m.cronsReturnValid {
		t.Error("expected the preserved chat to be released after restore")
	}
}

// TestAppModel_ShowCrons_ClearsStalePreservedChat: each fresh crons
// excursion must forget any chat preserved by a previous one, so an esc
// that never opened a transcript doesn't restore a stale chat.
func TestAppModel_ShowCrons_ClearsStalePreservedChat(t *testing.T) {
	fake := newFakeBackend()
	m := AppModel{backend: fake, width: 120, height: 40}
	m.chatModel = newChatModel(fake, "orig-session", "agent-1", "Scout", "", config.Preferences{}, false, "", "", false)
	m.state = viewChat
	// Leftover state from a prior excursion that ended abnormally.
	m.cronsReturnValid = true

	updated, _ := m.Update(showCronsMsg{filterAgentID: "agent-1", filterLabel: "Scout"})
	m = updated.(AppModel)
	if m.state != viewCrons {
		t.Errorf("expected viewCrons after showCronsMsg, got %v", m.state)
	}
	if m.cronsReturnValid {
		t.Error("expected a fresh crons excursion to clear any preserved chat")
	}
}

// TestAppModel_EscFromCrons_NoTranscriptLeavesChatUntouched: when the
// user never opened a transcript there is nothing to restore, so esc
// just returns to the (untouched) chat.
func TestAppModel_EscFromCrons_NoTranscriptLeavesChatUntouched(t *testing.T) {
	fake := newFakeBackend()
	m := AppModel{backend: fake, width: 120, height: 40}
	m.chatModel = newChatModel(fake, "orig-session", "agent-1", "Scout", "", config.Preferences{}, false, "", "", false)
	m.state = viewCrons

	updated, _ := m.Update(goBackFromCronsMsg{})
	m = updated.(AppModel)
	if m.state != viewChat {
		t.Errorf("expected viewChat after esc from crons, got %v", m.state)
	}
	if m.chatModel.sessionKey != "orig-session" {
		t.Errorf("expected chat left untouched, got session %q", m.chatModel.sessionKey)
	}
}

// TestAppModel_SessionCreatedError_ClearsSelectingLock: an error from
// CreateSession bounces the user back to the agent picker; the picker's
// `selecting` flag must be cleared in the same step so it doesn't stay
// frozen on the loading line forever. Without this, the user would be
// staring at "Loading <agent>..." with no way to retry — selecting
// would still gate every keystroke into a no-op.
// TestAppModel_MouseMode_TogglesCaptureAndView verifies the /mouse
// command path: a mouseModeMsg flips the authoritative mouseCapture flag,
// View() emits MouseModeCellMotion only when capture is on, and the chat
// model gets a feedback row. NewApp defaults capture ON (wheel scrolls,
// in-app selection copies — see selection.go); /mouse off remains the
// opt-out that hands click-drag back to the terminal's native selection.
func TestAppModel_MouseMode_TogglesCaptureAndView(t *testing.T) {
	m := AppModel{state: viewChat}
	// hideInput skips the textarea render so View() doesn't touch the
	// zero-valued composer; the mouse-mode gating under test is applied
	// after the per-view switch and is independent of the chat body.
	m.chatModel = chatModel{viewport: viewport.New(), hideInput: true}

	// Zero-value model starts with capture off; the NewApp default (on) is
	// covered by TestNewApp_MouseCaptureDefaultsOn.
	if got := m.View(); got.MouseMode != tea.MouseModeNone {
		t.Fatalf("zero-value MouseMode = %v, want MouseModeNone", got.MouseMode)
	}

	on, _ := m.update(mouseModeMsg{action: "on"})
	m = on
	if !m.mouseCapture {
		t.Fatal("expected mouseCapture true after /mouse on")
	}
	if got := m.View(); got.MouseMode != tea.MouseModeCellMotion {
		t.Errorf("MouseMode after on = %v, want MouseModeCellMotion", got.MouseMode)
	}

	tog, _ := m.update(mouseModeMsg{action: "toggle"})
	m = tog
	if m.mouseCapture {
		t.Fatal("expected mouseCapture false after toggle")
	}
	if got := m.View(); got.MouseMode != tea.MouseModeNone {
		t.Errorf("MouseMode after toggle-off = %v, want MouseModeNone", got.MouseMode)
	}

	// "status" reports without changing state.
	before := m.mouseCapture
	st, _ := m.update(mouseModeMsg{action: "status"})
	if st.mouseCapture != before {
		t.Errorf("status changed capture: before=%v after=%v", before, st.mouseCapture)
	}
}

func TestAppModel_SessionCreatedError_ClearsSelectingLock(t *testing.T) {
	m := AppModel{state: viewSelect}
	m.selectModel = newSelectModel(nil, false, false, nil, false, "")
	m.selectModel.loading = false
	m.selectModel.selecting = true
	m.selectModel.selectingName = "Alpha"

	next, _ := m.update(sessionCreatedMsg{err: errors.New("boom")})

	if next.selectModel.selecting {
		t.Error("selecting must be cleared so the user isn't stuck on the loading line")
	}
	if next.selectModel.selectingName != "" {
		t.Errorf("selectingName not cleared: %q", next.selectModel.selectingName)
	}
	if next.selectModel.err == nil {
		t.Error("expected the error to be surfaced on the picker")
	}
	if next.state != viewSelect {
		t.Errorf("state = %v, want viewSelect", next.state)
	}
}

// TestAppModel_SessionCreatedSuccess_ClearsSelectingLock: on the happy
// path the picker hands off to chat — but selectModel outlives the
// picker view (only rebuilt on connect, not on /agents), so the
// `selecting` flag has to be cleared on success too. Otherwise the
// next /agents from chat lands the user on "Loading <agent>..." for
// the agent they just opened.
func TestAppModel_SessionCreatedSuccess_ClearsSelectingLock(t *testing.T) {
	m := AppModel{state: viewSelect, prefs: config.Preferences{}}
	m.selectModel = newSelectModel(nil, false, false, nil, false, "")
	m.selectModel.loading = false
	m.selectModel.selecting = true
	m.selectModel.selectingName = "Alpha"

	next, _ := m.update(sessionCreatedMsg{
		sessionKey: "main",
		agentID:    "alpha",
		agentName:  "Alpha",
	})

	if next.selectModel.selecting {
		t.Error("selecting must be cleared on success so /agents doesn't return to a frozen picker")
	}
	if next.selectModel.selectingName != "" {
		t.Errorf("selectingName not cleared: %q", next.selectModel.selectingName)
	}
	if next.state != viewChat {
		t.Errorf("state = %v, want viewChat", next.state)
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

// TestAppModel_ConfigReturnsToOpeningView verifies the config screen
// hands control back to whichever view opened it. Config is reachable
// from chat (/config), the connections list, and the agent list, so a
// hard-coded "return to chat" would strand the user in chat after
// opening config before any session exists.
func TestAppModel_ConfigReturnsToOpeningView(t *testing.T) {
	for _, origin := range []viewState{viewChat, viewConnections, viewSelect} {
		m := AppModel{state: origin}

		opened, _ := m.update(showConfigMsg{})
		if opened.state != viewConfig {
			t.Fatalf("origin %v: expected viewConfig, got %v", origin, opened.state)
		}
		if opened.configReturn != origin {
			t.Fatalf("origin %v: configReturn = %v, want origin", origin, opened.configReturn)
		}

		// Esc (the global handler) returns to the opening view.
		back, _ := opened.update(tea.KeyPressMsg{Code: tea.KeyEscape})
		if back.state != origin {
			t.Errorf("origin %v: esc should return to origin, got %v", origin, back.state)
		}

		// The embedder-facing Back action (goBackFromConfigMsg) agrees.
		back2, _ := opened.update(goBackFromConfigMsg{})
		if back2.state != origin {
			t.Errorf("origin %v: Back should return to origin, got %v", origin, back2.state)
		}
	}
}

// TestAppModel_AskConfigSubScreenRoundTrip walks the full ask-defaults
// path: config → ask sub-screen → save → back to config, with the
// original opening view preserved across the hop so the eventual Esc
// from config still lands where the user came from.
func TestAppModel_AskConfigSubScreenRoundTrip(t *testing.T) {
	// Opened from the agent list.
	m := AppModel{state: viewSelect}
	cfg, _ := m.update(showConfigMsg{})

	ask, cmd := cfg.update(showAskConfigMsg{})
	if ask.state != viewAskConfig {
		t.Fatalf("expected viewAskConfig, got %v", ask.state)
	}
	if cmd == nil {
		t.Error("expected a focus cmd on entering the ask sub-screen")
	}

	// Esc inside the sub-screen is handled by the model (which saves),
	// not by the global handler — the view stays put until the close
	// message it emits is processed.
	escd, saveCmd := ask.update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if escd.state != viewAskConfig {
		t.Errorf("esc should stay in viewAskConfig until the save resolves, got %v", escd.state)
	}
	if saveCmd == nil {
		t.Fatal("expected a save cmd from esc in the sub-screen")
	}
	if _, ok := saveCmd().(askConfigClosedMsg); !ok {
		t.Errorf("expected askConfigClosedMsg from the save cmd, got %T", saveCmd())
	}

	// Closing the sub-screen returns to config and propagates the prefs.
	prefs := config.DefaultPreferences()
	prefs.Ask = config.AskDefaults{Connection: "home", Agent: "bot"}
	closed, _ := ask.update(askConfigClosedMsg{prefs: prefs})
	if closed.state != viewConfig {
		t.Fatalf("expected viewConfig after closing the sub-screen, got %v", closed.state)
	}
	if closed.prefs.Ask.Connection != "home" || closed.prefs.Ask.Agent != "bot" {
		t.Errorf("ask prefs not propagated to AppModel: %+v", closed.prefs.Ask)
	}

	// configReturn survived the sub-screen hop, so Esc from config still
	// returns to the agent list it was opened from.
	back, _ := closed.update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if back.state != viewSelect {
		t.Errorf("expected esc from config to return to viewSelect origin, got %v", back.state)
	}
}

// TestAppModel_FilterResetsWhenReenteringAgentList checks that returning
// to the agent picker from another screen clears any active fuzzy filter
// so the list reopens showing every agent, not a stale narrowed view.
// Both re-entry messages (Back-from-config and the connections goBackMsg)
// route through the same Update view-transition hook.
func TestAppModel_FilterResetsWhenReenteringAgentList(t *testing.T) {
	cases := []struct {
		name string
		from viewState
		msg  tea.Msg
		prep func(AppModel) AppModel
	}{
		{"from config", viewConfig, goBackFromConfigMsg{}, func(m AppModel) AppModel { m.configReturn = viewSelect; return m }},
		{"from connections", viewConnections, goBackMsg{}, func(m AppModel) AppModel { return m }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sm := newSelectModel(nil, false, false, nil, false, "")
			sm = loadAgents(sm,
				protocol.AgentSummary{ID: "alpha", Name: "Alpha"},
				protocol.AgentSummary{ID: "beta", Name: "Beta"},
			)
			sm.list.SetFilterText("beta")
			if len(sm.list.VisibleItems()) != 1 {
				t.Fatalf("setup: expected 1 filtered agent, got %d", len(sm.list.VisibleItems()))
			}

			m := AppModel{state: tc.from}
			m.selectModel = sm
			m = tc.prep(m)

			back, _ := m.Update(tc.msg)
			app := back.(AppModel)

			if app.state != viewSelect {
				t.Fatalf("expected viewSelect after re-entry, got %v", app.state)
			}
			if app.selectModel.list.FilterState() != list.Unfiltered {
				t.Errorf("expected filter reset to Unfiltered, got %v", app.selectModel.list.FilterState())
			}
			if got := len(app.selectModel.list.VisibleItems()); got != 2 {
				t.Errorf("expected full list (2 agents) after reset, got %d", got)
			}
		})
	}
}
