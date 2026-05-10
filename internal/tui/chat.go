package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
	"github.com/lucinate-ai/lucinate/internal/routines"
)

// textareaCursorByteOffset converts the textarea's (Line, Column) cursor
// position — which is rune-indexed within a row — to a byte offset against
// Value(). Returns 0 when the textarea is empty.
func textareaCursorByteOffset(ta *textarea.Model) int {
	value := ta.Value()
	if value == "" {
		return 0
	}
	row := ta.Line()
	col := ta.Column()
	// Walk to the start of the target row.
	offset := 0
	for r := 0; r < row; r++ {
		nl := strings.IndexByte(value[offset:], '\n')
		if nl < 0 {
			return len(value)
		}
		offset += nl + 1
	}
	// Advance col runes into the row.
	rest := value[offset:]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[:nl]
	}
	for i := 0; i < col; i++ {
		_, size := utf8.DecodeRuneInString(rest)
		if size == 0 {
			break
		}
		rest = rest[size:]
		offset += size
	}
	return offset
}

// setTextareaToValueWithCursor replaces the textarea contents with newValue
// and positions the cursor at byte offset cursorByte.
func setTextareaToValueWithCursor(ta *textarea.Model, newValue string, cursorByte int) {
	ta.Reset()
	ta.SetValue(newValue)
	if cursorByte > len(newValue) {
		cursorByte = len(newValue)
	}
	targetRow := strings.Count(newValue[:cursorByte], "\n")
	lineStart := 0
	if idx := strings.LastIndexByte(newValue[:cursorByte], '\n'); idx >= 0 {
		lineStart = idx + 1
	}
	targetCol := utf8.RuneCountInString(newValue[lineStart:cursorByte])
	for ta.Line() > targetRow {
		ta.CursorUp()
	}
	ta.SetCursorColumn(targetCol)
}

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
			m.appendMessage(chatMessage{
				role:    "system",
				content: "Lost gateway connection — attempting to reconnect…",
			})
			m.updateViewport()
		}
	case client.StatusConnected:
		if prev.Status == client.StatusDisconnected || prev.Status == client.StatusReconnecting {
			m.appendMessage(chatMessage{
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
		m.appendMessage(chatMessage{role: "system", errMsg: errLine})
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
	viewport           viewport.Model
	textarea           textarea.Model
	messages           []chatMessage
	backend            backend.Backend
	connName           string // active connection name, rendered in the header bar
	sessionKey         string
	agentID            string
	agentName          string
	sending            bool
	runID              string // active run ID for cancellation
	finalisedRuns      finalisedRunSet // bounded LRU of run IDs we have already finalised; chat events still bearing one of these IDs are stale duplicates emitted by the gateway after final and must not corrupt the next run's placeholder
	gen                uint64 // generation counter stamped onto every newly-appended chatMessage; bumped after each turn finalises so the just-finalised turn can be replaced by a server-canonical refresh while the next turn's live state survives the merge
	pendingMessages    []string
	width              int
	height             int
	renderer           *glamour.TermRenderer
	stats              *sessionStats
	modelID            string
	promptTokens       int // input + cache read + cache write for the latest turn (a per-session snapshot, not cumulative); 0 until first sessions.list refresh
	contextWindow      int // model context capacity for the active session; 0 when unknown
	skills             []agentSkill
	agentNames         []string // populated asynchronously by loadAgentNames; powers /agent <TAB> completion
	routineNames       []string // populated by loadRoutineNames; powers /routine <TAB> completion
	completion         completionMenuState
	baseViewportHeight int // viewport height with the completion menu hidden; setSize updates it, applyLayout subtracts the menu's footprint when visible
	spinnerFrame       int
	spinnerTicking     bool
	prefs              config.Preferences
	pendingConfirm     *pendingConfirmation
	pendingNavConfirm  *pendingNavConfirm
	notifications      []notification // ephemeral status rows rendered above the input; cleared when the user submits an input
	historyLimit       int
	historyLoading     bool   // true while the initial history fetch is in flight; gates the placeholder in updateViewport
	thinkingLevel      string // current thinking level; "" means not set / using gateway default
	connState          ConnStateMsg
	hideInput          bool   // when true, the textarea + help line are not rendered; the textarea model still receives input bytes
	transcript         bool   // true when the model is rendering a cron run-log transcript: read-only, esc returns to the cron detail view that opened it
	terminalFocused    bool   // tracks tea.FocusMsg/BlurMsg so the completion bell only rings when the user is looking elsewhere
	updateLatest       string // populated by AppModel when the startup check finds a newer release; rendered as a header badge
	activeRoutine      *activeRoutine
}

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

// hasStreamingMessage reports whether any assistant message is still streaming,
// a tool is in the running state, or a system row is in the pending state
// (the spinner placeholder used by /compact and /reset while their actions
// are in flight). The spinner tick keeps firing as long as any of these is
// true so the streaming cursor, the tool-card glyph, and the system-row
// pending glyph all animate.
func (m *chatModel) hasStreamingMessage() bool {
	for i := range m.messages {
		if m.messages[i].streaming {
			return true
		}
		if m.messages[i].role == "tool" && m.messages[i].toolState == "running" {
			return true
		}
		if m.messages[i].role == "system" && m.messages[i].pending {
			return true
		}
	}
	return false
}

// appendMessage stamps the current gen counter onto msg and appends it
// to m.messages. Returns a pointer into the slice so callers that need
// to mutate the just-appended row (e.g. delta-streaming) can do so
// without a second slice walk; callers that don't may safely ignore
// the return.
//
// All "live" appends — user turns, streaming placeholders, tool cards,
// system rows, error rows — must go through this helper rather than
// raw append, so the merge in historyRefreshMsg can correctly
// distinguish "history-side" rows (replaced by server canonical state)
// from the "live tail" (preserved across the merge).
func (m *chatModel) appendMessage(msg chatMessage) *chatMessage {
	msg.gen = m.gen
	m.messages = append(m.messages, msg)
	return &m.messages[len(m.messages)-1]
}

// bumpGen captures the current generation and advances the counter.
// Called from the chat-event final/error/aborted paths after a turn
// has been successfully finalised, so the just-finalised turn (and
// everything before it) is on the "history-side" of the next
// refresh's boundary, while subsequent appends — drained queue,
// auto-advanced routine step, recovery system rows — fall on the
// "live tail" side and survive the merge.
//
// Returns the captured (pre-increment) value so callers can pass it
// straight into refreshHistoryAt.
func (m *chatModel) bumpGen() uint64 {
	boundary := m.gen
	m.gen++
	return boundary
}

// mergeHistoryRefresh replaces the history-side of m.messages with
// server-canonical history while preserving the live tail (rows whose
// gen exceeds the boundary captured at refresh-issue time). Concretely:
// fetched server messages come first, every existing row with
// gen > boundary is appended afterwards in original order.
//
// Used by the historyRefreshMsg handler. Pulled into a helper because
// the merge logic is non-trivial enough to warrant a unit test that
// doesn't have to thread through the full bubbletea Update path.
func (m *chatModel) mergeHistoryRefresh(server []chatMessage, boundary uint64) {
	// Walk once, partitioning. Pre-size the live slice from a count
	// pass to avoid repeated growth — long live tails (e.g. an active
	// routine with a tool card mid-execution) would otherwise reallocate.
	liveCount := 0
	for i := range m.messages {
		if m.messages[i].gen > boundary {
			liveCount++
		}
	}
	merged := make([]chatMessage, 0, len(server)+liveCount)
	merged = append(merged, server...)
	for i := range m.messages {
		if m.messages[i].gen > boundary {
			merged = append(merged, m.messages[i])
		}
	}
	m.messages = merged
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

// replacePendingSystem swaps the most recent pending system row for
// replacement, preserving its position in the transcript so the
// spinner placeholder turns into its outcome line in place. If no
// pending row is found (the prompt was cancelled, or some prior
// handler already cleared it), the replacement is appended instead so
// the user still sees the result.
//
// In-place swaps inherit the existing row's gen so the merge boundary
// stays consistent; the fall-through append goes through appendMessage
// for the same reason.
func (m *chatModel) replacePendingSystem(replacement chatMessage) {
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].role == "system" && m.messages[i].pending {
			replacement.gen = m.messages[i].gen
			m.messages[i] = replacement
			return
		}
	}
	m.appendMessage(replacement)
}

// ensureSpinnerTicking starts the spinner animation if one is not already scheduled.
func (m *chatModel) ensureSpinnerTicking() tea.Cmd {
	if m.spinnerTicking {
		return nil
	}
	m.spinnerTicking = true
	return spinnerTickCmd()
}

func newChatModel(b backend.Backend, sessionKey, agentID, agentName, modelID string, prefs config.Preferences, hideInput bool, connName, initialMessage string, brightCursor bool) chatModel {
	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.Focus()
	ta.CharLimit = 0
	ta.SetHeight(inputHeight)
	ta.ShowLineNumbers = false
	ta.Prompt = ""

	// Bubbles' default cursor renders its on-frame as
	// `Style.Reverse(true).Render(char)`. With the library default of
	// `Color("7")` (ANSI 8-colour light grey, no background), the
	// reverse-swap depends entirely on how the terminal maps ANSI
	// index 7: most desktop terminals render it bright enough to
	// produce a visible block, so the desktop CLI leaves brightCursor
	// false and keeps the library palette. Embedders driving the
	// program through a virtual terminal whose palette renders index
	// 7 too dimly (the reverse-swapped block then collapses to
	// near-invisible against the dim placeholder character — the
	// leading "T" of "Type a message..." disappears under the cursor)
	// pass brightCursor true. Pinning to ANSI 15 (bright white)
	// removes the dependency on palette luminance choices: every
	// reasonable terminal renders index 15 as bright white, so the
	// swap puts white in the cell's bg and the terminal's default-bg
	// colour in the fg — a guaranteed-visible block. SetStyles
	// propagates through the textarea's `updateVirtualCursorStyle`
	// so `virtualCursor.Style` picks up the new Foreground.
	if brightCursor {
		taStyles := ta.Styles()
		taStyles.Cursor.Color = lipgloss.Color("15")
		ta.SetStyles(taStyles)
	}

	ta.KeyMap.InsertNewline.SetKeys("alt+enter")
	ta.KeyMap.DeleteWordBackward.SetKeys("alt+backspace", "ctrl+w")
	ta.KeyMap.DeleteWordForward.SetKeys("alt+delete", "alt+d")

	vp := viewport.New()

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(80), // updated by setSize() when terminal dimensions are known
	)

	// initialMessage seeds pendingMessages so the first historyLoadedMsg
	// drains it through the same queue the textarea uses, matching what
	// a human typing would see (history scrollback, then their message).
	var pending []string
	if initialMessage != "" {
		pending = []string{initialMessage}
	}

	return chatModel{
		viewport:        vp,
		textarea:        ta,
		backend:         b,
		connName:        connName,
		sessionKey:      sessionKey,
		agentID:         agentID,
		agentName:       agentName,
		renderer:        renderer,
		modelID:         modelID,
		prefs:           prefs,
		historyLimit:    prefs.HistoryLimit,
		historyLoading:  true,
		hideInput:       hideInput,
		terminalFocused: true,
		pendingMessages: pending,
		// Start at gen=1 so the zero value on chatMessage.gen reads as
		// "older than any live turn" — server-history imports keep that
		// default and are always replaceable on subsequent refreshes.
		gen: 1,
	}
}

