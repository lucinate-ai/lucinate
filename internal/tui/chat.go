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

	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// connectionBadge returns a short status string for the chat header when the
// gateway connection is not in the steady-state Connected condition. Returns
// empty when connected so the header stays clean.
func connectionBadge(s ConnStateMsg) string {
	switch s.Status {
	case client.StatusDisconnected:
		return headerBadgeWarnStyle.Render("⚠ disconnected")
	case client.StatusReconnecting:
		if s.Attempt > 1 {
			return headerBadgeWarnStyle.Render(fmt.Sprintf("⟳ reconnecting (attempt %d)", s.Attempt))
		}
		return headerBadgeWarnStyle.Render("⟳ reconnecting")
	case client.StatusAuthFailed:
		return headerBadgeErrStyle.Render("✖ auth failed — restart")
	default:
		return ""
	}
}

// applyConnState updates the chat view in response to a connection-state
// transition: stores the new state, and on Connected→non-Connected
// transitions during a streaming reply, clears the stale streaming
// placeholder so the input is usable again.
func (m *chatModel) applyConnState(next ConnStateMsg) {
	prev := m.connState
	m.connState = next

	switch next.Status {
	case client.StatusDisconnected:
		// Only emit the system note on a real Connected→Disconnected edge,
		// not on an initial state push at startup.
		if prev.Status == client.StatusConnected {
			// Drop the stale spinner placeholder if a turn was in flight.
			// The gateway will not deliver any further deltas after a restart;
			// holding the placeholder forever would lock the input.
			m.removeThinkingPlaceholder()
			m.sending = false
			m.runID = ""
			m.messages = append(m.messages, chatMessage{
				role:    "system",
				content: "Lost gateway connection — attempting to reconnect…",
			})
			m.updateViewport()
		}
	case client.StatusConnected:
		if prev.Status == client.StatusDisconnected || prev.Status == client.StatusReconnecting {
			m.messages = append(m.messages, chatMessage{
				role:    "system",
				content: "Reconnected to gateway.",
			})
			m.updateViewport()
		}
	case client.StatusAuthFailed:
		errLine := "Reconnect failed: gateway rejected the device token. Quit (Ctrl+C) and restart to re-authenticate."
		if next.Err != nil {
			errLine = fmt.Sprintf("Reconnect failed (%v). Quit (Ctrl+C) and restart to re-authenticate.", next.Err)
		}
		m.messages = append(m.messages, chatMessage{role: "system", errMsg: errLine})
		m.updateViewport()
	}
}

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
	runID            string // active run ID for cancellation
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
	prefs            config.Preferences
	pendingConfirm   *pendingConfirmation
	historyLimit     int
	thinkingLevel    string // current thinking level; "" means not set / using gateway default
	connState        ConnStateMsg
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

// removeThinkingPlaceholder removes the streaming assistant placeholder added
// when a message is sent, before any gateway delta has arrived.
func (m *chatModel) removeThinkingPlaceholder() {
	if len(m.messages) > 0 {
		last := &m.messages[len(m.messages)-1]
		if last.role == "assistant" && last.streaming && last.awaitingDelta {
			m.messages = m.messages[:len(m.messages)-1]
		}
	}
}

// ensureSpinnerTicking starts the spinner animation if one is not already scheduled.
func (m *chatModel) ensureSpinnerTicking() tea.Cmd {
	if m.spinnerTicking {
		return nil
	}
	m.spinnerTicking = true
	return spinnerTickCmd()
}

