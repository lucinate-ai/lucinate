package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/a3tai/openclaw-go/protocol"
	"charm.land/bubbles/v2/viewport"
)

// makeChatEvent builds a protocol.Event wrapping a ChatEvent payload.
func makeChatEvent(state, runID string, seq int, message json.RawMessage) protocol.Event {
	return makeChatEventForSession(state, runID, "", seq, message)
}

// makeChatEventForSession builds a protocol.Event with an explicit sessionKey.
func makeChatEventForSession(state, runID, sessionKey string, seq int, message json.RawMessage) protocol.Event {
	chatEv := protocol.ChatEvent{
		RunID:      runID,
		SessionKey: sessionKey,
		State:      state,
		Seq:        seq,
		Message:    message,
	}
	payload, _ := json.Marshal(chatEv)
	return protocol.Event{
		EventName: protocol.EventChat,
		Payload:   payload,
	}
}

func makeChatEventWithError(state, runID, errMsg string) protocol.Event {
	chatEv := protocol.ChatEvent{
		RunID:        runID,
		State:        state,
		ErrorMessage: errMsg,
	}
	payload, _ := json.Marshal(chatEv)
	return protocol.Event{
		EventName: protocol.EventChat,
		Payload:   payload,
	}
}

// newTestChatModel creates a minimal chatModel suitable for unit tests.
func newTestChatModel() *chatModel {
	vp := viewport.New()
	return &chatModel{
		viewport:  vp,
		backend:   newFakeBackend(),
		agentName: "test",
		width:     80,
		height:    30,
	}
}

func TestExtractTextFromMessage_DeltaString(t *testing.T) {
	raw := json.RawMessage(`"Hello, world!"`)
	got := extractTextFromMessage(raw)
	if got != "Hello, world!" {
		t.Errorf("got %q, want %q", got, "Hello, world!")
	}
}

func TestExtractTextFromMessage_FinalStructured(t *testing.T) {
	raw := json.RawMessage(`{
		"role": "assistant",
		"content": [
			{"type": "text", "text": "First paragraph."},
			{"type": "text", "text": "Second paragraph."}
		],
		"timestamp": 1776540452625
	}`)
	got := extractTextFromMessage(raw)
	want := "First paragraph.\nSecond paragraph."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractTextFromMessage_FinalWithNonTextBlocks(t *testing.T) {
	raw := json.RawMessage(`{
		"role": "assistant",
		"content": [
			{"type": "tool_use", "text": ""},
			{"type": "text", "text": "Visible text."}
		]
	}`)
	got := extractTextFromMessage(raw)
	if got != "Visible text." {
		t.Errorf("got %q, want %q", got, "Visible text.")
	}
}

func TestExtractTextFromMessage_EmptyContent(t *testing.T) {
	raw := json.RawMessage(`{"role": "assistant", "content": []}`)
	got := extractTextFromMessage(raw)
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestExtractTextFromMessage_EmptyInput(t *testing.T) {
	got := extractTextFromMessage(nil)
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}

	got = extractTextFromMessage(json.RawMessage{})
	if got != "" {
		t.Errorf("got %q for empty slice, want empty string", got)
	}
}

func TestExtractTextFromMessage_Fallback(t *testing.T) {
	raw := json.RawMessage(`12345`)
	got := extractTextFromMessage(raw)
	if got != "12345" {
		t.Errorf("got %q, want %q", got, "12345")
	}
}

func TestHandleEvent_DeltaCreatesAssistantMessage(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{{role: "user", content: "hello"}}

	m.handleEvent(makeChatEvent("delta", "run1", 1, json.RawMessage(`"First chunk"`)))

	if len(m.messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(m.messages))
	}
	if m.messages[1].role != "assistant" {
		t.Errorf("role = %q, want assistant", m.messages[1].role)
	}
	if m.messages[1].content != "First chunk" {
		t.Errorf("content = %q, want %q", m.messages[1].content, "First chunk")
	}
	if !m.messages[1].streaming {
		t.Error("expected streaming = true")
	}
}

func TestHandleEvent_DeltasAreCumulative(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{{role: "user", content: "hello"}}

	m.handleEvent(makeChatEvent("delta", "run1", 1, json.RawMessage(`"Hello"`)))
	m.handleEvent(makeChatEvent("delta", "run1", 2, json.RawMessage(`"Hello world"`)))

	if len(m.messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(m.messages))
	}
	if m.messages[1].content != "Hello world" {
		t.Errorf("content = %q, want %q", m.messages[1].content, "Hello world")
	}
}

func TestHandleEvent_DeltaIgnoredAfterFinalised(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "done", streaming: false},
	}

	m.handleEvent(makeChatEvent("delta", "run1", 5, json.RawMessage(`"late delta"`)))

	if len(m.messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(m.messages))
	}
	if m.messages[1].content != "done" {
		t.Errorf("content changed to %q, should have stayed %q", m.messages[1].content, "done")
	}
}

func TestHandleEvent_FinalMarksStreamingDone(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "response text", streaming: true},
	}

	finalMsg := json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"response text"}],"timestamp":123}`)
	m.handleEvent(makeChatEvent("final", "run1", 3, finalMsg))

	if m.messages[1].streaming {
		t.Error("expected streaming = false after final")
	}
	if m.sending {
		t.Error("expected sending = false after final")
	}
}

func TestHandleEvent_FinalReturnsRefreshCmd(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "text", streaming: true},
	}

	finalMsg := json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"text"}],"timestamp":123}`)
	cmd := m.handleEvent(makeChatEvent("final", "run1", 3, finalMsg))

	if cmd == nil {
		t.Error("expected a non-nil cmd from final event")
	}
}

func TestHandleEvent_FinalWithNoMessages(t *testing.T) {
	m := newTestChatModel()
	m.messages = nil

	finalMsg := json.RawMessage(`{"role":"assistant","content":[],"timestamp":123}`)
	cmd := m.handleEvent(makeChatEvent("final", "run1", 1, finalMsg))

	// No streaming assistant message to finalise — treated as a gateway ack.
	if cmd != nil {
		t.Error("expected nil cmd for final with no streaming assistant message")
	}
}

func TestHandleEvent_ErrorSetsErrMsg(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "partial", streaming: true},
	}

	m.handleEvent(makeChatEventWithError("error", "run1", "something went wrong"))

	if m.messages[1].streaming {
		t.Error("expected streaming = false after error")
	}
	if m.messages[1].errMsg != "something went wrong" {
		t.Errorf("errMsg = %q", m.messages[1].errMsg)
	}
	if m.sending {
		t.Error("expected sending = false after error")
	}
}

func TestHandleEvent_AbortedAppendsMarker(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "partial", streaming: true},
	}

	m.handleEvent(makeChatEvent("aborted", "run1", 3, nil))

	if m.messages[1].streaming {
		t.Error("expected streaming = false after aborted")
	}
	if m.messages[1].content != "partial\n[aborted]" {
		t.Errorf("content = %q", m.messages[1].content)
	}
}

