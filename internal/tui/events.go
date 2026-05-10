package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
)

// chatContentBlock is the {type, text} shape of one entry in the
// Content array of a chat history message. Defined here (rather than
// in history.go) because the history fetch code is the single
// remaining caller — chat-event message parsing now goes through
// backend.ExtractChatText so the wire format lives in one place.
type chatContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// toolEventData is the shape of the AgentEvent.Data map for stream=="tool"
// events. The openclaw-go SDK ships ClientCapToolEvents but no typed payload
// for the tool lifecycle, so this lives here until the SDK gains one.
type toolEventData struct {
	Phase      string          `json:"phase"` // "start", "update", "result"
	Name       string          `json:"name"`
	ToolCallID string          `json:"toolCallId"`
	Args       json.RawMessage `json:"args,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	IsError    bool            `json:"isError,omitempty"`
}

// toolResultPayload mirrors the gateway's ToolResult shape just enough to
// pull a one-line error message out of a failed tool result. Full output
// rendering is intentionally deferred — see the "expand/collapse" follow-up
// issue.
type toolResultPayload struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// extractThinkingFromMessage parses the Message field and extracts thinking blocks.
// Only final events carry structured content blocks; delta events are plain strings.
func extractThinkingFromMessage(raw json.RawMessage) string {
	return backend.ExtractChatThinking(raw)
}

// extractTextFromMessage parses the Message field and extracts readable text.
// Delta events send a plain JSON string; final events send a structured object.
func extractTextFromMessage(raw json.RawMessage) string {
	return backend.ExtractChatText(raw)
}

// finalisedRunsCap bounds the size of finalisedRunSet. The gateway emits
// stale duplicates rarely and only within a small window after final, so
// 32 prior runs is far more than needed in practice — but keeping a hard
// cap means a long-lived chat with thousands of turns never grows the
// filter unboundedly.
const finalisedRunsCap = 32

// finalisedRunSet is a bounded FIFO set of run IDs we have already
// finalised, used to drop stale chat events the gateway emits after a
// run has completed. The previous implementation tracked only the most
// recent finalised run, which was sufficient for the duplicate-after-
// final case but missed the back-to-back routine race: if a stale event
// arrives for run N-2 while run N-1 is streaming, it would slip past
// the single-deep filter (since prevFinalised had moved on to run N-1)
// and corrupt the placeholder. With a bounded set we cover that window
// without retaining state across sessions.
type finalisedRunSet struct {
	ids   []string        // ordered oldest→newest; len ≤ finalisedRunsCap
	inSet map[string]bool // O(1) membership for contains()
}

// add records id as finalised. Empty IDs are ignored (older gateways,
// non-run-scoped events). Re-adding an existing id is a no-op so the
// FIFO ordering stays meaningful.
func (s *finalisedRunSet) add(id string) {
	if id == "" {
		return
	}
	if s.inSet == nil {
		s.inSet = make(map[string]bool, finalisedRunsCap)
	}
	if s.inSet[id] {
		return
	}
	if len(s.ids) >= finalisedRunsCap {
		oldest := s.ids[0]
		s.ids = s.ids[1:]
		delete(s.inSet, oldest)
	}
	s.ids = append(s.ids, id)
	s.inSet[id] = true
}

// contains reports whether id has been finalised. Empty IDs are never
// members so callers can pass chatEv.RunID directly without guarding.
func (s *finalisedRunSet) contains(id string) bool {
	if id == "" {
		return false
	}
	return s.inSet[id]
}

// last returns the most recently added id, or "" when the set is empty.
// Useful for tests that want to assert "the most recent finalisation
// was run X" without poking at internals.
func (s *finalisedRunSet) last() string {
	if len(s.ids) == 0 {
		return ""
	}
	return s.ids[len(s.ids)-1]
}

// extractEventSessionKey pulls a top-level "sessionKey" string out of any
// event payload. Returns "" when the payload has no sessionKey, is empty,
// or fails to parse — those cases must be allowed through (older gateways,
// non-session-scoped events). This lets a single check at the top of
// handleEvent cover every event type that carries a sessionKey, instead of
// repeating ad-hoc filters in each handler — and protects new event types
// added later without code changes here.
func extractEventSessionKey(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	var probe struct {
		SessionKey string `json:"sessionKey"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return ""
	}
	return probe.SessionKey
}

