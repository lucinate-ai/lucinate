package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucinate-ai/lucinate/internal/rooms"
	tg "github.com/lucinate-ai/lucinate/internal/testgateway"
)

func stateWith(room rooms.Room, pending ...rooms.PendingAction) rooms.StateResult {
	return rooms.StateResult{
		Room:         room,
		DriverStatus: &rooms.DriverStatus{PendingActions: pending},
	}
}

func roomWith(handles ...string) rooms.Room {
	return rooms.Room{RoomID: "sztab", Members: rooms.RosterFor(handles)}
}

func TestPendingApprovalFor_PicksTheMembersAction(t *testing.T) {
	state := stateWith(roomWith("matt", "kowal"), rooms.PendingAction{
		Kind: "approval", MemberID: "m2", TaskID: "task-kowal",
		ExecutionGeneration: 3, RequestID: "req-7",
	})
	action, err := pendingApprovalFor(state, "@kowal")
	if err != nil {
		t.Fatalf("pendingApprovalFor: %v", err)
	}
	if action.TaskID != "task-kowal" || action.ExecutionGeneration != 3 || action.RequestID != "req-7" {
		t.Errorf("action = %+v, want kowal's exact coordinates", action)
	}
}

func TestPendingApprovalFor_Refusals(t *testing.T) {
	tests := []struct {
		name   string
		handle string
		state  rooms.StateResult
		want   string
	}{
		{
			name:   "nothing pending",
			handle: "@kowal",
			state:  stateWith(roomWith("matt", "kowal")),
			want:   "no pending action",
		},
		{
			name:   "unknown member",
			handle: "@ghost",
			state:  stateWith(roomWith("matt", "kowal")),
			want:   "no member @ghost",
		},
		{
			name:   "a retry is not an approval",
			handle: "@kowal",
			state: stateWith(roomWith("matt", "kowal"), rooms.PendingAction{
				Kind: "retry", MemberID: "m2", TaskID: "task-kowal",
			}),
			want: "not an approval",
		},
		{
			name:   "ambiguous",
			handle: "@kowal",
			state: stateWith(roomWith("matt", "kowal"),
				rooms.PendingAction{Kind: "approval", MemberID: "m2", TaskID: "a", RequestID: "r1"},
				rooms.PendingAction{Kind: "approval", MemberID: "m2", TaskID: "b", RequestID: "r2"}),
			want: "2 pending actions",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pendingApprovalFor(tc.state, tc.handle); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPendingRetry_FindsTheRetryableTask(t *testing.T) {
	state := stateWith(roomWith("matt", "kowal"),
		rooms.PendingAction{Kind: "approval", MemberID: "m2", TaskID: "task-kowal"},
		rooms.PendingAction{Kind: "retry", MemberID: "m1", TaskID: "task-matt"},
	)
	action, err := pendingRetry(state)
	if err != nil {
		t.Fatalf("pendingRetry: %v", err)
	}
	if action.TaskID != "task-matt" {
		t.Errorf("task = %q, want task-matt", action.TaskID)
	}

	if _, err := pendingRetry(stateWith(roomWith("matt", "kowal"))); err == nil {
		t.Error("a room with no retryable task should be refused")
	}
}

// writeCLIConnection seeds the lucinate data dir with one saved Hermes
// connection, so a rooms subcommand can dial the fake gateway.
func writeCLIConnection(t *testing.T, url string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LUCINATE_DATA_DIR", dir)
	conn := map[string]any{
		"connections": []map[string]any{{
			"id": "probe", "name": "probe", "type": "hermes", "url": url,
			"createdAt": "2026-11-03T00:00:00Z", "lastUsed": "2026-11-03T00:00:00Z",
		}},
		"defaultId": "probe",
	}
	blob, _ := json.Marshal(conn)
	if err := os.WriteFile(filepath.Join(dir, "connections.json"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	secrets, _ := json.Marshal(map[string]any{"apiKeys": map[string]string{"probe": ""}})
	if err := os.WriteFile(filepath.Join(dir, "secrets", "secrets.json"), secrets, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRoomsApprove_ResolvesTheActionOnTheGateway(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	s.SetPendingApproval("kowal", "req-7")
	writeCLIConnection(t, s.URL())

	var out bytes.Buffer
	err := roomsApprove(context.Background(), []string{"sztab", "@kowal", "once"}, &out)
	if err != nil {
		t.Fatalf("roomsApprove: %v", err)
	}
	if got := s.ApproveCalls(); len(got) != 1 || got[0] != "kowal=once" {
		t.Fatalf("approve calls = %v, want kowal=once", got)
	}
	if !strings.Contains(out.String(), "approved @kowal (once)") {
		t.Errorf("out = %q", out.String())
	}
}

func TestRoomsApprove_RefusesWhenNothingIsPending(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	writeCLIConnection(t, s.URL())

	err := roomsApprove(context.Background(), []string{"sztab", "@kowal", "once"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no pending action") {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if got := s.ApproveCalls(); len(got) != 0 {
		t.Fatalf("approve calls = %v, want none", got)
	}
}

func TestRoomsRetry_RetriesTheTask(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	s.SetPendingRetry("matt")
	writeCLIConnection(t, s.URL())

	var out bytes.Buffer
	if err := roomsRetry(context.Background(), []string{"sztab"}, &out); err != nil {
		t.Fatalf("roomsRetry: %v", err)
	}
	if got := s.RetryCalls(); len(got) != 1 || got[0] != "matt" {
		t.Fatalf("retry calls = %v, want matt", got)
	}
	if !strings.Contains(out.String(), "retried task-matt") {
		t.Errorf("out = %q", out.String())
	}
}

func TestRoomsRename_SendsTheNewName(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	writeCLIConnection(t, s.URL())

	var out bytes.Buffer
	if err := roomsRename(context.Background(), []string{"sztab", "Nowa", "Sztab"}, &out); err != nil {
		t.Fatalf("roomsRename: %v", err)
	}
	for _, c := range s.Calls() {
		if c == "groups.rename" {
			if !strings.Contains(out.String(), "Nowa Sztab") {
				t.Errorf("out = %q, want the joined name", out.String())
			}
			return
		}
	}
	t.Fatalf("groups.rename was not called; calls = %v", s.Calls())
}

func TestActiveHandles_ResolvesMemberIDsToHandles(t *testing.T) {
	state := rooms.StateResult{
		Room: roomWith("matt", "kowal"),
		DriverStatus: &rooms.DriverStatus{MemberActivity: []rooms.MemberActivity{
			{MemberID: "m2", Status: "running"},
			{MemberID: "m9", Status: "running"},
		}},
	}
	got := activeHandles(state)
	if strings.Join(got, ",") != "@kowal,@m9" {
		t.Errorf("active = %v, want @kowal and the unresolved id", got)
	}
	if activeHandles(rooms.StateResult{}) != nil {
		t.Error("a state without a driver should report nothing")
	}
}