func TestHandleEvent_FinalClearsRunID(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.runID = "run1"
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "response", streaming: true},
	}

	finalMsg := json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"response"}],"timestamp":123}`)
	m.handleEvent(makeChatEvent("final", "run1", 3, finalMsg))

	if m.runID != "" {
		t.Errorf("expected runID to be cleared, got %q", m.runID)
	}
}

func TestHandleEvent_ErrorClearsRunID(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.runID = "run1"
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "partial", streaming: true},
	}

	m.handleEvent(makeChatEventWithError("error", "run1", "something went wrong"))

	if m.runID != "" {
		t.Errorf("expected runID to be cleared, got %q", m.runID)
	}
}

func TestHandleEvent_AbortedClearsRunID(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.runID = "run1"
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "partial", streaming: true},
	}

	m.handleEvent(makeChatEvent("aborted", "run1", 3, nil))

	if m.runID != "" {
		t.Errorf("expected runID to be cleared, got %q", m.runID)
	}
}

func TestChatSentMsg_StoresRunID(t *testing.T) {
	m := newTestChatModel()
	m.sending = true

	updated, _ := m.Update(chatSentMsg{runID: "run-42"})

	if updated.runID != "run-42" {
		t.Errorf("expected runID %q, got %q", "run-42", updated.runID)
	}
}

func TestDrainQueue_SendsFirstPendingMessage(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.pendingMessages = []string{"msg1", "msg2", "msg3"}
	m.messages = []chatMessage{
		{role: "user", content: "original"},
		{role: "assistant", content: "response", streaming: false},
	}

	cmd := m.drainQueue()

	if cmd == nil {
		t.Fatal("expected non-nil cmd from drainQueue")
	}
	if len(m.pendingMessages) != 2 {
		t.Errorf("expected 2 remaining pending messages, got %d", len(m.pendingMessages))
	}
	if m.pendingMessages[0] != "msg2" {
		t.Errorf("expected next pending to be %q, got %q", "msg2", m.pendingMessages[0])
	}
	if !m.sending {
		t.Error("expected sending to remain true while queue is non-empty")
	}
	// User message and thinking placeholder should be appended when dequeued.
	n := len(m.messages)
	userMsg := m.messages[n-2]
	if userMsg.role != "user" || userMsg.content != "msg1" {
		t.Errorf("expected second-to-last message to be user 'msg1', got %s %q", userMsg.role, userMsg.content)
	}
	placeholder := m.messages[n-1]
	if placeholder.role != "assistant" || !placeholder.streaming || !placeholder.awaitingDelta {
		t.Errorf("expected thinking placeholder (assistant streaming awaitingDelta), got %s streaming=%v awaitingDelta=%v", placeholder.role, placeholder.streaming, placeholder.awaitingDelta)
	}
}

func TestDrainQueue_EmptyQueueSetsSendingFalse(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.pendingMessages = nil

	cmd := m.drainQueue()

	if cmd == nil {
		t.Fatal("expected non-nil cmd (refresh) from drainQueue on empty queue")
	}
	if m.sending {
		t.Error("expected sending = false when queue is empty")
	}
}

func TestQueuedMessages_NotInMessagesUntilDrained(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.messages = []chatMessage{
		{role: "user", content: "original"},
	}
	m.pendingMessages = []string{"queued1", "queued2"}

	// Pending messages should not be in m.messages.
	for _, msg := range m.messages {
		if msg.content == "queued1" || msg.content == "queued2" {
			t.Error("queued messages should not be in m.messages before draining")
		}
	}

	// Drain first message.
	m.drainQueue()
	found := false
	for _, msg := range m.messages {
		if msg.role == "user" && msg.content == "queued1" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'queued1' in m.messages after draining")
	}

	// Second queued message should still not be in m.messages.
	for _, msg := range m.messages {
		if msg.content == "queued2" {
			t.Error("'queued2' should not be in m.messages yet")
		}
	}
}

func TestFinalEvent_EmptyAckDoesNotDrain(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.pendingMessages = []string{"queued"}
	// Reflect real runtime state: a spinner placeholder is present but no delta has arrived.
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", streaming: true, awaitingDelta: true},
	}

	// An early final ack from the gateway, arriving before any delta.
	finalMsg := json.RawMessage(`{"role":"assistant","content":[],"timestamp":123}`)
	cmd := m.handleEvent(makeChatEvent("final", "run1", 1, finalMsg))

	if cmd != nil {
		t.Error("expected nil cmd — placeholder should not be treated as a finalised response")
	}
	if len(m.pendingMessages) != 1 {
		t.Errorf("queue should be unchanged, got %d pending", len(m.pendingMessages))
	}
	if !m.sending {
		t.Error("sending should remain true — ack should not reset it")
	}
	if !m.messages[1].streaming {
		t.Error("placeholder should remain streaming")
	}
}

func TestFinalEvent_DrainAfterStreamingResponse(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.pendingMessages = []string{"next msg"}
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "response", streaming: true},
	}

	finalMsg := json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"response"}],"timestamp":123}`)
	cmd := m.handleEvent(makeChatEvent("final", "run1", 3, finalMsg))

	if cmd == nil {
		t.Fatal("expected non-nil cmd to drain the queue")
	}
	if m.messages[1].streaming {
		t.Error("assistant message should be finalised")
	}
	if len(m.pendingMessages) != 0 {
		t.Errorf("expected queue to be drained, got %d pending", len(m.pendingMessages))
	}
	// The dequeued user message and thinking placeholder should be in m.messages.
	n := len(m.messages)
	userMsg := m.messages[n-2]
	if userMsg.role != "user" || userMsg.content != "next msg" {
		t.Errorf("expected dequeued user message at second-to-last, got %s %q", userMsg.role, userMsg.content)
	}
	placeholder := m.messages[n-1]
	if placeholder.role != "assistant" || !placeholder.streaming || !placeholder.awaitingDelta {
		t.Errorf("expected thinking placeholder (assistant streaming awaitingDelta), got %s streaming=%v awaitingDelta=%v", placeholder.role, placeholder.streaming, placeholder.awaitingDelta)
	}
}

func TestDeltaAfterDrain_CreatesNewAssistant(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.messages = []chatMessage{
		{role: "user", content: "first"},
		{role: "assistant", content: "reply", streaming: false},
		{role: "user", content: "second"}, // appended by drainQueue
	}

	// A delta for the response to "second" should create a new assistant message,
	// not be ignored because of the earlier finalised assistant.
	m.handleEvent(makeChatEvent("delta", "run2", 1, json.RawMessage(`"new reply"`)))

	if len(m.messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(m.messages))
	}
	last := m.messages[3]
	if last.role != "assistant" || last.content != "new reply" || !last.streaming {
		t.Errorf("expected new streaming assistant, got role=%s content=%q streaming=%v",
			last.role, last.content, last.streaming)
	}
}