func newChatModel(c *client.Client, sessionKey, agentID, agentName, modelID string, prefs config.Preferences) chatModel {
	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.Focus()
	ta.CharLimit = 0
	ta.SetHeight(inputHeight)
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.KeyMap.InsertNewline.SetKeys("alt+enter")
	ta.KeyMap.DeleteWordBackward.SetKeys("alt+backspace", "ctrl+w")
	ta.KeyMap.DeleteWordForward.SetKeys("alt+delete", "alt+d")

	vp := viewport.New()

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(80), // updated by setSize() when terminal dimensions are known
	)

	return chatModel{
		viewport:     vp,
		textarea:     ta,
		client:       c,
		sessionKey:   sessionKey,
		agentID:      agentID,
		agentName:    agentName,
		renderer:     renderer,
		modelID:      modelID,
		prefs:        prefs,
		historyLimit: prefs.HistoryLimit,
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

	case gatewayStatusMsg:
		if msg.err != nil {
			m.messages = append(m.messages, chatMessage{role: "system", errMsg: msg.err.Error()})
		} else {
			m.messages = append(m.messages, chatMessage{
				role:    "system",
				content: formatGatewayStatus(msg.health, msg.uptimeMs),
			})
		}
		m.updateViewport()
		return m, nil

	case thinkingChangedMsg:
		if msg.err != nil {
			m.messages = append(m.messages, chatMessage{role: "system", errMsg: msg.err.Error()})
		} else {
			m.thinkingLevel = msg.level
			display := msg.level
			if display == "" || display == "off" {
				display = "off"
			}
			m.messages = append(m.messages, chatMessage{
				role:    "system",
				content: fmt.Sprintf("Thinking level set to %s", display),
			})
		}
		m.updateViewport()
		return m, nil

	case sessionCompactedMsg:
		if msg.err != nil {
			m.messages = append(m.messages, chatMessage{role: "system", errMsg: fmt.Sprintf("compact failed: %v", msg.err)})
		} else {
			m.messages = append(m.messages, chatMessage{role: "system", content: "Session compacted."})
		}
		m.updateViewport()
		return m, nil

	case sessionClearedMsg:
		if msg.err != nil {
			m.messages = append(m.messages, chatMessage{role: "system", errMsg: fmt.Sprintf("clear session failed: %v", msg.err)})
		} else {
			m.sessionKey = msg.newSessionKey
			m.messages = nil
			m.messages = append(m.messages, chatMessage{role: "system", content: "Session cleared. Starting fresh."})
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
		case "esc":
			if m.sending {
				return m, m.cancelTurn()
			}
			return m, nil
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

			// Resolve a pending confirmation prompt.
			if m.pendingConfirm != nil {
				m.textarea.Reset()
				confirm := m.pendingConfirm
				m.pendingConfirm = nil
				lower := strings.ToLower(text)
				if lower == "y" || lower == "yes" {
					m.messages = append(m.messages, chatMessage{role: "system", content: "Confirmed."})
					m.updateViewport()
					return m, confirm.action()
				}
				m.messages = append(m.messages, chatMessage{role: "system", content: "Cancelled."})
				m.updateViewport()
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

			if strings.HasPrefix(text, "!!") {
				command := strings.TrimSpace(text[2:])
				if command == "" {
					return m, nil
				}
				m.messages = append(m.messages, chatMessage{role: "system", content: fmt.Sprintf("!! %s", command)})
				m.messages = append(m.messages, chatMessage{role: "system", content: "running on gateway..."})
				m.sending = true
				m.updateViewport()
				cmds = append(cmds, m.execCommand(command))
				return m, tea.Batch(cmds...)
			}

			if strings.HasPrefix(text, "!") {
				command := strings.TrimSpace(text[1:])
				if command == "" {
					return m, nil
				}
				m.messages = append(m.messages, chatMessage{role: "system", content: fmt.Sprintf("$ %s", command)})
				m.messages = append(m.messages, chatMessage{role: "system", content: "running..."})
				m.sending = true
				m.updateViewport()
				cmds = append(cmds, localExecCommand(command))
				return m, tea.Batch(cmds...)
			}

			m.messages = append(m.messages, chatMessage{role: "user", content: text})
			m.messages = append(m.messages, chatMessage{role: "assistant", streaming: true, awaitingDelta: true})
			m.sending = true
			m.updateViewport()
			cmds = append(cmds, m.sendMessage(m.withSkillCatalog(text)), m.ensureSpinnerTicking())
			return m, tea.Batch(cmds...)
		}

	case execSubmittedMsg:
		if msg.err != nil {
			if len(m.messages) > 0 {
				last := &m.messages[len(m.messages)-1]
				if last.role == "system" && (last.content == "running..." || last.content == "running on gateway...") {
					last.content = ""
					last.errMsg = msg.err.Error()
				}
			}
			m.updateViewport()
			cmd := m.drainQueueSkipRefresh()
			return m, cmd
		}
		return m, nil

	case localExecFinishedMsg:
		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			if last.role == "system" && last.content == "running..." {
				if msg.err != nil {
					last.content = ""
					last.errMsg = msg.err.Error()
				} else {
					output := msg.output
					if output == "" {
						output = "(no output)"
					}
					if msg.exitCode != 0 {
						output += fmt.Sprintf("\nexit code: %d", msg.exitCode)
					}
					last.content = output
				}
			}
		}
		m.updateViewport()
		cmd := m.drainQueueSkipRefresh()
		return m, cmd

	case chatSentMsg:
		if msg.err != nil {
			logEvent("SEND_ERROR: %v", msg.err)
			m.removeThinkingPlaceholder()
			m.messages = append(m.messages, chatMessage{
				role:   "assistant",
				errMsg: msg.err.Error(),
			})
			m.updateViewport()
			cmd := m.drainQueue()
			return m, cmd
		}
		m.runID = msg.runID
		return m, nil

	case chatAbortMsg:
		if msg.err != nil {
			logEvent("ABORT_ERROR: %v", msg.err)
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
		idemKey := fmt.Sprintf("lucinate-%d", time.Now().UnixNano())
		result, err := m.client.ChatSend(context.Background(), sessionKey, text, idemKey)
		if err != nil {
			return chatSentMsg{err: err}
		}
		return chatSentMsg{runID: result.RunID}
	}
}

