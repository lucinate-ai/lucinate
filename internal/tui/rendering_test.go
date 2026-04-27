package tui

// TUI rendering tests driven by teatest/v2.
//
// teatest/v2 (github.com/charmbracelet/x/exp/teatest/v2) is the
// charm.land/bubbletea/v2-compatible fork of teatest — it runs the
// actual Bubble Tea program in a test harness and captures the rendered
// terminal output as raw bytes so tests can assert on what the user sees.
//
// The project's concrete models (chatModel, selectModel) have custom
// Update signatures that return their concrete type rather than tea.Model,
// so we wrap them in thin adapters that satisfy the tea.Model interface.

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/a3tai/openclaw-go/protocol"
	tea "charm.land/bubbletea/v2"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// chatModelAdapter wraps chatModel so it satisfies tea.Model for teatest.
type chatModelAdapter struct {
	inner chatModel
}

func (a chatModelAdapter) Init() tea.Cmd {
	// Skip the real Init (which would hit the gateway) — tests pre-populate state.
	return nil
}

func (a chatModelAdapter) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		a.inner.setSize(ws.Width, ws.Height)
		return a, nil
	}
	var cmd tea.Cmd
	a.inner, cmd = a.inner.Update(msg)
	return a, cmd
}

func (a chatModelAdapter) View() tea.View {
	return tea.NewView(a.inner.View())
}

// selectModelAdapter wraps selectModel so it satisfies tea.Model for teatest.
type selectModelAdapter struct {
	inner selectModel
}

func (a selectModelAdapter) Init() tea.Cmd { return nil }

func (a selectModelAdapter) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		a.inner.setSize(ws.Width, ws.Height)
		return a, nil
	}
	var cmd tea.Cmd
	a.inner, cmd = a.inner.Update(msg)
	return a, cmd
}

func (a selectModelAdapter) View() tea.View {
	return tea.NewView(a.inner.View())
}

// newRenderingChatModel builds a chatModel with sensible defaults for
// rendering tests, wrapped in an adapter.
func newRenderingChatModel(t *testing.T, agentName string) chatModelAdapter {
	t.Helper()
	m := newChatModel(nil, "session-key", "", agentName, "", config.DefaultPreferences(), false)
	m.setSize(120, 40)
	return chatModelAdapter{inner: m}
}

// waitForContains asserts that the program output eventually contains every
// given substring. Output is ANSI-stripped before comparison so tests assert
// on plain text. All substrings are checked against the same cumulative
// buffer — a single WaitFor call avoids the per-call drain of tm.Output()
// that would occur if WaitFor were called separately for each substring.
func waitForContains(t *testing.T, r io.Reader, substrs ...string) {
	t.Helper()
	teatest.WaitFor(t, r, func(b []byte) bool {
		s := ansi.Strip(string(b))
		for _, want := range substrs {
			if !strings.Contains(s, want) {
				return false
			}
		}
		return true
	},
		teatest.WithDuration(3*time.Second),
		teatest.WithCheckInterval(25*time.Millisecond),
	)
}

