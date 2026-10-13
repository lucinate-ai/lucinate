package tui

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/rooms"
	tg "github.com/lucinate-ai/lucinate/internal/testgateway"
)

// `/model` in a room: the member list, the filtered model list, the switch, and
// the two ways it can fail (a gateway with no switching method, and a gateway
// that refuses the model). The gateway here is the fake one, so a switch that
// "succeeds" has really crossed the wire.

// mdlModel builds a transcript model wired to a fake gateway with a two-member
// roster, plus a catalogue the picker can offer.
func mdlModel(t *testing.T, s *tg.Server) roomsModel {
	t.Helper()
	m := w1GatewayModel(t, s)
	m.sub = roomsTranscript
	m.width, m.height = 100, 30
	m.openRoomID = "sztab"
	room := liveRoom("sztab", "Sztab", "matt", "kowal")
	m.openRoom = &room
	return m
}

func mdlCatalogue(t *testing.T, s *tg.Server) *tg.Server {
	t.Helper()
	s.SetModelCatalogue("gpt-5", "gpt-5", "gpt-5-mini", "claude-sonnet-4", "llama-3.3-70b")
	return s
}

// mdlOpenModelStage runs `/model @handle` and loads the catalogue, which is the
// state every picker test starts from.
func mdlOpenModelStage(t *testing.T, m roomsModel, handle string) roomsModel {
	t.Helper()
	next, cmd, ok := m.handleRoomUXCommand("/model " + handle)
	if !ok {
		t.Fatal("/model was not handled by the rooms surface")
	}
	if !next.model.active {
		t.Fatal("the model picker did not open")
	}
	return feedCmd(t, next, cmd)
}

func TestRoomsModel_BareCommandListsMembers(t *testing.T) {
	s := mdlCatalogue(t, tg.New(t, tg.DefaultOptions()))
	s.SetMemberModelSupported(true)
	m := mdlModel(t, s)

	next, _, ok := m.handleRoomUXCommand("/model")
	if !ok {
		t.Fatal("/model was not handled")
	}
	if !next.model.active || next.model.stage != modelStageMember {
		t.Fatalf("picker state = %+v, want the member list open", next.model)
	}
	view := ansi.Strip(next.View())
	for _, want := range []string{"@matt", "@kowal", "profile default", "sesji pokoju"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}

	// Navigation walks the roster and stops at both ends.
	moved, _, _ := next.handleModelKey(tea.KeyPressMsg{}, "down")
	if moved.model.memberCursor != 1 {
		t.Fatalf("cursor = %d, want 1", moved.model.memberCursor)
	}
	moved, _, _ = moved.handleModelKey(tea.KeyPressMsg{}, "down")
	if moved.model.memberCursor != 1 {
		t.Error("the member cursor should stop at the last row")
	}
	back, _, _ := moved.handleModelKey(tea.KeyPressMsg{}, "up")
	if back.model.memberCursor != 0 {
		t.Error("up should walk back")
	}
	closed, _, _ := back.handleModelKey(tea.KeyPressMsg{}, "esc")
	if closed.model.active {
		t.Error("esc should close the overlay")
	}
}

