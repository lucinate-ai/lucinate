package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/a3tai/openclaw-go/protocol"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/olekukonko/tablewriter"

	"github.com/outofcoffee/repclaw/internal/client"
)

var debugLog *log.Logger

func init() {
	f, err := os.OpenFile("/tmp/repclaw-events.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err == nil {
		debugLog = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
	}
}

func logEvent(format string, args ...any) {
	if debugLog != nil {
		debugLog.Printf(format, args...)
	}
}

const inputHeight = 3

// slashCommands is the list of available slash commands for autocomplete.
var slashCommands = []string{"/back", "/clear", "/exit", "/help", "/model", "/quit", "/stats"}

// chatMessage represents a single message in the conversation.
type chatMessage struct {
	role      string // "user", "assistant", or "separator"
	content   string
	streaming bool
	errMsg    string
}

// sessionStats holds token usage stats for display.
type sessionStats struct {
	inputTokens  int
	outputTokens int
	cacheRead    int
	cacheWrite   int
	totalCost    float64
	inputCost    float64
	outputCost   float64
	cacheReadCost  float64
	cacheWriteCost float64
	totalMessages  int
	userMessages   int
	assistantMessages int
}

// chatModel is the chat view.
type chatModel struct {
	viewport   viewport.Model
	textarea   textarea.Model
	messages   []chatMessage
	client     *client.Client
	sessionKey string
	agentName  string
	sending    bool
	width      int
	height     int
	renderer   *glamour.TermRenderer
	stats      *sessionStats
	modelID    string
}

const historyLimit = 20

// historyLoadedMsg is returned when chat history is fetched.
type historyLoadedMsg struct {
	messages []chatMessage
	err      error
}

// historyRefreshMsg replaces all messages with fresh history after a response completes.
type historyRefreshMsg struct {
	messages []chatMessage
	err      error
}

// chatSentMsg is returned after a message is sent.
type chatSentMsg struct {
	err error
}

// statsLoadedMsg is returned when session usage stats are fetched.
type statsLoadedMsg struct {
	stats *sessionStats
	err   error
}

// GatewayEventMsg wraps a gateway event for the bubbletea loop.
// Exported so main.go can send events via p.Send().
type GatewayEventMsg protocol.Event

// historyResponse is the structure of the chat.history RPC response.
type historyResponse struct {
	Messages []historyMessage `json:"messages"`
}

type historyMessage struct {
	Role    string             `json:"role"`
	Content []chatContentBlock `json:"content"`
}

func newChatModel(c *client.Client, sessionKey, agentName, modelID string) chatModel {
	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.Focus()
	ta.CharLimit = 0
	ta.SetHeight(inputHeight)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetKeys("shift+enter")
	ta.KeyMap.DeleteWordBackward.SetKeys("alt+backspace", "ctrl+w")
	ta.KeyMap.DeleteWordForward.SetKeys("alt+delete", "alt+d")

	vp := viewport.New(0, 0)

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(0),
	)

	return chatModel{
		viewport:   vp,
		textarea:   ta,
		client:     c,
		sessionKey: sessionKey,
		agentName:  agentName,
		renderer:   renderer,
		modelID:    modelID,
	}
}

func (m chatModel) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.loadHistory(),
		m.loadStats(),
	)
}

func (m chatModel) loadHistory() tea.Cmd {
	sessionKey := m.sessionKey
	cl := m.client
	return func() tea.Msg {
		raw, err := cl.ChatHistory(context.Background(), sessionKey, historyLimit)
		if err != nil {
			return historyLoadedMsg{err: err}
		}
		var resp historyResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return historyLoadedMsg{err: err}
		}
		var msgs []chatMessage
		for _, hm := range resp.Messages {
			role := hm.Role
			if role != "user" && role != "assistant" {
				continue
			}
			var parts []string
			for _, block := range hm.Content {
				if block.Type == "text" && block.Text != "" {
					parts = append(parts, block.Text)
				}
			}
			text := strings.Join(parts, "\n")
			if text == "" {
				continue
			}
			// For user messages, strip system prefixes (lines starting with "System:")
			if role == "user" {
				text = stripSystemLines(text)
				if text == "" {
					continue
				}
			}
			msgs = append(msgs, chatMessage{role: role, content: text})
		}
		return historyLoadedMsg{messages: msgs}
	}
}

