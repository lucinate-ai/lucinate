package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lucinate-ai/lucinate/internal/rooms"
)

// The rooms view's second half: streaming replies, reconnect/resume, and the
// transcript commands that need no gateway round-trip of their own (routing,
// export, compact, find, cost, header colours).
//
// It lives in its own file because none of it is about the room lifecycle —
// browsing, seating and disbanding are in rooms.go — and because every piece
// here is a small pure function the tests can drive without a socket.

// roomsDialer is the one place the view opens a gateway connection. Tests
// substitute a dialer so a whole scenario (streaming, a dropped socket,
// resume) can run against an in-process gateway.
var roomsDialer = rooms.DialBaseURL

// ── messages ─────────────────────────────────────────────────────────────────

// roomsSpinnerTickMsg advances the braille glyph on the in-flight reply rows.
// It is separate from the chat view's spinnerTickMsg: only one view is live
// at a time, and a shared message would make each view's handler fight over
// the same tick.
type roomsSpinnerTickMsg struct{}

type roomsReconnectMsg struct {
	// attempt is the backoff attempt this tick belongs to. A stale tick
	// (the user left, or a later attempt superseded it) is ignored rather
	// than redialing twice.
	attempt int
}

type roomsPrefsMsg struct {
	store rooms.PrefsStore
	err   error
}

// pendingCompact is a /compact request waiting for the roster's brief.
type pendingCompact struct {
	throughSeq int
	promptSeq  int
	keepLast   int
}

const roomsSpinnerInterval = 120 * time.Millisecond

func roomsSpinnerCmd() tea.Cmd {
	return tea.Tick(roomsSpinnerInterval, func(time.Time) tea.Msg { return roomsSpinnerTickMsg{} })
}

// interruptedTurn is a member reply the connection cut in half.
//
// The gateway log never learns the turn died — no `turn.settled` arrives — so
// the transcript alone would show a reply that is still "being written"
// forever. The client is the only witness, so it records what it saw and marks
// the row: a half-written answer the user can see is honest, one frozen behind
// a spinner is a bug report.
type interruptedTurn struct {
	Member string
	Text   string
	At     time.Time
	Reason string
	// AfterSeq is the newest event the view had seen when the connection
	// dropped. Only an event past it proves the member is answering again —
	// without it, the reconnect's own history replay would erase the marker
	// it is meant to explain.
	AfterSeq int
}

// markInterrupted snapshots every in-flight reply and returns the room to a
// ready state, so the view cannot keep animating a turn nothing is serving.
func (m *roomsModel) markInterrupted(reason string) {
	if m.interrupted == nil {
		m.interrupted = map[string]interruptedTurn{}
	}
	watermark := m.since
	if m.page.LatestSeq > watermark {
		watermark = m.page.LatestSeq
	}
	now := time.Now()
	for _, s := range rooms.ActiveStreams(m.page.Events) {
		m.interrupted[s.Member] = interruptedTurn{
			Member:   s.Member,
			Text:     strings.TrimSpace(s.Text),
			At:       now,
			Reason:   reason,
			AfterSeq: watermark,
		}
	}
	// The room is ready again: nothing is answering, so holding a queued
	// message behind a dead driver would strand it.
	m.working = false
}

// visibleStreams are the in-flight replies the view still animates: a stream
// whose member was interrupted is shown as an interrupted row instead.
func (m roomsModel) visibleStreams() []rooms.Stream {
	streams := m.activeStreams()
	out := make([]rooms.Stream, 0, len(streams))
	for _, s := range streams {
		if _, ok := m.interrupted[s.Member]; ok {
			continue
		}
		out = append(out, s)
	}
	return out
}

// clearResumedInterruptions drops the interrupted marker for members that are
// answering again — a member that comes back should read as recovered, not as
// permanently broken. Only events newer than the interruption count.
func (m *roomsModel) clearResumedInterruptions(events []rooms.Event) {
	if len(m.interrupted) == 0 {
		return
	}
	for _, ev := range events {
		switch ev.Kind {
		case "message.member", "message.member.delta", "turn.started", "turn.settled":
		default:
			continue
		}
		member := streamMemberOf(ev)
		turn, ok := m.interrupted[member]
		if !ok || ev.Seq <= turn.AfterSeq {
			continue
		}
		delete(m.interrupted, member)
	}
}