func (m *chatModel) handleEvent(ev protocol.Event) tea.Cmd {
	logEvent("RAW_EVENT name=%s payload_len=%d", ev.EventName, len(ev.Payload))

	// Drop events scoped to a different session before any handler runs.
	// Events without a sessionKey (or with an empty one) fall through.
	if key := extractEventSessionKey(ev.Payload); key != "" && key != m.sessionKey {
		logEvent("  IGNORED (session %q != ours %q)", key, m.sessionKey)
		return nil
	}

	// Handle exec events.
	switch ev.EventName {
	case protocol.EventExecFinished:
		var finished protocol.ExecFinished
		if err := json.Unmarshal(ev.Payload, &finished); err != nil {
			logEvent("EXEC_FINISH parse error: %v", err)
			return nil
		}
		logEvent("EXEC_FINISHED session=%s cmd=%s exit=%v output_len=%d", finished.SessionKey, finished.Command, finished.ExitCode, len(finished.Output))
		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			if last.role == "system" && last.content == "running on gateway..." {
				output := finished.Output
				if output == "" {
					output = "(no output)"
				}
				if finished.ExitCode != nil && *finished.ExitCode != 0 {
					output += fmt.Sprintf("\nexit code: %d", *finished.ExitCode)
				}
				last.content = output
			}
		}
		m.updateViewport()
		return m.drainQueueSkipRefresh()

	case "exec.approval.resolved":
		var resolved protocol.ExecApprovalResolvedEvent
		if err := json.Unmarshal(ev.Payload, &resolved); err != nil {
			logEvent("EXEC_RESOLVED parse error: %v", err)
			return nil
		}
		logEvent("EXEC_RESOLVED id=%s decision=%s", resolved.ID, resolved.Decision)
		if resolved.Decision == "deny" {
			if len(m.messages) > 0 {
				last := &m.messages[len(m.messages)-1]
				if last.role == "system" && last.content == "running on gateway..." {
					last.content = ""
					last.errMsg = "command execution denied"
				}
			}
			m.updateViewport()
			return m.drainQueueSkipRefresh()
		}
		// "allow-once" / "allow-always" → exec.finished will follow.
		return nil

	case protocol.EventExecDenied:
		var denied protocol.ExecDenied
		if err := json.Unmarshal(ev.Payload, &denied); err != nil {
			logEvent("EXEC_DENIED parse error: %v", err)
			return nil
		}
		logEvent("EXEC_DENIED session=%s reason=%s", denied.SessionKey, denied.Reason)
		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			if last.role == "system" && last.content == "running on gateway..." {
				last.content = ""
				last.errMsg = "command execution denied"
			}
		}
		m.updateViewport()
		return m.drainQueueSkipRefresh()

	case protocol.EventAgent:
		return m.handleAgentEvent(ev)
	}

	if ev.EventName != protocol.EventChat {
		return nil
	}

	var chatEv protocol.ChatEvent
	if err := json.Unmarshal(ev.Payload, &chatEv); err != nil {
		logEvent("PARSE_ERROR: %v payload=%s", err, string(ev.Payload))
		return nil
	}

	logEvent("EVENT state=%s runID=%s seq=%d msgLen=%d sessionKey=%s", chatEv.State, chatEv.RunID, chatEv.Seq, len(chatEv.Message), chatEv.SessionKey)

	// Drop stale events from any run we have already finalised. The
	// gateway occasionally emits a duplicate `delta` (carrying the full
	// content) after `final` with the same runID; if we let it through,
	// the stale delta lands on the next routine step's placeholder,
	// flips awaitingDelta, and lets a subsequent empty final spuriously
	// finalise the next step. The set is bounded so the filter covers
	// back-to-back routine steps where a stale event for run N-k can
	// arrive while run N is streaming, not just the immediately prior
	// run.
	if m.finalisedRuns.contains(chatEv.RunID) {
		logEvent("  STALE event for finalised run %s — ignored", chatEv.RunID)
		return nil
	}

	switch chatEv.State {
	case "delta":
		deltaText := extractTextFromMessage(chatEv.Message)
		logEvent("  DELTA text=%q", deltaText)
		if deltaText == "" {
			return nil
		}

		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			if last.role == "assistant" && last.streaming {
				// Deltas are cumulative — each one contains the full text so far.
				last.content = deltaText
				last.awaitingDelta = false
				m.updateViewport()
				return nil
			}
			if last.role == "assistant" && !last.streaming {
				logEvent("  DELTA ignored (already finalised)")
				return nil
			}
		}
		m.appendMessage(chatMessage{
			role:      "assistant",
			content:   deltaText,
			streaming: true,
		})
		m.updateViewport()
		return m.ensureSpinnerTicking()

	case "final":
		logEvent("  FINAL msgContent=%s", string(chatEv.Message))
		m.runID = ""
		finalised := false
		assistantContent := ""
		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			if last.role == "assistant" && last.streaming && !last.awaitingDelta {
				last.streaming = false
				last.thinking = extractThinkingFromMessage(chatEv.Message)
				finalised = true
				assistantContent = last.content
				m.finalisedRuns.add(chatEv.RunID)
				logEvent("  FINALISED — refreshing history thinking_len=%d", len(last.thinking))
			}
		}
		m.updateViewport()
		if !finalised {
			// Empty ack from gateway — the real response hasn't arrived yet.
			logEvent("  FINAL ignored (no streaming assistant message)")
			return nil
		}
		// Capture the merge boundary *before* bumping so the just-
		// finalised turn is on the history-side of any refresh issued
		// from here on; subsequent appends (drained queue, auto-
		// advanced routine step, recovery system rows) get the new gen
		// and survive the merge.
		boundary := m.bumpGen()
		_ = boundary // wired up by Layer 3; Layer 2 keeps the old gating.
		// Routine bookkeeping: log assistant content, parse /routine: directives.
		// Done before drainQueue so a directive (stop/pause/mode) is honoured
		// before the next routine step would otherwise auto-fire.
		if m.activeRoutine != nil {
			if m.activeRoutine.logger != nil && assistantContent != "" {
				m.activeRoutine.logger.WriteAssistant(assistantContent)
			}
			m.applyDirectives(assistantContent)
		}
		var cmds []tea.Cmd
		if m.shouldRingBell() {
			cmds = append(cmds, bellCmd())
		}
		drainCmd := m.drainQueue()
		if m.sending {
			// Still draining the queue — defer history refresh until the queue is empty
			// to avoid replacing m.messages while queued user messages are visible.
			cmds = append(cmds, drainCmd)
			return tea.Batch(cmds...)
		}
		// User queue empty — try advancing the active routine. The advance itself
		// sets m.sending and dispatches the next step.
		if cmd := m.maybeAdvanceRoutine(); cmd != nil {
			cmds = append(cmds, cmd)
			return tea.Batch(cmds...)
		}
		cmds = append(cmds, m.refreshHistoryAt(boundary), m.loadStats())
		return tea.Batch(cmds...)

	case "error":
		logEvent("  ERROR: %s", chatEv.ErrorMessage)
		m.runID = ""
		finalised := false
		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			if last.role == "assistant" && last.streaming {
				last.streaming = false
				last.errMsg = chatEv.ErrorMessage
				finalised = true
				m.finalisedRuns.add(chatEv.RunID)
			}
		}
		m.updateViewport()
		if !finalised {
			return nil
		}
		// Bump so any subsequent refresh treats the errored turn as
		// history-side. The error row itself was stamped with the
		// pre-bump gen (still streaming when we mutated it in place),
		// so it's on the history-side of the boundary too.
		m.bumpGen()
		// Pause the routine so a transient error doesn't auto-loop the next
		// step. The user can press Enter to retry / continue, or Esc to end.
		if m.activeRoutine != nil {
			m.activeRoutine.paused = true
		}
		return m.drainQueue()

	case "aborted":
		logEvent("  ABORTED")
		m.runID = ""
		finalised := false
		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			if last.role == "assistant" && last.streaming {
				last.streaming = false
				last.content += "\n[aborted]"
				finalised = true
				m.finalisedRuns.add(chatEv.RunID)
			}
		}
		m.updateViewport()
		if !finalised {
			return nil
		}
		// Same rationale as the error branch — keep the boundary
		// monotonic so cancelled turns don't pollute the next merge.
		m.bumpGen()
		if m.activeRoutine != nil {
			m.activeRoutine.paused = true
		}
		return m.drainQueue()
	}
	return nil
}

