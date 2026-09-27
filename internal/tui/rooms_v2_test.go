package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/config"
	"github.com/lucinate-ai/lucinate/internal/rooms"
)

// The rooms v2 surface at the model level: routing, streaming rows, backoff,
// find/cost/colour commands. Nothing here opens a socket — the live pass over
// a real gateway is in rooms_live_test.go.

// v2Model is a model already inside a room's transcript, with a two-member
// roster and a temporary home, so it can be driven without a gateway.
func v2Model(t *testing.T) roomsModel {
	t.Helper()
	setTestHome(t)
	conn := &config.Connection{ID: "conn-1", Name: "fake", URL: "http://127.0.0.1:1", Type: config.ConnTypeHermes}
	m := newRoomsModel(conn, true)
	m.sub = roomsTranscript
	m.width, m.height = 100, 30
	m.openRoomID = "sztab"
	room := liveRoom("sztab", "Sztab", "matt", "kowal")
	m.openRoom = &room
	m.loaded = true
	return m
}

// feedCmd runs a command (expanding a batch) and feeds every result back into
// Update, deliberately ignoring the follow-up commands the handlers return: a
// poll or tick returned here would sleep and then reschedule itself forever.
func feedCmd(t *testing.T, m roomsModel, cmd tea.Cmd) roomsModel {
	t.Helper()
	for _, msg := range runCmdMsgs(cmd) {
		next, _ := m.Update(msg)
		m = next
	}
	return m
}

// runCmdMsgs executes a command tree one level deep and collects its messages.
func runCmdMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	switch v := msg.(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, sub := range v {
			out = append(out, runCmdMsgs(sub)...)
		}
		return out
	default:
		return []tea.Msg{msg}
	}
}

func v2Event(seq int, kind, profile, text string) rooms.Event {
	payload := map[string]any{}
	if text != "" {
		payload["text"] = text
	}
	return rooms.Event{
		RoomID: "sztab", Seq: seq, EventID: "e" + string(rune('0'+seq)), Kind: kind,
		Actor:     rooms.Actor{Kind: "member", ID: "m1", Profile: profile, DisplayName: profile},
		Payload:   payload,
		CreatedAt: float64(1700000000 + seq),
	}
}

// ── routing ──────────────────────────────────────────────────────────────────

func TestRoomsV2_RoundRobinRoutesAndAdvancesTheCursor(t *testing.T) {
	m := v2Model(t)
	m.prefs.Set("sztab", rooms.RoomPrefs{Mode: string(rooms.RouteRoundRobin)})

	routed, cmd := m.routeOutgoing("dalej")
	if routed != "@matt dalej" {
		t.Fatalf("first routed text = %q, want @matt dalej", routed)
	}
	if cmd == nil {
		t.Error("advancing the round-robin cursor must be persisted, got no cmd")
	}
	if got := m.roomPrefs().RoundRobinIndex; got != 1 {
		t.Fatalf("cursor = %d, want 1", got)
	}
	m = feedCmd(t, m, cmd)

	routed, _ = m.routeOutgoing("dalej")
	if routed != "@kowal dalej" {
		t.Fatalf("second routed text = %q, want @kowal dalej", routed)
	}
}

func TestRoomsV2_ModeratorModeAndExplicitMentions(t *testing.T) {
	m := v2Model(t)
	m.prefs.Set("sztab", rooms.RoomPrefs{Mode: string(rooms.RouteModerator), Moderator: "kowal"})

	routed, _ := m.routeOutgoing("status?")
	if routed != "@kowal status?" {
		t.Fatalf("moderator routing = %q, want @kowal status?", routed)
	}

	// An explicit mention is the user overriding the mode, and must survive.
	routed, _ = m.routeOutgoing("@matt tylko ty")
	if routed != "@matt tylko ty" {
		t.Fatalf("explicit mention = %q, want it sent as written", routed)
	}
}

