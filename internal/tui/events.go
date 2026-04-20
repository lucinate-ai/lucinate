package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/a3tai/openclaw-go/protocol"
	tea "charm.land/bubbletea/v2"
)

// chatFinalMessage is the structure of the "message" field in a "final" chat event.
type chatFinalMessage struct {
	Role    string             `json:"role"`
	Content []chatContentBlock `json:"content"`
}

type chatContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// extractTextFromMessage parses the Message field and extracts readable text.
// Delta events send a plain JSON string; final events send a structured object.
func extractTextFromMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Try as a plain JSON string first (delta events).
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}

	// Try as a structured chat message (final events).
	var msg chatFinalMessage
	if json.Unmarshal(raw, &msg) == nil {
		var parts []string
		for _, block := range msg.Content {
			if block.Type == "text" && block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		return strings.Join(parts, "\n")
	}

	// Fallback: return raw string.
	return string(raw)
}

func (m *chatModel) handleEvent(ev protocol.Event) tea.Cmd {
	logEvent("RAW_EVENT name=%s payload_len=%d", ev.EventName, len(ev.Payload))

	// Handle exec events.
	switch ev.EventName {
	case protocol.EventExecFinished:
		var finished protocol.ExecFinished
		if err := json.Unmarshal(ev.Payload, &finished); err != nil {
			logEvent("EXEC_FINISH parse error: %v", err)
			return nil
		}
		logEvent("EXEC_FINISHED session=%s cmd=%s exit=%v output_len=%d", finished.SessionKey, finished.Command, finished.ExitCode, len(finished.Output))
		// Ignore exec results from other sessions.
		if finished.SessionKey != "" && finished.SessionKey != m.sessionKey {
			logEvent("  EXEC_FINISHED ignored (different session)")
			return nil
		}
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
		if denied.SessionKey != "" && denied.SessionKey != m.sessionKey {
			logEvent("  EXEC_DENIED ignored (different session)")
			return nil
		}
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

	if ev.EventName != protocol.EventChat {
		return nil
	}

	var chatEv protocol.ChatEvent
	if err := json.Unmarshal(ev.Payload, &chatEv); err != nil {
		logEvent("PARSE_ERROR: %v payload=%s", err, string(ev.Payload))
		return nil
	}

	logEvent("EVENT state=%s runID=%s seq=%d msgLen=%d", chatEv.State, chatEv.RunID, chatEv.Seq, len(chatEv.Message))

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
				m.updateViewport()
				return nil
			}
			if last.role == "assistant" && !last.streaming {
				logEvent("  DELTA ignored (already finalised)")
				return nil
			}
		}
		m.messages = append(m.messages, chatMessage{
			role:      "assistant",
			content:   deltaText,
			streaming: true,
		})
		m.updateViewport()
		return m.ensureSpinnerTicking()

	case "final":
		logEvent("  FINAL msgContent=%s", string(chatEv.Message))
		finalised := false
		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			if last.role == "assistant" && last.streaming {
				last.streaming = false
				finalised = true
				logEvent("  FINALISED — refreshing history")
			}
		}
		m.updateViewport()
		if !finalised {
			// Empty ack from gateway — the real response hasn't arrived yet.
			logEvent("  FINAL ignored (no streaming assistant message)")
			return nil
		}
		var cmds []tea.Cmd
		if m.prefs.CompletionBell {
			cmds = append(cmds, bellCmd())
		}
		drainCmd := m.drainQueue()
		if m.sending {
			// Still draining the queue — defer history refresh until the queue is empty
			// to avoid replacing m.messages while queued user messages are visible.
			cmds = append(cmds, drainCmd)
			return tea.Batch(cmds...)
		}
		cmds = append(cmds, m.refreshHistory(), m.loadStats())
		return tea.Batch(cmds...)

	case "error":
		logEvent("  ERROR: %s", chatEv.ErrorMessage)
		finalised := false
		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			if last.role == "assistant" && last.streaming {
				last.streaming = false
				last.errMsg = chatEv.ErrorMessage
				finalised = true
			}
		}
		m.updateViewport()
		if !finalised {
			return nil
		}
		return m.drainQueue()

	case "aborted":
		logEvent("  ABORTED")
		finalised := false
		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			if last.role == "assistant" && last.streaming {
				last.streaming = false
				last.content += "\n[aborted]"
				finalised = true
			}
		}
		m.updateViewport()
		if !finalised {
			return nil
		}
		return m.drainQueue()
	}
	return nil
}

// bellCmd returns a command that writes a BEL character to the terminal.
func bellCmd() tea.Cmd {
	return func() tea.Msg {
		_, _ = os.Stdout.Write([]byte("\a"))
		return nil
	}
}
