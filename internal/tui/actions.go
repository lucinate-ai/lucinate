package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// Action is a discoverable, view-level command exposed by the active
// view. The TUI auto-renders the bound key in the inline help line and
// dispatches the keystroke through TriggerAction; embedders on other
// platforms render the same list as native controls and trigger the
// action by ID via Program.TriggerAction. Keeping both surfaces driven
// by a single declaration in each view's Actions() method removes the
// long-standing drift risk between the hand-written help string and
// the actual key handler.
type Action struct {
	// ID is a stable, view-scoped identifier embedders use to invoke
	// the action ("new-agent", "back", "retry"). Must be unique within
	// a single view's action list.
	ID string

	// Label is the human-readable name embedders show next to the
	// trigger ("New agent"). Should be a short verb phrase.
	Label string

	// Key is the single-character (or single-token) keystroke the TUI
	// binds to the action ("n", "esc"). Embedders typically ignore it
	// — they trigger by ID — but it is kept here so the TUI's auto-
	// rendered help line reads "n: new agent" without the view having
	// to repeat itself.
	Key string
}

// TriggerActionMsg is emitted by Program.TriggerAction. AppModel routes
// it to the active view's TriggerAction(id) so embedders can invoke a
// view's commands without forging fake key events.
type TriggerActionMsg struct {
	ID string
}

// renderActionHints joins an action list into the inline-help format
// each view used to hand-write ("  n: new agent · r: retry"). Returns
// the empty string when no actions are present so the caller can decide
// whether to render a help line at all.
func renderActionHints(actions []Action) string {
	if len(actions) == 0 {
		return ""
	}
	parts := make([]string, 0, len(actions))
	for _, a := range actions {
		if a.Key == "" || a.Label == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", a.Key, strings.ToLower(a.Label)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "  " + strings.Join(parts, " · ")
}

// renderHintLine renders the app-owned action hint as one dim block,
// word-wrapped to the view width so every action stays readable on a
// narrow terminal. Views render this instead of the bubbles list's own
// footer: that footer advertised the widget's `v select` / `? more` keys,
// which fire at best nothing here (Quit is unbound) and at worst a quit.
func renderHintLine(actions []Action, width int) string {
	hint := renderActionHints(actions)
	if hint == "" {
		return ""
	}
	if width > 0 {
		hint = wordWrap(hint, width)
	}
	return helpStyle.Render(hint)
}

// renderFilterHint is the app-owned hint while a list's filter input owns
// the keys. It says what esc really does there — clears the query — rather
// than advertising the view's back action, which is what produced the
// double "esc cancel" + "esc: back" on the model picker.
func renderFilterHint(width int) string {
	return helpStyle.Render(wordWrap("  type to filter · enter: pick · esc: clear filter", width))
}

// blockRows reports how many terminal rows a view block occupies when it
// is stacked above another block. A trailing newline is the separator to
// the next block, not a row of its own; the empty string is no block.
func blockRows(block string) int {
	block = strings.TrimSuffix(block, "\n")
	if block == "" {
		return 0
	}
	return lipgloss.Height(block)
}

// maxInt is the two-argument integer maximum, kept here so the layout
// helpers in the pickers read plainly.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// actionsEqual reports whether two action lists are identical. AppModel
// uses it to suppress redundant OnActionsChanged callbacks when the
// recomputed list happens to match the previously-reported one.
func actionsEqual(a, b []Action) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
