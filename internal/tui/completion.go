package tui

import (
	"fmt"
	"strings"
)

// completionMenuState backs the slash-command completion popup.
//
// When the cursor sits at the end of a "/foo" token, the menu displays
// every matching candidate (built-ins + skills). Tab extends the input
// to the longest common prefix; once at the LCP and multiple matches
// remain, repeated Tab cycles cycleCandidates and replaces the input
// each press (Shift+Tab cycles backward). Any non-Tab keystroke drops
// out of cycle mode — refreshCompletionMenu is the single place that
// flips cycling back to false.
type completionMenuState struct {
	visible         bool
	candidates      []string
	cycling         bool
	cycleCandidates []string
	cycleIndex      int
}

// completionMenuMaxRows caps the number of candidate rows rendered in
// the completion menu before collapsing the tail into a "+N more" line.
// Tuned low to keep the chat viewport breathable; users narrow the list
// by typing rather than scrolling the menu.
const completionMenuMaxRows = 4

// completionMenuViewportFloor is the minimum number of viewport rows
// the completion menu will leave visible. On terminals too short to
// honour this floor, the menu suppresses itself — Tab still does LCP
// extension on the underlying state.
const completionMenuViewportFloor = 5

// longestCommonPrefix returns the longest byte-prefix shared by every string
// in strs. Empty slice and any zero-length string short-circuit to "".
// Callers are expected to pass already-lowercased inputs (slash commands and
// skill names are stored lowercased), so the comparison is byte-level.
func longestCommonPrefix(strs []string) string {
	if len(strs) == 0 {
		return ""
	}
	prefix := strs[0]
	for _, s := range strs[1:] {
		n := len(prefix)
		if len(s) < n {
			n = len(s)
		}
		i := 0
		for i < n && prefix[i] == s[i] {
			i++
		}
		prefix = prefix[:i]
		if prefix == "" {
			return ""
		}
	}
	return prefix
}

// longestCommonPrefixFold returns the longest case-insensitive (ASCII fold)
// prefix shared by every string in strs, taken from the first candidate's
// casing — so the inserted text reads naturally next to the user's cursor.
// Used for sources whose candidates carry mixed casing (e.g. agent names);
// for already-lowercase data it produces the same result as
// longestCommonPrefix.
func longestCommonPrefixFold(strs []string) string {
	if len(strs) == 0 {
		return ""
	}
	first := strs[0]
	if first == "" {
		return ""
	}
	n := len(first)
	for _, s := range strs[1:] {
		if len(s) < n {
			n = len(s)
		}
		i := 0
		for i < n {
			a, b := first[i], s[i]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				break
			}
			i++
		}
		n = i
		if n == 0 {
			return ""
		}
	}
	return first[:n]
}

