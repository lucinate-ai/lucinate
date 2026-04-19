package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/a3tai/openclaw-go/protocol"
	tea "github.com/charmbracelet/bubbletea"
)

// slashCommands is the list of available slash commands for autocomplete.
var slashCommands = []string{"/back", "/clear", "/exit", "/help", "/model", "/quit", "/skills", "/stats"}

// completeSlashCommand returns the first matching slash command for the given
// prefix, or "" if no match. Includes skill names as slash commands.
func (m *chatModel) completeSlashCommand(prefix string) string {
	lower := strings.ToLower(prefix)
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
	case "/back":
		return true, func() tea.Msg { return goBackMsg{} }
	case "/clear":
		m.messages = nil
		m.updateViewport()
		return true, nil
	case "/help":
		helpText := "/quit, /exit — quit repclaw\n/back — return to agent list\n/clear — clear chat display\n/model — list available models\n/model <name> — switch model\n/stats — show session statistics\n/skills — list available agent skills\n/help — show this help\n\n!<command> — run command on gateway host"
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

	// Skill activation: /skill-name sends the skill body as a System:-prefixed message.
	if strings.HasPrefix(command, "/") {
		skillName := strings.TrimPrefix(command, "/")
		for _, s := range m.skills {
			if strings.ToLower(s.Name) == skillName {
				msg := fmt.Sprintf("System: [Skill: %s]\n%s", s.Name, s.Body)
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
