package tui

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lucinate-ai/lucinate/internal/routines"
)

// activeRoutine holds the in-flight state for a routine the user has
// activated via /routine <name>. While set, the chatModel auto-advances in
// auto mode after each assistant final, repurposes Enter on empty input to
// send the next step, and binds Shift+Tab to mode cycling.
type activeRoutine struct {
	routine routines.Routine
	mode    routines.Mode
	sent    int  // count of steps already dispatched (== index of next step)
	paused  bool // true when the routine should not auto-advance even in auto mode
	logger  *routines.Logger
}

// startRoutine loads a routine by name, prepares the controller, and sends
// the first step. Returns a tea.Cmd that submits the step to the gateway.
// On any failure (load, parse, log open) renders a system error message
// inline and returns nil.
func (m *chatModel) startRoutine(name string) tea.Cmd {
	if m.activeRoutine != nil {
		m.appendSystemError(fmt.Sprintf("a routine is already active: %s", m.activeRoutine.routine.Name))
		return nil
	}
	if m.sending {
		m.appendSystemError("a turn is in flight — wait for it to finish before starting a routine")
		return nil
	}
	r, err := routines.Load(name)
	if err != nil {
		if err == routines.ErrNotFound {
			m.appendSystemError(fmt.Sprintf("routine %q not found", name))
		} else {
			m.appendSystemError(fmt.Sprintf("could not load routine %q: %v", name, err))
		}
		return nil
	}
	if len(r.Steps) == 0 {
		m.appendSystemError(fmt.Sprintf("routine %q has no steps", name))
		return nil
	}

	ar := &activeRoutine{
		routine: r,
		mode:    r.ResolvedMode(),
	}
	if logPath := strings.TrimSpace(r.Frontmatter.Log); logPath != "" {
		cwd, _ := os.Getwd()
		logger, err := routines.Open(logPath, cwd, r.Name)
		if err != nil {
			m.appendSystemError(fmt.Sprintf("routine log unavailable: %v (continuing without log)", err))
		} else {
			ar.logger = logger
		}
	}
	m.activeRoutine = ar
	m.applyLayout()
	m.notify(fmt.Sprintf("Routine %q started — %d step(s), %s mode.", r.Name, len(r.Steps), ar.mode))
	return m.sendNextRoutineStep()
}

// sendNextRoutineStep dispatches the next pending step as a user message.
// Caller must verify activeRoutine is non-nil and a step remains. After
// returning, sending is true and the assistant placeholder is in place.
func (m *chatModel) sendNextRoutineStep() tea.Cmd {
	ar := m.activeRoutine
	text := ar.routine.Steps[ar.sent]
	ar.sent++
	ar.paused = false

	sent := text
	if len(m.skills) > 0 {
		if expanded, ok := expandSkillReferences(text, m.skills); ok {
			sent = expanded
		}
	}
	m.appendMessage(chatMessage{role: "user", content: text})
	m.appendMessage(chatMessage{role: "assistant", streaming: true, awaitingDelta: true})
	m.sending = true
	if ar.logger != nil {
		ar.logger.WriteUser(text)
	}
	m.updateViewport()
	return tea.Batch(m.sendMessage(sent), m.ensureSpinnerTicking())
}

// maybeAdvanceRoutine reacts to a chat-final event for an active routine:
// it ends the routine when the last step has now been answered, or fires
// the next step in auto mode. Returns nil for manual/paused mid-routine
// finals, where the user drives the next step. Callers invoke it after
// the user-message queue has drained so user-typed messages take
// precedence over routine steps.
func (m *chatModel) maybeAdvanceRoutine() tea.Cmd {
	ar := m.activeRoutine
	if ar == nil {
		return nil
	}
	// Completion fires regardless of mode — a manual routine that just
	// answered its final step is just as "done" as an auto one, and the
	// user needs the same "Routine X completed." notification + cleared
	// activeRoutine so the input returns to normal chat.
	if ar.sent >= len(ar.routine.Steps) {
		m.endRoutine("completed")
		return nil
	}
	if ar.paused || ar.mode != routines.ModeAuto {
		return nil
	}
	return m.sendNextRoutineStep()
}

