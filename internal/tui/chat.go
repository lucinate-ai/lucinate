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
var slashCommands = []string{"/back", "/clear", "/exit", "/help", "/quit"}

// chatMessage represents a single message in the conversation.
type chatMessage struct {
	role      string // "user", "assistant", or "separator"
	content   string
	streaming bool
	errMsg    string
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

func newChatModel(c *client.Client, sessionKey, agentName string) chatModel {
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
	}
}

func (m chatModel) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.loadHistory(),
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

			m.messages = append(m.messages, chatMessage{role: "user", content: text})
			m.sending = true
			m.updateViewport()
			cmds = append(cmds, m.sendMessage(text))
			return m, tea.Batch(cmds...)
		}

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
		// Reload history from server to get properly split messages.
		return m.refreshHistory()

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
			content: "/quit, /exit — quit repclaw\n/back — return to agent list\n/clear — clear chat display\n/help — show this help",
		})
		m.updateViewport()
		return true, nil
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
				b.WriteString(errorStyle.Render(wordWrap(msg.errMsg, contentWidth)))
			} else {
				b.WriteString(statusStyle.Render(wordWrap(msg.content, contentWidth)))
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
	header := headerStyle.
		Width(m.width).
		Render(fmt.Sprintf(" repclaw — %s", m.agentName))

	input := inputBorderStyle.
		Width(m.width - 4).
		Render(m.textarea.View())

	// Show completion hint or default help.
	hint := slashCommandHint(m.textarea.Value())
	var help string
	if hint != "" {
		help = helpStyle.Render(fmt.Sprintf(" %s%s — tab to complete", m.textarea.Value(), hint))
	} else {
		help = helpStyle.Render(" enter: send | shift+enter: newline | ctrl+w: delete word | pgup/pgdn: scroll | /help: commands")
	}

	return lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		m.viewport.View(),
		input,
		help,
	)
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