// streamMemberOf mirrors the rooms package's stream key, so an interruption is
// filed under the same handle its stream is.
func streamMemberOf(ev rooms.Event) string {
	if h, _ := ev.Payload["handle"].(string); strings.TrimSpace(h) != "" {
		return strings.ToLower(strings.TrimSpace(h))
	}
	if ev.Actor.Profile != "" {
		return rooms.HandleFor(ev.Actor.Profile)
	}
	if ev.Actor.DisplayName != "" {
		return rooms.HandleFor(ev.Actor.DisplayName)
	}
	return rooms.HandleFor(ev.Speaker())
}

// interruptedRows renders the marked turns, sorted by member so the view is
// stable between frames.
func (m roomsModel) interruptedRows() []string {
	if len(m.interrupted) == 0 {
		return nil
	}
	members := make([]string, 0, len(m.interrupted))
	for member := range m.interrupted {
		members = append(members, member)
	}
	sort.Strings(members)
	rows := make([]string, 0, len(members))
	for _, member := range members {
		turn := m.interrupted[member]
		reason := turn.Reason
		if reason == "" {
			reason = "connection lost"
		}
		rows = append(rows, errorStyle.Render(fmt.Sprintf("⚠  @%s reply interrupted (%s)", member, reason)))
		if turn.Text != "" {
			rows = append(rows, statusStyle.Render("   "+strings.ReplaceAll(oneLineShort(turn.Text, 160), "\n", " ")))
		} else {
			rows = append(rows, statusStyle.Render("   no output arrived before the connection dropped"))
		}
	}
	return rows
}

// oneLineShort flattens and truncates a fragment for the interrupted row.
func oneLineShort(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max-1]) + "…"
	}
	return s
}

// ── streaming ────────────────────────────────────────────────────────────────

// activeStreams are the members still answering, folded straight out of the
// log so the view holds no separate streaming bookkeeping that could drift.
func (m roomsModel) activeStreams() []rooms.Stream {
	return rooms.ActiveStreams(m.page.Events)
}

// spinnerActive reports whether any animated row is on screen.
func (m roomsModel) spinnerActive() bool {
	return len(m.visibleStreams()) > 0 || m.working || len(m.queue) > 0
}

// ensureRoomsSpinner starts the glyph animation if it is not already running,
// so a reply that arrives between polls animates immediately.
func (m *roomsModel) ensureRoomsSpinner() tea.Cmd {
	if m.spinnerTicking {
		return nil
	}
	m.spinnerTicking = true
	return roomsSpinnerCmd()
}

func (m *roomsModel) handleSpinnerTick() (roomsModel, tea.Cmd) {
	if !m.spinnerActive() || m.sub != roomsTranscript {
		m.spinnerTicking = false
		m.spinnerFrame = 0
		return *m, nil
	}
	m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
	return *m, roomsSpinnerCmd()
}

func spinnerGlyph(frame int) string {
	if len(spinnerFrames) == 0 {
		return "⠋"
	}
	if frame < 0 {
		frame = 0
	}
	return spinnerFrames[frame%len(spinnerFrames)]
}

// streamRows renders the in-flight replies. A member that has produced text
// shows it growing; one that has not shows the glyph and an ellipsis, which
// is what "still thinking" looks like.
//
// An empty reply never reaches here: ActiveStreams excludes settled turns, so
// the placeholder disappears the moment the log says the turn ended without
// text — a spinner left running against a silent member reads as a hung room.
func (m roomsModel) streamRows() []string {
	streams := m.visibleStreams()
	rows := make([]string, 0, len(streams))
	for _, s := range streams {
		glyph := cursorStyle.Render(spinnerGlyph(m.spinnerFrame))
		text := s.Text
		if strings.TrimSpace(text) == "" {
			text = statusStyle.Render("…")
		}
		rows = append(rows, fmt.Sprintf("%s %s %s", glyph, m.memberStyle(s.Member).Render(s.Label()), text))
	}
	return rows
}

// ── colours ──────────────────────────────────────────────────────────────────

// memberStyle renders a member's header. The colour comes from the room's
// preferences when the user set one, else from the deterministic palette, so
// two members never swap colours between sessions.
func (m roomsModel) memberStyle(handle string) lipgloss.Style {
	hex := rooms.ColorFor(handle, m.roomPrefs().Colours)
	if hex == "" {
		return assistantPrefixStyle
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(hex))
}

