package rooms

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucinate-ai/lucinate/internal/backend/hermes/rpc"
	tg "github.com/lucinate-ai/lucinate/internal/testgateway"
)

// ── /restart, and creating against an id the gateway refuses ─────────────────
//
// The two operator reports share one root cause, measured on the live gateway
// (hermes-local, 2026-09-28):
//
//	disbanded id, same roster  → 4110 "room_id belongs to a disbanded room"
//	live id, different roster  → 4110 "room_id already exists with different state"
//	live id, identical roster  → success (idempotent re-adopt)
//
// So "disband a room, then start the same roster again" and "start a
// predefined room whose id was disbanded" both died at groups.create, before
// any roster or preset logic could matter.

func TestCreate_MintsAFreshIDWhenTheGatewayRefusesTheId(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	s.SetRoomIDConflict("room_id belongs to a disbanded room", "sztab")
	c := dialGateway(t, s)
	ctx := context.Background()
	want := RosterFor([]string{"matt", "kowal"})

	room, err := c.Create(ctx, "sztab", "Sztab", want)
	if err != nil {
		t.Fatalf("Create with a refused id: %v", err)
	}
	if room.RoomID == "sztab" {
		t.Fatal("Create echoed the refused id — nothing was created")
	}
	if !strings.HasPrefix(room.RoomID, "sztab-") {
		t.Errorf("fresh id = %q, want it derived from the requested id (sztab-…)", room.RoomID)
	}
	if len(room.Members) != len(want) {
		t.Errorf("fresh roster = %d members, want the same %d", len(room.Members), len(want))
	}
	if _, ok := s.MemberRow("kowal"); !ok {
		t.Error("the roster did not reach the gateway under the minted id")
	}

	// The minted id must be reusable: the next start adopts it rather than
	// minting yet another room.
	again, err := c.Create(ctx, room.RoomID, "Sztab", want)
	if err != nil {
		t.Fatalf("re-Create with the minted id: %v", err)
	}
	if again.RoomID != room.RoomID {
		t.Errorf("re-Create minted %q, want an adopt of %q", again.RoomID, room.RoomID)
	}
	if n := s.CreateCalls(); n != 3 {
		t.Errorf("groups.create calls = %d, want 3 (refused, minted, adopted)", n)
	}
}

func TestCreate_GivesUpAfterTheAttemptBudget(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	s.SetRoomIDConflictAll("room_id belongs to a disbanded room")
	c := dialGateway(t, s)

	_, err := c.Create(context.Background(), "sztab", "Sztab", RosterFor([]string{"matt", "kowal"}))
	if err == nil {
		t.Fatal("Create against a gateway that refuses every id = nil error, want the refusal surfaced")
	}
	if !IsRoomIDConflict(err) {
		t.Errorf("err = %v, want the room-id conflict itself, not a wrapped surprise", err)
	}
	if n := s.CreateCalls(); n != 1+maxCreateAttempts {
		t.Errorf("groups.create calls = %d, want exactly %d (first try plus the budget)", n, 1+maxCreateAttempts)
	}
	seen := map[string]bool{}
	for _, id := range s.CreatedIDs() {
		if seen[id] {
			t.Errorf("the retry loop asked for %q twice — it would spin", id)
		}
		seen[id] = true
	}
}

func TestIsRoomIDConflict_MatchesOnlyTheGatewayConflictCode(t *testing.T) {
	if !IsRoomIDConflict(&rpc.RPCError{Code: RoomIDConflictCode, Message: "room_id belongs to a disbanded room"}) {
		t.Error("code 4110 must read as a room-id conflict")
	}
	if IsRoomIDConflict(&rpc.RPCError{Code: 4001, Message: "roster is invalid"}) {
		t.Error("a roster refusal must not be retried with another id")
	}
	if IsRoomIDConflict(errors.New("read tcp: connection reset")) {
		t.Error("a transport error must not be retried with another id")
	}
}

func TestFreshRoomID_StaysARoomID(t *testing.T) {
	got := FreshRoomID("kowal-matt")
	if got == "kowal-matt" || !strings.HasPrefix(got, "kowal-matt-") {
		t.Fatalf("FreshRoomID(kowal-matt) = %q, want kowal-matt-<time>", got)
	}
	if empty := FreshRoomID(""); empty == "" || strings.HasPrefix(empty, "-") {
		t.Errorf("FreshRoomID(\"\") = %q, want a usable id", empty)
	}
	if tr := FreshRoomID("a-b-"); strings.HasSuffix(tr, "-") || strings.Contains(tr, "--") {
		t.Errorf("FreshRoomID(a-b-) = %q, want no trailing or doubled dash", tr)
	}
	if long := FreshRoomID(strings.Repeat("x", 200)); len(long) > 128 {
		t.Errorf("FreshRoomID of an over-long base = %d chars, gateway limit is 128", len(long))
	}
	// Two mints inside one second share the suffix; the retry loop, not the
	// clock, is what keeps attempts apart (see the budget test).
	if same := FreshRoomID("kowal-matt"); same != got {
		t.Logf("FreshRoomID crossed a second boundary: %q then %q", got, same)
	}
}

