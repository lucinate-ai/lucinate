package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/a3tai/openclaw-go/protocol"
	"github.com/charmbracelet/x/ansi"
)

// The agent picker is the first screen after a connection, so the rooms
// surface has to be one keystroke from there — but only on a connection that
// actually hosts rooms, and never by navigating on the owner's behalf.

func roomsBackend() *fakeBackend {
	b := newFakeBackend()
	b.caps.Rooms = true
	return b
}

func pickerWithRooms(t *testing.T, agents ...protocol.AgentSummary) selectModel {
	t.Helper()
	m := newSelectModel(roomsBackend(), false, true, nil, false, "")
	if len(agents) == 0 {
		agents = []protocol.AgentSummary{{ID: "matt", Name: "Matt"}, {ID: "kowal", Name: "Kowal"}}
	}
	return loadAgents(m, agents...)
}

func TestSelectModel_RoomsRowLeadsThePicker(t *testing.T) {
	m := pickerWithRooms(t)

	items := m.list.Items()
	if len(items) == 0 {
		t.Fatal("the picker has no rows")
	}
	row, ok := items[0].(roomsItem)
	if !ok {
		t.Fatalf("first row is %T, want the rooms entry above the profiles", items[0])
	}
	if row.label != defaultRoomsLabel {
		t.Errorf("rooms row label = %q, want %q", row.label, defaultRoomsLabel)
	}

	view := ansi.Strip(m.View())
	if !strings.Contains(view, defaultRoomsLabel) {
		t.Errorf("picker view does not render the rooms row:\n%s", view)
	}
	// The row is advertised, not just present.
	hint := renderActionHints(m.Actions())
	if !strings.Contains(hint, "r: rooms") {
		t.Errorf("hint line does not advertise r: rooms: %q", hint)
	}
}

func TestSelectModel_NoRoomsRowWithoutTheCapability(t *testing.T) {
	b := newFakeBackend() // caps.Rooms false
	m := loadAgents(newSelectModel(b, false, true, nil, false, ""),
		protocol.AgentSummary{ID: "matt", Name: "Matt"},
		protocol.AgentSummary{ID: "kowal", Name: "Kowal"},
	)

	if _, ok := m.list.Items()[0].(roomsItem); ok {
		t.Fatal("a connection without rooms must not carry a rooms row")
	}
	if view := ansi.Strip(m.View()); strings.Contains(view, defaultRoomsLabel) {
		t.Errorf("picker advertises rooms on a backend that has none:\n%s", view)
	}
	for _, a := range m.Actions() {
		if a.ID == "view-rooms" {
			t.Fatal("a backend without rooms must not expose the rooms action")
		}
	}
}

func TestSelectModel_EnterOnRoomsRowOpensTheRoomList(t *testing.T) {
	m := pickerWithRooms(t)
	if _, ok := m.list.SelectedItem().(roomsItem); !ok {
		t.Fatal("test setup: the cursor should start on the rooms row")
	}

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on the rooms row armed no command")
	}
	if _, ok := cmd().(showRoomsMsg); !ok {
		t.Fatalf("enter on the rooms row produced %T, want showRoomsMsg", cmd())
	}
	if next.selected || next.selecting {
		t.Error("the rooms row must not be treated as picking an agent")
	}
}

func TestSelectModel_RKeyOpensTheRoomList(t *testing.T) {
	m := pickerWithRooms(t)

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'r'})
	if cmd == nil {
		t.Fatal("r armed no command")
	}
	if _, ok := cmd().(showRoomsMsg); !ok {
		t.Fatalf("r produced %T, want showRoomsMsg", cmd())
	}
	if next.selected {
		t.Error("r must not pick an agent")
	}
}

// The guard is the one /rooms runs: no endpoint, no view — and a reason.
func TestSelectModel_ViewRoomsRefusedWithoutTheCapability(t *testing.T) {
	m := newSelectModel(newFakeBackend(), false, true, nil, false, "")
	m.loading = false

	next, cmd := m.TriggerAction("view-rooms")
	if cmd != nil {
		t.Fatalf("a rooms-less backend opened the rooms view (%T)", cmd())
	}
	if next.err == nil || !strings.Contains(next.err.Error(), "Hermes gateway") {
		t.Fatalf("err = %v, want a reason naming the Hermes gateway", next.err)
	}
}

func TestSelectModel_RoomsRowCarriesTheLoadedCount(t *testing.T) {
	m := pickerWithRooms(t)
	m.roomCount = 3
	if got := m.roomsLabel(); !strings.HasSuffix(got, "· 3 rooms") {
		t.Errorf("label = %q, want the live count", got)
	}
	m.roomCount = 1
	if got := m.roomsLabel(); !strings.HasSuffix(got, "· 1 room") {
		t.Errorf("label = %q, want the singular", got)
	}
	m.roomCount = -1
	if got := m.roomsLabel(); got != defaultRoomsLabel {
		t.Errorf("label = %q, want no count before the rooms list has loaded", got)
	}
}

// One profile plus rooms is still a choice: the picker must not auto-enter
// chat and hide the rooms surface.
func TestSelectModel_SingleAgentWithRoomsKeepsThePicker(t *testing.T) {
	m := pickerWithRooms(t, protocol.AgentSummary{ID: "matt", Name: "Matt"})

	if m.selected {
		t.Fatal("the picker skipped past the rooms row into chat")
	}
	if _, ok := m.list.Items()[0].(roomsItem); !ok {
		t.Fatal("the rooms row must stay first")
	}
}

// Without rooms the one-agent pass-through is unchanged.
func TestSelectModel_SingleAgentWithoutRoomsStillAutoSelects(t *testing.T) {
	m := loadAgents(newSelectModel(newFakeBackend(), false, true, nil, false, ""),
		protocol.AgentSummary{ID: "matt", Name: "Matt"},
	)
	if !m.selected {
		t.Fatal("a rooms-less single-agent connection should still auto-select")
	}
}

// The rooms row must not shift the agent indices: moving down one row and
// pressing enter picks the first profile.
func TestSelectModel_DownFromRoomsPicksTheFirstProfile(t *testing.T) {
	m := pickerWithRooms(t)

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		if _, ok := cmd().(showRoomsMsg); ok {
			t.Fatal("enter below the rooms row reopened rooms")
		}
	}
	item, ok := next.selectedAgent()
	if !ok || item.agent.ID != "matt" {
		t.Fatalf("selected agent = %+v ok=%v, want the first profile", item.agent, ok)
	}
	if !next.selecting {
		t.Error("picking a profile should freeze the picker while the session opens")
	}
}
