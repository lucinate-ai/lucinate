package tui

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// newContextUsageTestModel builds a chatModel wired to fakeBackend with
// a session/agent key set, so the context-usage cmd has somewhere to
// look up. The fake's SessionsList behaviour is staged per-test via
// sessionsListHook.
func newContextUsageTestModel() (*chatModel, *fakeBackend) {
	fb := newFakeBackend()
	vp := viewport.New()
	m := &chatModel{
		viewport:   vp,
		backend:    fb,
		sessionKey: "agent:scout:main",
		agentID:    "scout",
		agentName:  "scout",
		width:      120,
		height:     40,
	}
	return m, fb
}

func TestLoadContextUsage_ReadsFromMatchingSessionEntry(t *testing.T) {
	m, fb := newContextUsageTestModel()
	fb.sessionsListHook = func(ctx context.Context, agentID string) (json.RawMessage, error) {
		return json.RawMessage(`{
			"sessions": [
				{"key": "agent:other:main", "totalTokens": 99999, "contextTokens": 200000},
				{"key": "agent:scout:main", "totalTokens": 65000, "contextTokens": 1000000}
			]
		}`), nil
	}

	msg, ok := m.loadContextUsage()().(contextUsageLoadedMsg)
	if !ok {
		t.Fatalf("expected contextUsageLoadedMsg, got %T", msg)
	}
	if msg.sessionKey != "agent:scout:main" {
		t.Errorf("sessionKey: got %q, want %q", msg.sessionKey, "agent:scout:main")
	}
	if msg.promptTokens != 65000 {
		t.Errorf("promptTokens: got %d, want 65000", msg.promptTokens)
	}
	if msg.contextWindow != 1000000 {
		t.Errorf("contextWindow: got %d, want 1000000", msg.contextWindow)
	}
}

func TestLoadContextUsage_FallsBackToDefaultsContextTokens(t *testing.T) {
	m, fb := newContextUsageTestModel()
	fb.sessionsListHook = func(ctx context.Context, agentID string) (json.RawMessage, error) {
		return json.RawMessage(`{
			"sessions": [
				{"key": "agent:scout:main", "totalTokens": 12345}
			],
			"defaults": {"contextTokens": 500000}
		}`), nil
	}

	msg := m.loadContextUsage()().(contextUsageLoadedMsg)
	if msg.promptTokens != 12345 {
		t.Errorf("promptTokens: got %d, want 12345", msg.promptTokens)
	}
	if msg.contextWindow != 500000 {
		t.Errorf("contextWindow should fall back to defaults.contextTokens: got %d, want 500000", msg.contextWindow)
	}
}

func TestLoadContextUsage_NoMatchingEntryReturnsZeros(t *testing.T) {
	m, fb := newContextUsageTestModel()
	fb.sessionsListHook = func(ctx context.Context, agentID string) (json.RawMessage, error) {
		return json.RawMessage(`{
			"sessions": [
				{"key": "agent:other:main", "totalTokens": 100, "contextTokens": 200}
			]
		}`), nil
	}

	msg := m.loadContextUsage()().(contextUsageLoadedMsg)
	if msg.promptTokens != 0 || msg.contextWindow != 0 {
		t.Errorf("expected zeros for unmatched session, got prompt=%d window=%d", msg.promptTokens, msg.contextWindow)
	}
	if msg.sessionKey != "agent:scout:main" {
		t.Errorf("sessionKey echoed back so handler can drop stale results: got %q", msg.sessionKey)
	}
}

func TestLoadContextUsage_EmptySessionKeyShortCircuits(t *testing.T) {
	m, fb := newContextUsageTestModel()
	m.sessionKey = ""
	called := false
	fb.sessionsListHook = func(ctx context.Context, agentID string) (json.RawMessage, error) {
		called = true
		return json.RawMessage(`{}`), nil
	}

	msg := m.loadContextUsage()().(contextUsageLoadedMsg)
	if called {
		t.Error("SessionsList must not be called when sessionKey is empty")
	}
	if msg.promptTokens != 0 || msg.contextWindow != 0 {
		t.Errorf("expected zeros, got prompt=%d window=%d", msg.promptTokens, msg.contextWindow)
	}
}

func TestLoadContextUsage_RPCErrorIsSwallowed(t *testing.T) {
	m, fb := newContextUsageTestModel()
	fb.sessionsListHook = func(ctx context.Context, agentID string) (json.RawMessage, error) {
		return nil, errors.New("gateway down")
	}

	// The header should never error out the chat view because the
	// percentage couldn't be loaded — the cmd must still emit a
	// well-formed (zero-valued) message.
	msg := m.loadContextUsage()().(contextUsageLoadedMsg)
	if msg.promptTokens != 0 || msg.contextWindow != 0 {
		t.Errorf("expected zeros on RPC error, got prompt=%d window=%d", msg.promptTokens, msg.contextWindow)
	}
}