func TestDrainQueueSkipRefresh_EmptyQueueNoRefresh(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.pendingMessages = nil

	cmd := m.drainQueueSkipRefresh()

	if m.sending {
		t.Error("expected sending = false")
	}
	// drainQueueSkipRefresh with empty queue should return nil (no refresh).
	if cmd != nil {
		t.Error("expected nil cmd (no refresh)")
	}
}

func TestDrainQueueSkipRefresh_DrainsPendingMessages(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.pendingMessages = []string{"queued"}

	cmd := m.drainQueueSkipRefresh()

	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	if len(m.pendingMessages) != 0 {
		t.Errorf("expected 0 pending, got %d", len(m.pendingMessages))
	}
}

func TestDrainQueueOpt_LocalExecCommandInQueue(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.pendingMessages = []string{"!ls -la"}

	cmd := m.drainQueue()

	if cmd == nil {
		t.Fatal("expected non-nil cmd for local exec command in queue")
	}
	// Should have added "$ ls -la" and "running..." messages.
	found := false
	for _, msg := range m.messages {
		if msg.content == "$ ls -la" {
			found = true
		}
	}
	if !found {
		t.Error("expected local exec command message in messages")
	}
}

func TestDrainQueueOpt_RemoteExecCommandInQueue(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.pendingMessages = []string{"!!uptime"}

	cmd := m.drainQueue()

	if cmd == nil {
		t.Fatal("expected non-nil cmd for remote exec command in queue")
	}
	// Should have added "!! uptime" and "running on gateway..." messages.
	foundCmd := false
	foundRunning := false
	for _, msg := range m.messages {
		if msg.content == "!! uptime" {
			foundCmd = true
		}
		if msg.content == "running on gateway..." {
			foundRunning = true
		}
	}
	if !foundCmd {
		t.Error("expected remote exec command message in messages")
	}
	if !foundRunning {
		t.Error("expected 'running on gateway...' message")
	}
}

func TestDrainQueueOpt_EmptyLocalExecCommandIgnored(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.pendingMessages = []string{"!"}

	cmd := m.drainQueue()

	// "!" with nothing after it should set sending=false and return nil.
	if m.sending {
		t.Error("expected sending = false for empty exec command")
	}
	if cmd != nil {
		t.Error("expected nil cmd for empty exec command")
	}
}

func TestDrainQueueOpt_EmptyRemoteExecCommandIgnored(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.pendingMessages = []string{"!!"}

	cmd := m.drainQueue()

	if m.sending {
		t.Error("expected sending = false for empty remote exec command")
	}
	if cmd != nil {
		t.Error("expected nil cmd for empty remote exec command")
	}
}

func TestLocalExecFinishedMsg_UpdatesMessage(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.messages = []chatMessage{
		{role: "system", content: "$ echo hello"},
		{role: "system", content: "running..."},
	}

	updated, _ := m.Update(localExecFinishedMsg{output: "hello", exitCode: 0})

	last := updated.messages[len(updated.messages)-1]
	if last.content != "hello" {
		t.Errorf("expected output 'hello', got %q", last.content)
	}
}

func TestLocalExecFinishedMsg_NoOutput(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.messages = []chatMessage{
		{role: "system", content: "$ true"},
		{role: "system", content: "running..."},
	}

	updated, _ := m.Update(localExecFinishedMsg{output: "", exitCode: 0})

	last := updated.messages[len(updated.messages)-1]
	if last.content != "(no output)" {
		t.Errorf("expected '(no output)', got %q", last.content)
	}
}

func TestLocalExecFinishedMsg_NonZeroExitCode(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.messages = []chatMessage{
		{role: "system", content: "$ false"},
		{role: "system", content: "running..."},
	}

	updated, _ := m.Update(localExecFinishedMsg{output: "error output", exitCode: 1})

	last := updated.messages[len(updated.messages)-1]
	if last.content != "error output\nexit code: 1" {
		t.Errorf("expected exit code in output, got %q", last.content)
	}
}

func TestLocalExecFinishedMsg_Error(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.messages = []chatMessage{
		{role: "system", content: "$ badcmd"},
		{role: "system", content: "running..."},
	}

	updated, _ := m.Update(localExecFinishedMsg{err: errString("exec: not found")})

	last := updated.messages[len(updated.messages)-1]
	if last.errMsg != "exec: not found" {
		t.Errorf("expected error message, got errMsg=%q", last.errMsg)
	}
}

func TestChatUpdate_SessionCompactedMsg_Success(t *testing.T) {
	m := newTestChatModel()
	initialCount := len(m.messages)

	updated, _ := m.Update(sessionCompactedMsg{err: nil})

	if len(updated.messages) != initialCount+1 {
		t.Fatalf("expected %d messages, got %d", initialCount+1, len(updated.messages))
	}
	last := updated.messages[len(updated.messages)-1]
	if last.role != "system" || last.content != "Session compacted." {
		t.Errorf("unexpected message: role=%q content=%q", last.role, last.content)
	}
}

// TestChatUpdate_SessionCompactedMsg_ReplacesPendingPlaceholder verifies
// that the pending spinner placeholder posted on confirmation is
// rewritten in place rather than appended after — otherwise the user
// would see "Compacting session..." stuck above "Session compacted."
// instead of the placeholder turning into the outcome.
func TestChatUpdate_SessionCompactedMsg_ReplacesPendingPlaceholder(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{
		{role: "system", content: "Confirmed."},
		{role: "system", content: "Compacting session...", pending: true},
	}

	updated, _ := m.Update(sessionCompactedMsg{err: nil})

	if len(updated.messages) != 2 {
		t.Fatalf("expected placeholder rewritten in place (2 messages), got %d: %+v", len(updated.messages), updated.messages)
	}
	got := updated.messages[1]
	if got.pending {
		t.Errorf("placeholder still flagged pending after success — spinner would never stop")
	}
	if got.content != "Session compacted." {
		t.Errorf("expected placeholder replaced with success line, got %q", got.content)
	}
}

// TestChatUpdate_SessionCompactedMsg_ErrorReplacesPendingPlaceholder is
// the symmetric error-path check: a failed compact must clear the
// pending flag (so the spinner stops) and surface the error in place
// of the placeholder content.
func TestChatUpdate_SessionCompactedMsg_ErrorReplacesPendingPlaceholder(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{
		{role: "system", content: "Compacting session...", pending: true},
	}

	updated, _ := m.Update(sessionCompactedMsg{err: errString("boom")})

	if len(updated.messages) != 1 {
		t.Fatalf("expected placeholder rewritten in place (1 message), got %d: %+v", len(updated.messages), updated.messages)
	}
	got := updated.messages[0]
	if got.pending {
		t.Errorf("placeholder still flagged pending after error — spinner would never stop")
	}
	if got.errMsg == "" || !strings.Contains(got.errMsg, "boom") {
		t.Errorf("expected placeholder replaced with error message, got %+v", got)
	}
}

