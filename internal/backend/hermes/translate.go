package hermes

import (
	"encoding/json"
	"strings"

	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend/hermes/rpc"
)

// eventEnvelope is the params object of every tui_gateway "event"
// notification: {type, session_id, payload}. Verified against the live
// gateway — see openspec/changes/replace-hermes-ws-backend/phase0-fixtures.md.
type eventEnvelope struct {
	Type      string          `json:"type"`
	SessionID string          `json:"session_id"`
	Payload   json.RawMessage `json:"payload"`
}

// Ask is a blocking server → client request from the agent
// (approval/clarify/sudo/secret). The translator surfaces it; the
// backend answers via the paired <type>.respond RPC (auto-decline in
// this phase) and renders a system message.
type Ask struct {
	Type      string // "approval.request", "clarify.request", …
	SessionID string
	RequestID string
	Payload   json.RawMessage
}

// textPayload covers every event whose payload is just {text}:
// message.delta, thinking.delta, reasoning.delta, reasoning.available.
type textPayload struct {
	Text string `json:"text"`
}

// completePayload is message.complete: the full assistant text, a
// status discriminating a finished turn from an interrupted one, and
// the turn's usage inline.
type completePayload struct {
	Text   string          `json:"text"`
	Status string          `json:"status"` // "complete" | "interrupted"
	Usage  json.RawMessage `json:"usage"`
}

// toolStartPayload is tool.start. Context is a human-readable summary
// of the call ("echo lucinate-tool-ok"), not structured args.
type toolStartPayload struct {
	ToolID  string `json:"tool_id"`
	Name    string `json:"name"`
	Context string `json:"context"`
}

// toolCompletePayload is tool.complete. Error state lives in
// Result.Error / Result.ExitCode — there is no isError flag.
type toolCompletePayload struct {
	ToolID string          `json:"tool_id"`
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args"`
	Result struct {
		Output   string  `json:"output"`
		ExitCode *int    `json:"exit_code"`
		Error    *string `json:"error"`
	} `json:"result"`
}

// askPayload pulls the request id shared by the interactive asks.
type askPayload struct {
	RequestID string `json:"request_id"`
}

// Translator converts tui_gateway event notifications into the
// protocol.Event frames the TUI already consumes. It is deliberately
// I/O-free so the whole mapping is table-testable against the Phase 0
// fixtures; the only state is the per-session delta accumulator (the
// TUI expects each chat delta to carry the full text so far, while the
// gateway sends increments) and the per-session run id.
//
// Not safe for concurrent use: the backend's single event pump owns it.
type Translator struct {
	acc    map[string]*strings.Builder
	runIDs map[string]string
}

func newTranslator() *Translator {
	return &Translator{
		acc:    make(map[string]*strings.Builder),
		runIDs: make(map[string]string),
	}
}

// SetRun records the run id ChatSend generated for a session, so chat
// events carry the id the TUI keys its streaming row on.
func (t *Translator) SetRun(sessionID, runID string) {
	t.runIDs[sessionID] = runID
}

// InjectSystemLine splices a "System: …" notice into the session's
// streaming text and returns the delta event carrying it. Used when
// the backend auto-declines an interactive ask mid-turn: a chat error
// event would finalise the run in the TUI while the server keeps
// streaming, so the notice rides the existing assistant row instead
// (and the history layer already strips System:-prefixed lines).
func (t *Translator) InjectSystemLine(sessionID, line string) protocol.Event {
	b := t.acc[sessionID]
	if b == nil {
		b = &strings.Builder{}
		t.acc[sessionID] = b
	}
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	b.WriteString("System: ")
	b.WriteString(line)
	b.WriteString("\n\n")
	return chatDeltaEvent(t.runIDs[sessionID], sessionID, b.String())
}

