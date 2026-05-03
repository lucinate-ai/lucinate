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

	"github.com/lucinate-ai/lucinate/internal/backend"
)

// pendingConfirmation holds a deferred action awaiting user confirmation.
type pendingConfirmation struct {
	prompt string
	action func() tea.Cmd
}

// slashCommands is the list of available slash commands for autocomplete.
// "/agents" is intentionally listed before "/agent" so tab-completing "/age"
// resolves to the picker (the more common command) rather than the switcher.
// "/model" likewise sits before "/models" — typing "s" to extend a completion
// is cheaper than backspacing one.
var slashCommands = []string{"/agents", "/agent", "/cancel", "/clear", "/commands", "/compact", "/config", "/connections", "/crons", "/exit", "/help", "/model", "/models", "/quit", "/reset", "/sessions", "/skills", "/stats", "/status", "/think"}

// thinkingLevels is the ordered list of valid thinking levels.
var thinkingLevels = []string{"off", "minimal", "low", "medium", "high"}

// findSlashTokenAt locates the slash-prefixed token whose end coincides with
// the cursor at byteOffset in value. Returns ok=false when the cursor is not
// at the end of a slash token (e.g. mid-token, or no slash preceding the
// cursor without an intervening word boundary).
func findSlashTokenAt(value string, byteOffset int) (start int, prefix string, ok bool) {
	if byteOffset < 0 || byteOffset > len(value) {
		return 0, "", false
	}
	// Cursor must be at a token boundary: next byte must be EOF or whitespace.
	if byteOffset < len(value) && !isSpaceByte(value[byteOffset]) {
		return 0, "", false
	}
	// Walk backward over slash-token chars.
	i := byteOffset
	for i > 0 && isSlashTokenByte(value[i-1]) {
		i--
	}
	// We need a '/' immediately before the run of token chars.
	if i == 0 || value[i-1] != '/' {
		return 0, "", false
	}
	slashIdx := i - 1
	// The slash itself must be at BOF or preceded by whitespace.
	if slashIdx > 0 && !isSpaceByte(value[slashIdx-1]) {
		return 0, "", false
	}
	return slashIdx, value[slashIdx:byteOffset], true
}

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