func TestRoomsV2_ModeCommandSetsReportsAndRefuses(t *testing.T) {
	m := v2Model(t)

	mm, cmd, ok := m.handleRoomUXCommand("/mode round-robin")
	if !ok {
		t.Fatal("/mode was not handled by the rooms v2 surface")
	}
	if cmd == nil {
		t.Error("setting the mode must persist it")
	}
	if got := mm.roomPrefs().Mode; got != string(rooms.RouteRoundRobin) {
		t.Errorf("stored mode = %q, want round-robin", got)
	}
	if !strings.Contains(mm.status, "round-robin") {
		t.Errorf("status = %q, want the new routing reported", mm.status)
	}

	bare, _, _ := m.handleRoomUXCommand("/mode")
	if !strings.Contains(bare.notice, "broadcast") || !strings.Contains(bare.notice, "round-robin") {
		t.Errorf("bare /mode notice = %q, want the modes listed", bare.notice)
	}

	bad, _, _ := m.handleRoomUXCommand("/mode dyktator")
	if bad.err == nil {
		t.Error("/mode with an unknown mode should be refused, not silently accepted")
	}

	mod, _, _ := m.handleRoomUXCommand("/moderator @kowal")
	if mod.roomPrefs().Moderator != "kowal" || mod.roomPrefs().Mode != string(rooms.RouteModerator) {
		t.Errorf("prefs = %+v, want moderator mode with @kowal", mod.roomPrefs())
	}
}

func TestRoomsV2_TranscriptHeaderNamesTheRouting(t *testing.T) {
	m := v2Model(t)
	m.prefs.Set("sztab", rooms.RoomPrefs{Mode: string(rooms.RouteModerator), Moderator: "kowal"})
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "moderator: @kowal") {
		t.Errorf("transcript header should name the moderator:\n%s", out)
	}
	m.prefs.Set("sztab", rooms.RoomPrefs{Mode: string(rooms.RouteRoundRobin)})
	if out := ansi.Strip(m.View()); !strings.Contains(out, "round-robin") {
		t.Errorf("transcript header should name the round-robin:\n%s", out)
	}
}

// ── streaming ────────────────────────────────────────────────────────────────

func TestRoomsV2_StreamingRowsShowPartialTextAndAnimate(t *testing.T) {
	m := v2Model(t)
	m.page.Events = []rooms.Event{
		v2Event(1, "message.user", "", "co robimy?"),
		v2Event(2, "turn.started", "matt", ""),
		v2Event(3, "message.member.delta", "matt", "Pracuje nad"),
	}
	rows := m.streamRows()
	if len(rows) != 1 {
		t.Fatalf("streamRows = %d rows, want 1: %v", len(rows), rows)
	}
	plain := ansi.Strip(rows[0])
	if !strings.Contains(plain, "@matt") || !strings.Contains(plain, "Pracuje nad") {
		t.Errorf("streaming row = %q, want the member and the partial text", plain)
	}
	if !strings.ContainsAny(plain, strings.Join(spinnerFrames, "")) {
		t.Errorf("streaming row = %q, want the braille spinner", plain)
	}

	before := ansi.Strip(m.streamRows()[0])
	next, cmd := m.handleSpinnerTick()
	if cmd == nil {
		t.Error("an in-flight reply must keep the spinner ticking")
	}
	after := ansi.Strip(next.streamRows()[0])
	if before == after {
		t.Errorf("glyph did not advance between frames: %q", before)
	}
}

// An empty answer is a real outcome: the placeholder has to go, or the room
// looks like it is still thinking.
func TestRoomsV2_EmptyReplyRemovesThePlaceholder(t *testing.T) {
	m := v2Model(t)
	m.page.Events = []rooms.Event{
		v2Event(1, "turn.started", "matt", ""),
		v2Event(2, "turn.settled", "matt", ""),
	}
	if rows := m.streamRows(); len(rows) != 0 {
		t.Fatalf("streamRows = %v, want none once the turn settled empty", rows)
	}
	if strings.Contains(ansi.Strip(m.View()), "⠋ @matt") {
		t.Error("the view still shows a spinner for a member that answered nothing")
	}
}

