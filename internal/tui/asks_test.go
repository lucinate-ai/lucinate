package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// askRecordingBackend embeds the standard fake and records every RespondAsk
// so the ask tests can assert what was sent without a gateway.
type askRecordingBackend struct {
	*fakeBackend
	responses []backend.AskResponse
	err       error
}

func (a *askRecordingBackend) RespondAsk(_ context.Context, resp backend.AskResponse) error {
	a.responses = append(a.responses, resp)
	return a.err
}

func newAskTestModel(t *testing.T, b backend.Backend) chatModel {
	t.Helper()
	m := newChatModel(b, "live1", "hermes", "Hermes", "", config.DefaultPreferences(), false, "", "", false)
	m.width = 80
	m.height = 24
	return m
}

// feedAsk delivers an ask agent event to the model exactly as the gateway
// event pump would.
func feedAsk(t *testing.T, m chatModel, data map[string]any) chatModel {
	t.Helper()
	payload, err := json.Marshal(protocol.AgentEvent{Stream: "ask", Data: data})
	if err != nil {
		t.Fatalf("marshal ask event: %v", err)
	}
	next, _ := m.Update(GatewayEventMsg(protocol.Event{
		EventName: protocol.EventAgent,
		Payload:   payload,
	}))
	return next
}

func pressKey(m chatModel, code rune, text string) (chatModel, tea.Cmd) {
	return m.Update(tea.KeyPressMsg{Code: code, Text: text})
}

func lastSystemLine(m chatModel) string {
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].role == "system" {
			return m.messages[i].content
		}
	}
	return ""
}

func approvalData(withAlways bool) map[string]any {
	data := map[string]any{
		"sessionKey": "live1",
		"requestId":  "req1",
		"rpcId":      7,
		"kind":       "approval",
		"command":    "rm -rf build",
		"cwd":        "/tmp/project",
	}
	if withAlways {
		data["choices"] = []string{"allow-once", "allow-always", "deny"}
	}
	return data
}

func clarifyData(choices []string) map[string]any {
	data := map[string]any{
		"sessionKey": "live1",
		"requestId":  "req2",
		"kind":       "clarify",
		"question":   "Which file should I edit?",
	}
	if choices != nil {
		data["choices"] = choices
	}
	return data
}

func TestAskApproval_YesAllowsOnce(t *testing.T) {
	fb := &askRecordingBackend{fakeBackend: newFakeBackend()}
	m := newAskTestModel(t, fb)
	m = feedAsk(t, m, approvalData(true))
	if m.ask == nil {
		t.Fatal("ask panel not populated")
	}

	m, cmd := pressKey(m, 'y', "y")
	if m.ask != nil {
		t.Fatal("ask panel not cleared after answering")
	}
	if cmd == nil {
		t.Fatal("no respond command returned")
	}
	_ = cmd()
	if len(fb.responses) != 1 {
		t.Fatalf("responses = %+v", fb.responses)
	}
	got := fb.responses[0]
	if got.Kind != "approval" || got.Choice != "allow-once" || got.RequestID != "req1" || got.SessionKey != "live1" {
		t.Fatalf("response = %+v", got)
	}
	if got.RPCID != "7" {
		t.Fatalf("RPCID = %q, want the ask's raw JSON-RPC id", got.RPCID)
	}
	if line := lastSystemLine(m); line != "answered: allow once" {
		t.Fatalf("transcript line = %q", line)
	}
}

func TestAskApproval_NoAndEscDeny(t *testing.T) {
	for _, key := range []struct {
		name string
		code rune
		text string
	}{
		{"n", 'n', "n"},
		{"esc", tea.KeyEsc, ""},
	} {
		t.Run(key.name, func(t *testing.T) {
			fb := &askRecordingBackend{fakeBackend: newFakeBackend()}
			m := newAskTestModel(t, fb)
			m = feedAsk(t, m, approvalData(true))
			m, cmd := pressKey(m, key.code, key.text)
			if m.ask != nil {
				t.Fatal("panel not cleared")
			}
			if cmd == nil {
				t.Fatal("no respond command")
			}
			_ = cmd()
			if len(fb.responses) != 1 || fb.responses[0].Choice != "deny" {
				t.Fatalf("responses = %+v", fb.responses)
			}
			if line := lastSystemLine(m); line != "answered: deny" {
				t.Fatalf("transcript line = %q", line)
			}
		})
	}
}

