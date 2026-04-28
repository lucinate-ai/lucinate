package tui

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/a3tai/openclaw-go/protocol"
	tea "charm.land/bubbletea/v2"
)

// pendingConfirmation holds a deferred action awaiting user confirmation.
type pendingConfirmation struct {
	prompt string
	action func() tea.Cmd
}

// slashCommands is the list of available slash commands for autocomplete.
var slashCommands = []string{"/agents", "/cancel", "/clear", "/commands", "/compact", "/config", "/connections", "/exit", "/help", "/model", "/quit", "/reset", "/sessions", "/skills", "/stats", "/status", "/think"}

// thinkingLevels is the ordered list of valid thinking levels.
var thinkingLevels = []string{"off", "minimal", "low", "medium", "high"}

// completeSlashCommand returns the first matching slash command for the given
// prefix, or "" if no match. Includes skill names as slash commands.
func (m *chatModel) completeSlashCommand(prefix string) string {
	lower := strings.ToLower(prefix)
	if lower == "/" {
		return "/commands"
	}
	for _, cmd := range slashCommands {
		if strings.HasPrefix(cmd, lower) {
			return cmd
		}
	}
	for _, s := range m.skills {
		cmd := "/" + strings.ToLower(s.Name)
		if strings.HasPrefix(cmd, lower) {
			return cmd
		}
	}
	return ""
}

// slashCommandHint returns the completion hint to display after the current
// input, or "" if no hint applies.
func (m *chatModel) slashCommandHint(input string) string {
	if !strings.HasPrefix(input, "/") || strings.Contains(input, " ") || input == "" {
		return ""
	}
	match := m.completeSlashCommand(input)
	if match == "" || match == strings.ToLower(input) {
		return ""
	}
	return match[len(input):]
}