// highlightQuery marks every occurrence of the live /find query inside a
// rendered line. Matching is case-insensitive on the plain text; the styling
// is applied to the original casing.
func highlightQuery(line, query string, style lipgloss.Style) string {
	if query == "" || line == "" {
		return line
	}
	lowerLine := strings.ToLower(line)
	lowerQuery := strings.ToLower(query)
	var b strings.Builder
	for {
		i := strings.Index(lowerLine, lowerQuery)
		if i < 0 {
			b.WriteString(line)
			return b.String()
		}
		b.WriteString(line[:i])
		b.WriteString(style.Render(line[i : i+len(query)]))
		line = line[i+len(query):]
		lowerLine = lowerLine[i+len(query):]
	}
}

// ── compaction ───────────────────────────────────────────────────────────────

// compactionRule draws the band that replaces a summarised transcript.
func compactionRule(c *rooms.Compaction) string {
	label := " brief "
	if c.Source == "local" {
		label = " brief (local, no roster reply) "
	}
	if c.KeepLast > 0 {
		label = fmt.Sprintf(" brief · covers seq 1-%d · keeps last %d ", c.ThroughSeq, c.KeepLast)
		if c.Source == "local" {
			label = fmt.Sprintf(" brief (local) · covers seq 1-%d · keeps last %d ", c.ThroughSeq, c.KeepLast)
		}
	}
	return statusStyle.Render("──" + label + strings.Repeat("─", 12))
}

// compactedEvents are the events the transcript still shows: everything after
// the compaction's cut, with the /compact request itself hidden with the part
// it summarised.
func (m roomsModel) compactedEvents() []rooms.Event {
	_, visible := rooms.ApplyCompaction(m.page.Events, m.roomPrefs().Compaction)
	return visible
}

// ── reconnect ────────────────────────────────────────────────────────────────

// disconnected reports whether err/socket state means the gateway socket is
// gone. A deliberate Close is not a disconnect: Client.Err() is only set when
// the read loop died on its own.
func disconnected(c *rooms.Client, err error) bool {
	if c != nil && c.Err() != nil {
		return true
	}
	return rooms.IsDisconnect(err)
}

// scheduleReconnect drops the dead socket and arms the next redial.
func (m *roomsModel) scheduleReconnect(reason string) tea.Cmd {
	m.shared.drop(m.shared.get())
	// Anything mid-answer is now an interrupted reply: record it before the
	// view stops showing it, so the user sees what arrived rather than a
	// spinner against a room nobody is serving.
	m.markInterrupted(reason)
	delay := m.backoff.Next()
	m.reconnecting = true
	m.reconnectAttempt = m.backoff.Attempt()
	if reason != "" {
		m.status = fmt.Sprintf("gateway connection lost (%s) — redial in %s (attempt %d); %d event(s) already loaded stay on screen",
			reason, delay.Round(time.Millisecond), m.reconnectAttempt, len(m.page.Events))
	} else {
		m.status = fmt.Sprintf("gateway connection lost — redial in %s (attempt %d); %d event(s) already loaded stay on screen",
			delay.Round(time.Millisecond), m.reconnectAttempt, len(m.page.Events))
	}
	return m.reconnectAfter(delay, m.reconnectAttempt)
}

func (m roomsModel) reconnectAfter(delay time.Duration, attempt int) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return roomsReconnectMsg{attempt: attempt}
	})
}

// handleReconnect redials and resumes: the log is refetched from the cursor
// the view already holds, so no part of the transcript is lost or repeated.
func (m *roomsModel) handleReconnect(attempt int) (roomsModel, tea.Cmd) {
	if attempt != m.reconnectAttempt || m.sub != roomsTranscript || m.openRoomID == "" {
		return *m, nil
	}
	if m.conn == nil {
		// No connection to redial against: retrying would spin forever.
		m.reconnecting = false
		m.reconnectAttempt = 0
		m.backoff.Reset()
		m.err = fmt.Errorf("gateway connection lost and there is no saved connection to redial — reopen the room")
		return *m, nil
	}
	// dial() caches the fresh connection in shared, so the reload below (and
	// every later poll) travels over the new socket.
	if _, err := m.dial(); err != nil {
		m.err = nil
		return *m, m.scheduleReconnect(err.Error())
	}
	m.backoff.Reset()
	m.reconnectAttempt = 0
	m.reconnecting = false
	m.err = nil
	// A new socket is a fresh session view: drop the local model overlay so the
	// roster the gateway sends next is what the user sees. Nothing else has to
	// be undone — the profile was never touched.
	m.memberModels = nil
	m.model = modelPicker{}
	m.status = fmt.Sprintf("reconnected — history resumed from seq %d", m.since)
	return *m, tea.Batch(m.loadLog(m.openRoomID, m.since), m.loadState(), m.ensureRoomsSpinner())
}

