package hermes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/backend/hermes/rpc"
)

// The fixtures under testdata/events are the golden payloads captured
// from a live nousresearch/hermes-agent:v2026.6.5 gateway during the
// Phase 0 spike (see openspec/changes/replace-hermes-ws-backend/
// phase0-fixtures.md). tool_complete_error.json is the one constructed
// variant: the real capture's shape with the error fields populated,
// since the live terminal run succeeded.

func fixtureNotification(t *testing.T, name string) rpc.Notification {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "events", name+".json"))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return rpc.Notification{Method: "event", Params: data}
}

// decodeChat unwraps a protocol.Event into its ChatEvent payload.
func decodeChatEvent(t *testing.T, ev protocol.Event) protocol.ChatEvent {
	t.Helper()
	if ev.EventName != protocol.EventChat {
		t.Fatalf("EventName = %s, want chat", ev.EventName)
	}
	var ce protocol.ChatEvent
	if err := json.Unmarshal(ev.Payload, &ce); err != nil {
		t.Fatalf("chat payload: %v", err)
	}
	return ce
}

// decodeToolData unwraps an agent event and returns the tool-stream
// data map through the same JSON round-trip the TUI performs.
func decodeToolData(t *testing.T, ev protocol.Event) map[string]any {
	t.Helper()
	if ev.EventName != protocol.EventAgent {
		t.Fatalf("EventName = %s, want agent", ev.EventName)
	}
	var ae protocol.AgentEvent
	if err := json.Unmarshal(ev.Payload, &ae); err != nil {
		t.Fatalf("agent payload: %v", err)
	}
	if ae.Stream != "tool" {
		t.Fatalf("stream = %s, want tool", ae.Stream)
	}
	return ae.Data
}

// A plain streaming turn: deltas accumulate into full-text-so-far
// frames (the TUI contract), and the final event carries the complete
// text plus the inline usage from message.complete.
func TestTranslate_StreamingTurnAccumulates(t *testing.T) {
	tr := newTranslator()
	tr.SetRun("96eb4e8e", "run-1")

	events, ask := tr.Translate(fixtureNotification(t, "message_start"))
	if len(events) != 0 || ask != nil {
		t.Fatalf("message.start should be internal, got %d events", len(events))
	}

	events, _ = tr.Translate(fixtureNotification(t, "message_delta"))
	if len(events) != 1 {
		t.Fatalf("delta events = %d, want 1", len(events))
	}
	ce := decodeChatEvent(t, events[0])
	if ce.State != "delta" || ce.RunID != "run-1" || ce.SessionKey != "96eb4e8e" {
		t.Fatalf("unexpected delta envelope: %+v", ce)
	}
	if got := backend.ExtractChatText(ce.Message); got != "hello" {
		t.Fatalf("first delta text = %q", got)
	}

	// Second increment must yield the cumulative string.
	inc := rpc.Notification{Method: "event", Params: json.RawMessage(
		`{"type":"message.delta","session_id":"96eb4e8e","payload":{"text":" world"}}`)}
	events, _ = tr.Translate(inc)
	if got := backend.ExtractChatText(decodeChatEvent(t, events[0]).Message); got != "hello world" {
		t.Fatalf("cumulative delta text = %q, want %q", got, "hello world")
	}

	events, _ = tr.Translate(fixtureNotification(t, "message_complete"))
	if len(events) != 1 {
		t.Fatalf("complete events = %d, want 1", len(events))
	}
	ce = decodeChatEvent(t, events[0])
	if ce.State != "final" {
		t.Fatalf("state = %q, want final", ce.State)
	}
	if got := backend.ExtractChatText(ce.Message); got != "hello world" {
		t.Fatalf("final text = %q", got)
	}
	var usage struct {
		Total      int `json:"total"`
		ContextMax int `json:"context_max"`
	}
	if err := json.Unmarshal(ce.Usage, &usage); err != nil {
		t.Fatalf("usage: %v", err)
	}
	if usage.Total != 14094 || usage.ContextMax != 65536 {
		t.Fatalf("usage passthrough broken: %+v", usage)
	}
}

// An interrupted turn ends with message.complete status="interrupted",
// which must surface as an aborted chat event, not a final one.
func TestTranslate_InterruptedTurnAborts(t *testing.T) {
	tr := newTranslator()
	events, _ := tr.Translate(fixtureNotification(t, "message_complete_interrupted"))
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if ce := decodeChatEvent(t, events[0]); ce.State != "aborted" {
		t.Fatalf("state = %q, want aborted", ce.State)
	}
}