// slashCommandHint returns the completion hint for the slash token at the
// given cursor byte offset. token is what the user has typed so far
// (including the leading '/'), suffix is the remainder that Tab would insert.
// Both are empty when no hint applies.
func (m *chatModel) slashCommandHint(value string, cursorByte int) (token, suffix string) {
	_, prefix, ok := findSlashTokenAt(value, cursorByte)
	if !ok {
		return "", ""
	}
	match := m.completeSlashCommand(prefix)
	if match == "" || match == strings.ToLower(prefix) {
		return "", ""
	}
	return prefix, match[len(prefix):]
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
	case "/models":
		sessionKey := m.sessionKey
		currentModelID := m.modelID
		return true, func() tea.Msg {
			return showModelPickerMsg{
				sessionKey:     sessionKey,
				currentModelID: currentModelID,
			}
		}
	case "/cancel":
		return true, m.cancelTurn()
	case "/clear":
		m.messages = nil
		m.updateViewport()
		return true, nil
	case "/compact":
		compact, ok := m.backend.(backend.CompactBackend)
		if !ok {
			m.messages = append(m.messages, chatMessage{
				role:   "system",
				errMsg: "/compact is not available on this connection",
			})
			m.updateViewport()
			return true, nil
		}
		sessionKey := m.sessionKey
		m.pendingConfirm = &pendingConfirmation{
			prompt: "Compact session context? This summarises older messages to reduce token usage. (y/n)",
			action: func() tea.Cmd {
				return func() tea.Msg {
					err := compact.SessionCompact(context.Background(), sessionKey)
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
		b := m.backend
		sessionKey := m.sessionKey
		agentID := m.agentID
		m.pendingConfirm = &pendingConfirmation{
			prompt: "Clear this session? This permanently deletes all messages and starts fresh. (y/n)",
			action: func() tea.Cmd {
				return func() tea.Msg {
					if err := b.SessionDelete(context.Background(), sessionKey); err != nil {
						return sessionClearedMsg{err: err}
					}
					// Create a new session to replace the deleted one.
					newKey, err := b.CreateSession(context.Background(), agentID, "")
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
	case "/crons", "/crons all":
		if _, ok := m.backend.(backend.CronBackend); !ok {
			m.messages = append(m.messages, chatMessage{
				role:   "system",
				errMsg: "/crons is not available on this connection",
			})
			m.updateViewport()
			return true, nil
		}
		filterAgentID := m.agentID
		filterLabel := m.agentName
		if command == "/crons all" {
			filterAgentID = ""
			filterLabel = "all agents"
		}
		return true, func() tea.Msg {
			return showCronsMsg{filterAgentID: filterAgentID, filterLabel: filterLabel}
		}
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
		helpText := "/quit, /exit — quit lucinate\n/agents — return to agent picker\n/agent <name> — switch agent directly\n/cancel — cancel the current response (also: Esc)\n/clear — clear chat display\n/compact — compact session context\n/config — open preferences\n/connections — switch gateway connection\n/crons — list and manage cron jobs (use /crons all for global)\n/models — open model picker (filter as you type)\n/model <name> — switch model directly\n/reset — delete session and start fresh\n/sessions — browse and restore previous sessions\n/stats — show session statistics\n/status — show gateway health and agent status\n/skills — list available agent skills\n/think — show current thinking level\n/think <level> — set thinking level (off/minimal/low/medium/high)\n/help — show this help\n\n!<command> — run command locally\n!!<command> — run command on gateway host"
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
		status, ok := m.backend.(backend.StatusBackend)
		if !ok {
			m.messages = append(m.messages, chatMessage{
				role:   "system",
				errMsg: "/status is not available on this connection",
			})
			m.updateViewport()
			return true, nil
		}
		return true, func() tea.Msg {
			health, err := status.GatewayHealth(context.Background())
			uptimeMs := status.HelloUptimeMs()
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

	// /agent with optional name argument.
	if command == "/agent" || strings.HasPrefix(command, "/agent ") {
		return m.handleAgentCommand(text)
	}

	// /model with optional argument.
	if command == "/model" || strings.HasPrefix(command, "/model ") {
		return m.handleModelCommand(text)
	}

	// /think with optional level argument.
	if command == "/think" || strings.HasPrefix(command, "/think ") {
		return m.handleThinkCommand(text)
	}

	// Slash-prefixed input that isn't a built-in: if the first token names a
	// known skill, delegate to the regular send path so expandSkillReferences
	// wraps it in a <local-agent-skill> envelope. Otherwise emit an
	// unknown-command error.
	if strings.HasPrefix(command, "/") {
		firstToken := command
		if idx := strings.IndexByte(command, ' '); idx >= 0 {
			firstToken = command[:idx]
		}
		name := strings.TrimPrefix(firstToken, "/")
		for _, s := range m.skills {
			if strings.EqualFold(s.Name, name) {
				return false, nil
			}
		}

		m.messages = append(m.messages, chatMessage{
			role:   "system",
			errMsg: fmt.Sprintf("unknown command: %s (try /help)", firstToken),
		})
		m.updateViewport()
		return true, nil
	}

	return false, nil
}

// handleAgentCommand handles `/agent` and `/agent <name>`. With no argument
// it returns to the agent picker (same as `/agents`). With a name it resolves
// the agent and creates a session, mirroring the picker selection path.
func (m *chatModel) handleAgentCommand(text string) (bool, tea.Cmd) {
	parts := strings.SplitN(strings.TrimSpace(text), " ", 2)
	if len(parts) == 1 || strings.TrimSpace(parts[1]) == "" {
		return true, func() tea.Msg { return goBackMsg{} }
	}

	query := strings.ToLower(strings.TrimSpace(parts[1]))
	b := m.backend
	return true, func() tea.Msg {
		ctx := context.Background()
		result, err := b.ListAgents(ctx)
		if err != nil {
			return agentSwitchFailedMsg{err: err}
		}
		var match *protocol.AgentSummary
		for i, a := range result.Agents {
			lowerName := strings.ToLower(a.Name)
			lowerID := strings.ToLower(a.ID)
			if lowerName == query || lowerID == query {
				match = &result.Agents[i]
				break
			}
			if match == nil && (strings.Contains(lowerName, query) || strings.Contains(lowerID, query)) {
				match = &result.Agents[i]
			}
		}
		if match == nil {
			return agentSwitchFailedMsg{err: fmt.Errorf("no agent matching %q", query)}
		}
		name := match.Name
		if name == "" {
			name = match.ID
		}
		modelID := ""
		if match.Model != nil {
			modelID = match.Model.Primary
		}
		key, err := b.CreateSession(ctx, match.ID, "main")
		return sessionCreatedMsg{
			sessionKey: key,
			agentID:    match.ID,
			agentName:  name,
			modelID:    modelID,
			err:        err,
		}
	}
}

// handleModelCommand handles `/model <name>`. Bare `/model` is an error —
// `/models` opens the picker.
func (m *chatModel) handleModelCommand(text string) (bool, tea.Cmd) {
	parts := strings.SplitN(strings.TrimSpace(text), " ", 2)
	if len(parts) == 1 || strings.TrimSpace(parts[1]) == "" {
		m.messages = append(m.messages, chatMessage{
			role:   "system",
			errMsg: "/model requires a name — use /models to open the picker",
		})
		m.updateViewport()
		return true, nil
	}

	query := strings.ToLower(strings.TrimSpace(parts[1]))
	b := m.backend
	sessionKey := m.sessionKey
	return true, func() tea.Msg {
		result, err := b.ModelsList(context.Background())
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
		if err := b.SessionPatchModel(context.Background(), sessionKey, match.ID); err != nil {
			return modelSwitchedMsg{err: err}
		}
		return modelSwitchedMsg{modelID: match.ID}
	}
}

// execCommand submits a command for remote execution on the gateway host.
func (m *chatModel) execCommand(command string) tea.Cmd {
	execB, ok := m.backend.(backend.ExecBackend)
	if !ok {
		return func() tea.Msg {
			return execSubmittedMsg{err: fmt.Errorf("remote command execution is not available on this connection")}
		}
	}
	sessionKey := m.sessionKey
	return func() tea.Msg {
		ctx := context.Background()

		// Two-phase request: gateway returns immediately with status "accepted".
		result, err := execB.ExecRequest(ctx, command, sessionKey)
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
			_, err = execB.ExecResolve(ctx, result.ID, "allow-once")
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

	thinking, ok := m.backend.(backend.ThinkingBackend)
	if !ok {
		m.messages = append(m.messages, chatMessage{
			role:   "system",
			errMsg: "/think is not available on this connection",
		})
		m.updateViewport()
		return true, nil
	}
	sessionKey := m.sessionKey
	return true, func() tea.Msg {
		err := thinking.SessionPatchThinking(context.Background(), sessionKey, level)
		return thinkingChangedMsg{level: level, err: err}
	}
}