// finishProgram quits the program and waits for cleanup.
func finishProgram(t *testing.T, tm *teatest.TestModel) {
	t.Helper()
	_ = tm.Quit()
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func TestRender_ChatView_UserAndAssistantPrefixes(t *testing.T) {
	adapter := newRenderingChatModel(t, "main")
	adapter.inner.messages = []chatMessage{
		{role: "user", content: "hello there"},
		{role: "assistant", content: "general kenobi"},
	}
	adapter.inner.updateViewport()

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	waitForContains(t, tm.Output(), "You:", "main:", "hello there", "general kenobi")
}

func TestRender_ChatView_HeaderShowsAgentName(t *testing.T) {
	adapter := newRenderingChatModel(t, "scout")

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	waitForContains(t, tm.Output(), "lucinate", "scout")
}

func TestRender_ChatView_QueuedCountShownInHelpBar(t *testing.T) {
	adapter := newRenderingChatModel(t, "main")
	adapter.inner.sending = true
	adapter.inner.pendingMessages = []string{"queued msg"}
	adapter.inner.updateViewport()

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	waitForContains(t, tm.Output(), "1 queued")
}

func TestRender_ChatView_DefaultHelpHint(t *testing.T) {
	adapter := newRenderingChatModel(t, "main")

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	waitForContains(t, tm.Output(), "/help: commands")
}

func TestRender_ChatView_PendingMessageShownBeforeSending(t *testing.T) {
	adapter := newRenderingChatModel(t, "main")
	adapter.inner.sending = true
	adapter.inner.pendingMessages = []string{"pending-text-123"}
	adapter.inner.updateViewport()

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	waitForContains(t, tm.Output(), "pending-text-123")
}

func TestRender_ChatView_StreamingCursor(t *testing.T) {
	adapter := newRenderingChatModel(t, "main")
	adapter.inner.messages = []chatMessage{
		{role: "assistant", content: "partial response", streaming: true},
	}
	adapter.inner.updateViewport()

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	// Streaming assistant messages append an animated spinner frame after
	// the content. Any frame from spinnerFrames is acceptable.
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		s := ansi.Strip(string(b))
		if !strings.Contains(s, "partial response") {
			return false
		}
		for _, frame := range spinnerFrames {
			if strings.Contains(s, "partial response"+frame) {
				return true
			}
		}
		return false
	},
		teatest.WithDuration(3*time.Second),
		teatest.WithCheckInterval(25*time.Millisecond),
	)
}

func TestRender_ChatView_SlashHelpRendersAsSystemMessage(t *testing.T) {
	adapter := newRenderingChatModel(t, "main")

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	// Type the /help command and press enter.
	tm.Type("/help")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	waitForContains(t, tm.Output(), "/quit, /exit", "clear chat display")
}

func TestRender_ChatView_ErrorMessageStyled(t *testing.T) {
	adapter := newRenderingChatModel(t, "main")
	adapter.inner.messages = []chatMessage{
		{role: "assistant", errMsg: "connection refused"},
	}
	adapter.inner.updateViewport()

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	waitForContains(t, tm.Output(), "connection refused")
}

func TestRender_ChatView_SystemMessageRendered(t *testing.T) {
	adapter := newRenderingChatModel(t, "main")
	adapter.inner.messages = []chatMessage{
		{role: "system", content: "Switched to claude-sonnet-4-6"},
	}
	adapter.inner.updateViewport()

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	waitForContains(t, tm.Output(), "Switched to claude-sonnet-4-6")
}

func TestRender_ChatView_LocalExecModeHelpText(t *testing.T) {
	adapter := newRenderingChatModel(t, "main")

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	tm.Type("!ls")

	waitForContains(t, tm.Output(), "local command")
}

func TestRender_ChatView_RemoteExecModeHelpText(t *testing.T) {
	adapter := newRenderingChatModel(t, "main")

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	tm.Type("!!ls")

	waitForContains(t, tm.Output(), "remote command")
}

// newLoadedSelectAdapter returns a selectModelAdapter with agents already loaded.
func newLoadedSelectAdapter(t *testing.T, agents ...protocol.AgentSummary) selectModelAdapter {
	t.Helper()
	m := newSelectModel(nil)
	m.setSize(120, 40)
	m, _ = m.Update(agentsLoadedMsg{
		result: &protocol.AgentsListResult{
			DefaultID: "main",
			MainKey:   "main-key",
			Agents:    agents,
		},
	})
	return selectModelAdapter{inner: m}
}

func TestRender_SelectView_RendersAgentNames(t *testing.T) {
	adapter := newLoadedSelectAdapter(t,
		protocol.AgentSummary{ID: "main", Name: "Primary Agent"},
		protocol.AgentSummary{ID: "helper", Name: "Helper Agent"},
	)

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	waitForContains(t, tm.Output(), "Primary Agent", "Helper Agent")
}

func TestRender_SelectView_ShowsCreateHint(t *testing.T) {
	adapter := newLoadedSelectAdapter(t,
		protocol.AgentSummary{ID: "main", Name: "Primary"},
		protocol.AgentSummary{ID: "secondary", Name: "Secondary"},
	)

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	waitForContains(t, tm.Output(), "n: new agent")
}