// applyDirectives scans an assistant reply for /routine: control directives
// and applies them in order. End-of-routine directives win over later mode
// changes — once stop is seen the controller is cleared and subsequent
// directives are no-ops.
func (m *chatModel) applyDirectives(reply string) {
	if m.activeRoutine == nil || reply == "" {
		return
	}
	for _, d := range routines.ScanDirectives(reply) {
		if m.activeRoutine == nil {
			return
		}
		switch d.Kind {
		case routines.DirectiveStop:
			m.endRoutine("stopped by assistant")
		case routines.DirectivePause:
			m.activeRoutine.paused = true
			m.notify("Routine paused — press Enter to send the next step, Esc to end.")
		case routines.DirectiveContinue:
			// Explicit no-op: the assistant declared its intent to keep
			// going. Unsetting paused lets a /routine:continue resume a
			// previously-paused routine in auto mode.
			m.activeRoutine.paused = false
		case routines.DirectiveModeAuto:
			m.activeRoutine.mode = routines.ModeAuto
			m.activeRoutine.paused = false
		case routines.DirectiveModeManual:
			m.activeRoutine.mode = routines.ModeManual
		}
	}
}

// endRoutine releases the active routine. The reason is recorded in the log
// (if any) and surfaced as a brief system message in the transcript.
func (m *chatModel) endRoutine(reason string) {
	ar := m.activeRoutine
	if ar == nil {
		return
	}
	if ar.logger != nil {
		ar.logger.Close()
	}
	m.activeRoutine = nil
	m.applyLayout()
	m.notify(fmt.Sprintf("Routine %q %s.", ar.routine.Name, reason))
}

// cycleRoutineMode flips the active routine between auto and manual.
// Cycling out of paused-auto into auto unsets paused so the next final
// auto-advances; cycling into manual leaves paused alone.
func (m *chatModel) cycleRoutineMode() {
	ar := m.activeRoutine
	if ar == nil {
		return
	}
	switch ar.mode {
	case routines.ModeAuto:
		ar.mode = routines.ModeManual
	default:
		ar.mode = routines.ModeAuto
		ar.paused = false
	}
}

// gateNavigation returns navCmd unchanged when no routine is active, or
// queues a y/n confirmation that — on confirm — ends the active routine
// (closing its log) and then runs navCmd. Used to wrap slash commands
// that strand or replace the chat model the routine lives on.
//
// label is the short verb phrase that completes "Switching agents",
// "Opening sessions", etc., and is rendered into the prompt text.
func (m *chatModel) gateNavigation(label string, navCmd tea.Cmd) tea.Cmd {
	if m.activeRoutine == nil {
		return navCmd
	}
	prompt := fmt.Sprintf("Routine %q is active. %s will cancel it. Continue? (y/n)",
		m.activeRoutine.routine.Name, label)
	m.pendingNavConfirm = &pendingNavConfirm{
		prompt: prompt,
		nav:    navCmd,
	}
	m.notify(prompt)
	return nil
}

// appendSystemError publishes a routine-initiated error as an
// error-styled notification. The name is kept for callers that pre-date
// the notification system.
func (m *chatModel) appendSystemError(msg string) {
	m.notifyError(msg)
}

// routineStatusLine renders the in-chat status row for the active routine.
// Returns "" when no routine is active. The output is plain text scoped to
// at most one display line; the View pads it to the chat width. When the
// routine is awaiting user input (manual or paused, idle, with steps
// remaining) the trailing segment switches from a passive "next:" preview
// to a "▶ Press Enter to send:" call-to-action so the user can see both
// what the next message is and that the routine is parked on them.
func (m *chatModel) routineStatusLine() string {
	ar := m.activeRoutine
	if ar == nil {
		return ""
	}
	mode := strings.ToUpper(string(ar.mode))
	if ar.paused {
		mode += " (paused)"
	}
	total := len(ar.routine.Steps)
	status := fmt.Sprintf("routine: %s — %s — sent: %d/%d", ar.routine.Name, mode, ar.sent, total)
	if ar.sent >= total {
		return status
	}
	awaitingUser := !m.sending && (ar.mode == routines.ModeManual || ar.paused)
	prefix := " — next: "
	if awaitingUser {
		prefix = " — ▶ Press Enter to send: "
	}
	// Size the preview to the remaining width so the user can read as much
	// of the upcoming step as fits. Fall back to 40 when width is unknown.
	previewMax := 40
	if m.width > 0 {
		previewMax = m.width - 2 - len(status) - len(prefix) // -2 for routineStatusStyle's horizontal padding
		if previewMax < 20 {
			previewMax = 20
		}
	}
	return status + prefix + previewLine(ar.routine.Steps[ar.sent], previewMax)
}

// routineStatusStyle styles the in-chat status row.
var routineStatusStyle = lipgloss.NewStyle().
	Foreground(accent).
	Bold(true).
	Padding(0, 1)

// previewLine reduces text to a single-line, ellipsised preview.
func previewLine(text string, max int) string {
	t := strings.ReplaceAll(text, "\r", " ")
	t = strings.ReplaceAll(t, "\n", " ")
	t = strings.Join(strings.Fields(t), " ")
	runes := []rune(t)
	if len(runes) <= max {
		return t
	}
	return string(runes[:max-1]) + "…"
}