// ── prefs plumbing ───────────────────────────────────────────────────────────

func (m roomsModel) roomPrefs() rooms.RoomPrefs { return m.prefs.Get(m.openRoomID) }

// updateRoomPrefs mutates the open room's settings and persists them. The
// write happens in a cmd so a slow disk never blocks a keystroke.
func (m *roomsModel) updateRoomPrefs(mutate func(*rooms.RoomPrefs)) tea.Cmd {
	roomID := m.openRoomID
	if roomID == "" {
		return nil
	}
	m.prefs.Update(roomID, mutate)
	store := m.prefs
	return func() tea.Msg { return roomsPrefsMsg{store: store, err: savePrefs(store)} }
}

func savePrefs(store rooms.PrefsStore) error {
	path, err := rooms.DefaultPrefsPath()
	if err != nil {
		return err
	}
	return rooms.SavePrefs(path, store)
}

func (m roomsModel) loadPrefs() tea.Cmd {
	return func() tea.Msg {
		path, err := rooms.DefaultPrefsPath()
		if err != nil {
			return roomsPrefsMsg{err: err}
		}
		store, err := rooms.LoadPrefs(path)
		return roomsPrefsMsg{store: store, err: err}
	}
}

// routingNote is what the transcript header says about routing, so a room in
// round-robin never looks like a room where everyone is ignoring you.
func (m roomsModel) routingNote() string {
	p := m.roomPrefs()
	note := ""
	switch p.RoutingMode() {
	case rooms.RouteModerator:
		if strings.TrimSpace(p.Moderator) == "" {
			note = "moderator: first member (set one with /moderator <handle>)"
		} else {
			note = "moderator: @" + p.Moderator
		}
	case rooms.RouteRoundRobin:
		note = fmt.Sprintf("round-robin: next member #%d", p.RoundRobinIndex%max(1, len(m.roster()))+1)
	default:
		note = "broadcast: the whole roster answers"
	}
	if c := p.Compaction; c != nil {
		note += fmt.Sprintf(" · brief covers seq 1-%d (source: %s)", c.ThroughSeq, compactionSource(c))
	}
	return note
}

func compactionSource(c *rooms.Compaction) string {
	if c.Source == "" {
		return "roster"
	}
	return c.Source
}

func (m roomsModel) roster() []rooms.Member {
	if m.openRoom != nil && len(m.openRoom.Members) > 0 {
		return m.openRoom.Members
	}
	return nil
}

// ── notices ──────────────────────────────────────────────────────────────────

func (m roomsModel) noticeBlock() string {
	if strings.TrimSpace(m.notice) == "" {
		return ""
	}
	return roomsNoticeStyle.Render(m.notice)
}

// noticeLineCount is what the transcript layout reserves for the notice.
func (m roomsModel) noticeLineCount() int {
	if strings.TrimSpace(m.notice) == "" {
		return 0
	}
	return len(strings.Split(m.notice, "\n")) + 1
}

// ── transcript commands ──────────────────────────────────────────────────────

// handleRoomUXCommand runs the transcript commands added for the rooms v2
// work. It reports whether it handled the input, so rooms.go can keep its own
// commands (and its "not a room command" refusal) untouched.
func (m roomsModel) handleRoomUXCommand(text string) (roomsModel, tea.Cmd, bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return m, nil, false
	}
	name := strings.ToLower(fields[0])
	args := fields[1:]

	switch name {
	case "/mode", "/routing":
		return m.cmdRouting(args)
	case "/broadcast":
		return m.setRoutingMode(rooms.RouteBroadcast, "")
	case "/moderator", "/mod":
		return m.cmdModerator(args)
	case "/round-robin", "/roundrobin", "/rr":
		return m.setRoutingMode(rooms.RouteRoundRobin, "")
	case "/export":
		return m.cmdExport(args)
	case "/model", "/models":
		return m.cmdModel(args)
	case "/compact":
		return m.cmdCompact(args)
	case "/restart", "/re":
		return m.cmdRestart(args)
	case "/find":
		return m.cmdFind(args)
	case "/cost", "/usage":
		return m.cmdCost(args)
	case "/header", "/colour", "/color":
		return m.cmdHeader(args)
	}
	return m, nil, false
}

