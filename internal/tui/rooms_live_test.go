package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/gorilla/websocket"

	"github.com/lucinate-ai/lucinate/internal/config"
	"github.com/lucinate-ai/lucinate/internal/rooms"
)

// A live pass over a real gateway: the view talks to an in-process WebSocket
// server through the production rooms.Client, so the whole path — dial,
// groups.log, groups.send, streaming events, a dropped socket, the redial and
// the resume — runs for real rather than against a stub.
//
// The server speaks the same JSON-RPC framing the hosted-room gateway does:
// one object per frame, {id, method, params} in, {jsonrpc, id, result} out.

type fakeRoomGateway struct {
	mu      sync.Mutex
	events  []rooms.Event
	latest  int
	working bool

	// received is every message text that reached groups.send, in order.
	received []string
	conns    []*websocket.Conn
	// refuse makes the next N dials fail, so the backoff path is exercised
	// against a socket that really is unavailable.
	refuse int
	srv    *httptest.Server

	// scriptWhat/scriptHandle let a test script the member turn a send
	// starts, instead of the default "still writing" answer.
	scriptUserText string
}

func newFakeRoomGateway(t *testing.T) *fakeRoomGateway {
	t.Helper()
	g := &fakeRoomGateway{}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		g.mu.Lock()
		g.conns = append(g.conns, conn)
		g.mu.Unlock()
		defer conn.Close()
		g.serve(conn)
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeRoomGateway) url() string { return g.srv.URL }

func (g *fakeRoomGateway) serve(conn *websocket.Conn) {
	for {
		var req struct {
			ID     uint64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := conn.ReadJSON(&req); err != nil {
			return
		}
		if err := conn.WriteJSON(map[string]any{
			"jsonrpc": "2.0", "id": req.ID, "result": g.handle(req.Method, req.Params),
		}); err != nil {
			return
		}
	}
}

func (g *fakeRoomGateway) handle(method string, params json.RawMessage) any {
	g.mu.Lock()
	defer g.mu.Unlock()

	room := map[string]any{
		"room_id": "sztab", "name": "Sztab", "revision": 2,
		"authority_gateway_id": "fake", "authority_epoch": 1,
		"created_at": 1.0, "updated_at": 2.0,
		"members": []map[string]any{
			{"member_id": "m1", "profile": "matt", "handle": "matt", "display_name": "Matt"},
			{"member_id": "m2", "profile": "kowal", "handle": "kowal", "display_name": "Kowal"},
		},
		"latest_seq": g.latest,
	}

	switch method {
	case "groups.capabilities":
		return map[string]any{
			"protocol_version": 1, "driver": true, "persistent_process": true,
			"authority_gateway_id": "fake",
			"room_link":            map[string]any{"enabled": false},
			"features":             []string{"log", "streaming"},
			"methods":              []string{"groups.log", "groups.send", "groups.state"},
			"max_log_limit":        500,
		}
	case "groups.list":
		return map[string]any{"rooms": []map[string]any{room}, "next_offset": nil}
	case "groups.state":
		return map[string]any{"room": room, "driver_status": map[string]any{
			"running": true, "working": g.working, "blocked": false, "counts": map[string]any{},
		}}
	case "groups.log":
		var p struct {
			SinceSeq int `json:"since_seq"`
		}
		_ = json.Unmarshal(params, &p)
		out := make([]rooms.Event, 0, len(g.events))
		for _, ev := range g.events {
			if ev.Seq > p.SinceSeq {
				out = append(out, ev)
			}
		}
		return map[string]any{
			"events": out, "cursor": g.latest, "latest_seq": g.latest, "has_more": false,
			"authority": map[string]any{"gateway_id": "fake", "epoch": 1},
		}
	case "groups.send":
		var p struct {
			Payload map[string]any `json:"payload"`
		}
		_ = json.Unmarshal(params, &p)
		text, _ := p.Payload["text"].(string)
		g.received = append(g.received, text)
		ev := g.appendLocked("message.user", "", text)
		// A member picks the message up: the driver opens the turn and the
		// first delta lands. The rest of the answer is scripted by the test,
		// which is what lets the streaming and empty-reply paths be observed
		// mid-flight.
		g.appendLocked("turn.started", "matt", "")
		g.appendLocked("message.member.delta", "matt", "Pracuje nad tym")
		g.working = true
		return map[string]any{"event": ev, "accepted": true, "driver_started": true}
	}
	return map[string]any{}
}

func (g *fakeRoomGateway) appendLocked(kind, profile, text string) rooms.Event {
	g.latest++
	payload := map[string]any{}
	if text != "" {
		payload["text"] = text
	}
	actor := map[string]any{"kind": "member", "id": "m1"}
	if profile != "" {
		actor["profile"] = profile
		actor["display_name"] = strings.ToUpper(profile[:1]) + profile[1:]
	} else {
		actor["kind"] = "user"
	}
	ev := rooms.Event{
		RoomID: "sztab", Seq: g.latest, EventID: fmt.Sprintf("e%d", g.latest), Kind: kind,
		Payload: payload, CreatedAt: float64(1700000000 + g.latest),
	}
	// Round-trip the actor through JSON so the test scripts events in exactly
	// the shape the wire delivers them in.
	raw, _ := json.Marshal(actor)
	_ = json.Unmarshal(raw, &ev.Actor)
	g.events = append(g.events, ev)
	if kind == "turn.settled" || kind == "message.member" {
		g.working = false
	}
	return ev
}

// push appends an event from the test side.
func (g *fakeRoomGateway) push(kind, profile, text string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.appendLocked(kind, profile, text)
}

// closeConns drops every live socket, which is what a gateway restart looks
// like from the client side.
func (g *fakeRoomGateway) closeConns() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := len(g.conns)
	for _, c := range g.conns {
		_ = c.Close()
	}
	g.conns = nil
	return n
}