func TestRoomsV2_SpinnerStopsWhenNothingIsInFlight(t *testing.T) {
	m := v2Model(t)
	m.spinnerTicking = true
	next, cmd := m.handleSpinnerTick()
	if cmd != nil {
		t.Error("no in-flight reply should stop the spinner")
	}
	if next.spinnerTicking {
		t.Error("spinnerTicking should be cleared so the next reply can start it again")
	}
}

func TestRoomsV2_ViewShowsTheStreamingReplyInline(t *testing.T) {
	m := v2Model(t)
	m.page.Events = []rooms.Event{
		v2Event(1, "turn.started", "matt", ""),
		v2Event(2, "message.member.delta", "matt", "Kompiluje testy"),
	}
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "Kompiluje testy") {
		t.Errorf("view should render the in-flight text:\n%s", out)
	}
}

// ── reconnect ────────────────────────────────────────────────────────────────

func TestRoomsV2_DisconnectArmsTheBackoffAndKeepsTheTranscript(t *testing.T) {
	m := v2Model(t)
	m.page.Events = []rooms.Event{v2Event(1, "message.user", "", "hello")}
	m.since = 1

	next, cmd := m.Update(roomsLogMsg{err: errConnectionReset{}, client: &rooms.Client{}})
	if cmd == nil {
		t.Fatal("a dropped socket should schedule a redial")
	}
	if !next.reconnecting {
		t.Error("reconnecting should be true after a dropped socket")
	}
	if next.backoff.Attempt() != 1 {
		t.Errorf("backoff attempt = %d, want 1", next.backoff.Attempt())
	}
	if next.backoff.Peek() != 2*rooms.DefaultBackoffBase {
		t.Errorf("second delay = %v, want it to have doubled", next.backoff.Peek())
	}
	if next.since != 1 || len(next.page.Events) != 1 {
		t.Errorf("transcript lost on disconnect: since=%d events=%d", next.since, len(next.page.Events))
	}
	if next.err != nil {
		t.Errorf("a recoverable disconnect must not surface as a room error: %v", next.err)
	}
	if !strings.Contains(next.status, "redial") {
		t.Errorf("status = %q, want it to explain the redial", next.status)
	}
}

func TestRoomsV2_GatewayRejectionIsNotTreatedAsADisconnect(t *testing.T) {
	m := v2Model(t)
	next, cmd := m.Update(roomsLogMsg{err: roomRejectedError{}, client: &rooms.Client{}})
	if cmd != nil || next.reconnecting {
		t.Error("a gateway-side rejection must be reported, not retried forever")
	}
	if next.err == nil {
		t.Error("the rejection should surface as an error")
	}
}

func TestRoomsV2_StaleReconnectTickIsIgnored(t *testing.T) {
	m := v2Model(t)
	m.reconnecting = true
	m.reconnectAttempt = 3
	next, cmd := m.handleReconnect(1)
	if cmd != nil {
		t.Error("a tick from a superseded attempt must not redial")
	}
	if !next.reconnecting {
		t.Error("the live attempt should stay armed")
	}
}

func TestRoomsV2_ReconnectWithoutAConnectionFailsLoudly(t *testing.T) {
	m := v2Model(t)
	m.conn = nil
	m.reconnecting = true
	m.reconnectAttempt = 1
	next, _ := m.handleReconnect(1)
	if next.reconnecting {
		t.Error("with no connection to redial there is nothing to keep retrying")
	}
	if next.err == nil {
		t.Error("the user must be told the room cannot come back by itself")
	}
}

// ── find / cost / colours / compact ──────────────────────────────────────────

