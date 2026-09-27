package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/config"
	"github.com/lucinate-ai/lucinate/internal/rooms"
)

// The rooms view is lucinate's menu over Hermes hosted rooms ("Bot Mode"
// group chats). It is a three-screen flow:
//
//	browse     — predefined rooms + live rooms; pick one, or start a new one
//	invite     — the invite list: toggle local Hermes profiles (2-6) to seat a room
//	transcript — the room log, with a one-line composer; the roster answers
//
// The roster is frozen when the room is created (the gateway refuses a
// re-create with different members), so "invite" always means "seat this
// roster in a new room". That constraint is stated on screen rather than
// hidden, because the alternative — silently forking a room — would lose
// the transcript the user was reading.

type roomsSubState int

const (
	roomsBrowse roomsSubState = iota
	roomsInvite
	roomsTranscript
)

const roomsPollInterval = 1500 * time.Millisecond

// roomsVisibleRows caps how many list rows a rooms screen renders. The
// invite list is every local Hermes profile (dozens on a real host), and
// without a window the summary and the key hints get pushed off-screen —
// the menu then shows nothing about what is selected.
const roomsVisibleRows = 14

// windowRows returns the [start, end) slice of total rows to render so
// that cursor stays visible inside a window of at most size rows. It
// reports how many rows were hidden above and below so the caller can
// say so instead of silently truncating.
func windowRows(total, cursor, size int) (start, end, hiddenAbove, hiddenBelow int) {
	if total <= size {
		return 0, total, 0, 0
	}
	if size < 1 {
		size = 1
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > total-1 {
		cursor = total - 1
	}
	start = cursor - size/2
	if start < 0 {
		start = 0
	}
	if start > total-size {
		start = total - size
	}
	end = start + size
	return start, end, start, total - end
}

// ── messages ─────────────────────────────────────────────────────────────────

type roomsLoadedMsg struct {
	presets rooms.PresetStore
	live    []rooms.Room
	err     error
}

type roomsProfilesMsg struct {
	profiles []string
	err      error
}

type roomsCreatedMsg struct {
	roomID string
	room   rooms.Room
	err    error
}

type roomsLogMsg struct {
	page rooms.LogPage
	err  error
	// client is the connection the page came from, so the error path can
	// tell "the socket died" (redial) from "the gateway refused" (report).
	client *rooms.Client
}

type roomsSentMsg struct {
	err error
	// seq and text describe what actually reached the gateway: seq lets a
	// pending /compact learn where its request landed, text is the routed
	// form (a mode that picks a member prepends that member's mention).
	seq  int
	text string
}

// roomsStateMsg carries the driver status the composer needs to know
// whether the roster is still answering.
type roomsStateMsg struct {
	working bool
	err     error
}

type roomsTickMsg struct{}

// ── model ────────────────────────────────────────────────────────────────────

type roomsModel struct {
	conn      *config.Connection
	token     string
	hideHints bool

	width, height int
	sub           roomsSubState

	// shared holds the gateway connection across model copies. roomsModel is
	// a value type that Update returns by value, so a plain `client` field
	// written from a method lands in a temporary and is thrown away. That
	// is exactly what made dial() open — and leak — a new socket on every
	// poll: two per tick, thousands an hour, until Windows ran out of socket
	// buffers (WSAENOBUFS) and room members could no longer reach the model
	// APIs, which the user saw as "the roster stopped answering".
	shared *clientHolder

	presets  rooms.PresetStore
	live     []rooms.Room
	profiles []string
	page     rooms.LogPage

	cursor       int
	inviteCursor int
	selected     map[string]bool

	openRoomID string
	openRoom   *rooms.Room
	composer   string
	since      int

	// working mirrors the room driver: while a turn is in flight, sending
	// another message CANCELS it (the gateway marks the older event
	// `superseded_by_newer_user_event`). Messages typed meanwhile are held
	// in queue and sent when the roster settles, matching what the chat
	// view does for the same reason.
	working bool
	queue   []string

	// scroll is how many lines back from the live tail the transcript
	// window sits; 0 is pinned to the newest line. Without it the window
	// only ever showed the tail and history was unreachable.
	scroll int

	// lastProgress is when the room transcript last grew. It bounds the
	// hold: a driver can claim to be working while its task is pinned, and
	// the transcript then stops moving — holding a message behind that is
	// the same silence as no reply.
	lastProgress time.Time

	// prefs is the room's local settings — routing mode, per-member header
	// colours, the compaction window and the live compaction. They are
	// loaded with the view and persisted on the first change, so a mode set
	// once survives a restart.
	prefs rooms.PrefsStore

	// findQuery is the live /find query: matched lines are highlighted in
	// the transcript and listed in the notice.
	findQuery string

	// notice is a multi-line block above the composer: the /cost table,
	// /find hits, /help. It never becomes part of the transcript, and esc
	// clears it.
	notice string

	// spinnerFrame animates the braille glyph on the in-flight reply rows;
	// spinnerTicking keeps exactly one tick scheduled, the same discipline
	// the chat view uses.
	spinnerFrame   int
	spinnerTicking bool

	// backoff is the redial schedule for a dropped gateway socket, and
	// reconnecting says a redial is in flight. attempt is stamped onto the
	// scheduled tick so a stale one cannot redial twice.
	backoff          rooms.Backoff
	reconnecting     bool
	reconnectAttempt int

	// pendingCompact is a /compact request waiting for the roster's brief.
	pendingCompact *pendingCompact

	confirmDisband string
	err            error
	status         string
	loaded         bool
}

func newRoomsModel(conn *config.Connection, hideHints bool) roomsModel {
	m := roomsModel{
		conn:      conn,
		hideHints: hideHints,
		selected:  map[string]bool{},
		shared:    &clientHolder{},
	}
	if conn != nil {
		m.token = config.GetAPIKey(conn.ID)
	}
	return m
}

func (m *roomsModel) setSize(w, h int) {
	m.width, m.height = w, h
}

func (m roomsModel) Init() tea.Cmd {
	return tea.Batch(m.loadAll(), m.loadProfiles(), m.loadPrefs())
}

// clientHolder shares one gateway connection across model copies. Cmds run
// on their own goroutines, hence the mutex.
type clientHolder struct {
	mu     sync.Mutex
	client *rooms.Client
}

func (h *clientHolder) get() *rooms.Client {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.client
}

func (h *clientHolder) set(c *rooms.Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.client = c
}

// drop closes c if it is still the cached connection, so a failed call
// cannot pin a dead socket for the rest of the session.
func (h *clientHolder) drop(c *rooms.Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.client == c {
		_ = h.client.Close()
		h.client = nil
	}
}

// dial returns the view's one gateway connection, opening it on first use.
//
// Value receiver on purpose: every caller holds a model copy, and a pointer
// receiver here is what silently defeated the cache before. The connection
// lives in shared, which survives the copies.
func (m roomsModel) dial() (*rooms.Client, error) {
	if m.shared == nil {
		return nil, fmt.Errorf("no connection holder")
	}
	if c := m.shared.get(); c != nil {
		return c, nil
	}
	if m.conn == nil {
		return nil, fmt.Errorf("no active connection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := roomsDialer(ctx, m.conn.URL, m.token)
	if err != nil {
		return nil, err
	}
	m.shared.set(c)
	return c, nil
}

func (m roomsModel) Close() {
	if m.shared == nil {
		return
	}
	if c := m.shared.get(); c != nil {
		m.shared.drop(c)
	}
}

// ── commands ─────────────────────────────────────────────────────────────────

func (m roomsModel) loadAll() tea.Cmd {
	conn, token := m.conn, m.token
	return func() tea.Msg {
		path, err := rooms.DefaultPresetsPath()
		if err != nil {
			return roomsLoadedMsg{err: err}
		}
		presets, _ := rooms.LoadPresets(path)
		if conn == nil {
			return roomsLoadedMsg{presets: presets, err: fmt.Errorf("no active connection")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		c, err := rooms.DialBaseURL(ctx, conn.URL, token)
		if err != nil {
			return roomsLoadedMsg{presets: presets, err: err}
		}
		defer c.Close()
		live, err := c.List(ctx, false)
		return roomsLoadedMsg{presets: presets, live: live, err: err}
	}
}

func (m roomsModel) loadProfiles() tea.Cmd {
	return func() tea.Msg {
		profiles, err := rooms.DiscoverLocalProfiles()
		return roomsProfilesMsg{profiles: profiles, err: err}
	}
}

func (m roomsModel) createRoom() tea.Cmd {
	profiles := m.selectedProfiles()
	members := rooms.RosterFor(profiles)
	name := rooms.DisplayNameFor(profiles)
	conn, token := m.conn, m.token
	return func() tea.Msg {
		if err := rooms.ValidateRoster(members); err != nil {
			return roomsCreatedMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c, err := rooms.DialBaseURL(ctx, conn.URL, token)
		if err != nil {
			return roomsCreatedMsg{err: err}
		}
		defer c.Close()
		room, err := c.Create(ctx, rooms.Slug(name), name, members)
		if err != nil {
			return roomsCreatedMsg{err: err}
		}
		return roomsCreatedMsg{roomID: room.RoomID, room: room}
	}
}

func (m roomsModel) createFromPreset(p rooms.Preset) tea.Cmd {
	conn, token := m.conn, m.token
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c, err := rooms.DialBaseURL(ctx, conn.URL, token)
		if err != nil {
			return roomsCreatedMsg{err: err}
		}
		defer c.Close()
		room, err := c.Create(ctx, p.RoomID, p.Name, p.Members)
		if err != nil {
			return roomsCreatedMsg{err: err}
		}
		return roomsCreatedMsg{roomID: room.RoomID, room: room}
	}
}

func (m roomsModel) loadLog(roomID string, since int) tea.Cmd {
	return func() tea.Msg {
		c, err := m.dial()
		if err != nil {
			return roomsLogMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		page, err := c.Log(ctx, roomID, since, rooms.DefaultLogLimit)
		if err != nil {
			// A transport failure keeps the client in place: the message
			// handler drops it and arms the redial, so the reconnect
			// schedule is decided in one place.
			if disconnected(c, err) {
				return roomsLogMsg{err: err, client: c}
			}
			m.shared.drop(c)
		}
		return roomsLogMsg{page: page, err: err, client: c}
	}
}

func (m roomsModel) send() tea.Cmd {
	return m.sendText(strings.TrimSpace(m.composer))
}

// sendText posts a message to the open room. It is separate from send() so
// a queued message can be dispatched without touching the composer.
func (m roomsModel) sendText(text string) tea.Cmd {
	roomID := m.openRoomID
	if roomID == "" || text == "" {
		return nil
	}
	orient := m.orientationFor(roomID)
	return func() tea.Msg {
		c, err := m.dial()
		if err != nil {
			return roomsSentMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		event, err := c.SendOpening(ctx, roomID, text, rooms.DefaultThreadID, orient)
		if err != nil {
			m.shared.drop(c)
		}
		return roomsSentMsg{err: err, seq: event.Seq, text: text}
	}
}

// loadState asks the driver whether the roster is still answering. The
// composer needs this before it sends: a second user event cancels the
// first one's in-flight turns.
func (m roomsModel) loadState() tea.Cmd {
	roomID := m.openRoomID
	if roomID == "" {
		return nil
	}
	return func() tea.Msg {
		c, err := m.dial()
		if err != nil {
			return roomsStateMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		st, err := c.State(ctx, roomID)
		if err != nil {
			m.shared.drop(c)
			return roomsStateMsg{err: err}
		}
		working := st.DriverStatus != nil && st.DriverStatus.Working
		return roomsStateMsg{working: working}
	}
}

// orientationFor resolves the project context a room's first message
// carries: the predefined room's worktree when the room was seated from
// one, else the Orca terminal this TUI runs in.
func (m roomsModel) orientationFor(roomID string) rooms.Orientation {
	if path, err := rooms.DefaultPresetsPath(); err == nil {
		if store, err := rooms.LoadPresets(path); err == nil {
			if p := store.Find(roomID); p != nil && strings.TrimSpace(p.Worktree) != "" {
				return rooms.OrientationFromWorktree(p.Worktree)
			}
		}
	}
	return rooms.OrientationFromEnv()
}

func (m roomsModel) disband(roomID string) tea.Cmd {
	return func() tea.Msg {
		c, err := m.dial()
		if err != nil {
			return roomsSentMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = c.Disband(ctx, roomID)
		if err != nil {
			m.shared.drop(c)
		}
		return roomsSentMsg{err: err}
	}
}

func roomsTick() tea.Cmd {
	return tea.Tick(roomsPollInterval, func(time.Time) tea.Msg { return roomsTickMsg{} })
}

// ── selection helpers (pure; unit-tested) ────────────────────────────────────

func (m roomsModel) selectedProfiles() []string {
	out := make([]string, 0, len(m.selected))
	for p, on := range m.selected {
		if on {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// browseRows is the flat list the cursor walks: predefined rooms first,
// then the live rooms on the gateway.
func (m roomsModel) browseRows() []browseRow {
	rows := make([]browseRow, 0, len(m.presets.Presets)+len(m.live))
	for i := range m.presets.Presets {
		rows = append(rows, browseRow{kind: rowPreset, preset: &m.presets.Presets[i]})
	}
	for i := range m.live {
		rows = append(rows, browseRow{kind: rowLive, room: &m.live[i]})
	}
	return rows
}

type browseRowKind int

const (
	rowPreset browseRowKind = iota
	rowLive
)

type browseRow struct {
	kind   browseRowKind
	preset *rooms.Preset
	room   *rooms.Room
}

// toggle flips a profile in the invite list, refusing to exceed the
// gateway's roster ceiling rather than letting the create call fail.
func (m *roomsModel) toggle(profile string) string {
	if m.selected[profile] {
		delete(m.selected, profile)
		return ""
	}
	if len(m.selectedProfiles()) >= rooms.MaxMembers {
		return fmt.Sprintf("a room seats at most %d profiles — deselect one first", rooms.MaxMembers)
	}
	m.selected[profile] = true
	return ""
}

// ── update ───────────────────────────────────────────────────────────────────

func (m roomsModel) Update(msg tea.Msg) (roomsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case roomsLoadedMsg:
		m.presets, m.live, m.err, m.loaded = msg.presets, msg.live, msg.err, true
		if m.cursor >= len(m.browseRows()) && m.cursor > 0 {
			m.cursor = len(m.browseRows()) - 1
		}
		return m, nil

	case roomsProfilesMsg:
		if msg.err == nil {
			m.profiles = msg.profiles
		}
		return m, nil

	case roomsCreatedMsg:
		if msg.err != nil {
			m.err = msg.err
			m.sub = roomsBrowse
			return m, nil
		}
		m.status = fmt.Sprintf("room %s ready — %s", msg.roomID, rosterLabel(msg.room))
		m.sub = roomsBrowse
		m.openRoomID = msg.roomID
		m.openRoom = &msg.room
		m.page = rooms.LogPage{}
		m.since = 0
		return m, tea.Batch(m.loadAll(), m.loadLog(msg.roomID, 0))

	case roomsLogMsg:
		if msg.err != nil {
			// A dropped socket is recoverable: the transcript already on
			// screen stays valid, so it must not read as a room error.
			if disconnected(msg.client, msg.err) {
				return m, m.scheduleReconnect(msg.err.Error())
			}
			m.err = msg.err
			return m, nil
		}
		m.reconnecting = false
		if len(msg.page.Events) > 0 {
			if m.scroll > 0 {
				// The user is reading history. New arrivals must not push
				// the text they are looking at down the screen, so advance
				// the offset by exactly what was appended.
				for _, ev := range msg.page.Events {
					m.scroll += len(strings.Split(formatRoomsEvent(ev), "\n"))
				}
			}
			m.page.Events = append(m.page.Events, msg.page.Events...)
			m.since = msg.page.Cursor
		}
		// Progress = the room's newest seq moved. Polling returns the same
		// events again when the cursor does not advance, so counting any
		// payload as progress would keep the hold alive forever.
		if msg.page.LatestSeq > m.page.LatestSeq {
			m.lastProgress = time.Now()
		}
		m.page.LatestSeq = msg.page.LatestSeq
		return m, tea.Batch(m.ensureRoomsSpinner(), m.applyBrief())

	case roomsSentMsg:
		if msg.err != nil {
			if rooms.IsDisconnect(msg.err) {
				return m, m.scheduleReconnect(msg.err.Error())
			}
			m.err = msg.err
			return m, nil
		}
		m.status = "sent — the roster is answering"
		// Optimistic: the driver is now busy, and the state poll is up to
		// 1.5s away. Without this a message typed immediately after would
		// still be sent, cancelling the turn set that just started.
		m.working = true
		// A pending /compact learns where its request landed, so the brief
		// is the first member reply posted after it.
		if m.pendingCompact != nil && msg.seq > 0 {
			m.pendingCompact.promptSeq = msg.seq
		}
		if m.openRoomID != "" {
			return m, tea.Batch(m.loadLog(m.openRoomID, m.since), m.ensureRoomsSpinner())
		}
		return m, nil

	case roomsTickMsg:
		if m.sub == roomsTranscript && m.openRoomID != "" {
			return m, tea.Batch(m.loadLog(m.openRoomID, m.since), m.loadState(), roomsTick(), m.ensureRoomsSpinner())
		}
		return m, nil

	case roomsSpinnerTickMsg:
		return m.handleSpinnerTick()

	case roomsReconnectMsg:
		return m.handleReconnect(msg.attempt)

	case roomsPrefsMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.prefs = msg.store
		return m, nil

	case roomsExportedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.status = "exported: " + strings.Join(msg.paths, ", ")
		m.notice = "transcript exported:\n" + strings.Join(msg.paths, "\n")
		return m, nil

	case roomsStateMsg:
		if msg.err != nil {
			return m, nil
		}
		m.working = msg.working
		// Keep the queue's status honest: the user is staring at "queued"
		// and cannot tell a thinking roster from a pinned one. Show the
		// elapsed silence and the deadline, so it reads as a countdown
		// rather than a hang.
		if len(m.queue) > 0 && m.working && !m.logIdle() {
			elapsed := time.Since(m.lastProgress).Round(time.Second)
			left := (staleWorkingAfter - time.Since(m.lastProgress)).Round(time.Second)
			if left < 0 {
				left = 0
			}
			m.status = fmt.Sprintf(
				"queued %d — roster quiet for %s, sends in %s if it stays silent (sending now would cancel its work)",
				len(m.queue), elapsed, left)
		}
		// Drain one queued message per settle, so a burst typed while the
		// roster was busy does not itself supersede the next round.
		if !m.working && len(m.queue) > 0 {
			text := m.queue[0]
			m.queue = m.queue[1:]
			m.status = "sending queued message"
			m.working = true
			return m, tea.Batch(m.sendText(text), m.loadState())
		}
		return m, nil

	case tea.PasteMsg:
		// Bracketed paste. Only the transcript has a composer; anywhere
		// else there is nothing to paste into, and silently dropping it
		// is correct.
		if m.sub != roomsTranscript {
			return m, nil
		}
		text := sanitizePaste(msg.Content)
		if text == "" {
			return m, nil
		}
		m.composer += text
		return m, nil

	case tea.MouseWheelMsg:
		if m.sub != roomsTranscript {
			return m, nil
		}
		switch msg.Button {
		case tea.MouseWheelUp:
			return m.scrollTranscript(3)
		case tea.MouseWheelDown:
			return m.scrollTranscript(-3)
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// sanitizePaste normalises clipboard text. CRLF and lone CR become LF so a
// paste from a Windows app does not arrive as stray carriage returns, and
// control characters are dropped so they cannot corrupt the rendered box.
func sanitizePaste(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n', r >= 0x20, r == '\t':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// transcriptLines flattens the room history into the exact lines the view
// renders, so scrolling and rendering agree on what a "line" is.
//
// Long messages are wrapped HERE, to the pane width. A line left to wrap on
// its own occupies two terminal rows while the view counted one, so the
// next frame overwrites the wrapped remainder and the whole layout shifts —
// which is what a cursor that "jumps on its own" actually was.
func (m roomsModel) transcriptLines() []string {
	width := m.width - 1
	if width < 20 {
		width = 20
	}
	var lines []string
	// A compaction replaces the part of the transcript it covers, so the
	// brief is the first thing the reader sees — right where the summarised
	// messages used to be.
	if c := m.roomPrefs().Compaction; c != nil && strings.TrimSpace(c.Summary) != "" {
		lines = append(lines, compactionRule(c))
		for _, l := range strings.Split(c.Summary, "\n") {
			lines = append(lines, roomsBriefStyle.Render("  "+strings.TrimSpace(l)))
		}
		lines = append(lines, "")
	}
	for _, ev := range m.compactedEvents() {
		for _, l := range strings.Split(m.formatEvent(ev), "\n") {
			// Word-wrap for prose, then hard-wrap whatever is still too
			// wide (a long path or URL has no breakpoint to use).
			for _, wrapped := range strings.Split(ansi.Wordwrap(l, width, " "), "\n") {
				lines = append(lines, strings.Split(ansi.Hardwrap(wrapped, width, false), "\n")...)
			}
		}
	}
	return lines
}

// scrollTranscript moves the window: positive goes back into history,
// negative returns toward the live tail, 0 is pinned to the newest line.
func (m roomsModel) scrollTranscript(delta int) (roomsModel, tea.Cmd) {
	m.scroll += delta
	if m.scroll < 0 {
		m.scroll = 0
	}
	if total := len(m.transcriptLines()); m.scroll > total {
		m.scroll = total
	}
	return m, nil
}

// composerText is what the composer box displays: the raw text for a
// single line, or the tail plus a size summary for a pasted document.
// Cursor() measures this same string, so the caret cannot drift from what
// is drawn.
func composerText(composer string) string {
	if !strings.Contains(composer, "\n") {
		return composer
	}
	lines := strings.Split(composer, "\n")
	tail := []rune(lines[len(lines)-1])
	const maxTail = 48
	if len(tail) > maxTail {
		tail = append([]rune("…"), tail[len(tail)-maxTail:]...)
	}
	return fmt.Sprintf("%s [%d lines · %d chars]", string(tail), len(lines), len([]rune(composer)))
}

// composerView renders the composer inside its box. A pasted document
// would otherwise stretch the border down the screen, so a multi-line
// composer shows the tail of the text plus a size summary: the box keeps
// its shape and the summary is the confirmation that the paste landed.
func composerView(composer string) string {
	return inputBorderStyle.Render("you › " + composerText(composer))
}

// Cursor places the terminal's cursor inside the composer.
//
// The rooms composer is a plain string, so nothing ever positioned the
// cursor: the terminal painted its own wherever the last write ended —
// outside the box, below it. The row is found in the rendered view rather
// than recomputed, so the caret cannot drift when the layout changes.
func (m roomsModel) Cursor() *tea.Cursor {
	if m.sub != roomsTranscript {
		return nil
	}
	lines := strings.Split(m.View(), "\n")
	row := -1
	for i := len(lines) - 1; i >= 0; i-- {
		// The composer is the last rounded block in the view.
		if strings.HasPrefix(ansi.Strip(lines[i]), "╭") {
			row = i
			break
		}
	}
	if row < 0 || row+1 >= len(lines) {
		return nil
	}
	// Inside the box: its border and padding, the prompt, then the text
	// that is actually drawn.
	col := 2 + lipgloss.Width("you › ") + lipgloss.Width(composerText(m.composer))
	return tea.NewCursor(col, row+1)
}

// deleteLastWord removes the trailing word and the spaces before it, so
// ctrl+w in the composer behaves like it does in a shell.
func deleteLastWord(s string) string {
	trimmed := strings.TrimRight(s, " \t")
	if i := strings.LastIndexAny(trimmed, " \t"); i >= 0 {
		return trimmed[:i+1]
	}
	return ""
}

// mouseStatusText explains the capture mode from the rooms transcript. The
// chat has its own /mouse feedback row, but that row is off-screen while the
// rooms view is up, so the transcript reports it here instead.
func mouseStatusText(on bool) string {
	if on {
		return "mouse capture on — the wheel scrolls and drag selects in the chat; /mouse off gives click-drag back to the terminal"
	}
	return "mouse capture off — the terminal's own click-drag selection works now; /mouse on restores wheel scrolling"
}

func (m roomsModel) handleKey(msg tea.KeyPressMsg) (roomsModel, tea.Cmd) {
	key := msg.String()

	// A disband confirm swallows the next key: y commits, anything else cancels.
	if m.confirmDisband != "" {
		target := m.confirmDisband
		m.confirmDisband = ""
		if key == "y" || key == "Y" {
			m.status = "disbanding " + target
			return m, m.disband(target)
		}
		return m, nil
	}

	switch m.sub {
	case roomsInvite:
		return m.handleInviteKey(key)
	case roomsTranscript:
		return m.handleTranscriptKey(msg, key)
	}
	return m.handleBrowseKey(key)
}

func (m roomsModel) handleBrowseKey(key string) (roomsModel, tea.Cmd) {
	rows := m.browseRows()
	switch key {
	case "esc", "q":
		return m, func() tea.Msg { return goBackFromRoomsMsg{} }
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(rows)-1 {
			m.cursor++
		}
	case "r":
		m.err = nil
		m.status = "refreshing…"
		return m, m.loadAll()
	case "n":
		m.sub = roomsInvite
		m.inviteCursor = 0
		m.err = nil
		m.status = ""
		// Always re-discover: a profile created since the view opened (or
		// since the last visit) must appear, and the scan is a local disk
		// read.
		return m, m.loadProfiles()
	case "enter":
		if m.cursor < 0 || m.cursor >= len(rows) {
			return m, nil
		}
		row := rows[m.cursor]
		if row.kind == rowPreset {
			m.status = "starting " + row.preset.Name + "…"
			return m, m.createFromPreset(*row.preset)
		}
		m.sub = roomsTranscript
		m.openRoomID = row.room.RoomID
		m.openRoom = row.room
		m.page = rooms.LogPage{}
		m.since = 0
		m.err = nil
		return m, tea.Batch(m.loadLog(row.room.RoomID, 0), m.loadState(), roomsTick())
	case "x":
		if m.cursor >= 0 && m.cursor < len(rows) && rows[m.cursor].kind == rowLive {
			m.confirmDisband = rows[m.cursor].room.RoomID
		}
	case "p":
		if m.cursor >= 0 && m.cursor < len(rows) && rows[m.cursor].kind == rowLive {
			return m, m.savePreset(*rows[m.cursor].room)
		}
	}
	return m, nil
}

func (m roomsModel) handleInviteKey(key string) (roomsModel, tea.Cmd) {
	switch key {
	case "esc":
		m.sub = roomsBrowse
	case "up", "k":
		if m.inviteCursor > 0 {
			m.inviteCursor--
		}
	case "down", "j":
		if m.inviteCursor < len(m.profiles)-1 {
			m.inviteCursor++
		}
	case " ", "space":
		if m.inviteCursor >= 0 && m.inviteCursor < len(m.profiles) {
			if warn := m.toggle(m.profiles[m.inviteCursor]); warn != "" {
				m.status = warn
			} else {
				m.status = ""
			}
		}
	case "a":
		for _, p := range m.profiles {
			if len(m.selectedProfiles()) >= rooms.MaxMembers {
				break
			}
			m.selected[p] = true
		}
	case "c":
		m.selected = map[string]bool{}
		m.status = ""
	case "r":
		m.status = "refreshing profiles…"
		return m, m.loadProfiles()
	case "enter":
		members := rooms.RosterFor(m.selectedProfiles())
		if err := rooms.ValidateRoster(members); err != nil {
			m.err = err
			return m, nil
		}
		m.err = nil
		m.status = "creating room…"
		return m, m.createRoom()
	}
	return m, nil
}

func (m roomsModel) handleTranscriptKey(msg tea.KeyPressMsg, key string) (roomsModel, tea.Cmd) {
	switch key {
	case "esc":
		m.sub = roomsBrowse
		m.composer = ""
		m.status = ""
		m.notice = ""
		m.findQuery = ""
		return m, m.loadAll()
	case "enter":
		text := strings.TrimSpace(m.composer)
		if text == "" {
			return m, nil
		}
		// A leading backslash escapes a message that would otherwise be
		// read as a command.
		if strings.HasPrefix(text, "\\") {
			m.composer = ""
			return m.post(strings.TrimPrefix(text, "\\"))
		}
		if strings.HasPrefix(text, "/") {
			return m.handleTranscriptCommand(text)
		}
		m.composer = ""
		return m.post(text)
	case "up":
		return m.scrollTranscript(1)
	case "down":
		return m.scrollTranscript(-1)
	case "pgup":
		return m.scrollTranscript(m.transcriptRows())
	case "pgdown":
		return m.scrollTranscript(-m.transcriptRows())
	case "home":
		return m.scrollTranscript(len(m.transcriptLines()))
	case "end":
		m.scroll = 0
		return m, nil
	case "ctrl+u":
		// Kill the line. The composer has no cursor to move, so this is the
		// whole draft — the shell habit users reach for first.
		m.composer = ""
		return m, nil
	case "ctrl+w", "alt+backspace":
		m.composer = deleteLastWord(m.composer)
		return m, nil
	case "backspace":
		if len(m.composer) > 0 {
			m.composer = m.composer[:len(m.composer)-1]
		}
		return m, nil
	}
	if t := msg.Text; t != "" && !strings.HasPrefix(key, "ctrl+") && !strings.HasPrefix(key, "alt+") {
		m.composer += t
	}
	return m, nil
}

// logIdle reports whether the transcript's last event proves nothing is in
// flight.
//
// The driver's `working` flag sticks at true after a turn dies — the gateway
// keeps a stale `running` count — and a message held behind a flag that will
// never clear is the same silence as no reply at all. The transcript is the
// honest signal: a room mid-answer ends on the user's message or a member's
// reply, and once the set finishes it ends on a settle/activity event.
func (m roomsModel) logIdle() bool {
	evs := m.page.Events
	if len(evs) == 0 {
		return false // no evidence yet; trust the driver
	}
	switch evs[len(evs)-1].Kind {
	case "message.user", "message.member":
		return false
	}
	return true
}

// staleWorkingAfter bounds the hold. The transcript is the honest signal for
// whether a turn set is in flight, but it goes quiet forever when the driver
// pins a task — so a hold needs both a busy transcript AND recent movement.
const staleWorkingAfter = 75 * time.Second

// post sends a message, or holds it while the roster is still answering.
//
// Sending during a live turn set is not a queue on the gateway — it CANCELS
// the older turns (`superseded_by_newer_user_event`), which reads as a
// roster that suddenly stopped responding. So the message waits while the
// transcript shows a set in flight.
//
// Two signals, both required to hold: the transcript's last event says a set
// is in flight (logIdle), and that transcript is still MOVING. A pinned task
// leaves the first true and the second false, and a message held behind it
// would never go out at all.
func (m roomsModel) post(text string) (roomsModel, tea.Cmd) {
	if text == "" {
		return m, nil
	}
	// Route before the hold decision: the text that reaches the gateway (and
	// the round-robin cursor it advances) is what the queue has to carry, so
	// a drained message goes out the way it was addressed.
	routed, routeCmd := m.routeOutgoing(text)
	if m.working && !m.logIdle() && time.Since(m.lastProgress) < staleWorkingAfter {
		m.queue = append(m.queue, routed)
		m.status = fmt.Sprintf("queued — the roster is still answering (%d in queue)", len(m.queue))
		return m, routeCmd
	}
	if m.working && !m.logIdle() {
		m.status = fmt.Sprintf("roster quiet for %s — sending anyway",
			time.Since(m.lastProgress).Round(time.Second))
	}
	m.working = true
	m.lastProgress = time.Now()
	return m, tea.Batch(m.sendText(routed), routeCmd, m.ensureRoomsSpinner())
}

// handleTranscriptCommand runs a slash command typed in the transcript.
//
// Without this the composer posts it to the room as a message: typing
// /rooms to get back would land in the transcript as a user turn and set
// the whole roster answering — the exact misfire this prevents. An
// unrecognised command is refused rather than sent, so a typo costs a
// notice instead of a round of agent turns.
func (m roomsModel) handleTranscriptCommand(text string) (roomsModel, tea.Cmd) {
	fields := strings.Fields(text)
	name := fields[0]
	m.composer = ""
	m.err = nil
	switch name {
	case "/rooms", "/back", "/list":
		m.sub = roomsBrowse
		m.status = ""
		return m, m.loadAll()
	case "/mouse":
		arg := ""
		if len(fields) > 1 {
			arg = strings.ToLower(fields[1])
		}
		if arg == "" {
			arg = "status"
		}
		switch arg {
		case "status", "on", "off", "toggle":
		default:
			m.err = fmt.Errorf("unknown /mouse argument %q — use /mouse on, /mouse off or /mouse toggle", arg)
			return m, nil
		}
		// The capture flag lives on AppModel, so this only raises the msg;
		// the app applies it and writes the rooms status line.
		return m, func() tea.Msg { return mouseModeMsg{action: arg} }
	case "/help", "/?":
		m.notice = "room commands\n" +
			"  /rooms, /back, /list — back to the room list\n" +
			"  /mode [broadcast|moderator|round-robin] — who answers\n" +
			"  /moderator [@handle] — the member moderator mode sends to\n" +
			"  /export [md|json|both] — write the transcript to the lucinate data dir\n" +
			"  /compact [N] — brief the older transcript through the room, keep N verbatim\n" +
			"  /compact local [N] — same, but a local brief with no roster reply\n" +
			"  /find <phrase> — search the transcript (/find clears)\n" +
			"  /cost — tokens and cost per member\n" +
			"  /header [@handle #RRGGBB|default] — per-member header colours\n" +
			"  /mouse off — hand click-drag back to the terminal\n" +
			"typed text goes to the room; prefix \\\\ to send a line that starts with /\n" +
			"an @handle in the message addresses that member and outranks the mode\n" +
			"↑↓/PgUp/PgDn/wheel scroll · End jumps to the live tail · ctrl+u clears the draft"
		return m, nil
	case "/quit", "/exit":
		return m, func() tea.Msg { return goBackFromRoomsMsg{} }
	}
	// The rooms v2 surface (routing, export, compact, find, cost, colours)
	// lives in rooms_ux.go; it reports whether it recognised the input.
	if mm, cmd, ok := m.handleRoomUXCommand(text); ok {
		return mm, cmd
	}
	m.err = fmt.Errorf("%s is not a room command — /rooms goes back, /help lists them", name)
	return m, nil
}

// savePreset writes the room's roster to the preset store so it can be
// restarted with one keystroke later.
func (m roomsModel) savePreset(room rooms.Room) tea.Cmd {
	return func() tea.Msg {
		path, err := rooms.DefaultPresetsPath()
		if err != nil {
			return roomsSentMsg{err: err}
		}
		store, err := rooms.LoadPresets(path)
		if err != nil {
			return roomsSentMsg{err: err}
		}
		store.Remove(room.Name)
		if err := store.Add(rooms.Preset{
			Name: room.Name, RoomID: room.RoomID,
			ThreadID: rooms.DefaultThreadID, Members: room.Members,
		}); err != nil {
			return roomsSentMsg{err: err}
		}
		return roomsSentMsg{err: rooms.SavePresets(path, store)}
	}
}

// ── view ─────────────────────────────────────────────────────────────────────

func (m roomsModel) View() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("Rooms — Hermes Bot Mode") + "\n")
	if m.conn != nil {
		b.WriteString(connBannerStyle.Render(fmt.Sprintf("%s — %s", m.conn.Name, m.conn.URL)) + "\n")
	}
	b.WriteString("\n")

	switch m.sub {
	case roomsInvite:
		b.WriteString(m.viewInvite())
	case roomsTranscript:
		b.WriteString(m.viewTranscript())
	default:
		b.WriteString(m.viewBrowse())
	}

	if m.err != nil {
		b.WriteString("\n" + errorStyle.Render("error: "+m.err.Error()))
	}
	if m.status != "" {
		b.WriteString("\n" + statusStyle.Render(m.status))
	}
	if m.confirmDisband != "" {
		b.WriteString("\n" + navConfirmStyle.Render(
			fmt.Sprintf("disband %s permanently? its history is tombstoned. y/n", m.confirmDisband)))
	}
	b.WriteString("\n\n" + helpStyle.Render(m.hint()))
	return b.String()
}

func (m roomsModel) viewBrowse() string {
	var b strings.Builder
	rows := m.browseRows()
	if len(rows) == 0 {
		if !m.loaded {
			return statusStyle.Render("loading rooms…")
		}
		b.WriteString(emptyHistoryStyle.Render("no rooms yet") + "\n\n")
		b.WriteString(statusStyle.Render("press n to seat a room from the local Hermes profiles") + "\n")
		return b.String()
	}
	presets := len(m.presets.Presets)
	start, end, above, below := windowRows(len(rows), m.cursor, roomsVisibleRows)
	if above > 0 {
		b.WriteString(statusStyle.Render(fmt.Sprintf("  ↑ %d more", above)) + "\n")
	}
	for i := start; i < end; i++ {
		row := rows[i]
		if i == presets {
			b.WriteString("\n" + statusStyle.Render("rooms on this gateway") + "\n")
		}
		cursor := "  "
		if i == m.cursor {
			cursor = completionMenuHighlightStyle.Render("› ")
		}
		switch row.kind {
		case rowPreset:
			p := row.preset
			b.WriteString(fmt.Sprintf("%s%-22s %s\n", cursor, p.Name,
				statusStyle.Render("predefined · "+profileList(p.Members))))
		default:
			r := row.room
			b.WriteString(fmt.Sprintf("%s%-22s %s\n", cursor, r.RoomID,
				statusStyle.Render(fmt.Sprintf("rev=%d · %s", r.Revision, profileList(r.Members)))))
		}
	}
	if below > 0 {
		b.WriteString(statusStyle.Render(fmt.Sprintf("  ↓ %d more", below)) + "\n")
	}
	return b.String()
}

func (m roomsModel) viewInvite() string {
	var b strings.Builder
	b.WriteString(assistantPrefixStyle.Render("Invite profiles to a new room") + "\n")
	b.WriteString(statusStyle.Render(fmt.Sprintf(
		"seat %d-%d local Hermes profiles. The roster is frozen once the room exists — to change members, seat a new room.",
		rooms.MinMembers, rooms.MaxMembers)) + "\n\n")
	if len(m.profiles) == 0 {
		return b.String() + statusStyle.Render("discovering local profiles…")
	}
	start, end, above, below := windowRows(len(m.profiles), m.inviteCursor, roomsVisibleRows)
	if above > 0 {
		b.WriteString(statusStyle.Render(fmt.Sprintf("  ↑ %d more", above)) + "\n")
	}
	for i := start; i < end; i++ {
		cursor := "  "
		if i == m.inviteCursor {
			cursor = completionMenuHighlightStyle.Render("› ")
		}
		mark := "[ ]"
		if m.selected[m.profiles[i]] {
			mark = "[x]"
		}
		b.WriteString(fmt.Sprintf("%s%s %s\n", cursor, mark, m.profiles[i]))
	}
	if below > 0 {
		b.WriteString(statusStyle.Render(fmt.Sprintf("  ↓ %d more", below)) + "\n")
	}
	chosen := m.selectedProfiles()
	b.WriteString("\n" + statusStyle.Render(fmt.Sprintf("selected %d/%d: %s",
		len(chosen), rooms.MaxMembers, strings.Join(chosen, ", "))) + "\n")
	b.WriteString(statusStyle.Render("new room name: "+rooms.DisplayNameFor(chosen)) + "\n")
	return b.String()
}

func (m roomsModel) viewTranscript() string {
	var b strings.Builder
	title := m.openRoomID
	if m.openRoom != nil {
		title = fmt.Sprintf("%s — %s", m.openRoom.RoomID, rosterLabel(*m.openRoom))
	}
	// The routing mode shares the title line: it is context for the
	// transcript below, not another block of chrome, and a transcript pane
	// has no rows to spare.
	b.WriteString(headerStyle.Render(title) + " " + statusStyle.Render("· "+m.routingNote()) + "\n\n")

	// Flatten the transcript and window it around the scroll offset. The
	// room keeps growing as members reply, and an unbounded render slides
	// the composer — and with it the terminal cursor — further down on
	// every poll, which reads as a cursor that jumps on its own.
	lines := m.transcriptLines()
	budget := m.transcriptRows()
	total := len(lines)

	end := total - m.scroll
	if end < 0 {
		end = 0
	}
	if end > total {
		end = total
	}
	start := end - budget
	if start < 0 {
		start = 0
	}
	above, below := start, total-end

	if total == 0 {
		b.WriteString(emptyHistoryStyle.Render("no messages yet — type below and press enter") + "\n")
	} else if above > 0 || below > 0 {
		parts := make([]string, 0, 2)
		if above > 0 {
			parts = append(parts, fmt.Sprintf("↑ %d earlier line(s) hidden", above))
		}
		if below > 0 {
			parts = append(parts, fmt.Sprintf("↓ %d newer line(s)", below))
		}
		hint := fmt.Sprintf(" — full history: lucinate rooms log %s", m.openRoomID)
		if below > 0 {
			hint = " — End: back to the live tail"
		}
		b.WriteString(statusStyle.Render(strings.Join(parts, " · ")+hint) + "\n")
	}
	for _, l := range lines[start:end] {
		b.WriteString(l + "\n")
	}
	// In-flight replies render as their own rows under the transcript, so the
	// text a member has produced so far is visible while it is still being
	// written — and disappears the moment the turn ends (an empty reply must
	// not leave a spinner behind).
	streamRows := m.streamRows()
	for _, l := range streamRows {
		b.WriteString(l + "\n")
	}
	if m.reconnecting {
		b.WriteString(statusStyle.Render(spinnerGlyph(m.spinnerFrame)+" reconnecting — the transcript above is intact") + "\n")
	} else if len(streamRows) == 0 && (m.working || len(m.queue) > 0) {
		line := spinnerGlyph(m.spinnerFrame) + " roster is answering…"
		if n := len(m.queue); n > 0 {
			line = fmt.Sprintf("%s roster is answering… %d queued — sent when it settles (sending now would cancel its work)",
				spinnerGlyph(m.spinnerFrame), n)
		}
		b.WriteString(statusStyle.Render(line) + "\n")
	}
	if block := m.noticeBlock(); block != "" {
		b.WriteString("\n" + block + "\n")
	}
	b.WriteString("\n" + composerView(m.composer))
	return b.String()
}

// transcriptRows is how many transcript lines fit above the composer.
// The fixed chrome is: header (1), connection banner (1), blank (1), the
// hidden-lines notice (1), the composer block (3) and the hint block (2)
// — nine lines, plus two each for a status or error notice when present.
// One extra line is reserved so a wrap never pushes the composer off.
func (m roomsModel) transcriptRows() int {
	h := m.height
	if h <= 0 {
		h = 24
	}
	chrome := 10
	if m.err != nil {
		chrome += 2
	}
	if m.status != "" {
		chrome += 2
	}
	if m.confirmDisband != "" {
		chrome += 2
	}
	rows := h - chrome
	if n := m.noticeLineCount(); n > 0 {
		rows -= n
	}
	if n := len(m.activeStreams()); n > 0 {
		rows -= n
	}
	if m.working || len(m.queue) > 0 {
		rows-- // the roster-status line above the composer
	}
	if rows < 3 {
		rows = 3
	}
	return rows
}

func (m roomsModel) hint() string {
	switch m.sub {
	case roomsInvite:
		return "↑/↓ move · space toggle · a all · c clear · r refresh profiles · enter create · esc cancel"
	case roomsTranscript:
		return "enter send · ↑↓/PgUp/PgDn/wheel scroll · End live · /help · /mode · /export · /compact · /find · /cost · /rooms back"
	}
	return "↑/↓ move · enter open · n new room · p save as predefined · x disband · r refresh · esc back"
}

// ── rendering helpers (pure; unit-tested) ────────────────────────────────────

func profileList(members []rooms.Member) string {
	names := make([]string, 0, len(members))
	for _, m := range members {
		names = append(names, m.Profile)
	}
	return strings.Join(names, ", ")
}

func rosterLabel(room rooms.Room) string {
	if len(room.Members) == 0 {
		return "no members"
	}
	handles := make([]string, 0, len(room.Members))
	for _, m := range room.Members {
		handles = append(handles, "@"+m.Handle)
	}
	return strings.Join(handles, " ")
}

// formatRoomsEvent renders one transcript row the same way the CLI does,
// so the two surfaces read identically.
func formatRoomsEvent(ev rooms.Event) string {
	return formatRoomsEventStyled(ev, ev.Text(), assistantPrefixStyle)
}

// formatRoomsEventStyled renders one transcript row with a caller-supplied
// body (so /find can highlight inside the text) and the member's colour on
// the label. formatRoomsEvent is the plain wrapper the CLI-matching tests use.
func formatRoomsEventStyled(ev rooms.Event, body string, memberStyle lipgloss.Style) string {
	stamp := time.Unix(int64(ev.CreatedAt), 0).Format("15:04:05")
	switch ev.Kind {
	case "message.user":
		return fmt.Sprintf("%s  %s %s", stamp, userPrefixStyle.Render("you →"), body)
	case "message.member":
		return fmt.Sprintf("%s  %s %s", stamp, memberStyle.Render("@"+ev.Speaker()+":"), body)
	case "turn.started":
		return fmt.Sprintf("%s  %s", stamp, toolRunningStyle.Render(ev.Speaker()+" is thinking…"))
	case "turn.settled":
		if passed, _ := ev.Payload["passed"].(bool); passed {
			return fmt.Sprintf("%s  %s", stamp, toolSuccessStyle.Render(ev.Speaker()+" passed"))
		}
		return fmt.Sprintf("%s  %s", stamp, toolSuccessStyle.Render(ev.Speaker()+" finished"))
	case "turn.failed":
		return fmt.Sprintf("%s  %s", stamp, errorStyle.Render(fmt.Sprintf("%s failed: %v", ev.Speaker(), ev.Payload["error"])))
	case "turn.deferred":
		return fmt.Sprintf("%s  %s", stamp, toolRunningStyle.Render(fmt.Sprintf("%s deferred: %v", ev.Speaker(), ev.Payload["reason"])))
	case "room.activity":
		return fmt.Sprintf("%s  %s", stamp, statusStyle.Render(fmt.Sprintf("activity: %v", ev.Payload["status"])))
	case "member.unavailable":
		return fmt.Sprintf("%s  %s", stamp, errorStyle.Render(ev.Speaker()+" unavailable"))
	}
	return fmt.Sprintf("%s  %s", stamp, statusStyle.Render(ev.Kind+" "+ev.Speaker()))
}