func (m roomsModel) cmdRouting(args []string) (roomsModel, tea.Cmd, bool) {
	m.composer = ""
	m.err = nil
	if len(args) == 0 {
		m.notice = fmt.Sprintf("routing: %s\nrooms, in order:\n- /mode broadcast — every member answers (default)\n- /mode moderator [@handle] — one member answers\n- /mode round-robin — members take turns\nan explicit @handle in a message always wins over the mode",
			m.routingNote())
		return m, nil, true
	}
	mode, err := rooms.ParseRoutingMode(args[0])
	if err != nil {
		m.err = err
		return m, nil, true
	}
	if len(args) > 1 {
		m.composer = ""
		return m.setRoutingMode(mode, strings.TrimPrefix(args[1], "@"))
	}
	return m.setRoutingMode(mode, "")
}

func (m roomsModel) cmdModerator(args []string) (roomsModel, tea.Cmd, bool) {
	m.composer = ""
	m.err = nil
	if len(args) == 0 {
		handle := m.roomPrefs().Moderator
		if handle == "" {
			m.notice = "no moderator set — un-addressed messages go to the first member.\nset one with /moderator @handle"
			return m, nil, true
		}
		m.notice = "moderator: @" + handle
		return m, nil, true
	}
	return m.setRoutingMode(rooms.RouteModerator, strings.TrimPrefix(args[0], "@"))
}

// setRoutingMode stores the mode (and the moderator, when given) and reports
// it, so the user sees the room's routing without opening a config file.
func (m roomsModel) setRoutingMode(mode rooms.RoutingMode, moderator string) (roomsModel, tea.Cmd, bool) {
	m.composer = ""
	m.err = nil
	cmd := m.updateRoomPrefs(func(p *rooms.RoomPrefs) {
		p.Mode = string(mode)
		if moderator != "" {
			p.Moderator = moderator
		}
	})
	m.status = "routing: " + m.routingNote()
	return m, cmd, true
}

func (m roomsModel) cmdExport(args []string) (roomsModel, tea.Cmd, bool) {
	m.composer = ""
	m.err = nil
	format := ""
	if len(args) > 0 {
		format = strings.ToLower(args[0])
	}
	parsed, err := rooms.ParseExportFormat(format)
	if err != nil {
		m.err = err
		return m, nil, true
	}
	roomID, events := m.openRoomID, m.page.Events
	if roomID == "" || len(events) == 0 {
		m.err = fmt.Errorf("nothing to export yet — the room has no events loaded")
		return m, nil, true
	}
	opts := rooms.ExportOptions{RoomID: roomID, Members: m.roster(), Generated: time.Now()}
	if m.openRoom != nil {
		opts.RoomName = m.openRoom.Name
	}
	m.status = "exporting…"
	return m, func() tea.Msg {
		paths, err := rooms.WriteExport(roomID, events, opts, parsed)
		if err != nil {
			return roomsSentMsg{err: err}
		}
		return roomsExportedMsg{paths: paths}
	}, true
}