func TestRoomsV2_FindListsHitsAndHighlightsThem(t *testing.T) {
	m := v2Model(t)
	m.page.Events = []rooms.Event{
		v2Event(1, "message.user", "", "gdzie jest raport?"),
		v2Event(2, "message.member", "matt", "raport lezy w artifacts"),
	}
	mm, _, ok := m.handleRoomUXCommand("/find raport")
	if !ok {
		t.Fatal("/find not handled")
	}
	if mm.findQuery != "raport" {
		t.Fatalf("findQuery = %q, want raport", mm.findQuery)
	}
	if !strings.Contains(mm.notice, "2 match") {
		t.Errorf("notice = %q, want both hits counted", mm.notice)
	}
	if !strings.Contains(ansi.Strip(mm.View()), "raport") {
		t.Error("the view should still show the matching lines")
	}

	cleared, _, _ := mm.handleRoomUXCommand("/find")
	if cleared.findQuery != "" || cleared.notice != "" {
		t.Errorf("/find with no phrase should clear the search, got %q / %q", cleared.findQuery, cleared.notice)
	}

	none, _, _ := m.handleRoomUXCommand("/find nie-ma-takiego")
	if !strings.Contains(none.notice, "no matches") {
		t.Errorf("notice = %q, want an explicit no-match line", none.notice)
	}
}

func TestRoomsV2_CostNoticeReportsPerMemberUsage(t *testing.T) {
	m := v2Model(t)
	last := v2Event(2, "message.member", "matt", "gotowe")
	last.Payload["usage"] = map[string]any{
		"input_tokens": 1200, "output_tokens": 300, "total_tokens": 1500, "cost_usd": 0.0123,
	}
	m.page.Events = []rooms.Event{v2Event(1, "turn.started", "matt", ""), last}

	mm, _, ok := m.handleRoomUXCommand("/cost")
	if !ok {
		t.Fatal("/cost not handled")
	}
	if !strings.Contains(mm.notice, "matt") {
		t.Errorf("cost notice = %q, want the member named", mm.notice)
	}
	if !strings.Contains(mm.notice, "1500") {
		t.Errorf("cost notice = %q, want the token count", mm.notice)
	}
}

func TestRoomsV2_HeaderColourIsValidatedAndPersisted(t *testing.T) {
	m := v2Model(t)

	mm, cmd, ok := m.handleRoomUXCommand("/header @matt #ff0000")
	if !ok {
		t.Fatal("/header not handled")
	}
	if cmd == nil {
		t.Error("setting a colour must persist it")
	}
	if got := mm.roomPrefs().Colours["matt"]; got != "#FF0000" {
		t.Fatalf("stored colour = %q, want the normalised #FF0000", got)
	}
	if mm.memberStyle("matt").GetForeground() == nil {
		t.Error("the member style should carry the configured colour")
	}

	bad, _, _ := m.handleRoomUXCommand("/header @matt nie-kolor")
	if bad.err == nil {
		t.Error("an invalid colour must be refused")
	}

	listed, _, _ := m.handleRoomUXCommand("/header")
	if !strings.Contains(listed.notice, "@matt") || !strings.Contains(listed.notice, "@kowal") {
		t.Errorf("bare /header notice = %q, want the roster listed with colours", listed.notice)
	}

	cleared, cmd, _ := m.handleRoomUXCommand("/header @matt default")
	if cmd == nil {
		t.Error("clearing a colour must persist it")
	}
	if _, set := cleared.roomPrefs().Colours["matt"]; set {
		t.Errorf("colour should be cleared, got %v", cleared.roomPrefs().Colours)
	}
}

