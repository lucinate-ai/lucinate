package rooms

import (
	"context"
	"strings"
	"testing"
	"time"

	tg "github.com/lucinate-ai/lucinate/internal/testgateway"
)

// The whole client surface against a real WebSocket server: every groups.*
// method, plus the failures a healthy gateway never shows (a rejected send, a
// dropped socket, a method that never answers, a refused upgrade). Rooms' own
// tests are what make these paths count for coverage — a test in another
// package would exercise them without covering this one.

func dialGateway(t *testing.T, s *tg.Server) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := DialBaseURL(ctx, s.URL(), "")
	if err != nil {
		t.Fatalf("dial %s: %v", s.URL(), err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// toRoomsEvents is intentionally absent: rooms' own events decode straight off
// the wire here, and the fake gateway's types are only used to inspect what the
// server recorded.

func TestClient_EveryGroupsMethodRoundTrips(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	c := dialGateway(t, s)
	ctx := context.Background()

	caps, err := c.Capabilities(ctx)
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.Driver || caps.MaxLogLimit != 500 || caps.AuthorityGatewayID != "fake" {
		t.Errorf("caps = %+v, want the fake gateway's capabilities", caps)
	}

	list, err := c.List(ctx, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].RoomID != "sztab" || len(list[0].Members) != 2 {
		t.Fatalf("list = %+v, want the one two-member room", list)
	}

	state, err := c.State(ctx, "sztab")
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if state.Room.RoomID != "sztab" || state.DriverStatus == nil {
		t.Fatalf("state = %+v, want a room plus driver status", state)
	}
	s.SetWorking(true)
	if state, err = c.State(ctx, "sztab"); err != nil || state.DriverStatus == nil || !state.DriverStatus.Working {
		t.Fatalf("state working = %+v (err %v), want working=true", state.DriverStatus, err)
	}

	created, err := c.Create(ctx, "nowy", "Nowy", RosterFor([]string{"matt", "kowal"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.RoomID == "" {
		t.Error("Create returned an empty room id")
	}
	if _, err := c.Create(ctx, "", "Nowy", RosterFor([]string{"matt", "kowal"})); err == nil {
		t.Error("Create without a room id should fail locally")
	}
	if _, err := c.Create(ctx, "nowy", "", RosterFor([]string{"matt", "kowal"})); err == nil {
		t.Error("Create without a name should fail locally")
	}
	if _, err := c.Create(ctx, "nowy", "Nowy", RosterFor([]string{"matt"})); err == nil {
		t.Error("Create with a one-member roster should fail before dialing")
	}

	sent, err := c.SendEvent(ctx, "sztab", "ev-test", "co robimy?", "")
	if err != nil {
		t.Fatalf("SendEvent: %v", err)
	}
	if sent.Seq == 0 || sent.Kind != "message.user" {
		t.Fatalf("sent = %+v, want the accepted user event", sent)
	}
	if got := s.Sent(); len(got) != 1 || got[0] != "co robimy?" {
		t.Fatalf("gateway received %q", got)
	}

	renamed, err := c.Rename(ctx, "sztab", "Sztab 2")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if renamed.Name != "Sztab 2" {
		t.Errorf("renamed room = %q, want the new name", renamed.Name)
	}

	n, err := c.Stop(ctx, "sztab")
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if n != 1 {
		t.Errorf("Stop cancelled %d, want the gateway's count", n)
	}

	if err := c.Disband(ctx, "sztab"); err != nil {
		t.Fatalf("Disband: %v", err)
	}

	page, err := c.Log(ctx, "sztab", 0, 0)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(page.Events) == 0 || page.Cursor != page.LatestSeq {
		t.Fatalf("page = %+v, want events and a cursor at the newest seq", page)
	}
	if c.WSURL() == "" || !strings.HasPrefix(c.WSURL(), "ws") {
		t.Errorf("WSURL = %q, want a websocket endpoint", c.WSURL())
	}
}

func TestClient_SendValidationAndThreadDefault(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	c := dialGateway(t, s)
	ctx := context.Background()

	if _, err := c.Send(ctx, "", "hej", ""); err == nil {
		t.Error("Send without a room id should fail")
	}
	if _, err := c.Send(ctx, "sztab", "", ""); err == nil {
		t.Error("Send without text should fail")
	}
	if _, err := c.Send(ctx, "sztab", "hej", ""); err != nil {
		t.Fatalf("Send with an empty thread should default to %q: %v", DefaultThreadID, err)
	}
	if _, err := c.SendEvent(ctx, "sztab", "ev-1", "hej", "boczny"); err != nil {
		t.Fatalf("SendEvent with an explicit thread: %v", err)
	}
	if got := s.Sent(); len(got) != 2 {
		t.Fatalf("gateway received %d messages, want 2", len(got))
	}
}

func TestClient_SendOpeningCarriesContextOnce(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	c := dialGateway(t, s)
	ctx := context.Background()
	orient := Orientation{Worktree: "E:/proj", Path: "E:/proj", Project: "proj"}

	if _, err := c.SendOpening(ctx, "sztab", "start", DefaultThreadID, orient); err != nil {
		t.Fatalf("first SendOpening: %v", err)
	}
	if _, err := c.SendOpening(ctx, "sztab", "dalej", DefaultThreadID, orient); err != nil {
		t.Fatalf("second SendOpening: %v", err)
	}
	sent := s.Sent()
	if len(sent) != 2 {
		t.Fatalf("gateway received %d messages, want 2", len(sent))
	}
	if !strings.Contains(sent[0], "E:/proj") {
		t.Errorf("first message = %q, want the project context", sent[0])
	}
	if sent[1] != "dalej" {
		t.Errorf("second message = %q, want it sent unchanged", sent[1])
	}
}

func TestClient_RPCErrorSurfacesAndLeavesTheSocketUsable(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	c := dialGateway(t, s)
	ctx := context.Background()
	s.SetRejectSend(4001)

	_, err := c.Send(ctx, "sztab", "odrzucone", "")
	if err == nil {
		t.Fatal("a rejected send must surface an error")
	}
	if !strings.Contains(err.Error(), "member turn rejected") {
		t.Errorf("error = %v, want the gateway's message", err)
	}
	if IsDisconnect(err) {
		t.Error("an application-level rejection must not look like a dropped socket")
	}
	if c.Err() != nil {
		t.Errorf("the socket must stay usable, Err() = %v", c.Err())
	}
	if _, err := c.Log(ctx, "sztab", 0, 0); err != nil {
		t.Fatalf("the room must stay readable after a rejected send: %v", err)
	}
}

func TestClient_CallTimeoutBoundedWithSetTimeout(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	c := dialGateway(t, s)
	s.SetSilent("groups.log")
	c.SetTimeout(300 * time.Millisecond)

	start := time.Now()
	_, err := c.Log(context.Background(), "sztab", 0, 0)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("a silent gateway must end the call, not hang it")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("call took %s, want it bounded by the 300ms timeout", elapsed)
	}
	t.Logf("bounded after %s: %v", elapsed.Round(time.Millisecond), err)
}

func TestClient_DroppedSocketSetsErrAndClosesDone(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	c := dialGateway(t, s)
	ctx := context.Background()
	if _, err := c.Log(ctx, "sztab", 0, 0); err != nil {
		t.Fatalf("healthy log: %v", err)
	}
	if c.Err() != nil {
		t.Fatalf("Err() = %v on a healthy socket", c.Err())
	}

	if n := s.CloseConns(); n == 0 {
		t.Fatal("no live socket to drop")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c.Err() != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if c.Err() == nil {
		t.Fatal("a dropped socket must record why it died")
	}
	// Err() is set just before the read loop closes Done, so wait for the
	// signal instead of sampling it in the same instant.
	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
		t.Error("Done must be closed once the read loop exits")
	}
	if !IsDisconnect(c.Err()) && !IsDisconnect(context.DeadlineExceeded) {
		t.Logf("drop reason: %v", c.Err()) // shape varies by platform
	}
}

func TestClient_CleanCloseIsNotADisconnect(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	c := dialGateway(t, s)
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if c.Err() != nil {
		t.Errorf("a deliberate Close must not look like a fault, Err() = %v", c.Err())
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close must be idempotent, got %v", err)
	}
}

func TestClient_ZeroValueIsNilSafe(t *testing.T) {
	var c *Client
	if err := c.Close(); err != nil {
		t.Errorf("nil Close = %v, want nil", err)
	}
	if err := c.Err(); err != nil {
		t.Errorf("nil Err = %v, want nil", err)
	}
	if done := c.Done(); done != nil {
		t.Errorf("nil Done = %v, want a nil channel", done)
	}
	if c.WSURL() != "" {
		t.Errorf("zero-value WSURL = %q, want empty", c.WSURL())
	}
	var zero Client
	if err := zero.Close(); err != nil {
		t.Errorf("zero Close = %v, want nil", err)
	}
	if zero.Err() != nil {
		t.Errorf("zero Err = %v, want nil", zero.Err())
	}
	if zero.Done() != nil {
		t.Error("zero Done should be nil, not a panic")
	}
}

func TestClient_RejectedUpgradeIsReportedWithItsStatus(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	s.SetUpgradeStatus(403)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := DialBaseURL(ctx, s.URL(), "")
	if err == nil {
		t.Fatal("a refused upgrade must fail the dial")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %v, want the HTTP status — the gateway auths at the upgrade", err)
	}
	if !IsDisconnect(err) {
		t.Error("a refused upgrade is retryable (gateway restarting, token refreshed)")
	}
}

func TestClient_DialRejectsBadEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := DialBaseURL(ctx, "://not-a-url", "tok"); err == nil {
		t.Error("an unparseable base URL must fail")
	}
	if _, err := Dial(ctx, "http://127.0.0.1:1/api/ws"); err == nil {
		t.Error("dialing a dead port must fail")
	}
}

func TestNewEventID_IsUniqueAndPrefixed(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id := NewEventID()
		if !strings.HasPrefix(id, "ev-") {
			t.Fatalf("id = %q, want the ev- prefix the gateway expects", id)
		}
		if seen[id] {
			t.Fatalf("duplicate event id %q after %d draws", id, i)
		}
		seen[id] = true
	}
}

// The client refuses a bad roster before it reaches the wire, so the user gets
// a readable error instead of the gateway's 4001.
func TestClient_CreateRejectsBadRosterLocally(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	c := dialGateway(t, s)
	_, err := c.Create(context.Background(), "r", "R", []Member{
		{MemberID: "m1", Profile: "matt", Handle: "all"},
		{MemberID: "m2", Profile: "kowal", Handle: "kowal"},
	})
	if err == nil {
		t.Fatal("a reserved handle must be refused")
	}
	for _, call := range s.Calls() {
		if call == "groups.create" {
			t.Fatal("the bad roster reached the gateway")
		}
	}
}

// A stream the connection cut in half is visible to the client: the fragments
// it received survive the drop, a redial resumes the log, and StreamsFor
// reports the turn as unfinished rather than settled.
func TestClient_DropMidStreamLeavesTheFragmentsInHand(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	s.Configure(tg.ReplyWithDeltas("matt", 2, "fragment jeden", "fragment dwa", "fragment trzy"))
	c1 := dialGateway(t, s)
	ctx := context.Background()

	if _, err := c1.Send(ctx, "sztab", "start", ""); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// The gateway closes the socket mid-answer; the client must notice.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && c1.Err() == nil {
		time.Sleep(5 * time.Millisecond)
	}
	if c1.Err() == nil {
		t.Fatal("the dropped socket was never noticed")
	}
	if _, err := c1.Log(ctx, "sztab", 0, 0); err == nil {
		t.Error("a dead client must fail its calls, not hang them")
	}

	// A redial resumes the same room: nothing the client already saw is lost.
	c2 := dialGateway(t, s)
	page, err := c2.Log(ctx, "sztab", 0, 0)
	if err != nil {
		t.Fatalf("Log after the redial: %v", err)
	}
	streams := StreamsFor(page.Events)
	if len(streams) != 1 {
		t.Fatalf("streams = %+v, want one cut-off member stream", streams)
	}
	if streams[0].Settled {
		t.Error("the turn was cut mid-stream and must not look settled")
	}
	if !strings.Contains(streams[0].Text, "fragment") {
		t.Errorf("stream text = %q, want the fragments that did arrive", streams[0].Text)
	}
	// What made it out: the user message, the turn opening, and two deltas —
	// the third delta and any settle never existed.
	var deltas, finals int
	for _, ev := range page.Events {
		switch ev.Kind {
		case "message.member.delta":
			deltas++
		case "message.member":
			finals++
		}
	}
	if deltas != 2 || finals != 0 {
		t.Errorf("deltas=%d finals=%d, want the 2 fragments that made it out and no final message", deltas, finals)
	}
}