// TestHasStreamingMessage_PendingSystemRow keeps the spinner ticking on
// pending system rows (the /compact and /reset placeholders). Without
// this branch the spinner would stop on the next tick because no
// assistant or tool message is in flight.
func TestHasStreamingMessage_PendingSystemRow(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{
		{role: "user", content: "hi"},
		{role: "assistant", content: "hello"},
	}
	if m.hasStreamingMessage() {
		t.Error("precondition: no streaming/tool/pending rows should mean false")
	}

	m.messages = append(m.messages, chatMessage{role: "system", content: "Compacting session...", pending: true})
	if !m.hasStreamingMessage() {
		t.Error("expected pending system row to keep the spinner ticking")
	}
}

func TestChatUpdate_SessionCompactedMsg_Error(t *testing.T) {
	m := newTestChatModel()
	updated, _ := m.Update(sessionCompactedMsg{err: errString("gateway error")})
	last := updated.messages[len(updated.messages)-1]
	if last.errMsg == "" {
		t.Error("expected error message")
	}
}

func TestChatUpdate_SessionClearedMsg_Success(t *testing.T) {
	m := newTestChatModel()
	m.sessionKey = "old-key"
	m.messages = []chatMessage{{role: "user", content: "hello"}}

	updated, _ := m.Update(sessionClearedMsg{newSessionKey: "new-key"})

	if updated.sessionKey != "new-key" {
		t.Errorf("expected session key %q, got %q", "new-key", updated.sessionKey)
	}
	if len(updated.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(updated.messages))
	}
	if updated.messages[0].content != "Session cleared. Starting fresh." {
		t.Errorf("unexpected message: %q", updated.messages[0].content)
	}
}

func TestChatUpdate_SessionClearedMsg_Error(t *testing.T) {
	m := newTestChatModel()
	m.sessionKey = "old-key"

	updated, _ := m.Update(sessionClearedMsg{err: errString("delete failed")})

	if updated.sessionKey != "old-key" {
		t.Error("session key should not change on error")
	}
	last := updated.messages[len(updated.messages)-1]
	if last.errMsg == "" {
		t.Error("expected error message")
	}
}

func TestThinkingChangedMsg_SetsLevel(t *testing.T) {
	m := newTestChatModel()
	updated, _ := m.Update(thinkingChangedMsg{level: "medium"})
	if updated.thinkingLevel != "medium" {
		t.Errorf("expected thinkingLevel 'medium', got %q", updated.thinkingLevel)
	}
	last := updated.messages[len(updated.messages)-1]
	if last.role != "system" || last.content != "Thinking level set to medium" {
		t.Errorf("unexpected message: role=%q content=%q", last.role, last.content)
	}
}

func TestThinkingChangedMsg_OffLevel(t *testing.T) {
	m := newTestChatModel()
	m.thinkingLevel = "high"
	updated, _ := m.Update(thinkingChangedMsg{level: "off"})
	if updated.thinkingLevel != "off" {
		t.Errorf("expected thinkingLevel 'off', got %q", updated.thinkingLevel)
	}
	last := updated.messages[len(updated.messages)-1]
	if last.content != "Thinking level set to off" {
		t.Errorf("unexpected message: %q", last.content)
	}
}

func TestThinkingChangedMsg_Error(t *testing.T) {
	m := newTestChatModel()
	m.thinkingLevel = "low"
	updated, _ := m.Update(thinkingChangedMsg{level: "high", err: errString("patch failed")})
	if updated.thinkingLevel != "low" {
		t.Error("thinkingLevel should not change on error")
	}
	last := updated.messages[len(updated.messages)-1]
	if last.errMsg != "patch failed" {
		t.Errorf("expected error message, got %q", last.errMsg)
	}
}

func TestHandleThinkCommand_NoArg_ShowsCurrent(t *testing.T) {
	m := newTestChatModel()
	m.thinkingLevel = "low"
	handled, cmd := m.handleThinkCommand("/think")
	if !handled {
		t.Error("expected handled = true")
	}
	if cmd != nil {
		t.Error("expected nil cmd for status query")
	}
	last := m.messages[len(m.messages)-1]
	if last.role != "system" {
		t.Errorf("expected system message, got role=%q", last.role)
	}
	if !strings.Contains(last.content, "low") {
		t.Errorf("expected current level in message, got %q", last.content)
	}
}

func TestHandleThinkCommand_NoArg_DefaultMessage(t *testing.T) {
	m := newTestChatModel()
	// thinkingLevel is "" (unset)
	handled, cmd := m.handleThinkCommand("/think")
	if !handled {
		t.Error("expected handled = true")
	}
	if cmd != nil {
		t.Error("expected nil cmd")
	}
	last := m.messages[len(m.messages)-1]
	if !strings.Contains(last.content, "gateway default") {
		t.Errorf("expected 'gateway default' in message, got %q", last.content)
	}
}

func TestHandleThinkCommand_ValidLevel_ReturnsCmd(t *testing.T) {
	m := newTestChatModel()
	for _, level := range thinkingLevels {
		m.messages = nil
		handled, cmd := m.handleThinkCommand("/think " + level)
		if !handled {
			t.Errorf("level %q: expected handled = true", level)
		}
		if cmd == nil {
			t.Errorf("level %q: expected non-nil cmd", level)
		}
	}
}

func TestHandleThinkCommand_InvalidLevel_ReturnsError(t *testing.T) {
	m := newTestChatModel()
	handled, cmd := m.handleThinkCommand("/think turbo")
	if !handled {
		t.Error("expected handled = true")
	}
	if cmd != nil {
		t.Error("expected nil cmd for invalid level")
	}
	last := m.messages[len(m.messages)-1]
	if last.errMsg == "" {
		t.Error("expected error message for unknown level")
	}
	if !strings.Contains(last.errMsg, "turbo") {
		t.Errorf("expected level name in error, got %q", last.errMsg)
	}
}

func TestHandleEvent_NonChatEventIgnored(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{{role: "user", content: "hello"}}

	m.handleEvent(protocol.Event{EventName: "tick"})

	if len(m.messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(m.messages))
	}
}

func TestHandleEvent_ExecFinished_UpdatesMessage(t *testing.T) {
	m := newTestChatModel()
	m.sessionKey = "sess-1"
	m.messages = []chatMessage{
		{role: "system", content: "$ ls"},
		{role: "system", content: "running on gateway..."},
	}
	finished := protocol.ExecFinished{
		SessionKey: "sess-1",
		Command:    "ls",
		Output:     "file1.txt\nfile2.txt",
	}
	payload, _ := json.Marshal(finished)
	m.handleEvent(protocol.Event{EventName: protocol.EventExecFinished, Payload: payload})
	last := m.messages[len(m.messages)-1]
	if last.content != "file1.txt\nfile2.txt" {
		t.Errorf("expected output in message, got %q", last.content)
	}
}