func TestRoomsModel_EnterOpensTheModelListAndShowsTheCurrentModel(t *testing.T) {
	s := mdlCatalogue(t, tg.New(t, tg.DefaultOptions()))
	s.SetMemberModelSupported(true)
	m := mdlModel(t, s)

	next, _, _ := m.handleRoomUXCommand("/model")
	next, cmd, _ := next.handleModelKey(tea.KeyPressMsg{}, "enter")
	if next.model.stage != modelStageModel {
		t.Fatalf("stage = %v, want the model list", next.model.stage)
	}
	next = feedCmd(t, next, cmd)

	if len(next.model.catalogue) != 4 {
		t.Fatalf("catalogue = %v, want the gateway's four models", next.model.catalogue)
	}
	if next.model.supported == nil || !*next.model.supported {
		t.Fatal("the capability probe should have reported support")
	}
	view := ansi.Strip(next.View())
	for _, want := range []string{"Model dla @matt", "teraz: profile default", "gpt-5", "claude-sonnet-4"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

func TestRoomsModel_FilterNarrowsAndCursorResets(t *testing.T) {
	s := mdlCatalogue(t, tg.New(t, tg.DefaultOptions()))
	s.SetMemberModelSupported(true)
	m := mdlOpenModelStage(t, mdlModel(t, s), "@matt")

	if got := m.filteredModels(); len(got) != 4 {
		t.Fatalf("unfiltered options = %v, want all four", got)
	}
	typed, _, _ := m.handleModelKey(tea.KeyPressMsg{Text: "s"}, "s")
	if typed.model.filter != "s" {
		t.Fatalf("filter = %q, want s", typed.model.filter)
	}
	if got := typed.filteredModels(); len(got) != 1 || got[0] != "claude-sonnet-4" {
		t.Errorf("filtered options = %v, want just claude-sonnet-4", got)
	}
	// The cursor is reset by a filter change, so Enter cannot pick a row the
	// user can no longer see.
	typed.model.cursor = 1
	typed, _, _ = typed.handleModelKey(tea.KeyPressMsg{Text: "o"}, "o")
	if typed.model.cursor != 0 {
		t.Errorf("cursor = %d, want 0 after the filter changed", typed.model.cursor)
	}
	cleared, _, _ := typed.handleModelKey(tea.KeyPressMsg{}, "ctrl+u")
	if cleared.model.filter != "" || len(cleared.filteredModels()) != 4 {
		t.Errorf("ctrl+u should clear the filter, got %q", cleared.model.filter)
	}
	backspaced, _, _ := typed.handleModelKey(tea.KeyPressMsg{}, "backspace")
	if backspaced.model.filter != "s" {
		t.Errorf("backspace = %q, want s", backspaced.model.filter)
	}
}

func TestRoomsModel_EnterSwitchesTheModelForTheSession(t *testing.T) {
	home := setTestHome(t)
	s := mdlCatalogue(t, tg.New(t, tg.DefaultOptions()))
	s.SetMemberModelSupported(true)
	m := mdlModel(t, s)

	applied, cmd, _ := m.handleRoomUXCommand("/model @matt gpt-5-mini")
	applied = feedCmd(t, applied, cmd)

	if calls := s.MemberModelCalls(); len(calls) != 1 || calls[0] != "matt=gpt-5-mini" {
		t.Fatalf("gateway recorded %v, want the switch on the wire", calls)
	}
	if applied.model.active {
		t.Error("the overlay should close after a successful switch")
	}
	if got := applied.memberModel("matt"); got != "gpt-5-mini" {
		t.Fatalf("member model = %q, want the new one", got)
	}
	if view := ansi.Strip(applied.View()); !strings.Contains(view, "@matt (gpt-5-mini)") {
		t.Errorf("the roster line should show the member's model:\n%s", view)
	}
	if !strings.Contains(applied.status, "profile untouched") {
		t.Errorf("status = %q, want it to say the profile was not touched", applied.status)
	}

	// Nothing about the model is persisted: the change is this session's.
	prefs, err := os.ReadFile(home + "/.lucinate/rooms-prefs.json")
	if err == nil && strings.Contains(string(prefs), "model") {
		t.Errorf("prefs file carries the model, which must stay session-only:\n%s", prefs)
	}
}

func TestRoomsModel_GatewayWithoutTheMethodRefusesInsteadOfPretending(t *testing.T) {
	s := mdlCatalogue(t, tg.New(t, tg.DefaultOptions()))
	s.SetMemberModelSupported(false) // a gateway as shipped today
	m := mdlOpenModelStage(t, mdlModel(t, s), "@matt")

	if m.model.supported == nil || *m.model.supported {
		t.Fatal("the probe should have reported no support")
	}
	refused, cmd, _ := m.handleModelKey(tea.KeyPressMsg{Code: tea.KeyEnter}, "enter")
	if cmd != nil {
		t.Error("no request should be sent to a gateway that cannot honour it")
	}
	if refused.model.err == nil {
		t.Fatal("the refusal must be explained")
	}
	view := ansi.Strip(refused.View())
	if !strings.Contains(view, "nie wspiera zmiany modelu") {
		t.Errorf("view should say the gateway cannot do it:\n%s", view)
	}
	if !strings.Contains(view, rooms.MemberModelMethod) {
		t.Errorf("view should name the missing method:\n%s", view)
	}
	if calls := s.MemberModelCalls(); len(calls) != 0 {
		t.Errorf("gateway recorded %v, want nothing sent", calls)
	}
}

func TestRoomsModel_RejectedModelRollsBackAndExplains(t *testing.T) {
	s := mdlCatalogue(t, tg.New(t, tg.DefaultOptions()))
	s.SetMemberModelSupported(true)
	s.SetMemberModelReject(4104, "model gpt-9 is not available for this member")
	m := mdlModel(t, s)

	next, cmd, _ := m.handleRoomUXCommand("/model @matt gpt-9")
	next = feedCmd(t, next, cmd)

	if next.model.err == nil {
		t.Fatal("the gateway's rejection must be surfaced")
	}
	if !strings.Contains(next.model.err.Error(), "not available") {
		t.Errorf("error = %v, want the gateway's message", next.model.err)
	}
	if got := next.memberModel("matt"); got != "" {
		t.Errorf("member model = %q, want it unchanged (the UI rolled back)", got)
	}
	if view := ansi.Strip(next.View()); strings.Contains(view, "@matt (gpt-9)") {
		t.Error("the roster must not show a model the gateway refused")
	}
	if !strings.Contains(next.status, "unchanged") {
		t.Errorf("status = %q, want it to say the model did not change", next.status)
	}
}

func TestRoomsModel_CommandErrorsAreReportedNotSent(t *testing.T) {
	s := mdlCatalogue(t, tg.New(t, tg.DefaultOptions()))
	s.SetMemberModelSupported(true)
	m := mdlModel(t, s)

	// Only shapes a user can actually type: `strings.Fields` collapses
	// whitespace, so an empty model name is unreachable from the command line
	// and is covered by the parser's own test instead.
	for _, input := range []string{"/model @ghost gpt-5", "/model gpt-5"} {
		next, cmd, ok := m.handleRoomUXCommand(input)
		if !ok {
			t.Fatalf("%q was not handled", input)
		}
		if next.err == nil {
			t.Errorf("%q should have produced an error", input)
		}
		if cmd != nil {
			t.Errorf("%q must not schedule a gateway call", input)
		}
		if next.model.active {
			t.Errorf("%q should not open the picker", input)
		}
	}
	if calls := s.MemberModelCalls(); len(calls) != 0 {
		t.Errorf("invalid input reached the gateway: %v", calls)
	}
}

// A reconnect is a new session: the local overlay is dropped, and what the user
// sees afterwards is whatever the gateway reports. The profile was never
// touched, so there is nothing else to undo.
func TestRoomsModel_ReconnectDropsTheSessionOverlay(t *testing.T) {
	s := mdlCatalogue(t, tg.New(t, tg.DefaultOptions()))
	s.SetMemberModelSupported(true)
	m := mdlModel(t, s)

	applied, cmd, _ := m.handleRoomUXCommand("/model @matt gpt-5-mini")
	applied = feedCmd(t, applied, cmd)
	if applied.memberModels["matt"] != "gpt-5-mini" {
		t.Fatal("setup: the switch should be recorded for this session")
	}

	applied.reconnecting = true
	applied.reconnectAttempt = 1
	reconnected, _ := applied.handleReconnect(1)
	if reconnected.memberModels != nil {
		t.Error("a reconnect must drop the session overlay")
	}
	// The gateway's roster is the authority from here on: this fake kept the
	// switch (it has the method), so the view still shows it. A gateway that
	// only accepts the request without storing it would show the profile
	// default instead — either way the client invents nothing.
	if got := reconnected.memberModel("matt"); got != "gpt-5-mini" {
		t.Errorf("member model after reconnect = %q, want the gateway's answer", got)
	}
	if reconnected.model.active {
		t.Error("the overlay must not survive a reconnect")
	}
}

func TestRoomsModel_PickerStaysInsideTheTerminal(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	s.SetMemberModelSupported(true)
	options := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		options = append(options, "model-"+itoaTUI(i))
	}
	s.SetModelCatalogue("model-0", options...)

	m := mdlModel(t, s)
	m.height = 20
	next := mdlOpenModelStage(t, m, "@matt")
	if got := strings.Count(next.View(), "\n"); got > m.height {
		t.Fatalf("picker view is %d lines for a %d-line terminal", got, m.height)
	}
	if !strings.Contains(ansi.Strip(next.View()), "model-0") {
		t.Error("the first option should be visible")
	}
}

func TestRoomsModel_CostTableCarriesTheModel(t *testing.T) {
	s := mdlCatalogue(t, tg.New(t, tg.DefaultOptions()))
	s.SetMemberModelSupported(true)
	m := mdlModel(t, s)

	applied, cmd, _ := m.handleRoomUXCommand("/model @matt gpt-5-mini")
	applied = feedCmd(t, applied, cmd)

	applied, _, _ = applied.handleRoomUXCommand("/cost")
	if !strings.Contains(applied.notice, "MODEL") {
		t.Errorf("cost notice = %q, want a MODEL column", applied.notice)
	}
	if !strings.Contains(applied.notice, "gpt-5-mini") {
		t.Errorf("cost notice = %q, want the member's model", applied.notice)
	}
}

// The user's actual path: open the picker, type nothing, press enter on the
// highlighted model.
func TestRoomsModel_EnterSwitchesTheHighlightedModel(t *testing.T) {
	s := mdlCatalogue(t, tg.New(t, tg.DefaultOptions()))
	s.SetMemberModelSupported(true)
	m := mdlOpenModelStage(t, mdlModel(t, s), "@matt")

	moved, _, _ := m.handleModelKey(tea.KeyPressMsg{}, "down")
	if moved.model.cursor != 1 {
		t.Fatalf("cursor = %d, want the second row", moved.model.cursor)
	}
	applied, cmd, _ := moved.handleModelKey(tea.KeyPressMsg{Code: tea.KeyEnter}, "enter")
	applied = feedCmd(t, applied, cmd)

	if calls := s.MemberModelCalls(); len(calls) != 1 || calls[0] != "matt=gpt-5-mini" {
		t.Fatalf("gateway recorded %v, want the highlighted model switched", calls)
	}
	if applied.model.active {
		t.Error("the overlay should close after the switch")
	}
}

func TestRoomsModel_EscFromTheModelListReturnsToTheMemberList(t *testing.T) {
	s := mdlCatalogue(t, tg.New(t, tg.DefaultOptions()))
	s.SetMemberModelSupported(true)
	m := mdlOpenModelStage(t, mdlModel(t, s), "@kowal")

	back, _, _ := m.handleModelKey(tea.KeyPressMsg{}, "esc")
	if back.model.stage != modelStageMember || !back.model.active {
		t.Fatalf("state = %+v, want the member list still open", back.model)
	}
	if back.model.memberCursor != 1 {
		t.Errorf("cursor = %d, want it on kowal (the member we came from)", back.model.memberCursor)
	}
}

func TestRoomsModel_EmptyCatalogueIsExplainedNotSilent(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	s.SetMemberModelSupported(true)
	s.SetModelCatalogue("") // a gateway that reports no selectable models
	m := mdlOpenModelStage(t, mdlModel(t, s), "@matt")

	if m.model.err == nil {
		t.Fatal("an empty catalogue must be explained")
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "nie zwrócił listy modeli") {
		t.Errorf("view should point at typing the name:\n%s", view)
	}
	// Enter on nothing must not send anything.
	next, cmd, _ := m.handleModelKey(tea.KeyPressMsg{Code: tea.KeyEnter}, "enter")
	if cmd != nil || len(s.MemberModelCalls()) != 0 {
		t.Error("an empty catalogue must not produce a request")
	}
	if next.model.err == nil {
		t.Error("the refusal should stay visible")
	}
}

func TestRoomsModel_CatalogueFailureIsReported(t *testing.T) {
	s := tg.New(t, tg.DefaultOptions())
	s.SetMemberModelSupported(true)
	m := mdlModel(t, s)

	// A gateway that never answers the catalogue call: the picker must say so
	// rather than spin.
	s.SetSilent("model.options")
	restore := roomsDialer
	roomsDialer = func(ctx context.Context, baseURL, token string) (*rooms.Client, error) {
		c, err := rooms.DialBaseURL(ctx, baseURL, token)
		if err != nil {
			return nil, err
		}
		c.SetTimeout(200 * time.Millisecond)
		return c, nil
	}
	t.Cleanup(func() { roomsDialer = restore })

	next, cmd, _ := m.handleRoomUXCommand("/model @matt")
	next = feedCmd(t, next, cmd)
	if next.model.err == nil {
		t.Fatal("a silent catalogue call must surface an error")
	}
	if next.model.loading {
		t.Error("the picker should stop claiming it is loading")
	}
}

// The seat is what the member's turns will actually run on, and the gateway
// keeps it on the member's room session — not on the roster — so the roster
// cannot confirm it. The local overlay therefore wins, and the roster is what
// the view falls back to for a member nothing has been seated on.
func TestRoomsModel_SeatOverlayWinsAndRosterIsTheFallback(t *testing.T) {
	s := mdlCatalogue(t, tg.New(t, tg.DefaultOptions()))
	m := mdlModel(t, s)

	if got := m.memberModel("matt"); got != "" {
		t.Fatalf("member model = %q, want empty before anything is set", got)
	}
	// The roster's own answer is used while no seat exists.
	room := liveRoom("sztab", "Sztab", "matt", "kowal")
	room.Members[0] = rooms.WithMemberModel(room.Members[0], "claude-sonnet-4")
	m.openRoom = &room
	if got := m.memberModel("matt"); got != "claude-sonnet-4" {
		t.Fatalf("member model = %q, want the roster's model before a seat", got)
	}

	// A seat outranks it: that is the model the gateway will run the turns on.
	m.memberModels = map[string]string{"matt": "gpt-5-mini"}
	if got := m.memberModel("matt"); got != "gpt-5-mini" {
		t.Errorf("member model = %q, want the accepted seat to win", got)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "@matt (gpt-5-mini)") {
		t.Errorf("the roster line should show the seat:\n%s", view)
	}
}

func TestRoomsModel_UnknownMemberInTheOverlayIsReported(t *testing.T) {
	s := mdlCatalogue(t, tg.New(t, tg.DefaultOptions()))
	s.SetMemberModelSupported(true)
	m := mdlModel(t, s)
	m.model = modelPicker{active: true, stage: modelStageModel, handle: "ghost", catalogue: []string{"gpt-5"}}

	next, cmd, _ := m.handleModelKey(tea.KeyPressMsg{Code: tea.KeyEnter}, "enter")
	if cmd != nil {
		t.Error("no request should be made for a member the room does not have")
	}
	if next.model.err == nil || !strings.Contains(next.model.err.Error(), "ghost") {
		t.Errorf("error = %v, want the missing member named", next.model.err)
	}
}