// The gateway ids its requests with strings; the panel must carry that raw
// id (quoted JSON) back to the backend unchanged.
func TestAsk_StringRPCIDPropagates(t *testing.T) {
	fb := &askRecordingBackend{fakeBackend: newFakeBackend()}
	m := newAskTestModel(t, fb)
	data := approvalData(true)
	data["rpcId"] = "srq-abc123"
	m = feedAsk(t, m, data)

	_, cmd := pressKey(m, 'y', "y")
	if cmd == nil {
		t.Fatal("no respond command")
	}
	_ = cmd()
	if len(fb.responses) != 1 || fb.responses[0].RPCID != `"srq-abc123"` {
		t.Fatalf("responses = %+v", fb.responses)
	}
}

// The choice sent back must be one the gateway offered: the panel maps
// y/a/n onto the payload's own vocabulary, and falls back to the first
// consent (then deny) when the expected word is absent.
func TestAskApproval_ChoiceMatchesGatewayVocabulary(t *testing.T) {
	cases := []struct {
		name    string
		choices []string
		key     rune
		text    string
		want    string
	}{
		{"y maps once", []string{"deny", "once", "session", "always"}, 'y', "y", "once"},
		{"a maps always", []string{"deny", "once", "session", "always"}, 'a', "a", "always"},
		{"s maps session", []string{"deny", "once", "session", "always"}, 's', "s", "session"},
		{"n maps deny", []string{"deny", "once", "session", "always"}, 'n', "n", "deny"},
		{"y matches approve", []string{"deny", "approve"}, 'y', "y", "approve"},
		{"y falls back to first consent", []string{"deny", "session"}, 'y', "y", "session"},
		{"n never falls through to consent", []string{"once", "always"}, 'n', "n", "deny"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fb := &askRecordingBackend{fakeBackend: newFakeBackend()}
			m := newAskTestModel(t, fb)
			data := approvalData(false)
			data["choices"] = tc.choices
			m = feedAsk(t, m, data)

			_, cmd := pressKey(m, tc.key, tc.text)
			if cmd == nil {
				t.Fatal("no respond command")
			}
			_ = cmd()
			if len(fb.responses) != 1 || fb.responses[0].Choice != tc.want {
				t.Fatalf("responses = %+v, want choice %q", fb.responses, tc.want)
			}
		})
	}
}

func TestAskApproval_AlwaysOnlyWhenOffered(t *testing.T) {
	t.Run("offered", func(t *testing.T) {
		fb := &askRecordingBackend{fakeBackend: newFakeBackend()}
		m := newAskTestModel(t, fb)
		m = feedAsk(t, m, approvalData(true))
		m, cmd := pressKey(m, 'a', "a")
		if cmd == nil {
			t.Fatal("a should answer when an always option is offered")
		}
		_ = cmd()
		if len(fb.responses) != 1 || fb.responses[0].Choice != "allow-always" {
			t.Fatalf("responses = %+v", fb.responses)
		}
		if line := lastSystemLine(m); line != "answered: allow always" {
			t.Fatalf("transcript line = %q", line)
		}
	})

	t.Run("not offered", func(t *testing.T) {
		fb := &askRecordingBackend{fakeBackend: newFakeBackend()}
		m := newAskTestModel(t, fb)
		m = feedAsk(t, m, approvalData(false))
		m, cmd := pressKey(m, 'a', "a")
		if cmd != nil {
			_ = cmd()
		}
		if len(fb.responses) != 0 {
			t.Fatalf("a must not answer without an always option: %+v", fb.responses)
		}
		if m.ask == nil {
			t.Fatal("panel should stay open")
		}
	})
}

func TestAskClarify_ChoiceByNumber(t *testing.T) {
	fb := &askRecordingBackend{fakeBackend: newFakeBackend()}
	m := newAskTestModel(t, fb)
	m = feedAsk(t, m, clarifyData([]string{"main.go", "README.md"}))

	m, cmd := pressKey(m, '2', "2")
	if cmd == nil {
		t.Fatal("no respond command")
	}
	_ = cmd()
	if len(fb.responses) != 1 || fb.responses[0].Answer != "README.md" || fb.responses[0].Kind != "clarify" {
		t.Fatalf("responses = %+v", fb.responses)
	}
	if line := lastSystemLine(m); line != "answered: README.md" {
		t.Fatalf("transcript line = %q", line)
	}
}

