package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/a3tai/openclaw-go/protocol"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/outofcoffee/repclaw/internal/client"
)

const inputHeight = 3

// spinnerFrames cycles the streaming-response placeholder through a braille
// spinner. Each frame is a single display cell so line wrapping is unaffected.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinnerInterval = 120 * time.Millisecond

// chatModel is the chat view.
type chatModel struct {
	viewport         viewport.Model
	textarea         textarea.Model
	messages         []chatMessage
	client           *client.Client
	sessionKey       string
	agentID          string
	agentName        string
	sending          bool
	pendingMessages  []string
	width            int
	height           int
	renderer         *glamour.TermRenderer
	stats            *sessionStats
	modelID          string
	skills           []agentSkill
	skillCatalogSent bool
	spinnerFrame     int
	spinnerTicking   bool
}

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

// hasStreamingMessage reports whether any assistant message is still streaming.
func (m *chatModel) hasStreamingMessage() bool {
	for i := range m.messages {
		if m.messages[i].streaming {
			return true
		}
	}
	return false
}

// ensureSpinnerTicking starts the spinner animation if one is not already scheduled.
func (m *chatModel) ensureSpinnerTicking() tea.Cmd {
	if m.spinnerTicking {
		return nil
	}
	m.spinnerTicking = true
	return spinnerTickCmd()
}

func newChatModel(c *client.Client, sessionKey, agentID, agentName, modelID string) chatModel {
	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.Focus()
	ta.CharLimit = 0
	ta.SetHeight(inputHeight)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetKeys("alt+enter")
	ta.KeyMap.DeleteWordBackward.SetKeys("alt+backspace", "ctrl+w")
	ta.KeyMap.DeleteWordForward.SetKeys("alt+delete", "alt+d")

	vp := viewport.New()

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(80), // updated by setSize() when terminal dimensions are known
	)

	return chatModel{
		viewport:   vp,
		textarea:   ta,
		client:     c,
		sessionKey: sessionKey,
		agentID:    agentID,
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
		func() tea.Msg { return skillsDiscoveredMsg{skills: discoverSkills()} },
	)
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
			inputTokens:       resp.Totals.Input,
			outputTokens:      resp.Totals.Output,
			cacheRead:         resp.Totals.CacheRead,
			cacheWrite:        resp.Totals.CacheWrite,
			totalCost:         resp.Totals.TotalCost,
			inputCost:         resp.Totals.InputCost,
			outputCost:        resp.Totals.OutputCost,
			cacheReadCost:     resp.Totals.CacheReadCost,
			cacheWriteCost:    resp.Totals.CacheWriteCost,
			totalMessages:     resp.Aggregates.Messages.Total,
			userMessages:      resp.Aggregates.Messages.User,
			assistantMessages: resp.Aggregates.Messages.Assistant,
		}}
	}
}