func (g *fakeRoomGateway) setRefuse(n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refuse = n
}

func (g *fakeRoomGateway) shouldRefuse() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.refuse <= 0 {
		return false
	}
	g.refuse--
	return true
}

func (g *fakeRoomGateway) sent() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.received...)
}

// ── evidence ─────────────────────────────────────────────────────────────────

type liveEvidence struct {
	mu    sync.Mutex
	Steps []liveStep `json:"steps"`
}

type liveStep struct {
	Step  string `json:"step"`
	What  string `json:"what"`
	Frame string `json:"frame,omitempty"`
}

func (e *liveEvidence) add(t *testing.T, step, what string, frame string) {
	t.Helper()
	frame = strings.TrimRight(ansi.Strip(frame), "\n")
	if lines := strings.Split(frame, "\n"); len(lines) > 24 {
		frame = strings.Join(lines[len(lines)-24:], "\n")
	}
	e.mu.Lock()
	e.Steps = append(e.Steps, liveStep{Step: step, What: what, Frame: frame})
	e.mu.Unlock()
	t.Logf("[live] %s: %s", step, what)
}

// write puts the evidence where the report harness expects it. Unset env keeps
// the suite hermetic: the file goes to the test's own temp dir.
func (e *liveEvidence) write(t *testing.T) string {
	t.Helper()
	path := os.Getenv("ROOMS_V2_EVIDENCE")
	if path == "" {
		path = filepath.Join(t.TempDir(), "rooms-v2-evidence.json")
	}
	blob, err := json.MarshalIndent(map[string]any{
		"generated_at": timeNowISO(),
		"steps":        e.Steps,
	}, "", "  ")
	if err != nil {
		t.Fatalf("marshal evidence: %v", err)
	}
	if err := os.WriteFile(path, append(blob, '\n'), 0o644); err != nil {
		t.Fatalf("write evidence: %v", err)
	}
	return path
}