func TestAskClarify_FreeTextAndEsc(t *testing.T) {
	t.Run("free text", func(t *testing.T) {
		fb := &askRecordingBackend{fakeBackend: newFakeBackend()}
		m := newAskTestModel(t, fb)
		m = feedAsk(t, m, clarifyData(nil))

		for _, r := range []rune("main.go") {
			m, _ = pressKey(m, r, string(r))
		}
		if m.ask == nil || m.ask.answer != "main.go" {
			t.Fatalf("answer buffer = %+v", m.ask)
		}
		if m.textarea.Value() != "" {
			t.Fatalf("composer was edited: %q", m.textarea.Value())
		}

		m, cmd := pressKey(m, tea.KeyEnter, "")
		if cmd == nil {
			t.Fatal("no respond command")
		}
		_ = cmd()
		if len(fb.responses) != 1 || fb.responses[0].Answer != "main.go" {
			t.Fatalf("responses = %+v", fb.responses)
		}
	})

	t.Run("esc skips with empty answer", func(t *testing.T) {
		fb := &askRecordingBackend{fakeBackend: newFakeBackend()}
		m := newAskTestModel(t, fb)
		m = feedAsk(t, m, clarifyData(nil))

		m, cmd := pressKey(m, tea.KeyEsc, "")
		if cmd == nil {
			t.Fatal("no respond command")
		}
		_ = cmd()
		if len(fb.responses) != 1 || fb.responses[0].Answer != "" {
			t.Fatalf("responses = %+v", fb.responses)
		}
	})
}

// A typed draft is the user's work: while the composer is non-empty every
// key, answer keys included, goes to the textarea as normal. No answer is
// sent and the panel stays open.
func TestAskApproval_DraftIsNotEaten(t *testing.T) {
	fb := &askRecordingBackend{fakeBackend: newFakeBackend()}
	m := newAskTestModel(t, fb)
	m.textarea.SetValue("draft message")
	m = feedAsk(t, m, approvalData(true))

	for _, r := range []rune("yes") {
		next, cmd := pressKey(m, r, string(r))
		m = next
		if cmd != nil {
			_ = cmd()
		}
	}
	if len(fb.responses) != 0 {
		t.Fatalf("a draft must block answers: %+v", fb.responses)
	}
	if got := m.textarea.Value(); got != "draft messageyes" {
		t.Fatalf("draft = %q, want %q", got, "draft messageyes")
	}
	if m.ask == nil {
		t.Fatal("panel should stay open while the draft is non-empty")
	}
}

func TestAskPanel_RendersCommandAndKeys(t *testing.T) {
	fb := &askRecordingBackend{fakeBackend: newFakeBackend()}
	m := newAskTestModel(t, fb)
	data := approvalData(true)
	data["description"] = "The agent wants to remove the build directory."
	m = feedAsk(t, m, data)

	panel := m.renderAskPanel()
	for _, want := range []string{"rm -rf build", "/tmp/project", "The agent wants to remove", "y allow once", "a allow always", "n deny"} {
		if !strings.Contains(panel, want) {
			t.Errorf("panel missing %q:\n%s", want, panel)
		}
	}

	m2 := newAskTestModel(t, fb)
	m2 = feedAsk(t, m2, clarifyData([]string{"main.go", "README.md"}))
	panel = m2.renderAskPanel()
	for _, want := range []string{"Which file should I edit?", "1) main.go", "2) README.md"} {
		if !strings.Contains(panel, want) {
			t.Errorf("clarify panel missing %q:\n%s", want, panel)
		}
	}
}

// request.cancel (an ask event of kind cancel) closes the panel for that id
// without answering.
func TestAsk_CancelClosesPanel(t *testing.T) {
	fb := &askRecordingBackend{fakeBackend: newFakeBackend()}
	m := newAskTestModel(t, fb)
	m = feedAsk(t, m, approvalData(true))
	if m.ask == nil {
		t.Fatal("panel not populated")
	}
	data := map[string]any{
		"sessionKey": "live1",
		"requestId":  "req1",
		"kind":       "cancel",
	}
	m = feedAsk(t, m, data)
	if m.ask != nil {
		t.Fatal("cancel did not close the panel")
	}
	if len(fb.responses) != 0 {
		t.Fatalf("cancel must not answer: %+v", fb.responses)
	}
}

// A backend that does not implement AskBackend leaves a dim line instead of
// silently swallowing the answer.
func TestAsk_BackendWithoutCapability(t *testing.T) {
	m := newAskTestModel(t, newFakeBackend())
	m = feedAsk(t, m, approvalData(true))

	m, cmd := pressKey(m, 'y', "y")
	if cmd != nil {
		t.Fatal("no respond command expected from a backend without AskBackend")
	}
	if m.ask != nil {
		t.Fatal("panel should be cleared")
	}
	if line := lastSystemLine(m); !strings.Contains(line, "cannot answer") {
		t.Fatalf("expected a capability notice, got %q", line)
	}
}