// cancelTurn aborts the active turn and clears pending messages.
func (m *chatModel) cancelTurn() tea.Cmd {
	if !m.sending || m.runID == "" {
		m.messages = append(m.messages, chatMessage{role: "system", content: "Nothing to cancel."})
		m.updateViewport()
		return nil
	}
	cl := m.client
	sessionKey := m.sessionKey
	runID := m.runID
	m.pendingMessages = nil
	m.runID = ""
	m.sending = false
	// Stop streaming immediately so the spinner stops.
	if len(m.messages) > 0 {
		last := &m.messages[len(m.messages)-1]
		if last.role == "assistant" && last.streaming {
			last.streaming = false
			last.content += "\n[aborted]"
		}
	}
	m.messages = append(m.messages, chatMessage{role: "system", content: "Cancelled."})
	m.updateViewport()
	return func() tea.Msg {
		err := cl.ChatAbort(context.Background(), sessionKey, runID)
		return chatAbortMsg{err: err}
	}
}

// withSkillCatalog prepends the skill catalog to the message text on the first
// send. The catalog uses "System:" prefixed lines which stripSystemLines removes
// from display in history.
func (m *chatModel) withSkillCatalog(text string) string {
	if m.skillCatalogSent || len(m.skills) == 0 {
		return text
	}
	catalog := skillCatalogBlock(m.skills)
	if catalog == "" {
		return text
	}
	m.skillCatalogSent = true
	return catalog + "\n" + text
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

	if strings.HasPrefix(text, "!!") {
		command := strings.TrimSpace(text[2:])
		if command == "" {
			m.sending = false
			return nil
		}
		m.messages = append(m.messages, chatMessage{role: "system", content: fmt.Sprintf("!! %s", command)})
		m.messages = append(m.messages, chatMessage{role: "system", content: "running on gateway..."})
		m.updateViewport()
		return m.execCommand(command)
	}

	if strings.HasPrefix(text, "!") {
		command := strings.TrimSpace(text[1:])
		if command == "" {
			m.sending = false
			return nil
		}
		m.messages = append(m.messages, chatMessage{role: "system", content: fmt.Sprintf("$ %s", command)})
		m.messages = append(m.messages, chatMessage{role: "system", content: "running..."})
		m.updateViewport()
		return localExecCommand(command)
	}

	m.messages = append(m.messages, chatMessage{role: "user", content: text})
	m.messages = append(m.messages, chatMessage{role: "assistant", streaming: true, awaitingDelta: true})
	m.updateViewport()
	return tea.Batch(m.sendMessage(text), m.ensureSpinnerTicking())
}

func (m *chatModel) setSize(w, h int) {
	m.width = w
	m.height = h

	// Recreate the glamour renderer with the new wrap width. In narrow mode the
	// body uses the full content width (no inline prefix), so size accordingly.
	contentWidth := w - 4
	wrapWidth := contentWidth - m.prefixWidth()
	if m.narrowLayout() {
		wrapWidth = contentWidth
	}
	if wrapWidth < 20 {
		wrapWidth = 20
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(wrapWidth),
	)
	if err == nil {
		m.renderer = renderer
		// Re-render any previously glamour-rendered messages so markdown
		// reflows when the terminal is resized.
		for i := range m.messages {
			if m.messages[i].rendered && m.messages[i].raw != "" {
				if out, rerr := m.renderer.Render(m.messages[i].raw); rerr == nil {
					m.messages[i].content = strings.TrimSpace(out)
				}
			}
		}
	}

	headerH := 1
	helpH := 1
	borderH := 2
	vpHeight := h - inputHeight - headerH - helpH - borderH - 2

	m.viewport.SetWidth(w)
	m.viewport.SetHeight(vpHeight)

	m.textarea.SetWidth(w - 7)
	m.updateViewport()
}

func (m chatModel) View() string {
	left := fmt.Sprintf(" lucinate — %s", m.agentName)
	if m.modelID != "" {
		model := m.modelID
		if i := strings.LastIndex(model, "/"); i >= 0 {
			model = model[i+1:]
		}
		left += " · " + model
	}
	if m.thinkingLevel != "" && m.thinkingLevel != "off" {
		left += " · think:" + m.thinkingLevel
	}
	if badge := connectionBadge(m.connState); badge != "" {
		left += " · " + badge
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
	isRemoteExec := strings.HasPrefix(m.textarea.Value(), "!!")
	isLocalExec := !isRemoteExec && strings.HasPrefix(m.textarea.Value(), "!")
	if isRemoteExec {
		borderStyle = execBorderStyle
	} else if isLocalExec {
		borderStyle = localExecBorderStyle
	}
	input := borderStyle.
		Width(m.width - 4).
		Render(m.textarea.View())

	var help string
	if isRemoteExec {
		help = helpStyle.Render(execPrefixStyle.Render(" remote command") + " — runs on gateway host")
	} else if isLocalExec {
		help = helpStyle.Render(localExecPrefixStyle.Render(" local command") + " — runs on this machine")
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