func TestContextUsageLoadedMsg_IgnoresStaleSession(t *testing.T) {
	m, _ := newContextUsageTestModel()
	m.sessionKey = "agent:scout:main"
	m.promptTokens = 1
	m.contextWindow = 2

	updated, _ := m.Update(contextUsageLoadedMsg{
		sessionKey:    "agent:other:main",
		promptTokens:  9999,
		contextWindow: 8888,
	})

	if updated.promptTokens != 1 || updated.contextWindow != 2 {
		t.Errorf("stale snapshot should not overwrite live state, got prompt=%d window=%d",
			updated.promptTokens, updated.contextWindow)
	}
}

func TestContextUsageLoadedMsg_AppliesMatchingSession(t *testing.T) {
	m, _ := newContextUsageTestModel()

	updated, _ := m.Update(contextUsageLoadedMsg{
		sessionKey:    "agent:scout:main",
		promptTokens:  4242,
		contextWindow: 100000,
	})

	if updated.promptTokens != 4242 {
		t.Errorf("promptTokens: got %d, want 4242", updated.promptTokens)
	}
	if updated.contextWindow != 100000 {
		t.Errorf("contextWindow: got %d, want 100000", updated.contextWindow)
	}
}

func TestModelSwitchedMsg_TriggersContextUsageRefresh(t *testing.T) {
	m, fb := newContextUsageTestModel()
	called := false
	fb.sessionsListHook = func(ctx context.Context, agentID string) (json.RawMessage, error) {
		called = true
		return json.RawMessage(`{"sessions":[]}`), nil
	}

	_, cmd := m.Update(modelSwitchedMsg{modelID: "deepseek/deepseek-v4-flash"})
	if cmd == nil {
		t.Fatal("expected a refresh cmd after model switch")
	}
	// Drain the returned message — the cmd should hit SessionsList
	// because a new model can change the session's contextTokens.
	cmd()
	if !called {
		t.Error("model switch must refresh context usage via SessionsList")
	}
}

func TestHistoryRefreshMsg_TriggersContextUsageRefresh(t *testing.T) {
	m, fb := newContextUsageTestModel()
	called := false
	fb.sessionsListHook = func(ctx context.Context, agentID string) (json.RawMessage, error) {
		called = true
		return json.RawMessage(`{"sessions":[]}`), nil
	}

	_, cmd := m.Update(historyRefreshMsg{messages: []chatMessage{{role: "assistant", content: "ok"}}})
	if cmd == nil {
		t.Fatal("expected a batch cmd that refreshes context usage")
	}
	// historyRefreshMsg fires after every completed turn; both
	// loadStats and loadContextUsage are batched. Run the batch and
	// fish out the SessionsList call.
	drainBatch(t, cmd)
	if !called {
		t.Error("history refresh must re-pull the context-usage snapshot so the % keeps up per turn")
	}
}

// drainBatch executes every leaf cmd produced by tea.Batch so test
// hooks get hit even when the batch wraps multiple commands.
func drainBatch(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			drainBatch(t, sub)
		}
	}
}


// TestChatModel_PreloadedPendingMessage_DrainsOnHistoryLoaded verifies
// the `lucinate chat <message>` auto-submit path: a chatModel
// constructed with an initialMessage queues it so the first
// historyLoadedMsg drains it through drainQueue, surfacing the user
// turn and a streaming assistant placeholder in the visible message
// list. The pre-history-load delay matches what a human typing would
// see (history scrollback first, then their message).
func TestChatModel_PreloadedPendingMessage_DrainsOnHistoryLoaded(t *testing.T) {
	fb := newFakeBackend()
	m := newChatModel(fb, "session-key", "agent-id", "test", "", config.DefaultPreferences(), false, "", "hello", false)

	if len(m.pendingMessages) != 1 || m.pendingMessages[0] != "hello" {
		t.Fatalf("constructor should have queued initialMessage; pendingMessages=%v", m.pendingMessages)
	}
	if m.sending {
		t.Fatal("should not be sending until history loads")
	}
	if len(m.messages) != 0 {
		t.Fatalf("messages should be empty pre-history-load; got %d", len(m.messages))
	}

	var cmd tea.Cmd
	m, cmd = m.Update(historyLoadedMsg{messages: nil, err: nil})

	if cmd == nil {
		t.Fatal("expected drainQueue cmd after history load with a queued message")
	}
	if !m.sending {
		t.Error("sending must be true after drain so subsequent enters queue rather than fire")
	}
	if len(m.pendingMessages) != 0 {
		t.Errorf("pendingMessages should be drained; got %v", m.pendingMessages)
	}
	// drainQueue appends the user turn and a streaming assistant
	// placeholder. The exact role sequence is what the visible chat
	// will render.
	if len(m.messages) != 2 {
		t.Fatalf("expected 2 messages (user + streaming assistant), got %d: %+v", len(m.messages), m.messages)
	}
	if m.messages[0].role != "user" || m.messages[0].content != "hello" {
		t.Errorf("messages[0] = %+v, want user/hello", m.messages[0])
	}
	if m.messages[1].role != "assistant" || !m.messages[1].streaming {
		t.Errorf("messages[1] = %+v, want streaming assistant placeholder", m.messages[1])
	}
}