func (m chatModel) Update(msg tea.Msg) (chatModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case historyLoadedMsg:
		if msg.err == nil && len(msg.messages) > 0 {
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

	case skillsDiscoveredMsg:
		m.skills = msg.skills
		if len(m.skills) > 0 {
			m.messages = append(m.messages, chatMessage{
				role:    "system",
				content: fmt.Sprintf("%d agent skill(s) loaded — type /skills to list", len(m.skills)),
			})
			m.updateViewport()
		}
		return m, nil

	case historyRefreshMsg:
		if msg.err == nil && len(msg.messages) > 0 {
			m.messages = msg.messages
			m.updateViewport()
		}
		return m, nil

	case tea.KeyPressMsg:
		logEvent("KEY code=%d mod=%v string=%q", msg.Code, msg.Mod, msg.String())
		switch msg.String() {
		case "tab":
			text := m.textarea.Value()
			if strings.HasPrefix(text, "/") && !strings.Contains(text, " ") {
				if match := m.completeSlashCommand(text); match != "" {
					m.textarea.Reset()
					m.textarea.SetValue(match)
					m.textarea.CursorEnd()
				}
			}
			return m, nil
		case "up":
			// Recall the most recent queued message into the input for editing
			// or deletion. Only when the input is empty, so multi-line cursor
			// movement is preserved.
			if m.textarea.Value() == "" && len(m.pendingMessages) > 0 {
				last := len(m.pendingMessages) - 1
				text := m.pendingMessages[last]
				m.pendingMessages = m.pendingMessages[:last]
				m.textarea.Reset()
				m.textarea.SetValue(text)
				m.textarea.CursorEnd()
				m.updateViewport()
				return m, nil
			}
		case "enter":
			text := strings.TrimSpace(m.textarea.Value())
			if text == "" {
				return m, nil
			}

			// Slash commands are local — handle immediately even while sending.
			if handled, cmd := m.handleSlashCommand(text); handled {
				m.textarea.Reset()
				return m, cmd
			}

			if m.sending {
				// Queue the message for later delivery.
				m.textarea.Reset()
				m.pendingMessages = append(m.pendingMessages, text)
				m.updateViewport()
				return m, nil
			}

			m.textarea.Reset()

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
			cmds = append(cmds, m.sendMessage(m.withSkillCatalog(text)))
			return m, tea.Batch(cmds...)
		}

	case execSubmittedMsg:
		if msg.err != nil {
			if len(m.messages) > 0 {
				last := &m.messages[len(m.messages)-1]
				if last.role == "system" && last.content == "running..." {
					last.content = ""
					last.errMsg = msg.err.Error()
				}
			}
			m.updateViewport()
			cmd := m.drainQueueSkipRefresh()
			return m, cmd
		}
		return m, nil

	case chatSentMsg:
		if msg.err != nil {
			logEvent("SEND_ERROR: %v", msg.err)
			m.messages = append(m.messages, chatMessage{
				role:   "assistant",
				errMsg: msg.err.Error(),
			})
			m.updateViewport()
			cmd := m.drainQueue()
			return m, cmd
		}
		return m, nil

	case GatewayEventMsg:
		ev := protocol.Event(msg)
		cmd := m.handleEvent(ev)
		return m, cmd

	case spinnerTickMsg:
		if !m.hasStreamingMessage() {
			m.spinnerTicking = false
			return m, nil
		}
		m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
		m.updateViewport()
		return m, spinnerTickCmd()
	}

	// Update sub-components.
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	passToViewport := true
	switch msg.(type) {
	case tea.MouseWheelMsg:
		// Allow mouse wheel events through to the viewport.
	case tea.KeyPressMsg:
		km := msg.(tea.KeyPressMsg)
		switch km.Code {
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

func (m *chatModel) sendMessage(text string) tea.Cmd {
	sessionKey := m.sessionKey
	return func() tea.Msg {
		idemKey := fmt.Sprintf("repclaw-%d", time.Now().UnixNano())
		_, err := m.client.ChatSend(context.Background(), sessionKey, text, idemKey)
		return chatSentMsg{err: err}
	}
}

// withSkillCatalog prepends the skill catalog to the message text on the first
// send. The catalog uses "System:" prefixed lines which stripSystemLines removes
// from display in history.
func (m *chatModel) withSkillCatalog(text string) string {
	if m.skillCatalogSent || len(m.skills) == 0 {
		return text
	}
	m.skillCatalogSent = true
	return skillCatalogBlock(m.skills) + "\n" + text
}

// drainQueue sends the next queued message if any are pending.
// It should be called whenever m.sending would be set to false.
// Returns a tea.Cmd if a queued message was sent, nil otherwise.
func (m *chatModel) drainQueue() tea.Cmd {
	return m.drainQueueOpt(true)
}

// drainQueueSkipRefresh drains the queue without refreshing history when
// empty. Use this for command-execution paths where locally-added messages
// (e.g. "$ cmd" and output) would be lost by a server-side history refresh.
func (m *chatModel) drainQueueSkipRefresh() tea.Cmd {
	return m.drainQueueOpt(false)
}

func (m *chatModel) drainQueueOpt(refresh bool) tea.Cmd {
	if len(m.pendingMessages) == 0 {
		m.sending = false
		if refresh {
			// Queue fully drained — refresh history now that all messages have been sent.
			return tea.Batch(m.refreshHistory(), m.loadStats())
		}
		return nil
	}

	text := m.pendingMessages[0]
	m.pendingMessages = m.pendingMessages[1:]

	if strings.HasPrefix(text, "!") {
		command := strings.TrimSpace(text[1:])
		if command == "" {
			m.sending = false
			return nil
		}
		m.messages = append(m.messages, chatMessage{role: "system", content: fmt.Sprintf("$ %s", command)})
		m.messages = append(m.messages, chatMessage{role: "system", content: "running..."})
		m.updateViewport()
		return m.execCommand(command)
	}

	m.messages = append(m.messages, chatMessage{role: "user", content: text})
	m.updateViewport()
	return m.sendMessage(text)
}

func (m *chatModel) setSize(w, h int) {
	m.width = w
	m.height = h

	// Recreate the glamour renderer with the new wrap width.
	wrapWidth := w - 4 - m.prefixWidth() // contentWidth minus aligned prefix
	if wrapWidth < 20 {
		wrapWidth = 20
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(wrapWidth),
	)
	if err == nil {
		m.renderer = renderer
	}

	headerH := 1
	helpH := 1
	borderH := 2
	vpHeight := h - inputHeight - headerH - helpH - borderH - 2

	m.viewport.SetWidth(w)
	m.viewport.SetHeight(vpHeight)

	m.textarea.SetWidth(w - 4)
	m.updateViewport()
}

func (m chatModel) View() string {
	left := fmt.Sprintf(" repclaw — %s", m.agentName)
	if m.modelID != "" {
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

	borderStyle := inputBorderStyle
	isExecMode := strings.HasPrefix(m.textarea.Value(), "!")
	if isExecMode {
		borderStyle = execBorderStyle
	}
	input := borderStyle.
		Width(m.width - 4).
		Render(m.textarea.View())

	var help string
	if isExecMode {
		help = helpStyle.Render(execPrefixStyle.Render(" remote command") + " — runs on gateway host")
	} else {
		hint := m.slashCommandHint(m.textarea.Value())
		if hint != "" {
			help = helpStyle.Render(fmt.Sprintf(" %s%s — tab to complete", m.textarea.Value(), hint))
		} else {
			helpText := " enter: send | alt+enter: newline | /help: commands"
			if n := len(m.pendingMessages); n > 0 {
				helpText += fmt.Sprintf(" | %d queued (up: edit last)", n)
			}
			help = helpStyle.Render(helpText)
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