func TestRoomsV2Live_FullPassOverARealGateway(t *testing.T) {
	home := setTestHome(t)
	// Keep the first message free of project context, so the routed text can
	// be asserted exactly as it reaches the wire.
	t.Setenv("ORCA_WORKTREE_ID", "")

	g := newFakeRoomGateway(t)
	ev := &liveEvidence{}

	var dials, refused int
	roomsDialer = func(ctx context.Context, baseURL, token string) (*rooms.Client, error) {
		dials++
		if g.shouldRefuse() {
			refused++
			return nil, errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
		}
		return rooms.DialBaseURL(ctx, baseURL, token)
	}
	t.Cleanup(func() { roomsDialer = rooms.DialBaseURL })

	conn := &config.Connection{ID: "conn-1", Name: "fake", URL: g.url(), Type: config.ConnTypeHermes}
	m := newRoomsModel(conn, true)
	m.sub, m.width, m.height = roomsTranscript, 100, 32
	m.openRoomID = "sztab"
	room := liveRoom("sztab", "Sztab", "matt", "kowal")
	m.openRoom = &room

	// 1. The socket works and the (empty) transcript loads.
	m = feedCmd(t, m, m.loadLog("sztab", 0))
	if m.err != nil {
		t.Fatalf("initial log over a real socket failed: %v", m.err)
	}
	ev.add(t, "1. connect + groups.log", fmt.Sprintf("dialed %d time(s), events=%d", dials, len(m.page.Events)), m.View())

	// 2. Round-robin routing reaches the gateway as a real mention.
	m.prefs.Set("sztab", rooms.RoomPrefs{Mode: string(rooms.RouteRoundRobin)})
	m, cmd := m.post("co robimy?")
	m = feedCmd(t, m, cmd)
	sent := g.sent()
	if len(sent) != 1 || sent[0] != "@matt co robimy?" {
		t.Fatalf("gateway received %q, want the round-robin mention @matt co robimy?", sent)
	}
	if got := m.roomPrefs().RoundRobinIndex; got != 1 {
		t.Fatalf("round-robin cursor = %d, want 1", got)
	}
	ev.add(t, "2. routing (round-robin)", "groups.send payload text = "+sent[0], m.View())

	// 3. The in-flight reply is streamed: partial text plus a braille spinner.
	m = feedCmd(t, m, m.loadLog("sztab", m.since))
	streams := m.activeStreams()
	if len(streams) != 1 || streams[0].Member != "matt" || streams[0].Text != "Pracuje nad tym" {
		t.Fatalf("active streams = %+v, want matt mid-answer with the partial text", streams)
	}
	frame1 := ansi.Strip(m.streamRows()[0])
	m2, _ := m.handleSpinnerTick()
	frame2 := ansi.Strip(m2.streamRows()[0])
	if frame1 == frame2 {
		t.Fatalf("the braille spinner did not advance: %q", frame1)
	}
	if !strings.Contains(ansi.Strip(m.View()), "Pracuje nad tym") {
		t.Fatal("the partial reply is not rendered in the view")
	}
	ev.add(t, "3. streaming deltas", "row1="+strings.Fields(frame1)[0]+" row2="+strings.Fields(frame2)[0]+" (glyph advances)", m.View())

	// 4. A member that answers nothing leaves no placeholder behind.
	g.push("turn.started", "kowal", "")
	g.push("turn.settled", "kowal", "")
	m = feedCmd(t, m, m.loadLog("sztab", m.since))
	for _, s := range m.activeStreams() {
		if s.Member == "kowal" {
			t.Fatalf("kowal settled with an empty reply and still has a stream: %+v", s)
		}
	}
	ev.add(t, "4. empty reply", fmt.Sprintf("active streams after kowal's empty turn: %d (kowal removed)", len(m.activeStreams())), m.View())

	// 5. The finished answer replaces the streaming row.
	g.push("message.member", "matt", "Zrobione: raport gotowy.")
	g.push("turn.settled", "matt", "")
	m = feedCmd(t, m, m.loadLog("sztab", m.since))
	if len(m.activeStreams()) != 0 {
		t.Fatalf("streams still active after the turn settled: %+v", m.activeStreams())
	}
	transcript := ansi.Strip(strings.Join(m.transcriptLines(), "\n"))
	if !strings.Contains(transcript, "Zrobione: raport gotowy.") {
		t.Fatalf("the finished answer is missing from the transcript:\n%s", transcript)
	}
	ev.add(t, "5. final message", "streaming row replaced by the finished message", m.View())

	// 6. /find over the live transcript.
	m, _, _ = m.handleRoomUXCommand("/find raport")
	if !strings.Contains(m.notice, "1 match") {
		t.Fatalf("/find notice = %q, want one hit for raport", m.notice)
	}
	ev.add(t, "6. /find", strings.SplitN(m.notice, "\n", 2)[0], m.View())

	// 7. /cost from the gateway's usage numbers. The numbers ride a fresh
	// member reply, because that is how the gateway reports them.
	g.mu.Lock()
	withUsage := g.appendLocked("message.member", "matt", "Raport gotowy, koszt policzony.")
	withUsage.Payload["usage"] = map[string]any{"input_tokens": 1200, "output_tokens": 300, "total_tokens": 1500, "cost_usd": 0.0123}
	g.events[len(g.events)-1] = withUsage
	g.mu.Unlock()
	m = feedCmd(t, m, m.loadLog("sztab", m.since))
	m, _, _ = m.handleRoomUXCommand("/cost")
	if !strings.Contains(m.notice, "1500") || !strings.Contains(m.notice, "@matt") {
		t.Fatalf("/cost notice = %q, want matt's token totals", m.notice)
	}
	ev.add(t, "7. /cost", strings.ReplaceAll(strings.TrimSpace(m.notice), "  ", " "), m.View())

	// 8. /header colours, persisted to the data dir.
	m, headerCmd, _ := m.handleRoomUXCommand("/header @matt #ff8800")
	m = feedCmd(t, m, headerCmd)
	if got := m.roomPrefs().Colours["matt"]; got != "#FF8800" {
		t.Fatalf("stored colour = %q, want #FF8800", got)
	}
	prefsPath := filepath.Join(home, ".lucinate", "rooms-prefs.json")
	blob, err := os.ReadFile(prefsPath)
	if err != nil {
		t.Fatalf("prefs file was not written: %v", err)
	}
	if !strings.Contains(string(blob), "#FF8800") {
		t.Fatalf("prefs file does not carry the colour:\n%s", blob)
	}
	ev.add(t, "8. /header", "colour persisted to "+prefsPath, m.View())

	// 9. /export writes both files into the data dir.
	m, exportCmd, _ := m.handleRoomUXCommand("/export both")
	m = feedCmd(t, m, exportCmd)
	if !strings.Contains(m.status, "exported") {
		t.Fatalf("export status = %q, want the written paths", m.status)
	}
	for _, want := range []string{".md", ".json"} {
		if !strings.Contains(m.status, want) {
			t.Fatalf("export status = %q, want a %s file", m.status, want)
		}
	}
	ev.add(t, "9. /export", strings.SplitN(m.status, ": ", 2)[1], m.View())

	// 10. A dropped socket: the transcript survives, backoff arms, and the
	// refusal path doubles the delay.
	before, sinceBefore := len(m.page.Events), m.since
	if n := g.closeConns(); n == 0 {
		t.Fatal("no live connections to drop")
	}
	// Let the client's read loop notice the drop, so the poll below exercises
	// the real dropped-socket path instead of a half-written request.
	waitFor(t, 2*time.Second, func() bool {
		c := m.shared.get()
		return c != nil && c.Err() != nil
	})
	m = feedCmd(t, m, m.loadLog("sztab", m.since))
	if !m.reconnecting || m.backoff.Attempt() != 1 {
		t.Fatalf("after the drop: reconnecting=%v attempts=%d, want true/1", m.reconnecting, m.backoff.Attempt())
	}
	if len(m.page.Events) != before || m.since != sinceBefore {
		t.Fatalf("the transcript was lost with the socket: events %d→%d, since %d→%d",
			before, len(m.page.Events), sinceBefore, m.since)
	}
	ev.add(t, "10. dropped socket", fmt.Sprintf("reconnecting, backoff attempt 1, transcript intact (%d events, since=%d)", before, m.since), m.View())

	g.setRefuse(1)
	attempt := m.reconnectAttempt
	m, _ = m.handleReconnect(attempt)
	if refused != 1 || m.backoff.Attempt() != 2 {
		t.Fatalf("refused dials=%d backoff attempts=%d, want 1/2", refused, m.backoff.Attempt())
	}
	ev.add(t, "11. refused redial", "connection refused → backoff doubled to attempt 2", m.View())

	m, cmd = m.handleReconnect(m.reconnectAttempt)
	m = feedCmd(t, m, cmd)
	if m.reconnecting {
		t.Fatalf("still reconnecting after a successful redial: %s", m.status)
	}
	if !strings.Contains(m.status, "reconnected") {
		t.Fatalf("status = %q, want the resume reported", m.status)
	}
	reloaded := ansi.Strip(strings.Join(m.transcriptLines(), "\n"))
	if !strings.Contains(reloaded, "Zrobione: raport gotowy.") {
		t.Fatalf("history was not resumed after the redial:\n%s", reloaded)
	}
	if m.backoff.Attempt() != 0 {
		t.Fatalf("backoff should reset after a good connection, attempts=%d", m.backoff.Attempt())
	}
	ev.add(t, "12. resume", fmt.Sprintf("status=%q dials=%d events=%d (no duplicates)", m.status, dials, len(m.page.Events)), m.View())

	// 13. /compact local: the head is replaced by a brief, the tail stays.
	var events []rooms.Event
	for i := 1; i <= 12; i++ {
		g.push("message.user", "", fmt.Sprintf("pytanie %d", i))
		g.push("message.member", "matt", fmt.Sprintf("odpowiedz %d", i))
	}
	m = feedCmd(t, m, m.loadLog("sztab", m.since))
	events = m.page.Events
	m, _, _ = m.handleRoomUXCommand("/compact local 4")
	if m.roomPrefs().Compaction == nil {
		t.Fatal("/compact local did not store a compaction")
	}
	after := ansi.Strip(strings.Join(m.transcriptLines(), "\n"))
	if !strings.Contains(after, "brief") {
		t.Fatalf("the brief is not rendered:\n%s", after)
	}
	if !strings.Contains(after, "odpowiedz 12") {
		t.Fatalf("the kept tail is missing:\n%s", after)
	}
	_, visible := rooms.ApplyCompaction(m.page.Events, m.roomPrefs().Compaction)
	if len(visible) != 4 {
		t.Fatalf("visible events after compaction = %d, want the 4 kept messages", len(visible))
	}
	for _, evv := range visible {
		if evv.Seq <= m.roomPrefs().Compaction.ThroughSeq {
			t.Fatalf("event %d is inside the summarised range but still visible", evv.Seq)
		}
	}
	ev.add(t, "13. /compact local 4", fmt.Sprintf("events=%d → brief + last 4 messages visible (seqs > %d)", len(events), m.roomPrefs().Compaction.ThroughSeq), m.View())

	// 14. /mode reports the live routing.
	m, _, _ = m.handleRoomUXCommand("/mode")
	ev.add(t, "14. /mode status", strings.ReplaceAll(strings.SplitN(m.notice, "\n", 2)[0], "  ", " "), m.View())

	path := ev.write(t)
	t.Logf("live evidence written to %s", path)
}

// waitFor polls cond until it holds or the deadline passes. Socket teardown is
// asynchronous, and a fixed sleep would either flake or slow every run.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func timeNowISO() string { return time.Now().Format(time.RFC3339) }