func TestHandleEvent_ExecFinished_NoOutput(t *testing.T) {
	m := newTestChatModel()
	m.sessionKey = "sess-1"
	m.messages = []chatMessage{
		{role: "system", content: "$ touch foo"},
		{role: "system", content: "running on gateway..."},
	}
	finished := protocol.ExecFinished{SessionKey: "sess-1", Output: ""}
	payload, _ := json.Marshal(finished)
	m.handleEvent(protocol.Event{EventName: protocol.EventExecFinished, Payload: payload})
	last := m.messages[len(m.messages)-1]
	if last.content != "(no output)" {
		t.Errorf("expected '(no output)', got %q", last.content)
	}
}

func TestHandleEvent_ExecFinished_NonZeroExitCode(t *testing.T) {
	m := newTestChatModel()
	m.sessionKey = "sess-1"
	m.messages = []chatMessage{
		{role: "system", content: "$ false"},
		{role: "system", content: "running on gateway..."},
	}
	exitCode := 1
	finished := protocol.ExecFinished{SessionKey: "sess-1", ExitCode: &exitCode, Output: "error output"}
	payload, _ := json.Marshal(finished)
	m.handleEvent(protocol.Event{EventName: protocol.EventExecFinished, Payload: payload})
	last := m.messages[len(m.messages)-1]
	if last.content != "error output\nexit code: 1" {
		t.Errorf("expected exit code in output, got %q", last.content)
	}
}

func TestHandleEvent_ExecFinished_DifferentSession_Ignored(t *testing.T) {
	m := newTestChatModel()
	m.sessionKey = "sess-1"
	m.messages = []chatMessage{
		{role: "system", content: "running on gateway..."},
	}
	finished := protocol.ExecFinished{SessionKey: "other-session", Output: "should not appear"}
	payload, _ := json.Marshal(finished)
	m.handleEvent(protocol.Event{EventName: protocol.EventExecFinished, Payload: payload})
	if m.messages[0].content != "running on gateway..." {
		t.Error("message should be unchanged for different session")
	}
}

func TestHandleEvent_ExecApprovalResolved_Deny(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{
		{role: "system", content: "running on gateway..."},
	}
	resolved := protocol.ExecApprovalResolvedEvent{ID: "req-1", Decision: "deny"}
	payload, _ := json.Marshal(resolved)
	m.handleEvent(protocol.Event{EventName: "exec.approval.resolved", Payload: payload})
	last := m.messages[0]
	if last.errMsg != "command execution denied" {
		t.Errorf("expected denial error, got errMsg=%q content=%q", last.errMsg, last.content)
	}
}

func TestHandleEvent_ExecApprovalResolved_Allow(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{
		{role: "system", content: "running on gateway..."},
	}
	resolved := protocol.ExecApprovalResolvedEvent{ID: "req-1", Decision: "allow-once"}
	payload, _ := json.Marshal(resolved)
	cmd := m.handleEvent(protocol.Event{EventName: "exec.approval.resolved", Payload: payload})
	// Allow should return nil — exec.finished will follow.
	if cmd != nil {
		t.Error("expected nil cmd for allow decision")
	}
	if m.messages[0].content != "running on gateway..." {
		t.Error("message should be unchanged for allow decision")
	}
}

func TestHandleEvent_ExecDenied(t *testing.T) {
	m := newTestChatModel()
	m.sessionKey = "sess-1"
	m.messages = []chatMessage{
		{role: "system", content: "running on gateway..."},
	}
	denied := protocol.ExecDenied{SessionKey: "sess-1", Reason: "policy"}
	payload, _ := json.Marshal(denied)
	m.handleEvent(protocol.Event{EventName: protocol.EventExecDenied, Payload: payload})
	last := m.messages[0]
	if last.errMsg != "command execution denied" {
		t.Errorf("expected denial error, got errMsg=%q", last.errMsg)
	}
}

func TestHandleEvent_ExecDenied_DifferentSession_Ignored(t *testing.T) {
	m := newTestChatModel()
	m.sessionKey = "sess-1"
	m.messages = []chatMessage{
		{role: "system", content: "running on gateway..."},
	}
	denied := protocol.ExecDenied{SessionKey: "other-session"}
	payload, _ := json.Marshal(denied)
	m.handleEvent(protocol.Event{EventName: protocol.EventExecDenied, Payload: payload})
	if m.messages[0].content != "running on gateway..." {
		t.Error("message should be unchanged for different session")
	}
}

func TestHandleEvent_FinalWithBellPref(t *testing.T) {
	m := newTestChatModel()
	m.sending = true
	m.prefs.CompletionBell = true
	m.terminalFocused = false
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "text", streaming: true},
	}
	finalMsg := json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"text"}],"timestamp":123}`)
	cmd := m.handleEvent(makeChatEvent("final", "run1", 3, finalMsg))
	if cmd == nil {
		t.Error("expected a non-nil cmd (should include bell when terminal is blurred)")
	}
}

func TestShouldRingBell(t *testing.T) {
	tests := []struct {
		name     string
		pref     bool
		focused  bool
		wantRing bool
	}{
		{"pref off, focused", false, true, false},
		{"pref off, blurred", false, false, false},
		{"pref on, focused", true, true, false},
		{"pref on, blurred", true, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestChatModel()
			m.prefs.CompletionBell = tt.pref
			m.terminalFocused = tt.focused
			if got := m.shouldRingBell(); got != tt.wantRing {
				t.Errorf("shouldRingBell() = %v, want %v", got, tt.wantRing)
			}
		})
	}
}

func TestHandleEvent_InvalidPayloadIgnored(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{{role: "user", content: "hello"}}

	m.handleEvent(protocol.Event{
		EventName: protocol.EventChat,
		Payload:   json.RawMessage(`not valid json`),
	})

	if len(m.messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(m.messages))
	}
}

func TestHandleEvent_EmptyDeltaIgnored(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{{role: "user", content: "hello"}}

	m.handleEvent(makeChatEvent("delta", "run1", 1, json.RawMessage(`""`)))

	if len(m.messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(m.messages))
	}
}

// --- Session-key filtering for chat events ---

// TestHandleEvent_ChatDelta_DifferentSession_Ignored verifies that a delta
// event carrying a sessionKey that doesn't match the model's session is silently
// dropped and does not create or modify any assistant message.
func TestHandleEvent_ChatDelta_DifferentSession_Ignored(t *testing.T) {
	m := newTestChatModel()
	m.sessionKey = "sess-A"
	m.messages = []chatMessage{{role: "user", content: "hello"}}

	ev := makeChatEventForSession("delta", "run-B", "sess-B", 1, json.RawMessage(`"should not appear"`))
	m.handleEvent(ev)

	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message (no spurious assistant), got %d", len(m.messages))
	}
}

// TestHandleEvent_ChatDelta_DifferentSession_DoesNotCorruptStreaming verifies
// that a delta from another session does not overwrite an in-progress streaming
// assistant message from the correct session.
func TestHandleEvent_ChatDelta_DifferentSession_DoesNotCorruptStreaming(t *testing.T) {
	m := newTestChatModel()
	m.sessionKey = "sess-A"
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "correct text", streaming: true},
	}

	ev := makeChatEventForSession("delta", "run-B", "sess-B", 1, json.RawMessage(`"wrong text"`))
	m.handleEvent(ev)

	if m.messages[1].content != "correct text" {
		t.Errorf("streaming content corrupted: got %q, want %q", m.messages[1].content, "correct text")
	}
}

// TestHandleEvent_ChatFinal_DifferentSession_Ignored verifies that a final
// event from another session does not finalise the current streaming message.
func TestHandleEvent_ChatFinal_DifferentSession_Ignored(t *testing.T) {
	m := newTestChatModel()
	m.sessionKey = "sess-A"
	m.sending = true
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "streaming…", streaming: true},
	}

	finalMsg := json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"other"}],"timestamp":123}`)
	cmd := m.handleEvent(makeChatEventForSession("final", "run-B", "sess-B", 1, finalMsg))

	if cmd != nil {
		t.Error("expected nil cmd — foreign final must not trigger any action")
	}
	if !m.messages[1].streaming {
		t.Error("own streaming message should still be streaming")
	}
	if !m.sending {
		t.Error("sending should remain true")
	}
}

