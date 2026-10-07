package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lucinate-ai/lucinate/internal/config"
	"github.com/lucinate-ai/lucinate/internal/rooms"
)

// TestRoomsRestart_Live runs /restart against a real gateway, through the
// same code path the TUI takes: create the old room, give it a real message,
// restart it with a summary, then read the NEW room's log back off the
// gateway. The transcript quoted in the hand-over is the room's own log, not
// a fixture.
//
//	ROOMS_LIVE=1 ROOMS_LIVE_URL=http://127.0.0.1:9121 \
//	ROOMS_EVIDENCE=<file> go test ./internal/tui/ -run TestRoomsRestart_Live -v
func TestRoomsRestart_Live(t *testing.T) {
	if os.Getenv("ROOMS_LIVE") == "" {
		t.Skip("set ROOMS_LIVE=1 (and ROOMS_LIVE_URL) to run against a live gateway")
	}
	url := os.Getenv("ROOMS_LIVE_URL")
	if url == "" {
		t.Fatal("ROOMS_LIVE_URL is empty — point it at the gateway, e.g. http://127.0.0.1:9121")
	}
	conn := &config.Connection{ID: "hermes-local", Name: "live", URL: url, Type: config.ConnTypeHermes}
	m := newRoomsModel(conn, true)
	m.width, m.height = 100, 30

	// 1. The old room, created through the TUI's own create path.
	m.sub = roomsInvite
	m.selected = map[string]bool{"matt": true, "kowal": true}
	m = feedCmd(t, m, m.createRoom())
	if m.err != nil {
		t.Fatalf("create the old room: %v", m.err)
	}
	oldID := m.openRoomID

	// 2. Two real messages: the opening carries the context block, the second
	// is the short line the digest will quote back.
	m, cmd := m.post("restart proof: przygotowanie starego pokoju")
	if cmd == nil {
		t.Fatal("post armed no command")
	}
	m = feedCmd(t, m, cmd)
	if m.err != nil {
		t.Fatalf("post: %v", m.err)
	}
	// The roster would start answering; the proof does not need replies, and
	// the operator's gateway should not be left working on a test turn.
	stopLiveRoom(t, url, m.token, oldID)

	// Idle again — otherwise the model queues the next line behind a turn
	// that is no longer running.
	m.working = false
	m.queue = nil
	m, cmd = m.post("restart proof: ten pokoj zaraz zostanie zrestartowany z tym samym skladem")
	if cmd == nil {
		t.Fatal("second post armed no command")
	}
	m = feedCmd(t, m, cmd)
	if m.err != nil {
		t.Fatalf("second post: %v", m.err)
	}
	stopLiveRoom(t, url, m.token, oldID)

	// 3. The room's own transcript is what the summary will quote.
	m = feedCmd(t, m, m.loadLog(oldID, 0))
	if len(m.page.Events) == 0 {
		t.Fatalf("log of %s came back empty — nothing to hand over", oldID)
	}
	oldEvents := len(m.page.Events)

	// 4. /restart with-summary, exactly as it would be typed.
	m, cmd = m.handleTranscriptCommand("/restart with-summary")
	if cmd == nil {
		t.Fatal("/restart armed no command")
	}
	msgs := runCmdMsgs(cmd)
	if len(msgs) != 1 {
		t.Fatalf("restart produced %d messages, want 1", len(msgs))
	}
	next, follow := m.Update(msgs[0])
	if next.err != nil {
		t.Fatalf("restart: %v", next.err)
	}
	m = next
	newID := m.openRoomID
	if newID == "" || newID == oldID {
		t.Fatalf("restart opened %q (old was %q) — want a new room", newID, oldID)
	}
	if follow == nil {
		t.Fatal("with-summary armed no hand-over message")
	}
	m = feedCmd(t, m, follow)
	if m.err != nil {
		t.Fatalf("hand-over: %v", m.err)
	}
	stopLiveRoom(t, url, m.token, newID)

	// 5. The log the gateway holds for the NEW room, verbatim.
	page := liveRoomLog(t, url, m.token, newID)
	if len(page.Events) == 0 {
		t.Fatalf("the new room %s has no log", newID)
	}
	first := ""
	for _, ev := range page.Events {
		if ev.Kind == "message.user" {
			first = ev.Text()
			break
		}
	}
	if first == "" {
		t.Fatalf("no user message in the new room's log — the hand-over did not land:\n%s", liveLogText(page))
	}
	for _, want := range []string{oldID, "przeniesienie pokoju", "katalog starego pokoju", "restart proof"} {
		if !strings.Contains(first, want) {
			t.Errorf("hand-over message is missing %q:\n%s", want, first)
		}
	}

	subLabel := "inny widok"
	switch m.sub {
	case roomsTranscript:
		subLabel = "roomsTranscript — transkrypcja nowego pokoju"
	case roomsBrowse:
		subLabel = "roomsBrowse"
	case roomsInvite:
		subLabel = "roomsInvite"
	}
	evidence := fmt.Sprintf(
		"# /restart live — %s\n\n- stary pokój: %s (%d events in its log)\n- nowy pokój: %s (%d events)\n- roster: %s\n- widok TUI po resecie: %s\n\n## Log NOWEGO pokoju (fragment, z gatewaya)\n\n```\n%s```\n\n## Pierwsza wiadomość (skrót, bez bloku kontekstu)\n\n```\n%s\n```\n",
		time.Now().Format("2006-01-02 15:04:05"),
		oldID, oldEvents, newID, len(page.Events), rosterLabel(*m.openRoom),
		subLabel, liveLogText(page), first)
	if path := os.Getenv("ROOMS_EVIDENCE"); path != "" {
		if err := os.WriteFile(path, []byte(evidence), 0o644); err != nil {
			t.Fatalf("write evidence: %v", err)
		}
		t.Logf("evidence written to %s", path)
	}
	t.Logf("new room log:\n%s", liveLogText(page))
	t.Logf("first message (%d lines):", strings.Count(first, "\n")+1)
	t.Logf("%s", first)
}

// stopLiveRoom cancels whatever turns a live room has started. A proof does
// not leave the operator's roster working.
func stopLiveRoom(t *testing.T, url, token, roomID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := rooms.DialBaseURL(ctx, url, token)
	if err != nil {
		t.Logf("dial to stop %s: %v", roomID, err)
		return
	}
	defer c.Close()
	if n, err := c.Stop(ctx, roomID); err != nil {
		t.Logf("stop %s: %v (kept going, not fatal for the proof)", roomID, err)
	} else if n > 0 {
		t.Logf("stopped %d turn(s) in %s", n, roomID)
	}
}

// liveRoomLog reads a room's log straight from the gateway.
func liveRoomLog(t *testing.T, url, token, roomID string) rooms.LogPage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := rooms.DialBaseURL(ctx, url, token)
	if err != nil {
		t.Fatalf("dial for %s log: %v", roomID, err)
	}
	defer c.Close()
	page, err := c.Log(ctx, roomID, 0, 50)
	if err != nil {
		t.Fatalf("log of %s: %v", roomID, err)
	}
	return page
}

// liveLogText renders a log page the way the evidence file quotes it.
func liveLogText(page rooms.LogPage) string {
	var b strings.Builder
	for _, ev := range page.Events {
		text := strings.Join(strings.Fields(ev.Text()), " ")
		if len(text) > 160 {
			text = text[:159] + "…"
		}
		fmt.Fprintf(&b, "[%d] %-18s %s %s\n", ev.Seq, ev.Kind, ev.Actor.Profile, text)
	}
	return b.String()
}
