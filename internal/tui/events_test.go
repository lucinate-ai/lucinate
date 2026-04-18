package tui

import (
	"encoding/json"
	"testing"

	"github.com/a3tai/openclaw-go/protocol"
	"github.com/charmbracelet/bubbles/viewport"
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
	vp := viewport.New(80, 20)
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

	if cmd != nil {
		t.Error("expected nil cmd when no messages exist")
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

func TestHandleEvent_NonChatEventIgnored(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{{role: "user", content: "hello"}}

	m.handleEvent(protocol.Event{EventName: "tick"})

	if len(m.messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(m.messages))
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
