package rooms

import (
	"context"
	"strings"
	"testing"

	tg "github.com/lucinate-ai/lucinate/internal/testgateway"
)

// The real gateway never writes text deltas into the room log (they would
// exhaust its byte budget), so a client's only live signal is the state poll's
// member_activity list. It must decode into the typed field, not a raw map.
func TestState_ReportsLiveMemberActivity(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	c := dialGateway(t, s)
	s.SetMemberActivity("matt", "kowal")

	state, err := c.State(context.Background(), "sztab")
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	ds := state.DriverStatus
	if ds == nil || len(ds.MemberActivity) != 2 {
		t.Fatalf("member activity = %+v, want both members", ds)
	}
	if ds.MemberActivity[0].MemberID == "" || ds.MemberActivity[0].Status != "running" {
		t.Errorf("activity[0] = %+v", ds.MemberActivity[0])
	}
	if !ds.Working {
		t.Error("a room with active members should report working")
	}
}

func TestState_DecodesAPendingApproval(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	c := dialGateway(t, s)
	s.SetPendingApproval("kowal", "req-7")

	state, err := c.State(context.Background(), "sztab")
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	ds := state.DriverStatus
	if ds == nil || !ds.Blocked || len(ds.PendingActions) != 1 {
		t.Fatalf("driver status = %+v, want one pending action and blocked", ds)
	}
	a := ds.PendingActions[0]
	if !a.IsApproval() || a.MemberID != "m2" || a.TaskID != "task-kowal" || a.RequestID != "req-7" {
		t.Errorf("action = %+v, want kowal's approval coordinates", a)
	}
}

func TestApprove_SendsTheExactCoordinates(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	c := dialGateway(t, s)
	s.SetPendingApproval("kowal", "req-7")
	ctx := context.Background()

	state, err := c.State(ctx, "sztab")
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if err := c.Approve(ctx, "sztab", state.DriverStatus.PendingActions[0], "once"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if got := s.ApproveCalls(); len(got) != 1 || got[0] != "kowal=once" {
		t.Fatalf("approve calls = %v, want kowal=once", got)
	}
	// The action is gone, so a second attempt must be refused by the gateway
	// rather than silently re-approving something that no longer exists.
	if err := c.Approve(ctx, "sztab", state.DriverStatus.PendingActions[0], "once"); err == nil {
		t.Fatal("a stale approval was accepted")
	}
}

func TestApprove_RefusesBeforeDiallingWhenTheActionIsNotAnApproval(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	c := dialGateway(t, s)
	s.SetPendingRetry("matt")

	state, err := c.State(context.Background(), "sztab")
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	action := state.DriverStatus.PendingActions[0]
	if err := c.Approve(context.Background(), "sztab", action, "once"); err == nil {
		t.Fatal("approving a retry action should be refused")
	}
	if got := s.ApproveCalls(); len(got) != 0 {
		t.Fatalf("approve calls = %v, want none", got)
	}
}

func TestRetry_SendsTheTaskCoordinates(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	c := dialGateway(t, s)
	s.SetPendingRetry("matt")
	ctx := context.Background()

	state, err := c.State(ctx, "sztab")
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if err := c.Retry(ctx, "sztab", state.DriverStatus.PendingActions[0]); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if got := s.RetryCalls(); len(got) != 1 || got[0] != "matt" {
		t.Fatalf("retry calls = %v, want matt", got)
	}
}

func TestStop_ReportsHowManyTurnsWereCancelled(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	c := dialGateway(t, s)

	n, err := c.Stop(context.Background(), "sztab")
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if n != 1 {
		t.Errorf("cancelled = %d, want the fake's one turn", n)
	}
}

func TestCapabilities_ReportsProtocolTwo(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	c := dialGateway(t, s)

	caps, err := c.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if caps.ProtocolVersion != 2 {
		t.Errorf("protocol = %d, want 2", caps.ProtocolVersion)
	}
	if !containsString(caps.Methods, "groups.approve") || !containsString(caps.Methods, "groups.retry") {
		t.Errorf("methods = %v, want approve and retry advertised", caps.Methods)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}