func (m roomsModel) cmdCompact(args []string) (roomsModel, tea.Cmd, bool) {
	m.composer = ""
	m.err = nil

	local := false
	keepLast := 0
	for _, a := range args {
		if strings.EqualFold(a, "local") {
			local = true
			continue
		}
		n, err := strconv.Atoi(a)
		if err != nil {
			m.err = fmt.Errorf("unknown /compact argument %q — use /compact [N] or /compact local [N]", a)
			return m, nil, true
		}
		// A typed window is validated, not clamped: compacting with a
		// different N than asked would silently drop history the user wanted
		// kept.
		if _, err := rooms.ValidateKeepWindow(n); err != nil {
			m.err = err
			return m, nil, true
		}
		keepLast = n
	}
	if keepLast == 0 {
		keepLast = m.roomPrefs().KeepLast()
	}
	keepLast = rooms.NormalizeKeepLast(keepLast)

	older, _, through := rooms.CompactSplit(m.compactedEvents(), keepLast)
	if len(older) == 0 {
		m.status = fmt.Sprintf("nothing to compact — the room holds fewer than %d messages", keepLast)
		return m, nil, true
	}

	if local {
		summary := rooms.LocalSummary(older, 8)
		c := rooms.NewCompaction(through, 0, summary, "local", keepLast, time.Now())
		cmd := m.updateRoomPrefs(func(p *rooms.RoomPrefs) {
			p.Compaction = c
			p.CompactKeep = keepLast
		})
		m.scroll = 0
		m.status = fmt.Sprintf("compacted %d event(s) locally — %d message(s) stay verbatim", through, keepLast)
		return m, cmd, true
	}

	prompt := rooms.CompactPrompt(older, keepLast)
	if prompt == "" {
		m.status = "nothing to compact"
		return m, nil, true
	}
	m.pendingCompact = &pendingCompact{throughSeq: through, keepLast: keepLast}
	m.working = true
	m.lastProgress = time.Now()
	m.status = "asked the room for a brief…"
	return m, tea.Batch(m.sendText(prompt), m.ensureRoomsSpinner()), true
}

// applyBrief stores the roster's brief once it lands. The brief is the first
// member message posted after the /compact request.
func (m *roomsModel) applyBrief() tea.Cmd {
	if m.pendingCompact == nil {
		return nil
	}
	for _, ev := range m.page.Events {
		if ev.Seq <= m.pendingCompact.promptSeq || ev.Kind != "message.member" {
			continue
		}
		if strings.TrimSpace(ev.Text()) == "" {
			continue
		}
		pc := m.pendingCompact
		m.pendingCompact = nil
		c := rooms.NewCompaction(pc.throughSeq, pc.promptSeq, ev.Text(), "roster", pc.keepLast, time.Now())
		m.scroll = 0
		m.status = fmt.Sprintf("compacted — brief from %s covers seq 1-%d, %d message(s) stay verbatim",
			ev.Speaker(), pc.throughSeq, pc.keepLast)
		return m.updateRoomPrefs(func(p *rooms.RoomPrefs) {
			p.Compaction = c
			p.CompactKeep = pc.keepLast
		})
	}
	return nil
}

func (m roomsModel) cmdFind(args []string) (roomsModel, tea.Cmd, bool) {
	m.composer = ""
	m.err = nil
	query := strings.Join(args, " ")
	if strings.TrimSpace(query) == "" {
		m.findQuery = ""
		m.notice = ""
		m.status = "search cleared"
		return m, nil, true
	}
	events := m.compactedEvents()
	matches := rooms.Find(events, query)
	m.findQuery = query
	if len(matches) == 0 {
		m.notice = fmt.Sprintf("no matches for %q in %d event(s)", query, len(events))
		m.status = ""
		return m, nil, true
	}
	lines := []string{fmt.Sprintf("%d match(es) for %q — matching lines are highlighted:", len(matches), query)}
	const maxShown = 20
	for i, match := range matches {
		if i >= maxShown {
			lines = append(lines, fmt.Sprintf("… %d more", len(matches)-maxShown))
			break
		}
		lines = append(lines, rooms.MatchLine(match))
	}
	m.notice = strings.Join(lines, "\n")
	m.scroll = 0
	return m, nil, true
}

func (m roomsModel) cmdCost(args []string) (roomsModel, tea.Cmd, bool) {
	m.composer = ""
	m.err = nil
	rows := rooms.UsageFor(m.compactedEvents(), m.roster())
	if len(rows) == 0 {
		m.notice = "no usage yet — the gateway reports tokens per member turn once a turn completes"
		return m, nil, true
	}
	// The model belongs in a cost table: the same tokens cost different money
	// on different models, and this is where the user is already comparing.
	for i := range rows {
		rows[i].Model = m.memberModel(rows[i].Handle)
	}
	table := rooms.FormatUsage(rows)
	if !anyCostReported(rows) {
		table += "\n(this gateway reports token counts but no cost — no price table is configured)"
	}
	m.notice = table
	return m, nil, true
}

func anyCostReported(rows []rooms.MemberUsage) bool {
	for _, r := range rows {
		if r.HasCost {
			return true
		}
	}
	return false
}