// shouldRingBell reports whether the completion bell should fire on a final
// chat event. The bell is intended as a "look back at me" cue when the user
// has switched away, so a focused terminal suppresses it even when the pref
// is on.
func (m *chatModel) shouldRingBell() bool {
	return m.prefs.CompletionBell && !m.terminalFocused
}

// bellCmd returns a command that writes a BEL character to the terminal.
func bellCmd() tea.Cmd {
	return func() tea.Msg {
		_, _ = os.Stdout.Write([]byte("\a"))
		return nil
	}
}

// handleAgentEvent processes an "agent" event frame. Only the tool-stream
// lifecycle is consumed for now (start/result phases) — other streams
// (lifecycle, item, approval) are ignored and may be wired up later.
func (m *chatModel) handleAgentEvent(ev protocol.Event) tea.Cmd {
	var agentEv protocol.AgentEvent
	if err := json.Unmarshal(ev.Payload, &agentEv); err != nil {
		logEvent("AGENT parse error: %v", err)
		return nil
	}
	if agentEv.Stream != "tool" {
		return nil
	}

	// AgentEvent.Data is map[string]any; round-trip through JSON to get a
	// typed view.
	rawData, err := json.Marshal(agentEv.Data)
	if err != nil {
		logEvent("TOOL marshal data error: %v", err)
		return nil
	}
	var td toolEventData
	if err := json.Unmarshal(rawData, &td); err != nil {
		logEvent("TOOL parse error: %v", err)
		return nil
	}
	if td.ToolCallID == "" {
		return nil
	}
	logEvent("TOOL phase=%s name=%s id=%s isErr=%v", td.Phase, td.Name, td.ToolCallID, td.IsError)

	switch td.Phase {
	case "start":
		// Freeze any currently streaming assistant message so subsequent
		// chat deltas land on a fresh row after the tool card. Drop the
		// pre-delta placeholder if it never received any text — leaving an
		// empty assistant block above the tool card looks broken.
		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			if last.role == "assistant" && last.streaming {
				if last.awaitingDelta && last.content == "" {
					m.messages = m.messages[:len(m.messages)-1]
				} else {
					last.streaming = false
					last.awaitingDelta = false
				}
			}
		}
		name := td.Name
		if name == "" {
			name = "tool"
		}
		m.appendMessage(chatMessage{
			role:         "tool",
			toolName:     name,
			toolCallID:   td.ToolCallID,
			toolArgsLine: summariseArgs(td.Args),
			toolState:    "running",
		})
		m.updateViewport()
		return m.ensureSpinnerTicking()

	case "update":
		// Partial results are ignored in this pass; the running glyph keeps
		// ticking and the final phase resolves the card. See the
		// expand/collapse follow-up for full output rendering.
		return nil

	case "result":
		for i := range m.messages {
			if m.messages[i].role != "tool" {
				continue
			}
			if m.messages[i].toolCallID != td.ToolCallID {
				continue
			}
			if td.IsError {
				m.messages[i].toolState = "error"
				m.messages[i].toolError = extractToolErrorText(td.Result)
			} else {
				m.messages[i].toolState = "success"
			}
			break
		}
		m.updateViewport()
		return nil
	}
	return nil
}