// TestChatModel_HistoryLoadError_ShowsSystemMessage verifies that
// a failed chat.history fetch (e.g. when the gateway can't resolve a
// cron-isolated session key) surfaces the error to the user instead
// of leaving the view silently blank — the original symptom that
// pressing 'T' on a cron details page produced.
func TestChatModel_HistoryLoadError_ShowsSystemMessage(t *testing.T) {
	fb := newFakeBackend()
	m := newChatModel(fb, "session-key", "agent-id", "test", "", config.DefaultPreferences(), false, "", "", false)

	loadErr := errors.New("gateway returned ENOENT")
	m, _ = m.Update(historyLoadedMsg{messages: nil, err: loadErr})

	if m.historyLoading {
		t.Error("historyLoading should be cleared after the load attempt")
	}
	if len(m.messages) != 1 {
		t.Fatalf("expected a system error message; got %d messages", len(m.messages))
	}
	if m.messages[0].role != "system" || m.messages[0].errMsg == "" {
		t.Errorf("expected system message with errMsg set; got %+v", m.messages[0])
	}
}

// TestChatModel_NoInitialMessage_NoDrain verifies the regression
// case: without a preloaded message, a historyLoadedMsg must not
// kick a stray drain (which would surface as a confused user turn
// with no actual content to send).
func TestChatModel_NoInitialMessage_NoDrain(t *testing.T) {
	fb := newFakeBackend()
	m := newChatModel(fb, "session-key", "agent-id", "test", "", config.DefaultPreferences(), false, "", "", false)

	if len(m.pendingMessages) != 0 {
		t.Fatalf("no initialMessage must mean empty pendingMessages; got %v", m.pendingMessages)
	}

	var cmd tea.Cmd
	m, cmd = m.Update(historyLoadedMsg{messages: nil, err: nil})

	if cmd != nil {
		t.Errorf("no drain expected when pendingMessages empty; got cmd=%v", cmd)
	}
	if m.sending {
		t.Error("sending must remain false")
	}
	if len(m.messages) != 0 {
		t.Errorf("messages should remain empty; got %+v", m.messages)
	}
}

func TestChatModel_TranscriptEsc_ReturnsToCronDetail(t *testing.T) {
	fb := newFakeBackend()
	m := newChatModel(fb, "", "agent-id", "agent-id", "", config.DefaultPreferences(), true, "", "", false)
	m.transcript = true

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("expected esc on transcript chat to emit a back cmd")
	}
	if _, ok := cmd().(goBackFromCronTranscriptMsg); !ok {
		t.Errorf("expected goBackFromCronTranscriptMsg, got %T", cmd())
	}
}

func TestChatModel_NonTranscriptEsc_DoesNothingWhenIdle(t *testing.T) {
	fb := newFakeBackend()
	m := newChatModel(fb, "session-key", "agent-id", "agent-id", "", config.DefaultPreferences(), false, "", "", false)

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd != nil {
		// A regular chat must not bubble a back signal on esc — that path
		// is reserved for the transcript-mode model.
		if _, ok := cmd().(goBackFromCronTranscriptMsg); ok {
			t.Errorf("non-transcript esc must not emit goBackFromCronTranscriptMsg")
		}
	}
}

// TestNewChatModel_DefaultCursorMatchesBubblesPalette guards the
// default branch of the gate: when the embedder hasn't asked for a
// bright cursor (the desktop CLI case), the textarea is left on
// Bubbles' library default — `lipgloss.Color("7")`, ANSI 8-colour
// light grey — so terminals that render that index brightly enough
// keep their familiar appearance.
func TestNewChatModel_DefaultCursorMatchesBubblesPalette(t *testing.T) {
	m := newChatModel(newFakeBackend(), "agent:scout:main", "scout", "scout", "", config.DefaultPreferences(), false, "home", "", false)
	got := m.textarea.Styles().Cursor.Color
	want := lipgloss.Color("7")
	if got != want {
		t.Errorf("default cursor color = %v, want %v (bubbles default ANSI 7) — desktop CLI must keep the library palette",
			got, want)
	}
}

// TestNewChatModel_BrightCursor_PinsToAnsi15 guards the override
// branch: embedders that pass brightCursor=true (typically because
// their host's ANSI 7 mapping is too dim) get the cursor pinned to
// ANSI 15 (bright white) so the reverse-swapped block is visible
// regardless of palette luminance choices.
func TestNewChatModel_BrightCursor_PinsToAnsi15(t *testing.T) {
	m := newChatModel(newFakeBackend(), "agent:scout:main", "scout", "scout", "", config.DefaultPreferences(), false, "home", "", true)
	got := m.textarea.Styles().Cursor.Color
	want := lipgloss.Color("15")
	if got != want {
		t.Errorf("textarea cursor color = %v, want %v (ANSI bright white). brightCursor=true must pin to a guaranteed-visible block.",
			got, want)
	}
}