func (m chatModel) refreshHistory() tea.Cmd {
	sessionKey := m.sessionKey
	cl := m.client
	return func() tea.Msg {
		raw, err := cl.ChatHistory(context.Background(), sessionKey, historyLimit)
		if err != nil {
			return historyRefreshMsg{err: err}
		}
		var resp historyResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return historyRefreshMsg{err: err}
		}
		var msgs []chatMessage
		for _, hm := range resp.Messages {
			role := hm.Role
			if role != "user" && role != "assistant" {
				continue
			}
			var parts []string
			for _, block := range hm.Content {
				if block.Type == "text" && block.Text != "" {
					parts = append(parts, block.Text)
				}
			}
			text := strings.Join(parts, "\n")
			if text == "" {
				continue
			}
			if role == "user" {
				text = stripSystemLines(text)
				if text == "" {
					continue
				}
			}
			msgs = append(msgs, chatMessage{role: role, content: text})
		}
		return historyRefreshMsg{messages: msgs}
	}
}

func (m chatModel) loadStats() tea.Cmd {
	cl := m.client
	return func() tea.Msg {
		raw, err := cl.SessionUsage(context.Background(), "")
		if err != nil {
			return statsLoadedMsg{err: err}
		}
		var resp struct {
			Totals struct {
				Input          int     `json:"input"`
				Output         int     `json:"output"`
				CacheRead      int     `json:"cacheRead"`
				CacheWrite     int     `json:"cacheWrite"`
				TotalCost      float64 `json:"totalCost"`
				InputCost      float64 `json:"inputCost"`
				OutputCost     float64 `json:"outputCost"`
				CacheReadCost  float64 `json:"cacheReadCost"`
				CacheWriteCost float64 `json:"cacheWriteCost"`
			} `json:"totals"`
			Aggregates struct {
				Messages struct {
					Total     int `json:"total"`
					User      int `json:"user"`
					Assistant int `json:"assistant"`
				} `json:"messages"`
			} `json:"aggregates"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return statsLoadedMsg{err: err}
		}
		return statsLoadedMsg{stats: &sessionStats{
			inputTokens:      resp.Totals.Input,
			outputTokens:     resp.Totals.Output,
			cacheRead:        resp.Totals.CacheRead,
			cacheWrite:       resp.Totals.CacheWrite,
			totalCost:        resp.Totals.TotalCost,
			inputCost:        resp.Totals.InputCost,
			outputCost:       resp.Totals.OutputCost,
			cacheReadCost:    resp.Totals.CacheReadCost,
			cacheWriteCost:   resp.Totals.CacheWriteCost,
			totalMessages:    resp.Aggregates.Messages.Total,
			userMessages:     resp.Aggregates.Messages.User,
			assistantMessages: resp.Aggregates.Messages.Assistant,
		}}
	}
}

// stripSystemLines removes "System:" prefixed lines and leading whitespace
// from user messages, returning only the human-authored portion.
func stripSystemLines(s string) string {
	lines := strings.Split(s, "\n")
	var kept []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "System:") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func (m chatModel) Update(msg tea.Msg) (chatModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case historyLoadedMsg:
		if msg.err == nil && len(msg.messages) > 0 {
			// Prepend history + separator before any current messages.
			hist := append(msg.messages, chatMessage{role: "separator"})
			m.messages = append(hist, m.messages...)
			m.updateViewport()
		}
		return m, nil

	case modelListMsg:
		if msg.err != nil {
			m.messages = append(m.messages, chatMessage{role: "system", errMsg: msg.err.Error()})
		} else {
			var lines []string
			for _, mc := range msg.models {
				lines = append(lines, fmt.Sprintf("  %s (%s)", mc.ID, mc.Provider))
			}
			m.messages = append(m.messages, chatMessage{
				role:    "system",
				content: "Available models:\n" + strings.Join(lines, "\n") + "\n\nUse /model <name> to switch",
			})
		}
		m.updateViewport()
		return m, nil

	case modelSwitchedMsg:
		if msg.err != nil {
			m.messages = append(m.messages, chatMessage{role: "system", errMsg: msg.err.Error()})
		} else {
			m.modelID = msg.modelID
			m.messages = append(m.messages, chatMessage{
				role:    "system",
				content: fmt.Sprintf("Switched to %s", msg.modelID),
			})
		}
		m.updateViewport()
		return m, nil

	case statsLoadedMsg:
		if msg.err == nil && msg.stats != nil {
			m.stats = msg.stats
		}
		return m, nil

	case historyRefreshMsg:
		if msg.err == nil && len(msg.messages) > 0 {
			m.messages = msg.messages
			m.updateViewport()
		}
		return m, nil

	case tea.KeyMsg:
		logEvent("KEY type=%d alt=%v string=%q", msg.Type, msg.Alt, msg.String())
		switch msg.String() {
		case "tab":
			text := m.textarea.Value()
			if strings.HasPrefix(text, "/") && !strings.Contains(text, " ") {
				if match := completeSlashCommand(text); match != "" {
					m.textarea.Reset()
					m.textarea.SetValue(match)
					// Move cursor to end.
					m.textarea.CursorEnd()
				}
			}
			return m, nil
		case "enter":
			if m.sending {
				return m, nil
			}
			text := strings.TrimSpace(m.textarea.Value())
			if text == "" {
				return m, nil
			}
			m.textarea.Reset()

			// Handle slash commands locally.
			if handled, cmd := m.handleSlashCommand(text); handled {
				return m, cmd
			}

			// Handle remote command execution.
			if strings.HasPrefix(text, "!") {
				command := strings.TrimSpace(text[1:])
				if command == "" {
					return m, nil
				}
				m.messages = append(m.messages, chatMessage{role: "system", content: fmt.Sprintf("$ %s", command)})
				m.messages = append(m.messages, chatMessage{role: "system", content: "running..."})
				m.sending = true
				m.updateViewport()
				cmds = append(cmds, m.execCommand(command))
				return m, tea.Batch(cmds...)
			}

			m.messages = append(m.messages, chatMessage{role: "user", content: text})
			m.sending = true
			m.updateViewport()
			cmds = append(cmds, m.sendMessage(text))
			return m, tea.Batch(cmds...)
		}

	case execSubmittedMsg:
		if msg.err != nil {
			m.sending = false
			if len(m.messages) > 0 {
				last := &m.messages[len(m.messages)-1]
				if last.role == "system" && last.content == "running..." {
					last.content = ""
					last.errMsg = msg.err.Error()
				}
			}
			m.updateViewport()
		}
		// If no error, exec.finished event will update the "running..." message.
		return m, nil

	case chatSentMsg:
		if msg.err != nil {
			logEvent("SEND_ERROR: %v", msg.err)
			// Show the error as an assistant message.
			m.messages = append(m.messages, chatMessage{
				role:   "assistant",
				errMsg: msg.err.Error(),
			})
			m.sending = false
			m.updateViewport()
		}
		return m, nil

	case GatewayEventMsg:
		ev := protocol.Event(msg)
		cmd := m.handleEvent(ev)
		return m, cmd
	}

	// Update sub-components.
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	// Pass non-key messages and scrolling keys to the viewport.
	// Block regular typing keys to prevent the viewport jumping while typing.
	passToViewport := true
	if km, isKey := msg.(tea.KeyMsg); isKey {
		switch km.Type {
		case tea.KeyPgUp, tea.KeyPgDown, tea.KeyUp, tea.KeyDown:
			// Allow scrolling keys through.
		default:
			passToViewport = false
		}
	}
	if passToViewport {
		m.viewport, cmd = m.viewport.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	return m, tea.Batch(cmds...)
}

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
		logEvent("EXEC_FINISHED cmd=%s exit=%v output_len=%d", finished.Command, finished.ExitCode, len(finished.Output))
		m.sending = false
		// Replace the "running..." message.
		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			if last.role == "system" && last.content == "running..." {
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
		return nil

	case protocol.EventExecDenied:
		logEvent("EXEC_DENIED")
		m.sending = false
		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			if last.role == "system" && last.content == "running..." {
				last.content = ""
				last.errMsg = "command execution denied"
			}
		}
		m.updateViewport()
		return nil
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

		// Find or create the current streaming assistant message.
		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			if last.role == "assistant" && last.streaming {
				// Deltas are cumulative — each one contains the full text so far.
				last.content = deltaText
				m.updateViewport()
				return nil
			}
			// Ignore deltas that arrive after the message is already finalised.
			if last.role == "assistant" && !last.streaming {
				logEvent("  DELTA ignored (already finalised)")
				return nil
			}
		}
		// No assistant message yet — create one.
		m.messages = append(m.messages, chatMessage{
			role:      "assistant",
			content:   deltaText,
			streaming: true,
		})
		m.updateViewport()

	case "final":
		logEvent("  FINAL msgContent=%s", string(chatEv.Message))
		m.sending = false
		if len(m.messages) == 0 {
			return nil
		}
		last := &m.messages[len(m.messages)-1]
		if last.role == "assistant" && last.streaming {
			last.streaming = false
			logEvent("  FINALISED — refreshing history")
		}
		m.updateViewport()
		// Reload history and stats from server.
		return tea.Batch(m.refreshHistory(), m.loadStats())

	case "error":
		logEvent("  ERROR: %s", chatEv.ErrorMessage)
		m.sending = false
		if len(m.messages) == 0 {
			return nil
		}
		last := &m.messages[len(m.messages)-1]
		if last.role == "assistant" && last.streaming {
			last.streaming = false
			last.errMsg = chatEv.ErrorMessage
		}
		m.updateViewport()

	case "aborted":
		logEvent("  ABORTED")
		m.sending = false
		if len(m.messages) == 0 {
			return nil
		}
		last := &m.messages[len(m.messages)-1]
		if last.role == "assistant" && last.streaming {
			last.streaming = false
			last.content += "\n[aborted]"
		}
		m.updateViewport()
	}
	return nil
}

// completeSlashCommand returns the first matching slash command for the given
// prefix, or "" if no match.
func completeSlashCommand(prefix string) string {
	lower := strings.ToLower(prefix)
	for _, cmd := range slashCommands {
		if strings.HasPrefix(cmd, lower) {
			return cmd
		}
	}
	return ""
}

// slashCommandHint returns the completion hint to display after the current
// input, or "" if no hint applies.
func slashCommandHint(input string) string {
	if !strings.HasPrefix(input, "/") || strings.Contains(input, " ") || input == "" {
		return ""
	}
	match := completeSlashCommand(input)
	if match == "" || match == strings.ToLower(input) {
		return ""
	}
	// Return only the suffix that hasn't been typed yet.
	return match[len(input):]
}

// modelListMsg is returned when the model list is fetched.
type modelListMsg struct {
	models []protocol.ModelChoice
	err    error
}

// modelSwitchedMsg is returned after switching models.
type modelSwitchedMsg struct {
	modelID string
	err     error
}

// handleModelCommand handles `/model` and `/model <name>`.
func (m *chatModel) handleModelCommand(text string) (bool, tea.Cmd) {
	parts := strings.SplitN(strings.TrimSpace(text), " ", 2)
	if len(parts) == 1 {
		// `/model` — list available models.
		cl := m.client
		return true, func() tea.Msg {
			result, err := cl.ModelsList(context.Background())
			if err != nil {
				return modelListMsg{err: err}
			}
			return modelListMsg{models: result.Models}
		}
	}

	// `/model <name>` — switch model.
	query := strings.ToLower(strings.TrimSpace(parts[1]))
	cl := m.client
	sessionKey := m.sessionKey
	return true, func() tea.Msg {
		result, err := cl.ModelsList(context.Background())
		if err != nil {
			return modelSwitchedMsg{err: err}
		}
		// Find best match.
		var match *protocol.ModelChoice
		for i, mc := range result.Models {
			lower := strings.ToLower(mc.ID)
			if lower == query || strings.ToLower(mc.Name) == query {
				match = &result.Models[i]
				break
			}
			if strings.Contains(lower, query) || strings.Contains(strings.ToLower(mc.Name), query) {
				match = &result.Models[i]
				// Keep looking for an exact match.
			}
		}
		if match == nil {
			return modelSwitchedMsg{err: fmt.Errorf("no model matching %q", query)}
		}
		if err := cl.SessionPatchModel(context.Background(), sessionKey, match.ID); err != nil {
			return modelSwitchedMsg{err: err}
		}
		return modelSwitchedMsg{modelID: match.ID}
	}
}

// goBackMsg signals the AppModel to return to agent selection.
type goBackMsg struct{}

// handleSlashCommand processes local slash commands. Returns (true, cmd) if
// the input was handled as a command, or (false, nil) if it should be sent
// to the gateway.
func (m *chatModel) handleSlashCommand(text string) (handled bool, cmd tea.Cmd) {
	command := strings.ToLower(strings.TrimSpace(text))
	switch command {
	case "/quit", "/exit":
		return true, tea.Quit
	case "/back":
		return true, func() tea.Msg { return goBackMsg{} }
	case "/clear":
		m.messages = nil
		m.updateViewport()
		return true, nil
	case "/help":
		m.messages = append(m.messages, chatMessage{
			role:    "system",
			content: "/quit, /exit — quit repclaw\n/back — return to agent list\n/clear — clear chat display\n/model — list available models\n/model <name> — switch model\n/stats — show session statistics\n/help — show this help\n\n!<command> — run command on gateway host",
		})
		m.updateViewport()
		return true, nil
	case "/stats":
		if m.stats == nil {
			m.messages = append(m.messages, chatMessage{role: "system", content: "Stats not yet loaded..."})
			m.updateViewport()
			return true, m.loadStats()
		}
		m.messages = append(m.messages, chatMessage{role: "system", content: m.formatStatsTable()})
		m.updateViewport()
		return true, nil
	}

	// /model with optional argument.
	if command == "/model" || strings.HasPrefix(command, "/model ") {
		return m.handleModelCommand(text)
	}

	// Unknown slash command.
	if strings.HasPrefix(text, "/") {
		m.messages = append(m.messages, chatMessage{
			role:   "system",
			errMsg: fmt.Sprintf("unknown command: %s (try /help)", command),
		})
		m.updateViewport()
		return true, nil
	}

	return false, nil
}

// renderMarkdown applies glamour markdown rendering to a completed message.
func (m *chatModel) renderMarkdown(msg *chatMessage) {
	if m.renderer != nil && msg.content != "" {
		if rendered, err := m.renderer.Render(msg.content); err == nil {
			msg.content = strings.TrimSpace(rendered)
		}
	}
}

// execSubmittedMsg signals the exec request was submitted (output comes via events).
type execSubmittedMsg struct {
	err error
}

func (m *chatModel) execCommand(command string) tea.Cmd {
	cl := m.client
	sessionKey := m.sessionKey
	return func() tea.Msg {
		ctx := context.Background()

		// Request execution.
		result, err := cl.ExecRequest(ctx, command, sessionKey)
		if err != nil {
			return execSubmittedMsg{err: err}
		}

		decision := ""
		if result.Decision != nil {
			decision = *result.Decision
		}
		logEvent("EXEC request id=%s status=%s decision=%q", result.ID, result.Status, decision)

		switch decision {
		case "deny":
			return execSubmittedMsg{err: fmt.Errorf("command execution denied by gateway")}
		case "":
			// No decision yet — auto-approve (user explicitly typed the command).
			_, err := cl.ExecResolve(ctx, result.ID, "approve")
			if err != nil {
				return execSubmittedMsg{err: fmt.Errorf("approval failed: %w", err)}
			}
			logEvent("EXEC auto-approved id=%s", result.ID)
		default:
			logEvent("EXEC decision=%q — waiting for exec.finished event", decision)
		}

		// Output arrives via exec.finished event through the event pump.
		return execSubmittedMsg{}
	}
}

func (m *chatModel) sendMessage(text string) tea.Cmd {
	sessionKey := m.sessionKey
	return func() tea.Msg {
		idemKey := fmt.Sprintf("repclaw-%d", time.Now().UnixNano())
		_, err := m.client.ChatSend(context.Background(), sessionKey, text, idemKey)
		return chatSentMsg{err: err}
	}
}

func (m *chatModel) updateViewport() {
	var b strings.Builder
	contentWidth := m.width - 4 // leave some padding

	for i, msg := range m.messages {
		if i > 0 {
			b.WriteString("\n")
		}
		switch msg.role {
		case "separator":
			sep := strings.Repeat("─", contentWidth)
			b.WriteString(statusStyle.Render(sep))
			b.WriteString("\n")

		case "user":
			prefix := userPrefixStyle.Render("You: ")
			b.WriteString(prefix)
			b.WriteString(wordWrap(msg.content, contentWidth-6))
			b.WriteString("\n")

		case "assistant":
			prefixLen := len(m.agentName) + 2 // "name: "
			prefix := assistantPrefixStyle.Render(m.agentName + ": ")
			b.WriteString(prefix)
			wrapWidth := contentWidth - prefixLen
			if msg.errMsg != "" {
				b.WriteString(errorStyle.Render(wordWrap(msg.errMsg, wrapWidth)))
			} else if msg.streaming {
				b.WriteString(wordWrap(msg.content, wrapWidth))
				b.WriteString(cursorStyle.Render("_"))
			} else {
				b.WriteString(wordWrap(msg.content, wrapWidth))
			}
			b.WriteString("\n")

		case "system":
			if msg.errMsg != "" {
				b.WriteString(errorStyle.Render(msg.errMsg))
			} else {
				b.WriteString(statusStyle.Render(msg.content))
			}
			b.WriteString("\n")
		}
	}

	content := b.String()

	// Pad the top so messages are anchored to the bottom of the viewport,
	// like a chat app. Count content lines and prepend empty lines if needed.
	contentLines := strings.Count(content, "\n")
	if contentLines < m.viewport.Height {
		padding := strings.Repeat("\n", m.viewport.Height-contentLines)
		content = padding + content
	}

	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
}

func (m *chatModel) setSize(w, h int) {
	m.width = w
	m.height = h

	headerH := 1
	helpH := 1
	borderH := 2
	vpHeight := h - inputHeight - headerH - helpH - borderH - 2

	m.viewport.Width = w
	m.viewport.Height = vpHeight

	m.textarea.SetWidth(w - 4)
	m.updateViewport()
}

func (m chatModel) View() string {
	// Build header: left side = app/agent/model, right side = stats.
	left := fmt.Sprintf(" repclaw — %s", m.agentName)
	if m.modelID != "" {
		// Show short model name (strip provider prefix).
		model := m.modelID
		if i := strings.LastIndex(model, "/"); i >= 0 {
			model = model[i+1:]
		}
		left += " · " + model
	}
	right := ""
	if m.stats != nil {
		newTokens := m.stats.inputTokens + m.stats.outputTokens
		right = fmt.Sprintf("tokens: %s (%s cached)  $%.2f ",
			formatTokens(newTokens), formatTokens(m.stats.cacheRead), m.stats.totalCost)
	}
	// Only show right side if it fits.
	title := left
	if right != "" {
		padding := m.width - len(left) - len(right)
		if padding > 0 {
			title += strings.Repeat(" ", padding) + right
		}
	}
	header := headerStyle.
		Width(m.width).
		Render(title)

	// Change input border colour when in exec mode.
	borderStyle := inputBorderStyle
	isExecMode := strings.HasPrefix(m.textarea.Value(), "!")
	if isExecMode {
		borderStyle = execBorderStyle
	}
	input := borderStyle.
		Width(m.width - 4).
		Render(m.textarea.View())

	// Show completion hint or contextual help.
	var help string
	if isExecMode {
		help = helpStyle.Render(execPrefixStyle.Render(" remote command") + " — runs on gateway host")
	} else {
		hint := slashCommandHint(m.textarea.Value())
		if hint != "" {
			help = helpStyle.Render(fmt.Sprintf(" %s%s — tab to complete", m.textarea.Value(), hint))
		} else {
			help = helpStyle.Render(" enter: send | shift+enter: newline | ctrl+w: delete word | pgup/pgdn: scroll | /help: commands")
		}
	}

	return lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		m.viewport.View(),
		input,
		help,
	)
}

// formatStatsTable renders session stats as a formatted table.
func (m *chatModel) formatStatsTable() string {
	s := m.stats
	var buf strings.Builder

	allTokens := s.inputTokens + s.outputTokens + s.cacheRead + s.cacheWrite

	// Token/cost table.
	t := tablewriter.NewWriter(&buf)
	t.Header([]string{"", "Tokens", "Cost"})
	t.Bulk([][]string{
		{"Input", formatTokens(s.inputTokens), formatCost(s.inputCost)},
		{"Output", formatTokens(s.outputTokens), formatCost(s.outputCost)},
		{"Cache read", formatTokens(s.cacheRead), formatCost(s.cacheReadCost)},
		{"Cache write", formatTokens(s.cacheWrite), formatCost(s.cacheWriteCost)},
		{"Total", formatTokens(allTokens), formatCost(s.totalCost)},
	})
	t.Footer(nil)
	t.Render()

	buf.WriteString("\n")

	// Messages table.
	t2 := tablewriter.NewWriter(&buf)
	t2.Header([]string{"Messages", "Count"})
	t2.Bulk([][]string{
		{"User", fmt.Sprintf("%d", s.userMessages)},
		{"Assistant", fmt.Sprintf("%d", s.assistantMessages)},
		{"Total", fmt.Sprintf("%d", s.totalMessages)},
	})
	t2.Footer(nil)
	t2.Render()

	return buf.String()
}

// formatCost formats a dollar amount.
func formatCost(c float64) string {
	if c < 0.01 {
		return fmt.Sprintf("$%.4f", c)
	}
	return fmt.Sprintf("$%.2f", c)
}

// formatTokens formats a token count with K/M suffixes.
func formatTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// wordWrap is a simple word wrapper.
func wordWrap(s string, width int) string {
	if width <= 0 || len(s) <= width {
		return s
	}
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if len(line) <= width {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(line)
			continue
		}
		words := strings.Fields(line)
		lineLen := 0
		for i, w := range words {
			if i > 0 && lineLen+1+len(w) > width {
				b.WriteString("\n")
				lineLen = 0
			} else if i > 0 {
				b.WriteString(" ")
				lineLen++
			}
			b.WriteString(w)
			lineLen += len(w)
		}
		b.WriteString("\n")
	}
	return b.String()
}
