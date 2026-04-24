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
	chatEv := protocol.ChatEvent{
		RunID:   runID,
		State:   state,
		Seq:     seq,
		Message: message,
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
	// User message should be appended to m.messages when dequeued.
	last := m.messages[len(m.messages)-1]
	if last.role != "user" || last.content != "msg1" {
		t.Errorf("expected last message to be user 'msg1', got %s %q", last.role, last.content)
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
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
	}

	// An empty final with no streaming assistant message (gateway ack).
	finalMsg := json.RawMessage(`{"role":"assistant","content":[],"timestamp":123}`)
	cmd := m.handleEvent(makeChatEvent("final", "run1", 1, finalMsg))

	if cmd != nil {
		t.Error("expected nil cmd for empty ack final")
	}
	if len(m.pendingMessages) != 1 {
		t.Errorf("queue should be unchanged, got %d pending", len(m.pendingMessages))
	}
	if !m.sending {
		t.Error("sending should remain true — ack should not reset it")
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
	// The dequeued message should now be in m.messages.
	last := m.messages[len(m.messages)-1]
	if last.role != "user" || last.content != "next msg" {
		t.Errorf("expected dequeued user message, got %s %q", last.role, last.content)
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
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "text", streaming: true},
	}
	finalMsg := json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"text"}],"timestamp":123}`)
	cmd := m.handleEvent(makeChatEvent("final", "run1", 3, finalMsg))
	if cmd == nil {
		t.Error("expected a non-nil cmd (should include bell)")
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