// TestHandleEvent_ChatFinal_DifferentSession_DoesNotDrainQueue is the critical
// regression test: a final event from another session must NOT trigger drainQueue
// and send pending messages prematurely.
func TestHandleEvent_ChatFinal_DifferentSession_DoesNotDrainQueue(t *testing.T) {
	m := newTestChatModel()
	m.sessionKey = "sess-A"
	m.sending = true
	m.pendingMessages = []string{"queued msg"}
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "streaming…", streaming: true},
	}

	finalMsg := json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"other session done"}],"timestamp":123}`)
	cmd := m.handleEvent(makeChatEventForSession("final", "run-B", "sess-B", 1, finalMsg))

	if cmd != nil {
		t.Error("expected nil cmd — foreign final must not drain the queue")
	}
	if len(m.pendingMessages) != 1 {
		t.Errorf("queue should be untouched: got %d pending, want 1", len(m.pendingMessages))
	}
	if !m.sending {
		t.Error("sending should remain true")
	}
}

// TestHandleEvent_ChatError_DifferentSession_Ignored verifies that an error
// event from another session does not affect the current model state.
func TestHandleEvent_ChatError_DifferentSession_Ignored(t *testing.T) {
	m := newTestChatModel()
	m.sessionKey = "sess-A"
	m.sending = true
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "streaming…", streaming: true},
	}

	chatEv := protocol.ChatEvent{
		RunID:        "run-B",
		SessionKey:   "sess-B",
		State:        "error",
		ErrorMessage: "something failed",
	}
	payload, _ := json.Marshal(chatEv)
	cmd := m.handleEvent(protocol.Event{EventName: protocol.EventChat, Payload: payload})

	if cmd != nil {
		t.Error("expected nil cmd for foreign error event")
	}
	if m.messages[1].errMsg != "" {
		t.Errorf("errMsg should be empty, got %q", m.messages[1].errMsg)
	}
	if !m.messages[1].streaming {
		t.Error("own streaming message should still be streaming")
	}
}

// TestHandleEvent_ChatDelta_EmptySessionKey_Processed verifies that events
// without a sessionKey (e.g. from older gateway versions) are still handled.
func TestHandleEvent_ChatDelta_EmptySessionKey_Processed(t *testing.T) {
	m := newTestChatModel()
	m.sessionKey = "sess-A"
	m.messages = []chatMessage{{role: "user", content: "hello"}}

	// sessionKey="" — should not be filtered out.
	ev := makeChatEventForSession("delta", "run-1", "", 1, json.RawMessage(`"hello from gateway"`))
	m.handleEvent(ev)

	if len(m.messages) != 2 {
		t.Fatalf("expected 2 messages (delta processed), got %d", len(m.messages))
	}
	if m.messages[1].content != "hello from gateway" {
		t.Errorf("content = %q, want %q", m.messages[1].content, "hello from gateway")
	}
}

// TestHandleEvent_ChatDelta_MatchingSession_Processed verifies that events
// with a matching sessionKey are handled normally.
func TestHandleEvent_ChatDelta_MatchingSession_Processed(t *testing.T) {
	m := newTestChatModel()
	m.sessionKey = "sess-A"
	m.messages = []chatMessage{{role: "user", content: "hello"}}

	ev := makeChatEventForSession("delta", "run-1", "sess-A", 1, json.RawMessage(`"correct response"`))
	m.handleEvent(ev)

	if len(m.messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(m.messages))
	}
	if m.messages[1].content != "correct response" {
		t.Errorf("content = %q, want %q", m.messages[1].content, "correct response")
	}
}

func TestHandleEvent_FullStreamingFlow(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{{role: "user", content: "ping"}}
	m.sending = true

	m.handleEvent(makeChatEvent("delta", "run1", 1, json.RawMessage(`"Hel"`)))
	if m.messages[1].content != "Hel" {
		t.Errorf("after delta 1: content = %q", m.messages[1].content)
	}

	m.handleEvent(makeChatEvent("delta", "run1", 2, json.RawMessage(`"Hello!"`)))
	if m.messages[1].content != "Hello!" {
		t.Errorf("after delta 2: content = %q", m.messages[1].content)
	}

	finalMsg := json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"Hello!"}],"timestamp":123}`)
	cmd := m.handleEvent(makeChatEvent("final", "run1", 3, finalMsg))
	if m.messages[1].streaming {
		t.Error("after final: should not be streaming")
	}
	if cmd == nil {
		t.Error("after final: expected refresh cmd")
	}

	m.handleEvent(makeChatEvent("delta", "run1", 3, json.RawMessage(`"Hello!"`)))
	if len(m.messages) != 2 {
		t.Errorf("after late delta: expected 2 messages, got %d", len(m.messages))
	}
}

func TestGatewayStatusMsg_Success(t *testing.T) {
	m := newTestChatModel()
	configured := true
	linked := true
	health := &protocol.HealthEvent{
		OK:               true,
		DurationMs:       12,
		HeartbeatSeconds: 30,
		Sessions:         protocol.HealthSessionsSummary{Count: 3},
		Agents: []protocol.AgentHealthSummary{
			{AgentID: "a1", Name: "Alpha", IsDefault: true, Sessions: protocol.HealthSessionsSummary{Count: 2}},
			{AgentID: "a2", Name: "Beta", IsDefault: false, Sessions: protocol.HealthSessionsSummary{Count: 1}},
		},
		ChannelOrder:  []string{"slack"},
		ChannelLabels: map[string]string{"slack": "Slack"},
		Channels: map[string]protocol.ChannelHealthSummary{
			"slack": {Configured: &configured, Linked: &linked},
		},
	}

	updated, _ := m.Update(gatewayStatusMsg{health: health, uptimeMs: 7200000})

	if len(updated.messages) == 0 {
		t.Fatal("expected a status message")
	}
	last := updated.messages[len(updated.messages)-1]
	if last.role != "system" {
		t.Errorf("expected system role, got %q", last.role)
	}
	if last.errMsg != "" {
		t.Errorf("expected no error, got %q", last.errMsg)
	}
	if !strings.Contains(last.content, "Gateway: OK") {
		t.Errorf("expected 'Gateway: OK' in content, got %q", last.content)
	}
	if !strings.Contains(last.content, "Alpha") {
		t.Errorf("expected agent name 'Alpha' in content, got %q", last.content)
	}
	if !strings.Contains(last.content, "Slack") {
		t.Errorf("expected channel label 'Slack' in content, got %q", last.content)
	}
	if !strings.Contains(last.content, "2h0m") {
		t.Errorf("expected uptime '2h0m' in content, got %q", last.content)
	}
}

