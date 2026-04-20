package tui

// TUI rendering tests.
//
// The associated issue suggested using github.com/charmbracelet/x/exp/teatest,
// but that package targets bubbletea v1. This project uses
// charm.land/bubbletea/v2 whose Model interface is source-incompatible
// (`Update` returns the concrete v2 Model, `View` returns a tea.View struct,
// messages are typed differently, etc.). So teatest cannot drive these
// models directly.
//
// Instead, these tests drive each model's View() / viewport.View() output
// directly and assert on the ANSI-stripped bytes — which is the same level
// of coverage teatest provides (a user-visible rendering snapshot).

import (
	"strings"
	"testing"

	"github.com/a3tai/openclaw-go/protocol"
	"github.com/charmbracelet/x/ansi"
)

// newRenderingChatModel creates a chatModel with sensible defaults for
// rendering tests.
func newRenderingChatModel(t *testing.T, agentName string) chatModel {
	t.Helper()
	m := newChatModel(nil, "session-key", agentName, "")
	m.setSize(120, 40)
	return m
}

// renderChatView renders the full chat view and returns the ANSI-stripped
// string so tests can make plain-text assertions on what the user sees.
func renderChatView(m chatModel) string {
	return ansi.Strip(m.View())
}

func TestRender_ChatView_UserAndAssistantPrefixes(t *testing.T) {
	m := newRenderingChatModel(t, "main")
	m.messages = []chatMessage{
		{role: "user", content: "hello there"},
		{role: "assistant", content: "general kenobi"},
	}
	m.updateViewport()

	view := renderChatView(m)

	if !strings.Contains(view, "You:") {
		t.Errorf("expected user prefix 'You:' in rendered view, got:\n%s", view)
	}
	if !strings.Contains(view, "main:") {
		t.Errorf("expected agent prefix 'main:' in rendered view, got:\n%s", view)
	}
	if !strings.Contains(view, "hello there") {
		t.Errorf("expected user message body in rendered view, got:\n%s", view)
	}
	if !strings.Contains(view, "general kenobi") {
		t.Errorf("expected assistant message body in rendered view, got:\n%s", view)
	}
}

func TestRender_ChatView_HeaderShowsAgentName(t *testing.T) {
	m := newRenderingChatModel(t, "scout")
	view := renderChatView(m)

	if !strings.Contains(view, "repclaw — scout") {
		t.Errorf("expected header to show agent name, got:\n%s", view)
	}
}

func TestRender_ChatView_QueuedCountShownInHelpBar(t *testing.T) {
	m := newRenderingChatModel(t, "main")
	// Simulate a message being queued while another is in flight.
	m.sending = true
	m.pendingMessages = []string{"queued msg"}
	m.updateViewport()

	view := renderChatView(m)

	if !strings.Contains(view, "1 queued") {
		t.Errorf("expected help bar to show '1 queued', got:\n%s", view)
	}
}

func TestRender_ChatView_DefaultHelpHint(t *testing.T) {
	m := newRenderingChatModel(t, "main")
	view := renderChatView(m)

	if !strings.Contains(view, "/help: commands") {
		t.Errorf("expected help hint in help bar, got:\n%s", view)
	}
}

func TestRender_ChatView_PendingMessageShownBeforeSending(t *testing.T) {
	m := newRenderingChatModel(t, "main")
	m.sending = true
	m.pendingMessages = []string{"pending-text-123"}
	m.updateViewport()

	view := renderChatView(m)

	if !strings.Contains(view, "pending-text-123") {
		t.Errorf("expected queued message body to appear in viewport, got:\n%s", view)
	}
}

func TestRender_ChatView_StreamingCursor(t *testing.T) {
	m := newRenderingChatModel(t, "main")
	m.messages = []chatMessage{
		{role: "assistant", content: "partial response", streaming: true},
	}
	m.updateViewport()

	view := renderChatView(m)

	// Streaming assistant messages append a "_" cursor after the content.
	if !strings.Contains(view, "partial response_") {
		t.Errorf("expected streaming cursor '_' adjacent to partial content, got:\n%s", view)
	}
}

func TestRender_ChatView_SlashHelpRendersAsSystemMessage(t *testing.T) {
	m := newRenderingChatModel(t, "main")

	handled, _ := m.handleSlashCommand("/help")
	if !handled {
		t.Fatal("expected /help to be handled")
	}

	view := renderChatView(m)

	// The help text includes the line describing /quit and /exit.
	if !strings.Contains(view, "/quit, /exit") {
		t.Errorf("expected /help body to appear in viewport, got:\n%s", view)
	}
	if !strings.Contains(view, "clear chat display") {
		t.Errorf("expected /clear description to appear in help output, got:\n%s", view)
	}
}