// matchingSlashCommands returns every slash command whose lowercased form
// has prefix as a prefix. Built-ins come first in their curated order
// (preserving the /agents-before-/agent etc. tiebreaks used by the legacy
// inline ghost-hint), followed by skill names. Duplicates between built-ins
// and skills are dropped. Returns nil for non-slash inputs.
func (m *chatModel) matchingSlashCommands(prefix string) []string {
	lower := strings.ToLower(prefix)
	if !strings.HasPrefix(lower, "/") {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	for _, cmd := range slashCommands {
		if strings.HasPrefix(cmd, lower) {
			seen[cmd] = struct{}{}
			out = append(out, cmd)
		}
	}
	for _, s := range m.skills {
		cmd := "/" + strings.ToLower(s.Name)
		if _, dup := seen[cmd]; dup {
			continue
		}
		if strings.HasPrefix(cmd, lower) {
			seen[cmd] = struct{}{}
			out = append(out, cmd)
		}
	}
	return out
}

// matchingAgentNames returns every agent name whose lowercased form has
// prefix as a prefix, preserving each agent's original casing in the
// returned slice. Empty prefix matches every loaded agent. Returns nil
// when m.agentNames hasn't been populated yet — Tab silently no-ops in
// that case, matching the legacy completeAgentName behaviour.
func (m *chatModel) matchingAgentNames(prefix string) []string {
	if len(m.agentNames) == 0 {
		return nil
	}
	if prefix == "" {
		out := make([]string, len(m.agentNames))
		copy(out, m.agentNames)
		return out
	}
	lower := strings.ToLower(prefix)
	var out []string
	for _, n := range m.agentNames {
		if strings.HasPrefix(strings.ToLower(n), lower) {
			out = append(out, n)
		}
	}
	return out
}

// completionContext describes the completable token at the cursor: where
// it begins, where the cursor sits, what the user has typed, and the
// candidate list for that prefix. Sources differ (slash commands +
// skills vs. agent names) but the menu/Tab/cycle machinery is uniform.
type completionContext struct {
	start      int
	cursorByte int
	prefix     string
	candidates []string
}

// completionAtCursor returns the active completion context at the
// textarea cursor. Slash commands take priority over agent-name
// completion — the latter only applies inside the special "/agent "
// argument context. Returns ok=false when no source applies.
func (m *chatModel) completionAtCursor() (completionContext, bool) {
	value := m.textarea.Value()
	cursorByte := textareaCursorByteOffset(&m.textarea)
	if start, prefix, ok := findSlashTokenAt(value, cursorByte); ok {
		return completionContext{
			start:      start,
			cursorByte: cursorByte,
			prefix:     prefix,
			candidates: m.matchingSlashCommands(prefix),
		}, true
	}
	if start, prefix, ok := findAgentArgAt(value, cursorByte); ok {
		return completionContext{
			start:      start,
			cursorByte: cursorByte,
			prefix:     prefix,
			candidates: m.matchingAgentNames(prefix),
		}, true
	}
	return completionContext{}, false
}

// handleCompletionTab implements the Tab semantics for the active
// completion source: extend to longest common prefix on the first useful
// press, then cycle candidates on subsequent presses while still at the
// LCP. The candidate list is sourced from ctx — slash commands, skills,
// or agent names — so a single state machine drives both menus.
func (m *chatModel) handleCompletionTab(ctx completionContext) {
	value := m.textarea.Value()

	// If we're already cycling and the user hasn't typed since, advance
	// the index. Detected by checking that the current token is one of
	// the snapshotted cycleCandidates — refreshCompletionMenu would have
	// flipped cycling=false on any non-Tab keystroke.
	if m.completion.cycling && len(m.completion.cycleCandidates) > 0 {
		for _, c := range m.completion.cycleCandidates {
			if strings.EqualFold(c, ctx.prefix) {
				m.completion.cycleIndex = (m.completion.cycleIndex + 1) % len(m.completion.cycleCandidates)
				pick := m.completion.cycleCandidates[m.completion.cycleIndex]
				newValue := value[:ctx.start] + pick + value[ctx.cursorByte:]
				setTextareaToValueWithCursor(&m.textarea, newValue, ctx.start+len(pick))
				return
			}
		}
		// Token no longer matches the cycle snapshot — drop out and
		// re-evaluate from scratch.
		m.completion.cycling = false
		m.completion.cycleCandidates = nil
	}

	switch {
	case len(ctx.candidates) == 0:
		return
	case len(ctx.candidates) == 1:
		pick := ctx.candidates[0]
		newValue := value[:ctx.start] + pick + value[ctx.cursorByte:]
		setTextareaToValueWithCursor(&m.textarea, newValue, ctx.start+len(pick))
		m.refreshCompletionMenu()
		return
	}

	lcp := longestCommonPrefixFold(ctx.candidates)
	if len(lcp) > len(ctx.prefix) {
		newValue := value[:ctx.start] + lcp + value[ctx.cursorByte:]
		setTextareaToValueWithCursor(&m.textarea, newValue, ctx.start+len(lcp))
		m.refreshCompletionMenu()
		return
	}

	// At LCP with multiple matches — start cycling.
	m.completion.cycling = true
	m.completion.cycleCandidates = ctx.candidates
	m.completion.cycleIndex = 0
	pick := ctx.candidates[0]
	newValue := value[:ctx.start] + pick + value[ctx.cursorByte:]
	setTextareaToValueWithCursor(&m.textarea, newValue, ctx.start+len(pick))
	m.completion.visible = true
	m.completion.candidates = ctx.candidates
	m.applyLayout()
}

// menuRowsToRender returns the number of vertical rows the completion
// menu wants to occupy on screen, including the optional "+N more"
// overflow line. Returns 0 when the menu is hidden or the baseline
// viewport is too short to host it without crushing the conversation
// pane below completionMenuViewportFloor rows.
func (m *chatModel) menuRowsToRender() int {
	if !m.completion.visible {
		return 0
	}
	src := m.completion.candidates
	if m.completion.cycling {
		src = m.completion.cycleCandidates
	}
	if len(src) == 0 {
		return 0
	}
	rows := len(src)
	if rows > completionMenuMaxRows {
		rows = completionMenuMaxRows + 1 // +N more line
	}
	maxAllowed := m.baseViewportHeight - completionMenuViewportFloor
	if maxAllowed < 2 {
		return 0
	}
	if rows > maxAllowed {
		rows = maxAllowed
	}
	return rows
}

// renderCompletionMenu builds the menu's vertical block. Returns an
// empty string and 0 height when the menu is hidden or the viewport is
// too short to host it. The highlight glyph marks the current cycle
// index when cycling; otherwise candidates are rendered uniformly in
// helpStyle.
func (m chatModel) renderCompletionMenu() (string, int) {
	budget := m.menuRowsToRender()
	if budget == 0 {
		return "", 0
	}
	src := m.completion.candidates
	if m.completion.cycling {
		src = m.completion.cycleCandidates
	}
	if len(src) == 0 {
		return "", 0
	}

	// Reserve a row for "+N more" when we can't fit every candidate.
	showCount := len(src)
	overflow := 0
	if showCount > budget {
		showCount = budget - 1
		if showCount < 1 {
			showCount = budget
		} else {
			overflow = len(src) - showCount
		}
	}

	var lines []string
	for i := 0; i < showCount; i++ {
		cmd := src[i]
		if m.completion.cycling && i == m.completion.cycleIndex {
			lines = append(lines, completionMenuHighlightStyle.Render("▸ "+cmd))
			continue
		}
		lines = append(lines, helpStyle.Render("  "+cmd))
	}
	if overflow > 0 {
		lines = append(lines, helpStyle.Render(fmt.Sprintf("  +%d more", overflow)))
	}
	return strings.Join(lines, "\n"), len(lines)
}

// applyLayout sizes the viewport so the completion menu fits between
// the conversation pane and the input. setSize is the canonical owner
// of the "menu hidden" baseline; this method reapplies the menu's
// footprint on top of that baseline whenever menu state changes.
func (m *chatModel) applyLayout() {
	h := m.baseViewportHeight - m.menuRowsToRender()
	if h < 1 {
		h = 1
	}
	m.viewport.SetHeight(h)
}

// refreshCompletionMenu recomputes the menu state from the current
// textarea contents. Called after every non-Tab keypress: the Tab
// handler manages cycling state explicitly and never invokes this.
// Any visit here resets cycling=false because, by definition, the
// user has typed something other than Tab.
func (m *chatModel) refreshCompletionMenu() {
	ctx, ok := m.completionAtCursor()

	m.completion.cycling = false
	m.completion.cycleCandidates = nil
	m.completion.cycleIndex = 0

	wasVisible := m.completion.visible

	if !ok || len(ctx.candidates) == 0 {
		if wasVisible {
			m.completion.visible = false
			m.completion.candidates = nil
			m.applyLayout()
		}
		return
	}

	prevLen := len(m.completion.candidates)
	m.completion.visible = true
	m.completion.candidates = ctx.candidates

	if !wasVisible || prevLen != len(ctx.candidates) {
		m.applyLayout()
		if !wasVisible {
			m.viewport.GotoBottom()
		}
	}
}