func TestGatewayStatusMsg_Degraded(t *testing.T) {
	m := newTestChatModel()
	health := &protocol.HealthEvent{
		OK:         false,
		DurationMs: 500,
	}

	updated, _ := m.Update(gatewayStatusMsg{health: health, uptimeMs: 0})

	last := updated.messages[len(updated.messages)-1]
	if !strings.Contains(last.content, "DEGRADED") {
		t.Errorf("expected 'DEGRADED' in content, got %q", last.content)
	}
}

func TestGatewayStatusMsg_Error(t *testing.T) {
	m := newTestChatModel()

	updated, _ := m.Update(gatewayStatusMsg{err: errString("connection refused")})

	last := updated.messages[len(updated.messages)-1]
	if last.errMsg != "connection refused" {
		t.Errorf("expected error message, got %q", last.errMsg)
	}
}

func TestFormatGatewayStatus_DefaultAgent(t *testing.T) {
	health := &protocol.HealthEvent{
		OK: true,
		Agents: []protocol.AgentHealthSummary{
			{AgentID: "a1", Name: "Main", IsDefault: true},
			{AgentID: "a2", Name: "Other", IsDefault: false},
		},
	}
	out := formatGatewayStatus(health, 0)
	if !strings.Contains(out, "* Main") {
		t.Errorf("expected default agent marked with '*', got %q", out)
	}
	if strings.Contains(out, "* Other") {
		t.Errorf("non-default agent should not be marked with '*'")
	}
}

func TestFormatGatewayStatus_NoUptime(t *testing.T) {
	health := &protocol.HealthEvent{OK: true}
	out := formatGatewayStatus(health, 0)
	if strings.Contains(out, "Uptime") {
		t.Errorf("expected no Uptime line when uptimeMs=0, got %q", out)
	}
}

func TestFormatGatewayStatus_ChannelNilConfigured(t *testing.T) {
	health := &protocol.HealthEvent{
		OK:            true,
		ChannelOrder:  []string{"email"},
		ChannelLabels: map[string]string{"email": "Email"},
		Channels: map[string]protocol.ChannelHealthSummary{
			"email": {Configured: nil, Linked: nil},
		},
	}
	out := formatGatewayStatus(health, 0)
	if !strings.Contains(out, "configured:?") {
		t.Errorf("expected '?' for nil configured field, got %q", out)
	}
}

func TestFormatDuration_Seconds(t *testing.T) {
	got := formatDuration(45000)
	if got != "45s" {
		t.Errorf("expected '45s', got %q", got)
	}
}

func TestFormatDuration_Minutes(t *testing.T) {
	got := formatDuration(90000)
	if got != "1m30s" {
		t.Errorf("expected '1m30s', got %q", got)
	}
}

func TestFormatDuration_Hours(t *testing.T) {
	got := formatDuration(3661000)
	if got != "1h1m" {
		t.Errorf("expected '1h1m', got %q", got)
	}
}

// makeAgentToolEvent builds an "agent" event frame carrying a stream:"tool"
// payload with the given phase and data fields.
func makeAgentToolEvent(phase, name, toolCallID string, args, result json.RawMessage, isError bool) protocol.Event {
	data := map[string]any{
		"phase":      phase,
		"name":       name,
		"toolCallId": toolCallID,
	}
	if len(args) > 0 {
		data["args"] = args
	}
	if len(result) > 0 {
		data["result"] = result
	}
	if isError {
		data["isError"] = true
	}
	agentEv := protocol.AgentEvent{
		RunID:  "run1",
		Stream: "tool",
		Seq:    1,
		Data:   data,
	}
	payload, _ := json.Marshal(agentEv)
	return protocol.Event{EventName: protocol.EventAgent, Payload: payload}
}

func TestHandleAgentEvent_ToolStartFreezesStreamingAndAppendsCard(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{
		{role: "user", content: "search please"},
		{role: "assistant", content: "Sure, searching", streaming: true},
	}

	m.handleEvent(makeAgentToolEvent("start", "search", "tc-1",
		json.RawMessage(`{"query":"hello"}`), nil, false))

	if len(m.messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(m.messages))
	}
	if m.messages[1].streaming {
		t.Error("expected streaming assistant to be frozen on tool start")
	}
	tool := m.messages[2]
	if tool.role != "tool" {
		t.Fatalf("expected role=tool, got %q", tool.role)
	}
	if tool.toolName != "search" {
		t.Errorf("toolName = %q, want %q", tool.toolName, "search")
	}
	if tool.toolCallID != "tc-1" {
		t.Errorf("toolCallID = %q, want %q", tool.toolCallID, "tc-1")
	}
	if tool.toolState != "running" {
		t.Errorf("toolState = %q, want running", tool.toolState)
	}
	if !strings.Contains(tool.toolArgsLine, "query") || !strings.Contains(tool.toolArgsLine, "hello") {
		t.Errorf("toolArgsLine = %q, want it to mention query=hello", tool.toolArgsLine)
	}
}

func TestHandleAgentEvent_ToolStartDropsEmptyPlaceholder(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{
		{role: "user", content: "hi"},
		{role: "assistant", streaming: true, awaitingDelta: true},
	}

	m.handleEvent(makeAgentToolEvent("start", "search", "tc-1", nil, nil, false))

	if len(m.messages) != 2 {
		t.Fatalf("expected 2 messages (user + tool), got %d", len(m.messages))
	}
	if m.messages[1].role != "tool" {
		t.Errorf("expected the empty placeholder to be replaced by a tool row, got %q", m.messages[1].role)
	}
}