// Translate maps one notification to zero or more protocol events,
// plus the interactive ask when the frame is one (nil otherwise).
func (t *Translator) Translate(n rpc.Notification) ([]protocol.Event, *Ask) {
	if n.Method != "event" {
		return nil, nil
	}
	var env eventEnvelope
	if err := json.Unmarshal(n.Params, &env); err != nil {
		return nil, nil
	}
	sid := env.SessionID
	runID := t.runIDs[sid]

	switch env.Type {
	case "message.start":
		// Arms the accumulator; the TUI needs no frame until text flows.
		t.acc[sid] = &strings.Builder{}
		return nil, nil

	case "message.delta":
		var p textPayload
		_ = json.Unmarshal(env.Payload, &p)
		b := t.acc[sid]
		if b == nil {
			b = &strings.Builder{}
			t.acc[sid] = b
		}
		b.WriteString(p.Text)
		return []protocol.Event{chatDeltaEvent(runID, sid, b.String())}, nil

	case "message.complete":
		var p completePayload
		_ = json.Unmarshal(env.Payload, &p)
		delete(t.acc, sid)
		if p.Status == "interrupted" {
			return []protocol.Event{chatAbortedEvent(runID, sid)}, nil
		}
		return []protocol.Event{chatFinalEvent(runID, sid, p.Text, p.Usage)}, nil

	case "error":
		var p struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(env.Payload, &p)
		delete(t.acc, sid)
		return []protocol.Event{chatErrorEvent(runID, sid, p.Message)}, nil

	case "tool.generating":
		// Pre-call: the model is still drafting the invocation and no
		// tool_id exists yet. The TUI drops id-less tool frames, so
		// there is nothing useful to forward.
		return nil, nil

	case "tool.start":
		var p toolStartPayload
		_ = json.Unmarshal(env.Payload, &p)
		return []protocol.Event{toolEvent(runID, "start", map[string]any{
			"phase":      "start",
			"name":       p.Name,
			"toolCallId": p.ToolID,
			// Context is a display summary, not structured args; encode
			// it as a JSON string so the TUI's args preview renders it.
			"args": p.Context,
		})}, nil

	case "tool.complete":
		var p toolCompletePayload
		_ = json.Unmarshal(env.Payload, &p)
		isError := p.Result.Error != nil && *p.Result.Error != ""
		if p.Result.ExitCode != nil && *p.Result.ExitCode != 0 {
			isError = true
		}
		text := p.Result.Output
		if isError && p.Result.Error != nil && *p.Result.Error != "" {
			text = *p.Result.Error
		}
		return []protocol.Event{toolEvent(runID, "result", map[string]any{
			"phase":      "result",
			"name":       p.Name,
			"toolCallId": p.ToolID,
			"isError":    isError,
			// Re-wrap into the {content:[{type,text}]} shape the TUI's
			// tool-error extraction expects.
			"result": map[string]any{
				"content": []map[string]string{{"type": "text", "text": text}},
			},
		})}, nil

	case "thinking.delta", "reasoning.delta":
		var p textPayload
		_ = json.Unmarshal(env.Payload, &p)
		return []protocol.Event{{
			EventName: protocol.EventAgent,
			Payload: mustMarshal(protocol.AgentEvent{
				RunID:  runID,
				Stream: "thinking",
				Data:   map[string]any{"text": p.Text},
			}),
		}}, nil

	case "approval.request", "clarify.request", "sudo.request", "secret.request":
		var p askPayload
		_ = json.Unmarshal(env.Payload, &p)
		return nil, &Ask{Type: env.Type, SessionID: sid, RequestID: p.RequestID, Payload: env.Payload}

	default:
		// gateway.ready, session.info, status.update,
		// reasoning.available, tool.output_risk, … — internal signals
		// the backend consumes directly, or annotations with no TUI
		// surface yet.
		return nil, nil
	}
}

func chatDeltaEvent(runID, sid, full string) protocol.Event {
	return protocol.Event{
		EventName: protocol.EventChat,
		Payload: mustMarshal(protocol.ChatEvent{
			State:      "delta",
			RunID:      runID,
			SessionKey: sid,
			Message:    mustMarshal(full),
		}),
	}
}

func chatFinalEvent(runID, sid, text string, usage json.RawMessage) protocol.Event {
	final := struct {
		Role    string              `json:"role"`
		Content []map[string]string `json:"content"`
	}{
		Role:    "assistant",
		Content: []map[string]string{{"type": "text", "text": text}},
	}
	return protocol.Event{
		EventName: protocol.EventChat,
		Payload: mustMarshal(protocol.ChatEvent{
			State:      "final",
			RunID:      runID,
			SessionKey: sid,
			Message:    mustMarshal(final),
			Usage:      usage,
		}),
	}
}

func chatErrorEvent(runID, sid, msg string) protocol.Event {
	return protocol.Event{
		EventName: protocol.EventChat,
		Payload: mustMarshal(protocol.ChatEvent{
			State:        "error",
			RunID:        runID,
			SessionKey:   sid,
			ErrorMessage: msg,
		}),
	}
}

func chatAbortedEvent(runID, sid string) protocol.Event {
	return protocol.Event{
		EventName: protocol.EventChat,
		Payload: mustMarshal(protocol.ChatEvent{
			State:      "aborted",
			RunID:      runID,
			SessionKey: sid,
		}),
	}
}

func toolEvent(runID, phase string, data map[string]any) protocol.Event {
	return protocol.Event{
		EventName: protocol.EventAgent,
		Payload: mustMarshal(protocol.AgentEvent{
			RunID:  runID,
			Stream: "tool",
			Data:   data,
		}),
	}
}

// mustMarshal encodes values whose shape this package controls; a
// failure is a programming error, and the zero RawMessage keeps the
// event stream flowing rather than panicking mid-turn.
func mustMarshal(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