// ── presets follow the room they actually start ──────────────────────────────

func TestUpdatePresetRoomID_RepointsThePresetAndKeepsTheRoster(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rooms.json")
	store := PresetStore{}
	if err := store.Add(Preset{Name: "Sztab", RoomID: "sztab", ThreadID: "main",
		Members: []Member{member("m1", "matt", "matt"), member("m2", "kowal", "kowal")}}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := SavePresets(path, store); err != nil {
		t.Fatalf("SavePresets: %v", err)
	}

	changed, err := UpdatePresetRoomID(path, "Sztab", "sztab-190530")
	if err != nil {
		t.Fatalf("UpdatePresetRoomID: %v", err)
	}
	if !changed {
		t.Fatal("UpdatePresetRoomID changed nothing, want the room id repointed")
	}
	again, err := LoadPresets(path)
	if err != nil {
		t.Fatalf("LoadPresets: %v", err)
	}
	got := again.Find("sztab-190530")
	if got == nil {
		got = again.Find("Sztab")
	}
	if got == nil || got.RoomID != "sztab-190530" {
		t.Fatalf("preset after update = %+v, want RoomID sztab-190530", got)
	}
	if len(got.Members) != 2 {
		t.Errorf("roster after update = %d members, want the 2 it had", len(got.Members))
	}
}

func TestUpdatePresetRoomID_NoOpsWhenNothingNeedsChanging(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rooms.json")

	if changed, err := UpdatePresetRoomID(path, "Sztab", "sztab-190530"); err != nil || changed {
		t.Errorf("missing file: (changed, err) = (%v, %v), want (false, nil)", changed, err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a missing store must stay missing — the update writes nothing on a no-op")
	}

	store := PresetStore{}
	if err := store.Add(Preset{Name: "Sztab", RoomID: "sztab",
		Members: []Member{member("m1", "matt", "matt"), member("m2", "kowal", "kowal")}}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := SavePresets(path, store); err != nil {
		t.Fatalf("SavePresets: %v", err)
	}
	if changed, err := UpdatePresetRoomID(path, "Sztab", "sztab"); err != nil || changed {
		t.Errorf("same id: (changed, err) = (%v, %v), want (false, nil)", changed, err)
	}
	if changed, err := UpdatePresetRoomID(path, "nie-ma-go", "whatever"); err != nil || changed {
		t.Errorf("unknown preset: (changed, err) = (%v, %v), want (false, nil)", changed, err)
	}
}

// ── the hand-over block a restart posts ──────────────────────────────────────

func TestRestartSummary_CarriesOldRoomStateWithinTheLineBudget(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "INDEX.md"), []byte("stan prac"), 0o644); err != nil {
		t.Fatalf("write INDEX: %v", err)
	}
	var events []Event
	for i := 1; i <= 40; i++ {
		ev := Event{RoomID: "stary-pokoj", Seq: i, Kind: "message.user",
			Payload: map[string]any{"text": "wiadomość numer " + itoaSmall(i)}}
		if i%2 == 0 {
			ev.Kind = "message.member"
			ev.Actor = Actor{Kind: "member", Profile: "matt", DisplayName: "matt"}
			ev.Payload = map[string]any{"text": "decyzja: trzymamy " + itoaSmall(i)}
		}
		events = append(events, ev)
	}

	out := RestartSummary("stary-pokoj", "Stary pokój", dir, events)
	if lines := strings.Split(out, "\n"); len(lines) > MaxRestartSummaryLines {
		t.Errorf("summary = %d lines, budget is %d", len(lines), MaxRestartSummaryLines)
	}
	for _, want := range []string{"stary-pokoj", dir, "INDEX.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary does not carry %q:\n%s", want, out)
		}
	}
	// The digest quotes the transcript instead of interpreting it.
	if !strings.Contains(out, "wiadomość numer") || !strings.Contains(out, "decyzja") {
		t.Errorf("summary has no digest of the old transcript:\n%s", out)
	}
}

func TestRestartSummary_IsUsableWithoutHistoryOrDirectory(t *testing.T) {
	out := RestartSummary("stary-pokoj", "", "", nil)
	if strings.TrimSpace(out) == "" {
		t.Fatal("an empty history must still produce a hand-over block")
	}
	if !strings.Contains(out, "stary-pokoj") {
		t.Errorf("summary does not name the old room:\n%s", out)
	}
	if lines := strings.Split(out, "\n"); len(lines) > MaxRestartSummaryLines {
		t.Errorf("summary = %d lines, budget is %d", len(lines), MaxRestartSummaryLines)
	}
}