// /compact local gives the same reading experience with no roster reply, and
// the tail the user asked for stays verbatim.
func TestRoomsV2_CompactionHidesOlderMessagesAndKeepsTheTail(t *testing.T) {
	m := v2Model(t)
	var events []rooms.Event
	for i := 1; i <= 12; i++ {
		if i%2 == 1 {
			events = append(events, v2Event(i, "message.user", "", "pytanie "+itoaTUI(i)))
			continue
		}
		events = append(events, v2Event(i, "message.member", "matt", "odpowiedz "+itoaTUI(i)))
	}
	m.page.Events = events

	m.pendingCompact = &pendingCompact{throughSeq: 8, promptSeq: 40, keepLast: 3}
	brief := v2Event(41, "message.member", "matt", "BRIEF: ustalilismy plan")
	m.page.Events = append(m.page.Events, brief)
	if cmd := m.applyBrief(); cmd == nil {
		t.Fatal("the roster's brief should be stored as the compaction")
	}
	if m.pendingCompact != nil {
		t.Error("the pending request should be cleared once the brief lands")
	}
	c := m.roomPrefs().Compaction
	if c == nil || c.Summary != "BRIEF: ustalilismy plan" || c.Source != "roster" {
		t.Fatalf("compaction = %+v, want the roster's brief recorded", c)
	}

	visible := m.compactedEvents()
	var seqs []int
	for _, ev := range visible {
		seqs = append(seqs, ev.Seq)
	}
	want := []int{9, 10, 11, 12, 41}
	if len(seqs) != len(want) {
		t.Fatalf("visible seqs = %v, want %v (head hidden, kept tail and brief visible)", seqs, want)
	}

	out := ansi.Strip(strings.Join(m.transcriptLines(), "\n"))
	if !strings.Contains(out, "BRIEF: ustalilismy plan") {
		t.Errorf("the brief should head the transcript:\n%s", out)
	}
	if !strings.Contains(out, "odpowiedz 12") || !strings.Contains(out, "odpowiedz 10") {
		t.Errorf("the kept tail is missing:\n%s", out)
	}
	if !strings.Contains(ansi.Strip(m.View()), "brief covers seq 1-8") {
		t.Error("the header should say what the brief covers")
	}
}

func TestRoomsV2_CompactLocalWorksWithoutTheRoster(t *testing.T) {
	m := v2Model(t)
	var events []rooms.Event
	for i := 1; i <= 10; i++ {
		events = append(events, v2Event(i, "message.member", "matt", "linia "+itoaTUI(i)))
	}
	m.page.Events = events

	mm, cmd, ok := m.handleRoomUXCommand("/compact local 2")
	if !ok {
		t.Fatal("/compact not handled")
	}
	if cmd == nil {
		t.Error("a local compaction must be persisted")
	}
	c := mm.roomPrefs().Compaction
	if c == nil || c.Source != "local" || c.KeepLast != 2 {
		t.Fatalf("compaction = %+v, want a local briefing keeping 2 messages", c)
	}
	if !strings.Contains(c.Summary, "@matt") {
		t.Errorf("local summary = %q, want it to quote the members", c.Summary)
	}
	// The two kept messages stay visible; everything the brief covers is
	// gone from the rendered transcript.
	visible := mm.compactedEvents()
	if len(visible) != 2 || visible[0].Seq != 9 || visible[1].Seq != 10 {
		t.Fatalf("visible = %+v, want only the last two messages", visible)
	}
	rendered := ansi.Strip(strings.Join(mm.transcriptLines(), "\n"))
	if !strings.Contains(rendered, "linia 9") || !strings.Contains(rendered, "linia 10") {
		t.Errorf("the kept tail is missing:\n%s", rendered)
	}
}

func TestRoomsV2_HelpListsTheNewSurface(t *testing.T) {
	m := v2Model(t)
	mm, _ := m.handleTranscriptCommand("/help")
	for _, want := range []string{"/mode", "/export", "/compact", "/find", "/cost", "/header"} {
		if !strings.Contains(mm.notice, want) {
			t.Errorf("/help notice is missing %s:\n%s", want, mm.notice)
		}
	}
}

// ── test doubles ─────────────────────────────────────────────────────────────

// errConnectionReset stands in for the wrapped OS error a dropped socket
// produces; the shape matters, not the type.
type errConnectionReset struct{}

func (errConnectionReset) Error() string { return "read tcp 127.0.0.1:5000: connection reset by peer" }

type roomRejectedError struct{}

func (roomRejectedError) Error() string { return "room id is required" }

func itoaTUI(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