func TestRender_SelectView_LoadingState(t *testing.T) {
	m := newSelectModel(nil)
	m.setSize(120, 40)
	adapter := selectModelAdapter{inner: m}

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	waitForContains(t, tm.Output(), "Connecting to gateway")
}

func TestRender_SelectView_CreateFormLabels(t *testing.T) {
	adapter := newLoadedSelectAdapter(t,
		protocol.AgentSummary{ID: "main", Name: "Primary"},
		protocol.AgentSummary{ID: "secondary", Name: "Secondary"},
	)
	adapter.inner.initCreateForm()

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	waitForContains(t, tm.Output(), "Create new agent", "Name (e.g. my-agent):", "Workspace:", "Tab: switch fields")
}

func TestRender_SelectView_ErrorStateShowsRetryHint(t *testing.T) {
	m := newSelectModel(nil)
	m.setSize(120, 40)
	m, _ = m.Update(agentsLoadedMsg{err: errString("gateway unreachable")})
	adapter := selectModelAdapter{inner: m}

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	waitForContains(t, tm.Output(), "gateway unreachable", "r: retry")
}

// sessionsModelAdapter wraps sessionsModel so it satisfies tea.Model for teatest.
type sessionsModelAdapter struct {
	inner sessionsModel
}

func (a sessionsModelAdapter) Init() tea.Cmd { return nil }

func (a sessionsModelAdapter) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		a.inner.setSize(ws.Width, ws.Height)
		return a, nil
	}
	var cmd tea.Cmd
	a.inner, cmd = a.inner.Update(msg)
	return a, cmd
}

func (a sessionsModelAdapter) View() tea.View {
	return tea.NewView(a.inner.View())
}

// newLoadedSessionsAdapter returns a sessionsModelAdapter with sessions already loaded.
func newLoadedSessionsAdapter(t *testing.T, sessions ...sessionItem) sessionsModelAdapter {
	t.Helper()
	m := newSessionsModel(nil, "agent-1", "Scout", "model-1", "main-key")
	m.setSize(120, 40)
	m, _ = m.Update(sessionsLoadedMsg{sessions: sessions})
	return sessionsModelAdapter{inner: m}
}

func TestRender_SessionsView_RendersSessionTitles(t *testing.T) {
	adapter := newLoadedSessionsAdapter(t,
		sessionItem{key: "s1", title: "Planning the roadmap"},
		sessionItem{key: "s2", title: "Debugging auth flow"},
	)

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	waitForContains(t, tm.Output(), "Planning the roadmap", "Debugging auth flow")
}

func TestRender_SessionsView_ShowsCreateHint(t *testing.T) {
	adapter := newLoadedSessionsAdapter(t,
		sessionItem{key: "s1", title: "Some session"},
	)

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	waitForContains(t, tm.Output(), "n: new session")
}

func TestRender_SessionsView_LoadingState(t *testing.T) {
	m := newSessionsModel(nil, "agent-1", "Scout", "model-1", "main-key")
	m.setSize(120, 40)
	adapter := sessionsModelAdapter{inner: m}

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	waitForContains(t, tm.Output(), "Loading sessions")
}

func TestRender_SessionsView_EmptyState(t *testing.T) {
	adapter := newLoadedSessionsAdapter(t) // no sessions

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	waitForContains(t, tm.Output(), "No sessions found", "n: new session")
}

func TestRender_SessionsView_ErrorState(t *testing.T) {
	m := newSessionsModel(nil, "agent-1", "Scout", "model-1", "main-key")
	m.setSize(120, 40)
	m, _ = m.Update(sessionsLoadedMsg{err: errString("network timeout")})
	adapter := sessionsModelAdapter{inner: m}

	tm := teatest.NewTestModel(t, adapter, teatest.WithInitialTermSize(120, 40))
	defer finishProgram(t, tm)

	waitForContains(t, tm.Output(), "network timeout", "r: retry")
}

// errString is a tiny error helper so tests don't need to import "errors".
type errString string

func (e errString) Error() string { return string(e) }
