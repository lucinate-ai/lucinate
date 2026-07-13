package tui

import (
	"encoding/json"
	"fmt"
	"log/slog"
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
	slog.Debug("raw event", "name", ev.EventName, "payload_len", len(ev.Payload))

	// Drop events scoped to a different session before any handler runs.
	// Events without a sessionKey (or with an empty one) fall through.
	if key := extractEventSessionKey(ev.Payload); key != "" && key != m.sessionKey {
		slog.Debug("event ignored: session mismatch", "key", key, "ours", m.sessionKey)
		return nil
	}

	// Handle exec events.
	switch ev.EventName {
	case protocol.EventExecFinished:
		var finished protocol.ExecFinished
		if err := json.Unmarshal(ev.Payload, &finished); err != nil {
			slog.Debug("exec_finished parse error", "err", err)
			return nil
		}
		slog.Debug("exec finished", "session", finished.SessionKey, "cmd", finished.Command, "exit", finished.ExitCode, "output_len", len(finished.Output))
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
			slog.Debug("exec.approval.resolved parse error", "err", err)
			return nil
		}
		slog.Debug("exec approval resolved", "id", resolved.ID, "decision", resolved.Decision)
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
			slog.Debug("exec_denied parse error", "err", err)
			return nil
		}
		slog.Debug("exec denied", "session", denied.SessionKey, "reason", denied.Reason)
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
		slog.Debug("chat event parse error", "err", err, "payload", string(ev.Payload))
		// A malformed chat event for an in-flight turn is functionally a
		// hang — clear sending and tell the user, so the spinner stops.
		if m.sending {
			m.runID = ""
			m.notifyError(fmt.Sprintf("gateway sent malformed chat event: %v", err))
			return m.drainQueue()
		}
		return nil
	}

	slog.Debug("chat event", "state", chatEv.State, "run_id", chatEv.RunID, "seq", chatEv.Seq, "msg_len", len(chatEv.Message), "session_key", chatEv.SessionKey)

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
		slog.Debug("stale event ignored for finalised run", "run_id", chatEv.RunID)
		return nil
	}

	switch chatEv.State {
	case "delta":
		deltaText := extractTextFromMessage(chatEv.Message)
		slog.Debug("delta", "text", deltaText)
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
				slog.Debug("delta ignored: already finalised")
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
		slog.Debug("final", "msg_content", string(chatEv.Message))
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
				slog.Debug("finalised, refreshing history", "thinking_len", len(last.thinking))
			}
		}
		m.updateViewport()
		if !finalised {
			// Empty ack from gateway — the real response hasn't arrived yet.
			slog.Debug("final ignored: no streaming assistant message")
			return nil
		}
		// Capture the merge boundary *before* bumping so the just-
		// finalised turn is on the history-side of any refresh issued
		// from here on; subsequent appends (drained queue, auto-
		// advanced routine step, recovery system rows) get the new gen
		// and survive the merge.
		boundary := m.bumpGen()
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
		// Always resync canonical history. Layer 2's merge in the
		// historyRefreshMsg handler keeps the live tail (anything
		// appended below at gen > boundary) intact, so this is safe
		// even when a routine is auto-advancing or a queued message
		// is about to be dispatched. Without this unconditional
		// resync, mid-routine drift would accumulate over many steps
		// and could let a stale chat event slip through the gate that
		// guards spurious step submission.
		cmds = append(cmds, m.refreshHistoryAt(boundary), m.loadStats())
		// drainQueueSkipRefresh because we have already queued the
		// resync above; the queue-empty branch of the regular
		// drainQueue would otherwise issue a redundant refresh with
		// the same boundary.
		if cmd := m.drainQueueSkipRefresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		// If the queue was empty, drainQueueSkipRefresh has set
		// m.sending=false and returned nil. The routine controller
		// can advance now; it sets m.sending=true and dispatches the
		// next step. Tagged with the new gen, that step's appended
		// rows are on the live side of the boundary the refresh is
		// carrying — the merge will preserve them.
		if !m.sending {
			if cmd := m.maybeAdvanceRoutine(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		// Reflow now that the turn has settled: with sending cleared the
		// strip collapses from its expanded list to the one-line summary,
		// so the viewport must grow to reclaim those rows. (A routine that
		// auto-advanced already reset the strip and reflowed; this is a
		// harmless recompute in that case.)
		m.applyLayout()
		m.updateViewport()
		return tea.Batch(cmds...)

	case "error":
		slog.Debug("chat error", "message", chatEv.ErrorMessage)
		m.runID = ""
		m.finalisedRuns.add(chatEv.RunID)
		// Drop the tool strip: a tool left mid-run would otherwise keep the
		// spinner ticking forever and freeze a running glyph in the strip.
		m.resetToolActivity()
		attached := false
		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			if last.role == "assistant" && last.streaming {
				last.streaming = false
				last.errMsg = chatEv.ErrorMessage
				attached = true
			}
		}
		if !attached {
			// No streaming row to surface the error on (the placeholder
			// was already finalised, or never appeared). Without this
			// fallback the spinner would tick forever and the user
			// would never see the gateway's error.
			msg := chatEv.ErrorMessage
			if msg == "" {
				msg = "gateway returned an error"
			}
			m.notifyError(msg)
			slog.Debug("error not attached, surfaced via notification", "run_id", chatEv.RunID)
		}
		m.updateViewport()
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
		slog.Debug("chat aborted")
		m.runID = ""
		m.finalisedRuns.add(chatEv.RunID)
		// Same as the error branch — clear any mid-run tool so the strip
		// and spinner don't stick after cancellation.
		m.resetToolActivity()
		attached := false
		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			if last.role == "assistant" && last.streaming {
				last.streaming = false
				last.content += "\n[aborted]"
				attached = true
			}
		}
		if !attached {
			m.notify("Run aborted.")
			slog.Debug("aborted not attached, surfaced via notification", "run_id", chatEv.RunID)
		}
		m.updateViewport()
		// Same rationale as the error branch — keep the boundary
		// monotonic so cancelled turns don't pollute the next merge.
		m.bumpGen()
		if m.activeRoutine != nil {
			m.activeRoutine.paused = true
		}
		return m.drainQueue()

	default:
		slog.Debug("unknown chat state", "state", chatEv.State, "run_id", chatEv.RunID)
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
		slog.Debug("agent event parse error", "err", err)
		return nil
	}
	if agentEv.Stream != "tool" {
		return nil
	}

	// AgentEvent.Data is map[string]any; round-trip through JSON to get a
	// typed view.
	rawData, err := json.Marshal(agentEv.Data)
	if err != nil {
		slog.Debug("tool event marshal error", "err", err)
		return nil
	}
	var td toolEventData
	if err := json.Unmarshal(rawData, &td); err != nil {
		slog.Debug("tool event parse error", "err", err)
		return nil
	}
	if td.ToolCallID == "" {
		return nil
	}
	slog.Debug("tool event", "phase", td.Phase, "name", td.Name, "id", td.ToolCallID, "is_err", td.IsError)

	switch td.Phase {
	case "start":
		// Record the tool in the ephemeral activity strip rather than the
		// message list. Keeping it off m.messages leaves the streaming
		// assistant row intact, so the whole turn's cumulative deltas land
		// on one row instead of being re-rendered from the top after every
		// tool call. The streaming placeholder stays put — its spinner
		// reads as "the agent is working" while the strip shows on what.
		name := td.Name
		if name == "" {
			name = "tool"
		}
		m.activeTools = append(m.activeTools, toolActivity{
			name:     name,
			callID:   td.ToolCallID,
			argsLine: summariseArgs(td.Args),
			state:    "running",
		})
		m.applyLayout()
		m.updateViewport()
		return m.ensureSpinnerTicking()

	case "update":
		// Partial results are ignored in this pass; the running glyph keeps
		// ticking and the final phase resolves the entry. See the
		// expand/collapse follow-up for full output rendering.
		return nil

	case "result":
		for i := range m.activeTools {
			if m.activeTools[i].callID != td.ToolCallID {
				continue
			}
			if td.IsError {
				m.activeTools[i].state = "error"
				m.activeTools[i].errText = extractToolErrorText(td.Result)
			} else {
				m.activeTools[i].state = "success"
			}
			break
		}
		m.applyLayout()
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