func (m roomsModel) cmdHeader(args []string) (roomsModel, tea.Cmd, bool) {
	m.composer = ""
	m.err = nil
	prefs := m.roomPrefs()
	if len(args) == 0 {
		lines := make([]string, 0, len(m.roster())+1)
		lines = append(lines, "header colours — /header @handle #RRGGBB, /header @handle default")
		handles := make([]string, 0, len(m.roster()))
		for _, member := range m.roster() {
			handles = append(handles, member.Handle)
		}
		sort.Strings(handles)
		for _, h := range handles {
			colour := rooms.ColorFor(h, prefs.Colours)
			set := ""
			if _, ok := prefs.Colours[h]; ok {
				set = " (set)"
			}
			lines = append(lines, fmt.Sprintf("  @%-20s %s%s", h, colour, set))
		}
		m.notice = strings.Join(lines, "\n")
		return m, nil, true
	}
	if len(args) < 2 {
		m.err = fmt.Errorf("/header needs a handle and a colour — /header @handle #RRGGBB, or /header @handle default")
		return m, nil, true
	}
	handle := strings.ToLower(strings.TrimPrefix(args[0], "@"))
	switch strings.ToLower(args[1]) {
	case "default", "clear", "off":
		cmd := m.updateRoomPrefs(func(p *rooms.RoomPrefs) {
			delete(p.Colours, handle)
		})
		m.status = fmt.Sprintf("@%s back to the default colour (%s)", handle, rooms.ColorFor(handle, nil))
		return m, cmd, true
	}
	hex, err := rooms.NormalizeHex(args[1])
	if err != nil {
		m.err = err
		return m, nil, true
	}
	cmd := m.updateRoomPrefs(func(p *rooms.RoomPrefs) {
		if p.Colours == nil {
			p.Colours = map[string]string{}
		}
		p.Colours[handle] = hex
	})
	m.status = fmt.Sprintf("@%s header colour set to %s", handle, hex)
	return m, cmd, true
}

// ── sent/exported messages ───────────────────────────────────────────────────

// roomsExportedMsg carries the files /export wrote.
type roomsExportedMsg struct {
	paths []string
	err   error
}

// ── rendering helpers ────────────────────────────────────────────────────────

var (
	// roomsNoticeStyle is the multi-line block above the composer: usage,
	// search hits, help.
	roomsNoticeStyle = lipgloss.NewStyle().Foreground(subtle)
	// roomsMatchStyle marks the live /find query inside a transcript row.
	roomsMatchStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#1A0033")).
			Background(lipgloss.Color("#FFD166"))
	// roomsBriefStyle renders a compaction's brief.
	roomsBriefStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E0D7FF")).
			Italic(true)
)

// eventHandle is the handle a transcript row's colour is keyed by.
func eventHandle(ev rooms.Event) string {
	if ev.Actor.Profile != "" {
		return rooms.HandleFor(ev.Actor.Profile)
	}
	return rooms.HandleFor(ev.Speaker())
}

// formatEvent renders one transcript row with the member's colour applied and
// the live /find query highlighted inside the body text. Highlighting is done
// on the raw text, before styling, so a query can never match an ANSI escape.
func (m roomsModel) formatEvent(ev rooms.Event) string {
	body := ev.Text()
	if m.findQuery != "" && body != "" {
		body = highlightQuery(body, m.findQuery, roomsMatchStyle)
	}
	return formatRoomsEventStyled(ev, body, m.memberStyle(eventHandle(ev)))
}

// routeOutgoing applies the room's routing mode to a message the user typed,
// returning the text that should reach the gateway (a mode that picks one
// member prepends that member's mention, because mention routing is
// server-side), the note to show the user, and a cmd that persists an advanced
// round-robin cursor.
//
// The note is returned rather than assigned to m.status: this is a value
// receiver, so writing to the copy would throw the explanation away and the
// user would never learn where their message went.
func (m roomsModel) routeOutgoing(text string) (routed, note string, cmd tea.Cmd) {
	prefs := m.roomPrefs()
	decision, next := rooms.Route(text, m.roster(), prefs.RoutingMode(), prefs.Moderator, prefs.RoundRobinIndex)
	if prefs.RoutingMode() == rooms.RouteRoundRobin && next != prefs.RoundRobinIndex {
		return decision.Text, decision.Note, m.updateRoomPrefs(func(p *rooms.RoomPrefs) { p.RoundRobinIndex = next })
	}
	return decision.Text, decision.Note, nil
}