func TestHandleAgentEvent_ToolResultSuccessFlipsState(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{
		{role: "tool", toolName: "search", toolCallID: "tc-1", toolState: "running"},
	}

	m.handleEvent(makeAgentToolEvent("result", "search", "tc-1", nil,
		json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`), false))

	if m.messages[0].toolState != "success" {
		t.Errorf("toolState = %q, want success", m.messages[0].toolState)
	}
	if m.messages[0].toolError != "" {
		t.Errorf("toolError = %q, want empty on success", m.messages[0].toolError)
	}
}

func TestHandleAgentEvent_ToolResultErrorCarriesMessage(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{
		{role: "tool", toolName: "read", toolCallID: "tc-2", toolState: "running"},
	}

	result := json.RawMessage(`{"content":[{"type":"text","text":"file not found: /nope"}]}`)
	m.handleEvent(makeAgentToolEvent("result", "read", "tc-2", nil, result, true))

	if m.messages[0].toolState != "error" {
		t.Errorf("toolState = %q, want error", m.messages[0].toolState)
	}
	if !strings.Contains(m.messages[0].toolError, "file not found") {
		t.Errorf("toolError = %q, want it to mention 'file not found'", m.messages[0].toolError)
	}
}

// makeAgentToolEventForSession builds an EventAgent payload carrying a tool
// event with an explicit top-level sessionKey. The Go SDK's protocol.AgentEvent
// struct doesn't declare SessionKey, so we hand-build the JSON to match the
// gateway's actual wire shape (see infra/agent-events.ts AgentEventPayload).
func makeAgentToolEventForSession(sessionKey, phase, name, toolCallID string) protocol.Event {
	data := map[string]any{
		"phase":      phase,
		"name":       name,
		"toolCallId": toolCallID,
	}
	envelope := map[string]any{
		"runId":  "run1",
		"seq":    1,
		"stream": "tool",
		"data":   data,
	}
	if sessionKey != "" {
		envelope["sessionKey"] = sessionKey
	}
	payload, _ := json.Marshal(envelope)
	return protocol.Event{EventName: protocol.EventAgent, Payload: payload}
}

// --- Session-key filtering for tool/agent events ---

// TestHandleAgentEvent_ToolStart_DifferentSession_Ignored verifies that a
// tool-start event from another session does not append a tool card or freeze
// the current streaming assistant message.
func TestHandleAgentEvent_ToolStart_DifferentSession_Ignored(t *testing.T) {
	m := newTestChatModel()
	m.sessionKey = "sess-A"
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "thinking", streaming: true},
	}

	m.handleEvent(makeAgentToolEventForSession("sess-B", "start", "search", "tc-1"))

	if len(m.messages) != 2 {
		t.Fatalf("expected 2 messages (foreign tool dropped), got %d", len(m.messages))
	}
	if !m.messages[1].streaming {
		t.Error("own streaming assistant should not be frozen by a foreign tool start")
	}
}

// TestHandleAgentEvent_ToolResult_DifferentSession_Ignored verifies that a
// tool-result event from another session does not flip the state of an
// existing tool card belonging to the current session (e.g. when toolCallId
// values collide across sessions).
func TestHandleAgentEvent_ToolResult_DifferentSession_Ignored(t *testing.T) {
	m := newTestChatModel()
	m.sessionKey = "sess-A"
	m.messages = []chatMessage{
		{role: "tool", toolName: "search", toolCallID: "tc-1", toolState: "running"},
	}

	m.handleEvent(makeAgentToolEventForSession("sess-B", "result", "search", "tc-1"))

	if m.messages[0].toolState != "running" {
		t.Errorf("own tool card was modified by foreign session: state=%q", m.messages[0].toolState)
	}
}

// TestHandleAgentEvent_ToolStart_MatchingSession_Processed verifies the
// centralized filter doesn't accidentally drop tool events with a matching
// sessionKey.
func TestHandleAgentEvent_ToolStart_MatchingSession_Processed(t *testing.T) {
	m := newTestChatModel()
	m.sessionKey = "sess-A"

	m.handleEvent(makeAgentToolEventForSession("sess-A", "start", "search", "tc-1"))

	if len(m.messages) != 1 || m.messages[0].role != "tool" {
		t.Fatalf("expected tool card to be appended, got %+v", m.messages)
	}
}

// TestHandleAgentEvent_ToolStart_EmptySessionKey_Processed verifies that
// agent events without a sessionKey (older gateway versions, or events
// emitted while isControlUiVisible is false) are still processed.
func TestHandleAgentEvent_ToolStart_EmptySessionKey_Processed(t *testing.T) {
	m := newTestChatModel()
	m.sessionKey = "sess-A"

	m.handleEvent(makeAgentToolEventForSession("", "start", "search", "tc-1"))

	if len(m.messages) != 1 || m.messages[0].role != "tool" {
		t.Fatalf("expected tool card to be appended for empty sessionKey, got %+v", m.messages)
	}
}

// --- Centralized session-key filter coverage ---

// TestExtractEventSessionKey_Variants verifies the helper that backs the
// centralized filter handles every shape we expect on the wire.
func TestExtractEventSessionKey_Variants(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{"empty payload", ``, ""},
		{"no sessionKey field", `{"foo":"bar"}`, ""},
		{"explicit empty sessionKey", `{"sessionKey":""}`, ""},
		{"populated sessionKey", `{"sessionKey":"sess-A","other":1}`, "sess-A"},
		{"malformed JSON", `not json`, ""},
		{"sessionKey at nested level only", `{"data":{"sessionKey":"sess-A"}}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractEventSessionKey([]byte(tc.payload)); got != tc.want {
				t.Errorf("extractEventSessionKey(%q) = %q, want %q", tc.payload, got, tc.want)
			}
		})
	}
}

func TestHandleAgentEvent_NonToolStreamIgnored(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{
		{role: "assistant", content: "thinking", streaming: true},
	}

	agentEv := protocol.AgentEvent{
		RunID:  "run1",
		Stream: "lifecycle",
		Data:   map[string]any{"phase": "start"},
	}
	payload, _ := json.Marshal(agentEv)
	m.handleEvent(protocol.Event{EventName: protocol.EventAgent, Payload: payload})

	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if !m.messages[0].streaming {
		t.Error("non-tool agent stream should not freeze the streaming assistant")
	}
}

func TestSummariseArgs_PrefersCommonKeys(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"command", `{"command":"ls -la","cwd":"/tmp"}`, `command="ls -la"`},
		{"path", `{"path":"/etc/hosts"}`, `path="/etc/hosts"`},
		{"query", `{"query":"hello"}`, `query="hello"`},
		{"url", `{"url":"https://example.com"}`, `url="https://example.com"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := summariseArgs(json.RawMessage(tc.raw))
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSummariseArgs_FallsBackToCompactJSON(t *testing.T) {
	got := summariseArgs(json.RawMessage(`{"foo":1,"bar":"baz"}`))
	// Order of keys in map iteration is non-deterministic; just check both
	// keys appear and the result is one line.
	if !strings.Contains(got, "foo") || !strings.Contains(got, "bar") {
		t.Errorf("got %q, want it to contain both keys", got)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("got multi-line summary %q", got)
	}
}

func TestSummariseArgs_TruncatesLongValues(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := summariseArgs(json.RawMessage(`{"command":"` + long + `"}`))
	if n := len([]rune(got)); n > 80 {
		t.Errorf("summary not truncated: runes=%d, value=%q", n, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected truncation ellipsis at end, got %q", got)
	}
}