// tool.start / tool.complete pair by the server-supplied tool_id and
// land in the data shape the TUI's tool cards decode.
func TestTranslate_ToolPairUsesServerID(t *testing.T) {
	tr := newTranslator()

	events, _ := tr.Translate(fixtureNotification(t, "tool_start"))
	if len(events) != 1 {
		t.Fatalf("start events = %d, want 1", len(events))
	}
	data := decodeToolData(t, events[0])
	if data["phase"] != "start" || data["name"] != "terminal" {
		t.Fatalf("start data: %+v", data)
	}
	if data["toolCallId"] != "call_QyqcRwF2l2VU6qptv3WtANqV" {
		t.Fatalf("toolCallId = %v", data["toolCallId"])
	}

	events, _ = tr.Translate(fixtureNotification(t, "tool_complete"))
	data = decodeToolData(t, events[0])
	if data["phase"] != "result" || data["toolCallId"] != "call_QyqcRwF2l2VU6qptv3WtANqV" {
		t.Fatalf("result data: %+v", data)
	}
	if data["isError"] != false {
		t.Fatalf("success run marked isError: %+v", data)
	}
}

// A failed tool result (non-null error / non-zero exit) marks the card
// as an error and exposes the message where the TUI's extractor looks.
func TestTranslate_ToolErrorResult(t *testing.T) {
	tr := newTranslator()
	events, _ := tr.Translate(fixtureNotification(t, "tool_complete_error"))
	data := decodeToolData(t, events[0])
	if data["isError"] != true {
		t.Fatalf("error run not flagged: %+v", data)
	}
	raw, _ := json.Marshal(data["result"])
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("result shape: %v", err)
	}
	if len(res.Content) != 1 || res.Content[0].Text != "command exited with status 1" {
		t.Fatalf("error text not extractable: %+v", res)
	}
}

// error events surface as chat errors carrying the gateway's message.
func TestTranslate_ErrorEvent(t *testing.T) {
	tr := newTranslator()
	events, _ := tr.Translate(fixtureNotification(t, "error"))
	ce := decodeChatEvent(t, events[0])
	if ce.State != "error" {
		t.Fatalf("state = %q", ce.State)
	}
	if ce.ErrorMessage == "" {
		t.Fatal("error message dropped")
	}
}

// Interactive asks come back as an Ask (for the backend to auto-decline
// via the paired respond RPC), not as TUI events.
func TestTranslate_ClarifyRequestSurfacesAsAsk(t *testing.T) {
	tr := newTranslator()
	events, ask := tr.Translate(fixtureNotification(t, "clarify_request"))
	if len(events) != 0 {
		t.Fatalf("asks must not emit TUI events, got %d", len(events))
	}
	if ask == nil {
		t.Fatal("ask = nil")
	}
	if ask.Type != "clarify.request" || ask.RequestID != "d7987369" || ask.SessionID != "3d0df6d7" {
		t.Fatalf("ask = %+v", ask)
	}
}

// thinking.delta maps to the thinking stream (unused by the TUI today,
// wired so a thinking indicator needs no backend change later).
func TestTranslate_ThinkingDelta(t *testing.T) {
	tr := newTranslator()
	events, _ := tr.Translate(fixtureNotification(t, "thinking_delta"))
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	var ae protocol.AgentEvent
	if err := json.Unmarshal(events[0].Payload, &ae); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if ae.Stream != "thinking" {
		t.Fatalf("stream = %q", ae.Stream)
	}
}

// Internal frames produce no TUI events and no ask.
func TestTranslate_InternalFramesIgnored(t *testing.T) {
	tr := newTranslator()
	for _, name := range []string{"gateway_ready", "session_info", "tool_generating", "reasoning_available"} {
		events, ask := tr.Translate(fixtureNotification(t, name))
		if len(events) != 0 || ask != nil {
			t.Fatalf("%s should be internal, got %d events, ask=%v", name, len(events), ask)
		}
	}
}

// Non-event notifications and malformed params fall through silently.
func TestTranslate_GarbageTolerant(t *testing.T) {
	tr := newTranslator()
	for _, n := range []rpc.Notification{
		{Method: "something.else", Params: json.RawMessage(`{}`)},
		{Method: "event", Params: json.RawMessage(`not json`)},
		{Method: "event", Params: json.RawMessage(`{"type":"never.seen.before","session_id":"x"}`)},
	} {
		events, ask := tr.Translate(n)
		if len(events) != 0 || ask != nil {
			t.Fatalf("garbage produced output: %+v", n)
		}
	}
}