func (m chatModel) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.loadHistory(),
		m.loadStats(),
		m.loadContextUsage(),
		func() tea.Msg { return skillsDiscoveredMsg{skills: discoverSkills()} },
		m.loadAgentNames(),
		loadRoutineNames(),
	)
}

func (m chatModel) loadAgentNames() tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		result, err := b.ListAgents(context.Background())
		if err != nil || result == nil {
			return chatAgentNamesLoadedMsg{}
		}
		names := make([]string, 0, len(result.Agents))
		for _, a := range result.Agents {
			n := a.Name
			if n == "" {
				n = a.ID
			}
			if n != "" {
				names = append(names, n)
			}
		}
		return chatAgentNamesLoadedMsg{names: names}
	}
}

// loadContextUsage fetches the per-session context-usage snapshot
// (numerator and denominator for the header's "N/W (P%)" display)
// from sessions.list. The gateway emits a totalTokens field per
// session entry that is the prompt-token snapshot for the latest run
// (input + cache read + cache write, intentionally excluding output),
// and a contextTokens field that is the model window for that specific
// session. Reading both from the same entry keeps the percentage scoped
// to *this* session rather than a gateway-wide aggregate.
func (m chatModel) loadContextUsage() tea.Cmd {
	if m.sessionKey == "" {
		return func() tea.Msg { return contextUsageLoadedMsg{} }
	}
	b := m.backend
	sessionKey := m.sessionKey
	agentID := m.agentID
	return func() tea.Msg {
		raw, err := b.SessionsList(context.Background(), agentID)
		if err != nil {
			return contextUsageLoadedMsg{sessionKey: sessionKey}
		}
		var resp struct {
			Sessions []json.RawMessage `json:"sessions"`
			Defaults struct {
				ContextTokens *int `json:"contextTokens"`
			} `json:"defaults"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return contextUsageLoadedMsg{sessionKey: sessionKey}
		}
		var entry struct {
			Key           string `json:"key"`
			TotalTokens   *int   `json:"totalTokens"`
			ContextTokens *int   `json:"contextTokens"`
		}
		for _, rawEntry := range resp.Sessions {
			var probe struct {
				Key string `json:"key"`
			}
			if err := json.Unmarshal(rawEntry, &probe); err != nil || probe.Key != sessionKey {
				continue
			}
			if err := json.Unmarshal(rawEntry, &entry); err == nil {
				break
			}
		}
		out := contextUsageLoadedMsg{sessionKey: sessionKey}
		if entry.TotalTokens != nil {
			out.promptTokens = *entry.TotalTokens
		}
		switch {
		case entry.ContextTokens != nil:
			out.contextWindow = *entry.ContextTokens
		case resp.Defaults.ContextTokens != nil:
			out.contextWindow = *resp.Defaults.ContextTokens
		}
		return out
	}
}

func (m chatModel) loadStats() tea.Cmd {
	usage, ok := m.backend.(backend.UsageBackend)
	if !ok {
		// Backends without server-side usage stats simply skip the
		// load; the chat view falls back to the placeholder.
		return func() tea.Msg { return statsLoadedMsg{} }
	}
	return func() tea.Msg {
		raw, err := usage.SessionUsage(context.Background(), "")
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
		m.historyLoading = false
		switch {
		case msg.err != nil:
			m.appendMessage(chatMessage{
				role:   "system",
				errMsg: fmt.Sprintf("Could not load conversation history: %v", msg.err),
			})
		case len(msg.messages) > 0:
			lastTs := lastTimestampMs(msg.messages)
			// Server-imported rows keep gen=0 (the chatMessage zero
			// value) so any subsequent refresh treats them as
			// history-side and replaces them cleanly.
			hist := append(msg.messages, chatMessage{role: "separator", timestampMs: lastTs})
			m.messages = append(hist, m.messages...)
		}
		m.updateViewport()
		// If a caller pre-seeded pendingMessages (e.g. `lucinate chat`
		// passing an auto-submit message), drain it now so the user
		// turn is appended *after* the history scrollback — matching
		// what they'd see typing it themselves.
		if len(m.pendingMessages) > 0 && !m.sending {
			m.sending = true
			return m, m.drainQueue()
		}
		return m, nil

	case agentSwitchFailedMsg:
		m.appendMessage(chatMessage{role: "system", errMsg: msg.err.Error()})
		m.updateViewport()
		return m, nil

	case modelSwitchedMsg:
		if msg.err != nil {
			m.appendMessage(chatMessage{role: "system", errMsg: msg.err.Error()})
		} else {
			m.modelID = msg.modelID
			m.appendMessage(chatMessage{
				role:    "system",
				content: fmt.Sprintf("Switched to %s", msg.modelID),
			})
		}
		m.updateViewport()
		// New model means a new context window — refresh the snapshot
		// so the header doesn't keep showing the previous model's window.
		return m, m.loadContextUsage()

	case contextUsageLoadedMsg:
		// Discard if the active session changed mid-flight (e.g. user
		// navigated away then back) — the snapshot would belong to a
		// different session.
		if msg.sessionKey == m.sessionKey {
			m.promptTokens = msg.promptTokens
			m.contextWindow = msg.contextWindow
		}
		return m, nil

	case gatewayStatusMsg:
		if msg.err != nil {
			m.appendMessage(chatMessage{role: "system", errMsg: msg.err.Error()})
		} else {
			m.appendMessage(chatMessage{
				role:    "system",
				content: formatGatewayStatus(msg.health, msg.uptimeMs),
			})
		}
		m.updateViewport()
		return m, nil

	case thinkingChangedMsg:
		if msg.err != nil {
			m.appendMessage(chatMessage{role: "system", errMsg: msg.err.Error()})
		} else {
			m.thinkingLevel = msg.level
			display := msg.level
			if display == "" || display == "off" {
				display = "off"
			}
			m.appendMessage(chatMessage{
				role:    "system",
				content: fmt.Sprintf("Thinking level set to %s", display),
			})
		}
		m.updateViewport()
		return m, nil

	case sessionCompactedMsg:
		if msg.err != nil {
			m.replacePendingSystem(chatMessage{role: "system", errMsg: fmt.Sprintf("compact failed: %v", msg.err)})
		} else {
			m.replacePendingSystem(chatMessage{role: "system", content: "Session compacted."})
		}
		m.updateViewport()
		return m, nil

	case sessionClearedMsg:
		if msg.err != nil {
			m.replacePendingSystem(chatMessage{role: "system", errMsg: fmt.Sprintf("clear session failed: %v", msg.err)})
		} else {
			m.sessionKey = msg.newSessionKey
			m.messages = nil
			m.appendMessage(chatMessage{role: "system", content: "Session cleared. Starting fresh."})
		}
		m.updateViewport()
		return m, nil

	case statsLoadedMsg:
		if msg.err == nil && msg.stats != nil {
			m.stats = msg.stats
		}
		return m, nil

	case chatAgentNamesLoadedMsg:
		m.agentNames = msg.names
		return m, nil

	case chatRoutineNamesLoadedMsg:
		m.routineNames = msg.names
		return m, nil

	case startRoutineMsg:
		cmd := m.startRoutine(msg.name)
		m.updateViewport()
		return m, cmd

	case skillsDiscoveredMsg:
		m.skills = msg.skills
		if len(m.skills) > 0 {
			m.appendMessage(chatMessage{
				role:    "system",
				content: fmt.Sprintf("%d agent skill(s) loaded — type /skills to list", len(m.skills)),
			})
			m.updateViewport()
		}
		return m, nil

	case historyRefreshMsg:
		if msg.err == nil && len(msg.messages) > 0 {
			// Merge: replace history-side rows with server-canonical
			// state, preserve the live tail (rows whose gen exceeds
			// the boundary captured at refresh-issue time). This makes
			// it safe to refresh mid-routine, mid-queue-drain, and
			// while a tool card is in flight — scenarios where the old
			// wholesale-replace would have wiped live state.
			m.mergeHistoryRefresh(msg.messages, msg.boundary)
			m.updateViewport()
		}
		// History refresh fires after each completed turn — pull a
		// fresh prompt-token snapshot so the % keeps up with the
		// session's current context size.
		return m, tea.Batch(m.loadStats(), m.loadContextUsage())

	case tea.KeyPressMsg:
		logEvent("KEY code=%d mod=%v string=%q", msg.Code, msg.Mod, msg.String())
		switch msg.String() {
		case "esc":
			if m.pendingNavConfirm != nil {
				m.pendingNavConfirm = nil
				m.clearNotifications()
				m.notify("Cancelled — routine continues.")
				m.updateViewport()
				return m, nil
			}
			if m.activeRoutine != nil {
				var cmd tea.Cmd
				if m.sending {
					cmd = m.cancelTurn()
				}
				m.endRoutine("cancelled")
				m.updateViewport()
				return m, cmd
			}
			if m.sending {
				return m, m.cancelTurn()
			}
			if m.transcript {
				return m, func() tea.Msg { return goBackFromCronTranscriptMsg{} }
			}
			return m, nil
		case "alt+m":
			if m.activeRoutine != nil {
				m.cycleRoutineMode()
				m.updateViewport()
			}
			return m, nil
		case "tab":
			if ctx, ok := m.completionAtCursor(); ok {
				m.handleCompletionTab(ctx)
			}
			return m, nil
		case "shift+tab":
			if m.completion.cycling && len(m.completion.cycleCandidates) > 0 {
				if ctx, ok := m.completionAtCursor(); ok {
					value := m.textarea.Value()
					n := len(m.completion.cycleCandidates)
					m.completion.cycleIndex = (m.completion.cycleIndex - 1 + n) % n
					pick := m.completion.cycleCandidates[m.completion.cycleIndex]
					newValue := value[:ctx.start] + pick + value[ctx.cursorByte:]
					setTextareaToValueWithCursor(&m.textarea, newValue, ctx.start+len(pick))
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
				m.refreshCompletionMenu()
				m.updateViewport()
				return m, nil
			}
		case "enter":
			text := strings.TrimSpace(m.textarea.Value())
			if text == "" {
				// Empty Enter advances the active routine when manual or paused
				// and idle. Keeps the gesture out of the way of the textarea's
				// regular newline handling (alt+enter), which fires before this
				// branch only when there is content.
				if ar := m.activeRoutine; ar != nil && !m.sending {
					if (ar.mode == routines.ModeManual || ar.paused) && ar.sent < len(ar.routine.Steps) {
						m.clearNotifications()
						return m, m.sendNextRoutineStep()
					}
				}
				return m, nil
			}

			// Any non-empty submission (typed text, y/n on a confirm,
			// slash command, queued message) clears the ephemeral
			// notification rows above the input — the user has just
			// taken a meaningful action and any pending notifications
			// have either been read or no longer apply.
			m.clearNotifications()

			// Resolve a pending routine-cancel-on-navigation prompt. Done
			// before the generic pendingConfirm path because the nav
			// gate must end the routine synchronously so the log file
			// is closed before the chatModel is potentially replaced.
			if m.pendingNavConfirm != nil {
				m.textarea.Reset()
				m.refreshCompletionMenu()
				confirm := m.pendingNavConfirm
				m.pendingNavConfirm = nil
				lower := strings.ToLower(text)
				if lower == "y" || lower == "yes" {
					var cmds []tea.Cmd
					// Abort any in-flight turn the routine kicked off so a
					// follow-up startRoutineMsg (the /routine <name> path)
					// finds a clean controller and m.sending == false.
					if m.sending {
						if cmd := m.cancelTurn(); cmd != nil {
							cmds = append(cmds, cmd)
						}
					}
					m.endRoutine("cancelled")
					if confirm.nav != nil {
						cmds = append(cmds, confirm.nav)
					}
					m.updateViewport()
					return m, tea.Batch(cmds...)
				}
				m.notify("Cancelled — routine continues.")
				m.updateViewport()
				return m, nil
			}

			// Resolve a pending confirmation prompt.
			if m.pendingConfirm != nil {
				m.textarea.Reset()
				m.refreshCompletionMenu()
				confirm := m.pendingConfirm
				m.pendingConfirm = nil
				lower := strings.ToLower(text)
				if lower == "y" || lower == "yes" {
					m.notify("Confirmed.")
					var spinnerCmd tea.Cmd
					if confirm.runningStatus != "" {
						m.appendMessage(chatMessage{
							role:    "system",
							content: confirm.runningStatus,
							pending: true,
						})
						spinnerCmd = m.ensureSpinnerTicking()
					}
					m.updateViewport()
					return m, tea.Batch(confirm.action(), spinnerCmd)
				}
				m.notify("Cancelled.")
				m.updateViewport()
				return m, nil
			}

			// Slash commands are local — handle immediately even while sending.
			if handled, cmd := m.handleSlashCommand(text); handled {
				m.textarea.Reset()
				m.refreshCompletionMenu()
				return m, cmd
			}

			if m.sending {
				// Queue the message for later delivery.
				m.textarea.Reset()
				m.refreshCompletionMenu()
				m.pendingMessages = append(m.pendingMessages, text)
				m.updateViewport()
				return m, nil
			}

			m.textarea.Reset()
			m.refreshCompletionMenu()

			if strings.HasPrefix(text, "!!") {
				command := strings.TrimSpace(text[2:])
				if command == "" {
					return m, nil
				}
				m.appendMessage(chatMessage{role: "system", content: fmt.Sprintf("!! %s", command)})
				m.appendMessage(chatMessage{role: "system", content: "running on gateway..."})
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
				m.appendMessage(chatMessage{role: "system", content: fmt.Sprintf("$ %s", command)})
				m.appendMessage(chatMessage{role: "system", content: "running..."})
				m.sending = true
				m.updateViewport()
				cmds = append(cmds, localExecCommand(command))
				return m, tea.Batch(cmds...)
			}

			sent := text
			if len(m.skills) > 0 {
				if expanded, ok := expandSkillReferences(text, m.skills); ok {
					sent = expanded
				}
			}
			m.appendMessage(chatMessage{role: "user", content: text})
			m.appendMessage(chatMessage{role: "assistant", streaming: true, awaitingDelta: true})
			m.sending = true
			if ar := m.activeRoutine; ar != nil && ar.logger != nil {
				ar.logger.WriteUser(text)
			}
			m.updateViewport()
			cmds = append(cmds, m.sendMessage(sent), m.ensureSpinnerTicking())
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
			m.appendMessage(chatMessage{
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

	// Refresh the slash-command completion menu after the textarea has
	// consumed a keystroke. Tab/Shift+Tab return early above so this only
	// fires for keys that may have changed the input (typing, backspace,
	// arrow keys that move the cursor in/out of a slash token).
	if _, ok := msg.(tea.KeyPressMsg); ok {
		m.refreshCompletionMenu()
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
	b := m.backend
	skills := m.catalogParams()
	return func() tea.Msg {
		idemKey := fmt.Sprintf("lucinate-%d", time.Now().UnixNano())
		result, err := b.ChatSend(context.Background(), sessionKey, backend.ChatSendParams{
			Message:        text,
			IdempotencyKey: idemKey,
			Skills:         skills,
		})
		if err != nil {
			return chatSentMsg{err: err}
		}
		return chatSentMsg{runID: result.RunID}
	}
}

// cancelTurn aborts the active turn and clears pending messages.
func (m *chatModel) cancelTurn() tea.Cmd {
	if !m.sending || m.runID == "" {
		m.notify("Nothing to cancel.")
		m.updateViewport()
		return nil
	}
	b := m.backend
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
	m.notify("Cancelled.")
	m.updateViewport()
	return func() tea.Msg {
		err := b.ChatAbort(context.Background(), sessionKey, runID)
		return chatAbortMsg{err: err}
	}
}

// catalogParams converts the chat model's discovered skills into
// the protocol-neutral SkillCatalogEntry slice the backend expects.
// Returns nil when no skills are loaded so backends that no-op on
// empty input avoid an extra allocation per turn.
func (m *chatModel) catalogParams() []backend.SkillCatalogEntry {
	if len(m.skills) == 0 {
		return nil
	}
	out := make([]backend.SkillCatalogEntry, 0, len(m.skills))
	for _, s := range m.skills {
		out = append(out, backend.SkillCatalogEntry{Name: s.Name, Description: s.Description})
	}
	return out
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
			// Queue fully drained — refresh history with a boundary
			// of m.gen-1 so the just-finalised turn (and everything
			// older) is replaced by server canonical state, while any
			// rows that arrive between issue and result (typically
			// none on this path, but possible) survive the merge.
			return tea.Batch(m.refreshHistoryAt(m.gen-1), m.loadStats())
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
		m.appendMessage(chatMessage{role: "system", content: fmt.Sprintf("!! %s", command)})
		m.appendMessage(chatMessage{role: "system", content: "running on gateway..."})
		m.updateViewport()
		return m.execCommand(command)
	}

	if strings.HasPrefix(text, "!") {
		command := strings.TrimSpace(text[1:])
		if command == "" {
			m.sending = false
			return nil
		}
		m.appendMessage(chatMessage{role: "system", content: fmt.Sprintf("$ %s", command)})
		m.appendMessage(chatMessage{role: "system", content: "running..."})
		m.updateViewport()
		return localExecCommand(command)
	}

	sent := text
	if len(m.skills) > 0 {
		if expanded, ok := expandSkillReferences(text, m.skills); ok {
			sent = expanded
		}
	}
	m.appendMessage(chatMessage{role: "user", content: text})
	m.appendMessage(chatMessage{role: "assistant", streaming: true, awaitingDelta: true})
	if ar := m.activeRoutine; ar != nil && ar.logger != nil {
		ar.logger.WriteUser(text)
	}
	m.updateViewport()
	return tea.Batch(m.sendMessage(sent), m.ensureSpinnerTicking())
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
	if m.hideInput {
		vpHeight = h - headerH - helpH - 2
	}

	m.viewport.SetWidth(w)
	m.baseViewportHeight = vpHeight
	m.applyLayout()

	m.textarea.SetWidth(w - 7)
	m.updateViewport()
}

func (m chatModel) View() string {
	left := " lucinate"
	if m.connName != "" {
		left += " · " + m.connName
	}
	left += fmt.Sprintf(" — %s", m.agentName)
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
	if m.updateLatest != "" {
		left += " · " + headerBadgeWarnStyle.Render("↑ "+m.updateLatest)
	}
	right := ""
	if m.contextWindow > 0 && m.promptTokens > 0 {
		// Per-session prompt-token snapshot from sessions.list — the
		// "live context" size for the latest turn. Capped at 999% so
		// a runaway value never widens the header.
		pct := m.promptTokens * 100 / m.contextWindow
		if pct > 999 {
			pct = 999
		}
		cost := 0.0
		if m.stats != nil {
			cost = m.stats.totalCost
		}
		right = fmt.Sprintf("tokens: %s/%s (%d%%)  $%.2f ",
			formatTokensShort(m.promptTokens), formatTokensShort(m.contextWindow), pct, cost)
	} else if m.stats != nil {
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

	var menu string
	if !m.hideInput {
		menu, _ = m.renderCompletionMenu()
	}

	var help string
	if isRemoteExec {
		help = helpStyle.Render(execPrefixStyle.Render(" remote command") + " — runs on gateway host")
	} else if isLocalExec {
		help = helpStyle.Render(localExecPrefixStyle.Render(" local command") + " — runs on this machine")
	} else if menu != "" {
		help = helpStyle.Render(fmt.Sprintf(" Tab: extend · Shift+Tab: back · %d matches", len(m.completion.candidates)))
	} else {
		value := m.textarea.Value()
		cursorByte := textareaCursorByteOffset(&m.textarea)
		token, suffix := m.slashCommandHint(value, cursorByte)
		if suffix == "" {
			token, suffix = m.agentNameHint(value, cursorByte)
		}
		if suffix == "" {
			token, suffix = m.routineNameHint(value, cursorByte)
		}
		if suffix != "" {
			help = helpStyle.Render(fmt.Sprintf(" %s%s — tab to complete", token, suffix))
		} else if m.hideInput {
			help = helpStyle.Render(" /help: commands")
		} else {
			helpText := " enter: send | alt+enter: newline | /help: commands"
			if n := len(m.pendingMessages); n > 0 {
				helpText += fmt.Sprintf(" | %d queued (up: edit last)", n)
			}
			help = helpStyle.Render(helpText)
		}
	}

	routineStatus := ""
	if line := m.routineStatusLine(); line != "" {
		routineStatus = routineStatusStyle.Width(m.width).Render(line)
	}
	notifications := m.renderNotifications()

	if m.hideInput {
		parts := []string{header, m.viewport.View()}
		if notifications != "" {
			parts = append(parts, notifications)
		}
		if routineStatus != "" {
			parts = append(parts, routineStatus)
		}
		parts = append(parts, help)
		return lipgloss.JoinVertical(lipgloss.Left, parts...)
	}

	input := borderStyle.
		Width(m.width - 4).
		Render(m.textarea.View())

	parts := []string{header, m.viewport.View()}
	if menu != "" {
		parts = append(parts, menu)
	}
	if notifications != "" {
		parts = append(parts, notifications)
	}
	if routineStatus != "" {
		parts = append(parts, routineStatus)
	}
	parts = append(parts, input, help)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}