func TestRender_ChatView_ErrorMessageStyled(t *testing.T) {
	m := newRenderingChatModel(t, "main")
	m.messages = []chatMessage{
		{role: "assistant", errMsg: "connection refused"},
	}
	m.updateViewport()

	plain := renderChatView(m)
	raw := m.View()

	if !strings.Contains(plain, "connection refused") {
		t.Errorf("expected error text to appear in rendered view, got:\n%s", plain)
	}
	// errorStyle is bold + red-foreground; in a styled render we expect
	// ANSI escape codes to be present surrounding the error text.
	if raw == plain {
		t.Errorf("expected error message to carry ANSI styling, but raw output had none")
	}
}

func TestRender_ChatView_SystemMessageRendered(t *testing.T) {
	m := newRenderingChatModel(t, "main")
	m.messages = []chatMessage{
		{role: "system", content: "Switched to claude-sonnet-4-6"},
	}
	m.updateViewport()

	view := renderChatView(m)

	if !strings.Contains(view, "Switched to claude-sonnet-4-6") {
		t.Errorf("expected system message body in rendered view, got:\n%s", view)
	}
}

func TestRender_ChatView_ExecModeHelpText(t *testing.T) {
	m := newRenderingChatModel(t, "main")
	m.textarea.SetValue("!ls -la")

	view := renderChatView(m)

	if !strings.Contains(view, "remote command") {
		t.Errorf("expected exec-mode help text when input starts with '!', got:\n%s", view)
	}
}

// newLoadedSelectModel returns a selectModel with agents already loaded.
func newLoadedSelectModel(t *testing.T, agents ...protocol.AgentSummary) selectModel {
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
	return m
}

func TestRender_SelectView_RendersAgentNames(t *testing.T) {
	m := newLoadedSelectModel(t,
		protocol.AgentSummary{ID: "main", Name: "Primary Agent"},
		protocol.AgentSummary{ID: "helper", Name: "Helper Agent"},
	)

	view := ansi.Strip(m.View())

	if !strings.Contains(view, "Primary Agent") {
		t.Errorf("expected 'Primary Agent' in rendered list, got:\n%s", view)
	}
	if !strings.Contains(view, "Helper Agent") {
		t.Errorf("expected 'Helper Agent' in rendered list, got:\n%s", view)
	}
}

func TestRender_SelectView_ShowsCreateHint(t *testing.T) {
	m := newLoadedSelectModel(t,
		protocol.AgentSummary{ID: "main", Name: "Primary"},
		protocol.AgentSummary{ID: "secondary", Name: "Secondary"},
	)

	view := ansi.Strip(m.View())

	if !strings.Contains(view, "n: new agent") {
		t.Errorf("expected 'n: new agent' hint in select view, got:\n%s", view)
	}
}

func TestRender_SelectView_LoadingState(t *testing.T) {
	m := newSelectModel(nil)
	m.setSize(120, 40)

	view := ansi.Strip(m.View())

	if !strings.Contains(view, "Connecting to gateway") {
		t.Errorf("expected loading message in select view, got:\n%s", view)
	}
}

func TestRender_SelectView_CreateFormLabels(t *testing.T) {
	m := newLoadedSelectModel(t,
		protocol.AgentSummary{ID: "main", Name: "Primary"},
		protocol.AgentSummary{ID: "secondary", Name: "Secondary"},
	)
	// Activate the create form.
	m.initCreateForm()

	view := ansi.Strip(m.View())

	for _, want := range []string{"Create new agent", "Name:", "Workspace:", "Tab: switch fields"} {
		if !strings.Contains(view, want) {
			t.Errorf("expected %q in create form, got:\n%s", want, view)
		}
	}
}

func TestRender_SelectView_ErrorStateShowsRetryHint(t *testing.T) {
	m := newSelectModel(nil)
	m.setSize(120, 40)
	m, _ = m.Update(agentsLoadedMsg{err: errString("gateway unreachable")})

	view := ansi.Strip(m.View())

	if !strings.Contains(view, "gateway unreachable") {
		t.Errorf("expected error text in select view, got:\n%s", view)
	}
	if !strings.Contains(view, "'r' to retry") {
		t.Errorf("expected retry hint in select view, got:\n%s", view)
	}
}

// errString is a tiny error helper so tests don't need to import "errors".
type errString string

func (e errString) Error() string { return string(e) }
