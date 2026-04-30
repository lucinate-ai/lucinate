package httpcommon

import (
	"encoding/json"

	"github.com/a3tai/openclaw-go/protocol"
)

// EventEmitter wraps a buffered protocol.Event channel with the
// drop-on-full and recover-on-closed semantics the OpenClaw client
// established. Backends own one of these and surface its channel via
// Backend.Events().
type EventEmitter struct {
	ch chan protocol.Event
}

// NewEventEmitter constructs an emitter with the given buffer size.
// The OpenAI backend uses 64; the OpenClaw client similarly. Hermes
// follows suit.
func NewEventEmitter(buffer int) *EventEmitter {
	return &EventEmitter{ch: make(chan protocol.Event, buffer)}
}

// Channel returns the receive end. Backends expose this via Events().
func (e *EventEmitter) Channel() <-chan protocol.Event { return e.ch }

// Close closes the underlying channel. Subsequent Send calls become
// no-ops via the recover() guard below.
func (e *EventEmitter) Close() {
	defer func() { _ = recover() }()
	close(e.ch)
}

// Send forwards an event to the channel, dropping it if the buffer is
// full (matching the OpenClaw client's policy) and silently ignoring
// sends to a closed channel (the close-recover idiom keeps teardown
// from racing in-flight emit calls).
func (e *EventEmitter) Send(ev protocol.Event) {
	defer func() { _ = recover() }()
	select {
	case e.ch <- ev:
	default:
	}
}

// EmitChatDelta sends a chat-delta event with the running assistant
// content. Both backends emit the same payload shape — the message
// is just the JSON-encoded full string-so-far.
func (e *EventEmitter) EmitChatDelta(runID, sessionKey, full string) {
	payload, _ := json.Marshal(protocol.ChatEvent{
		State:      "delta",
		RunID:      runID,
		SessionKey: sessionKey,
		Message:    json.RawMessage(mustEncodeString(full)),
	})
	e.Send(protocol.Event{EventName: protocol.EventChat, Payload: payload})
}

// EmitChatFinal sends a chat-final event with the complete assistant
// reply wrapped in the structured-content shape the chat view's
// parser expects.
func (e *EventEmitter) EmitChatFinal(runID, sessionKey, full string) {
	final := struct {
		Role    string              `json:"role"`
		Content []map[string]string `json:"content"`
	}{
		Role:    "assistant",
		Content: []map[string]string{{"type": "text", "text": full}},
	}
	finalRaw, _ := json.Marshal(final)
	payload, _ := json.Marshal(protocol.ChatEvent{
		State:      "final",
		RunID:      runID,
		SessionKey: sessionKey,
		Message:    finalRaw,
	})
	e.Send(protocol.Event{EventName: protocol.EventChat, Payload: payload})
}

// EmitChatError sends a chat-error event surfaced by the chat view's
// existing error rendering.
func (e *EventEmitter) EmitChatError(runID, sessionKey, msg string) {
	payload, _ := json.Marshal(protocol.ChatEvent{
		State:        "error",
		RunID:        runID,
		SessionKey:   sessionKey,
		ErrorMessage: msg,
	})
	e.Send(protocol.Event{EventName: protocol.EventChat, Payload: payload})
}

// EmitChatAborted sends a chat-aborted event after a cancellation.
func (e *EventEmitter) EmitChatAborted(runID, sessionKey string) {
	payload, _ := json.Marshal(protocol.ChatEvent{
		State:      "aborted",
		RunID:      runID,
		SessionKey: sessionKey,
	})
	e.Send(protocol.Event{EventName: protocol.EventChat, Payload: payload})
}

// mustEncodeString wraps s as a JSON string. Used to embed an
// arbitrary delta in protocol.ChatEvent.Message without re-escaping.
func mustEncodeString(s string) string {
	buf, _ := json.Marshal(s)
	return string(buf)
}
