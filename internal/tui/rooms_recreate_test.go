package tui

import (
	"strings"
	"testing"

	"github.com/lucinate-ai/lucinate/internal/rooms"
	tg "github.com/lucinate-ai/lucinate/internal/testgateway"
)

// Recreating a room with the roster you just disbanded, and starting a
// predefined room, are the same failure the operator reported twice:
//
//	disbanded id → 4110 "room_id belongs to a disbanded room"    (repro A3)
//	preset's id   → 4110 "room_id belongs to a disbanded room"   (repro B1)
//
// The roster was never lost — the id was. These pin the fix: the id gets
// replaced, the roster rides along, and a preset follows the room it starts.

func TestRoomsCreate_SameRosterAfterADisbandStartsANewRoom(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	// What the gateway answers for this roster's slug once its room has been
	// disbanded.
	s.SetRoomIDConflict("room_id belongs to a disbanded room", "kowal-matt", "matt-kowal")

	m := w1GatewayModel(t, s)
	m.sub = roomsInvite
	m.selected = map[string]bool{"matt": true, "kowal": true}

	cmd := m.createRoom()
	if cmd == nil {
		t.Fatal("createRoom armed no command")
	}
	m = feedCmd(t, m, cmd)
	if m.err != nil {
		t.Fatalf("create with a tombstoned slug: %v", m.err)
	}
	if m.openRoomID == "" || m.openRoomID == "kowal-matt" || m.openRoomID == "matt-kowal" {
		t.Fatalf("open room = %q, want a minted id instead of the tombstoned one", m.openRoomID)
	}
	if !strings.Contains(m.openRoomID, "kowal-matt") && !strings.Contains(m.openRoomID, "matt-kowal") {
		t.Errorf("minted id = %q, want it derived from the roster's name", m.openRoomID)
	}
	requireRoomDir(t, m.openRoomID)
	for _, handle := range []string{"matt", "kowal"} {
		if _, ok := s.MemberRow(handle); !ok {
			t.Errorf("roster member %q did not make it into the new room", handle)
		}
	}
}

func TestRoomsCreateFromPreset_MintedIDRepointsThePreset(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	s.SetRoomIDConflict("room_id belongs to a disbanded room", "sztab")

	m := w1GatewayModel(t, s)
	path, err := rooms.DefaultPresetsPath()
	if err != nil {
		t.Fatalf("DefaultPresetsPath: %v", err)
	}
	store := rooms.PresetStore{}
	if err := store.Add(rooms.Preset{Name: "Sztab", RoomID: "sztab", ThreadID: "main",
		Members: rooms.RosterFor([]string{"matt", "kowal"})}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := rooms.SavePresets(path, store); err != nil {
		t.Fatalf("SavePresets: %v", err)
	}

	cmd := m.createFromPreset(store.Presets[0])
	if cmd == nil {
		t.Fatal("createFromPreset armed no command")
	}
	m = feedCmd(t, m, cmd)
	if m.err != nil {
		t.Fatalf("starting a predefined room whose id is gone: %v", m.err)
	}
	if m.openRoomID == "" || m.openRoomID == "sztab" {
		t.Fatalf("open room = %q, want the minted id", m.openRoomID)
	}
	requireRoomDir(t, m.openRoomID)

	again, err := rooms.LoadPresets(path)
	if err != nil {
		t.Fatalf("LoadPresets: %v", err)
	}
	preset := again.Find("Sztab")
	if preset == nil {
		t.Fatal("the preset disappeared")
	}
	if preset.RoomID != m.openRoomID {
		t.Errorf("preset points at %q, want the room it started (%q)", preset.RoomID, m.openRoomID)
	}
	if len(preset.Members) != 2 {
		t.Errorf("preset roster = %d members, want the 2 it was saved with", len(preset.Members))
	}
	if m.notice == "" || !strings.Contains(m.notice, "points at room") {
		t.Errorf("notice = %q, want the re-pointed preset reported", m.notice)
	}

	// The point of the fix: starting it a second time must adopt the room
	// that now exists instead of minting another one.
	before := m.openRoomID
	cmd = m.createFromPreset(*preset)
	m = feedCmd(t, m, cmd)
	if m.err != nil {
		t.Fatalf("second start: %v", m.err)
	}
	if m.openRoomID != before {
		t.Errorf("second start opened %q, want an adopt of %q", m.openRoomID, before)
	}
}