// summariseArgs produces a short single-line preview of a tool's arguments.
// For common shapes ({command:"..."}, {path:"..."}, {query:"..."}, ...) it
// pulls the most useful key. Otherwise it falls back to compact JSON,
// truncated.
func summariseArgs(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return truncateForArgs(strings.TrimSpace(string(raw)))
	}
	// Prefer human-readable keys in priority order.
	for _, key := range []string{"command", "path", "file", "filePath", "query", "url", "name", "message", "text"} {
		if v, ok := obj[key]; ok {
			s := unmarshalString(v)
			if s == "" {
				continue
			}
			return truncateForArgs(fmt.Sprintf("%s=%q", key, s))
		}
	}
	// Fall back to compact JSON.
	compact, err := json.Marshal(obj)
	if err != nil {
		return truncateForArgs(strings.TrimSpace(string(raw)))
	}
	return truncateForArgs(string(compact))
}

// unmarshalString tries to interpret raw as a JSON string. Returns the
// string value, or an empty string if raw is not a string.
func unmarshalString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

// truncateForArgs limits the args summary to a single line, capped at
// 80 runes with an ellipsis.
func truncateForArgs(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	const max = 80
	runes := []rune(s)
	if len(runes) > max {
		s = string(runes[:max-1]) + "…"
	}
	return s
}

// extractToolErrorText pulls a one-line error message out of a failed tool
// result. The gateway nests error text under content[].text — fall back to
// the raw JSON if the shape doesn't match.
func extractToolErrorText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var payload toolResultPayload
	if err := json.Unmarshal(raw, &payload); err == nil {
		var parts []string
		for _, c := range payload.Content {
			if c.Type == "text" && c.Text != "" {
				parts = append(parts, c.Text)
			}
		}
		if joined := strings.TrimSpace(strings.Join(parts, " ")); joined != "" {
			return truncateForArgs(joined)
		}
	}
	return truncateForArgs(strings.TrimSpace(string(raw)))
}