// handleSlashCommand processes local slash commands. Returns (true, cmd) if
// the input was handled as a command, or (false, nil) if it should be sent
// to the gateway.
func (m *chatModel) handleSlashCommand(text string) (handled bool, cmd tea.Cmd) {
	command := strings.ToLower(strings.TrimSpace(text))
	switch command {
	case "/quit", "/exit":
		return true, tea.Quit
	case "/agents":
		return true, func() tea.Msg { return goBackMsg{} }
	case "/cancel":
		return true, m.cancelTurn()
	case "/clear":
		m.messages = nil
		m.updateViewport()
		return true, nil
	case "/compact":
		cl := m.client
		sessionKey := m.sessionKey
		m.pendingConfirm = &pendingConfirmation{
			prompt: "Compact session context? This summarises older messages to reduce token usage. (y/n)",
			action: func() tea.Cmd {
				return func() tea.Msg {
					err := cl.SessionCompact(context.Background(), sessionKey)
					return sessionCompactedMsg{err: err}
				}
			},
		}
		m.messages = append(m.messages, chatMessage{
			role:    "system",
			content: m.pendingConfirm.prompt,
		})
		m.updateViewport()
		return true, nil
	case "/reset":
		cl := m.client
		sessionKey := m.sessionKey
		agentID := m.agentID
		m.pendingConfirm = &pendingConfirmation{
			prompt: "Clear this session? This permanently deletes all messages and starts fresh. (y/n)",
			action: func() tea.Cmd {
				return func() tea.Msg {
					if err := cl.SessionDelete(context.Background(), sessionKey); err != nil {
						return sessionClearedMsg{err: err}
					}
					// Create a new session to replace the deleted one.
					newKey, err := cl.CreateSession(context.Background(), agentID, "")
					if err != nil {
						return sessionClearedMsg{err: err}
					}
					return sessionClearedMsg{err: nil, newSessionKey: newKey}
				}
			},
		}
		m.messages = append(m.messages, chatMessage{
			role:    "system",
			content: m.pendingConfirm.prompt,
		})
		m.updateViewport()
		return true, nil
	case "/config":
		return true, func() tea.Msg { return showConfigMsg{} }
	case "/connections":
		return true, func() tea.Msg { return showConnectionsMsg{} }
	case "/sessions":
		agentID := m.agentID
		agentName := m.agentName
		modelID := m.modelID
		sessionKey := m.sessionKey
		return true, func() tea.Msg {
			return showSessionsMsg{
				agentID:   agentID,
				agentName: agentName,
				modelID:   modelID,
				mainKey:   sessionKey,
			}
		}
	case "/help", "/commands":
		helpText := "/quit, /exit — quit lucinate\n/agents — return to agent picker\n/cancel — cancel the current response (also: Esc)\n/clear — clear chat display\n/compact — compact session context\n/config — open preferences\n/connections — switch gateway connection\n/model — list available models\n/model <name> — switch model\n/reset — delete session and start fresh\n/sessions — browse and restore previous sessions\n/stats — show session statistics\n/status — show gateway health and agent status\n/skills — list available agent skills\n/think — show current thinking level\n/think <level> — set thinking level (off/minimal/low/medium/high)\n/help — show this help\n\n!<command> — run command locally\n!!<command> — run command on gateway host"
		if len(m.skills) > 0 {
			helpText += fmt.Sprintf("\n\n%d agent skill(s) available — type /skills to list", len(m.skills))
		}
		m.messages = append(m.messages, chatMessage{
			role:    "system",
			content: helpText,
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
	case "/status":
		cl := m.client
		return true, func() tea.Msg {
			health, err := cl.GatewayHealth(context.Background())
			uptimeMs := cl.HelloUptimeMs()
			return gatewayStatusMsg{health: health, uptimeMs: uptimeMs, err: err}
		}

	case "/skills":
		if len(m.skills) == 0 {
			m.messages = append(m.messages, chatMessage{role: "system", content: "No agent skills found.\nPlace skills in <cwd>/.agents/skills/<name>/SKILL.md or ~/.agents/skills/<name>/SKILL.md"})
		} else {
			var lines []string
			for _, s := range m.skills {
				lines = append(lines, fmt.Sprintf("  /%s — %s", s.Name, s.Description))
			}
			m.messages = append(m.messages, chatMessage{
				role:    "system",
				content: "Available skills:\n" + strings.Join(lines, "\n"),
			})
		}
		m.updateViewport()
		return true, nil
	}

	// /model with optional argument.
	if command == "/model" || strings.HasPrefix(command, "/model ") {
		return m.handleModelCommand(text)
	}

	// /think with optional level argument.
	if command == "/think" || strings.HasPrefix(command, "/think ") {
		return m.handleThinkCommand(text)
	}

	// Skill activation: /skill-name sends the skill body as a System:-prefixed message.
	if strings.HasPrefix(command, "/") {
		skillName := strings.TrimPrefix(command, "/")
		for _, s := range m.skills {
			if strings.ToLower(s.Name) == skillName {
				msg := prefixAllLines(fmt.Sprintf("[Skill: %s]\n%s", s.Name, s.Body))
				m.messages = append(m.messages, chatMessage{role: "user", content: fmt.Sprintf("/%s", s.Name)})
				m.sending = true
				m.updateViewport()
				return true, m.sendMessage(msg)
			}
		}

		// Unknown slash command.
		m.messages = append(m.messages, chatMessage{
			role:   "system",
			errMsg: fmt.Sprintf("unknown command: %s (try /help)", command),
		})
		m.updateViewport()
		return true, nil
	}

	return false, nil
}

// handleModelCommand handles `/model` and `/model <name>`.
func (m *chatModel) handleModelCommand(text string) (bool, tea.Cmd) {
	parts := strings.SplitN(strings.TrimSpace(text), " ", 2)
	if len(parts) == 1 {
		cl := m.client
		return true, func() tea.Msg {
			result, err := cl.ModelsList(context.Background())
			if err != nil {
				return modelListMsg{err: err}
			}
			return modelListMsg{models: result.Models}
		}
	}

	query := strings.ToLower(strings.TrimSpace(parts[1]))
	cl := m.client
	sessionKey := m.sessionKey
	return true, func() tea.Msg {
		result, err := cl.ModelsList(context.Background())
		if err != nil {
			return modelSwitchedMsg{err: err}
		}
		var match *protocol.ModelChoice
		for i, mc := range result.Models {
			lower := strings.ToLower(mc.ID)
			if lower == query || strings.ToLower(mc.Name) == query {
				match = &result.Models[i]
				break
			}
			if strings.Contains(lower, query) || strings.Contains(strings.ToLower(mc.Name), query) {
				match = &result.Models[i]
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

// execCommand submits a command for remote execution on the gateway host.
func (m *chatModel) execCommand(command string) tea.Cmd {
	cl := m.client
	sessionKey := m.sessionKey
	return func() tea.Msg {
		ctx := context.Background()

		// Two-phase request: gateway returns immediately with status "accepted".
		result, err := cl.ExecRequest(ctx, command, sessionKey)
		if err != nil {
			return execSubmittedMsg{err: err}
		}

		decision := ""
		if result.Decision != nil {
			decision = *result.Decision
		}
		logEvent("EXEC request id=%s status=%q decision=%q", result.ID, result.Status, decision)

		if decision == "deny" {
			return execSubmittedMsg{err: fmt.Errorf("command execution denied by gateway")}
		}

		// Auto-approve: the user explicitly typed the command.
		// The gateway may have already resolved the approval via its own exec policy,
		// so ignore "unknown or expired" errors.
		if decision == "" {
			_, err = cl.ExecResolve(ctx, result.ID, "allow-once")
			if err != nil {
				// If the approval was already resolved, that's fine — just wait for exec.finished.
				if !strings.Contains(err.Error(), "unknown or expired") {
					return execSubmittedMsg{err: fmt.Errorf("approval failed: %w", err)}
				}
				logEvent("EXEC approval already resolved id=%s", result.ID)
			} else {
				logEvent("EXEC auto-approved id=%s", result.ID)
			}
		}

		// Output arrives via exec.finished event through the event pump.
		return execSubmittedMsg{}
	}
}

// localExecCommand runs a command locally on the user's machine.
func localExecCommand(command string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("sh", "-c", command)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()

		output := stdout.String() + stderr.String()
		output = strings.TrimRight(output, "\n")

		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				return localExecFinishedMsg{err: err}
			}
		}
		return localExecFinishedMsg{output: output, exitCode: exitCode}
	}
}

// formatGatewayStatus renders a HealthEvent as a human-readable status block.
func formatGatewayStatus(h *protocol.HealthEvent, uptimeMs int64) string {
	var sb strings.Builder

	// Overall status line.
	status := "OK"
	if !h.OK {
		status = "DEGRADED"
	}
	sb.WriteString(fmt.Sprintf("Gateway: %s  (check: %dms, heartbeat: %ds)\n", status, h.DurationMs, h.HeartbeatSeconds))

	if uptimeMs > 0 {
		sb.WriteString(fmt.Sprintf("Uptime:  %s\n", formatDuration(uptimeMs)))
	}

	// Sessions summary.
	sb.WriteString(fmt.Sprintf("Sessions: %d active\n", h.Sessions.Count))

	// Agents table.
	if len(h.Agents) > 0 {
		sb.WriteString("\nAgents:\n")
		for _, a := range h.Agents {
			marker := "  "
			if a.IsDefault {
				marker = "* "
			}
			sb.WriteString(fmt.Sprintf("  %s%-30s  %d session(s)\n", marker, a.Name, a.Sessions.Count))
		}
	}

	// Channels table.
	if len(h.ChannelOrder) > 0 {
		sb.WriteString("\nChannels:\n")
		for _, key := range h.ChannelOrder {
			label := key
			if l, ok := h.ChannelLabels[key]; ok && l != "" {
				label = l
			}
			ch := h.Channels[key]
			configured := formatBoolPtr(ch.Configured)
			linked := formatBoolPtr(ch.Linked)
			var authAge string
			if ch.AuthAgeMs != nil {
				authAge = fmt.Sprintf(" auth: %s ago", formatDuration(*ch.AuthAgeMs))
			}
			sb.WriteString(fmt.Sprintf("  %-20s configured:%-3s  linked:%-3s%s\n",
				label, configured, linked, authAge))
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

// formatBoolPtr returns "yes", "no", or "?" for a *bool.
func formatBoolPtr(b *bool) string {
	if b == nil {
		return "?"
	}
	if *b {
		return "yes"
	}
	return "no"
}

// formatDuration renders a millisecond duration as a human-readable string.
func formatDuration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", h, m)
}

// handleThinkCommand handles `/think` and `/think <level>`.
func (m *chatModel) handleThinkCommand(text string) (bool, tea.Cmd) {
	parts := strings.SplitN(strings.TrimSpace(text), " ", 2)
	if len(parts) == 1 {
		// /think with no argument — show current level.
		level := m.thinkingLevel
		if level == "" {
			level = "off (gateway default)"
		}
		m.messages = append(m.messages, chatMessage{
			role:    "system",
			content: fmt.Sprintf("Thinking level: %s\nAvailable levels: %s", level, strings.Join(thinkingLevels, ", ")),
		})
		m.updateViewport()
		return true, nil
	}

	level := strings.ToLower(strings.TrimSpace(parts[1]))
	valid := false
	for _, l := range thinkingLevels {
		if l == level {
			valid = true
			break
		}
	}
	if !valid {
		m.messages = append(m.messages, chatMessage{
			role:   "system",
			errMsg: fmt.Sprintf("unknown thinking level %q — valid levels: %s", level, strings.Join(thinkingLevels, ", ")),
		})
		m.updateViewport()
		return true, nil
	}

	cl := m.client
	sessionKey := m.sessionKey
	return true, func() tea.Msg {
		err := cl.SessionPatchThinking(context.Background(), sessionKey, level)
		return thinkingChangedMsg{level: level, err: err}
	}
}
