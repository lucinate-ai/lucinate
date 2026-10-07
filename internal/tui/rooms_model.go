package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lucinate-ai/lucinate/internal/rooms"
)

// `/model` in a room: which model each member answers with.
//
// The room's roster is frozen and the hosted-room protocol has no method for
// changing a member's model (see internal/rooms/models.go for the verified
// method list). So this view does three things and refuses to do a fourth:
//
//	1. shows the model each member is on, read from the roster the gateway sent;
//	2. offers the gateway's model catalogue (the same one the chat's /model uses);
//	3. applies a switch through the room method *if the gateway advertises one*;
//	4. never pretends: a gateway without the method gets told "not supported"
//	   rather than showing a change that reached nobody.
//
// The switch is room-session state, held in memory only: nothing is written to
// the profile, to config.yaml or to lucinate's own prefs file, and the overlay
// is re-read from the gateway on every reconnect.

type modelPickerStage int

const (
	modelStageMember modelPickerStage = iota
	modelStageModel
)

// modelPicker is the overlay's state. active=false means the transcript's
// normal key handling applies.
type modelPicker struct {
	active bool
	stage  modelPickerStage

	// memberCursor indexes the roster on the member stage.
	memberCursor int
	// handle is the member being edited on the model stage.
	handle string
	// cursor indexes the *filtered* catalogue; filter is what the user typed.
	cursor int
	filter string

	loading bool
	err     error
	// supported is the gateway's answer to the capability probe. nil means
	// "not asked yet": the probe runs when the model stage opens.
	supported *bool

	catalogue []string
	current   string
}

// roomsModelsMsg carries the catalogue the gateway reported.
type roomsModelsMsg struct {
	catalogue []string
	current   string
	// supported is the gateway's answer to the capability probe: whether it
	// can switch a member's model at all.
	supported *bool
	err       error
}

// roomsModelSetMsg carries the outcome of one switch request.
type roomsModelSetMsg struct {
	handle    string
	model     string
	seat      rooms.MemberModelSeat
	supported bool
	err       error
}

// memberModels is the model each member's turns are seated on, as far as this
// session knows. The gateway stores that pick on the member's room session, not
// on the roster, so a roster row cannot confirm it: the roster reports the
// member's *configured* model, while this overlay reports the seat this client
// asked for and the gateway accepted. A seat therefore wins over the roster,
// and it is only written on an accepted switch — refusal rolls back by simply
// not writing it.
func (m roomsModel) memberModel(handle string) string {
	if seated := m.memberModels[handle]; seated != "" {
		return seated
	}
	for _, member := range m.roster() {
		if rooms.HandleFor(member.Profile) == handle || member.Handle == handle {
			if model := rooms.MemberModel(member); model != "" {
				return model
			}
			break
		}
	}
	return ""
}

// rosterLine renders a member with the model it answers with, so the room's
// header answers "which model is @matt on?" without a command.
func (m roomsModel) rosterLine() string {
	members := m.roster()
	if len(members) == 0 {
		return "no members"
	}
	parts := make([]string, 0, len(members))
	for _, member := range members {
		handle := rooms.HandleFor(member.Profile)
		model := m.memberModel(handle)
		if model == "" {
			parts = append(parts, "@"+handle+" (profile default)")
			continue
		}
		parts = append(parts, "@"+handle+" ("+model+")")
	}
	return strings.Join(parts, " ")
}

// ── opening the picker ───────────────────────────────────────────────────────

// cmdModel runs `/model` and its three other shapes.
func (m roomsModel) cmdModel(args []string) (roomsModel, tea.Cmd, bool) {
	m.composer = ""
	m.err = nil
	cmd, err := rooms.ParseModelCommand(args, m.roster())
	if err != nil {
		m.err = err
		return m, nil, true
	}
	if cmd.Member == nil {
		// No member yet: which one are we talking to?
		m.model = modelPicker{active: true, stage: modelStageMember, memberCursor: 0}
		if len(m.roster()) == 0 {
			m.model.err = fmt.Errorf("this room has no roster — nothing to switch")
		}
		return m, nil, true
	}
	m.openModelStage(*cmd.Member)
	if cmd.Model != "" {
		// /model @handle <name> — straight through.
		return m, m.setMemberModel(*cmd.Member, cmd.Model), true
	}
	return m, m.ensureCatalogue(), true
}

// openModelStage switches the overlay to the model list for one member. The
// current model travels with it, which is how `/model @handle` both shows the
// member's model and lets it be changed in one step.
func (m *roomsModel) openModelStage(member rooms.Member) {
	handle := rooms.HandleFor(member.Profile)
	m.model = modelPicker{
		active:    true,
		stage:     modelStageModel,
		handle:    handle,
		current:   m.memberModel(handle),
		supported: m.model.supported,
	}
}

// ensureCatalogue asks the gateway for its model list once per session, and
// probes whether it can switch a member's model at all.
func (m *roomsModel) ensureCatalogue() tea.Cmd {
	if len(m.model.catalogue) > 0 {
		return nil
	}
	if m.model.loading {
		return nil
	}
	m.model.loading = true
	return func() tea.Msg {
		c, err := m.dial()
		if err != nil {
			return roomsModelsMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		catalogue, err := c.ModelCatalogue(ctx)
		if err != nil {
			return roomsModelsMsg{err: err}
		}
		// The probe is a separate question from the catalogue: a gateway can
		// list models and still have no way to switch one for a member.
		supported, probeErr := c.SupportsMemberModel(ctx)
		if probeErr != nil {
			return roomsModelsMsg{catalogue: catalogue.Options, current: catalogue.Model, err: probeErr}
		}
		return roomsModelsMsg{
			catalogue: catalogue.Options,
			current:   catalogue.Model,
			supported: &supported,
		}
	}
}

// setMemberModel asks the gateway to run that member's turns with the model.
func (m roomsModel) setMemberModel(member rooms.Member, model string) tea.Cmd {
	roomID := m.openRoomID
	handle := rooms.HandleFor(member.Profile)
	return func() tea.Msg {
		c, err := m.dial()
		if err != nil {
			return roomsModelSetMsg{handle: handle, model: model, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		seat, err := c.SetMemberModel(ctx, roomID, handle, model, "")
		if err != nil {
			return roomsModelSetMsg{handle: handle, model: model, err: err}
		}
		return roomsModelSetMsg{handle: handle, model: seat.Model, seat: seat, supported: true}
	}
}

// ── keys ─────────────────────────────────────────────────────────────────────

// handleModelKey drives the overlay. It reports whether it consumed the key.
func (m roomsModel) handleModelKey(msg tea.KeyPressMsg, key string) (roomsModel, tea.Cmd, bool) {
	if !m.model.active {
		return m, nil, false
	}
	switch m.model.stage {
	case modelStageMember:
		return m.handleMemberStageKey(key)
	default:
		return m.handleModelStageKey(msg, key)
	}
}

func (m roomsModel) handleMemberStageKey(key string) (roomsModel, tea.Cmd, bool) {
	members := m.roster()
	switch key {
	case "esc", "q":
		m.model = modelPicker{}
	case "up", "k":
		if m.model.memberCursor > 0 {
			m.model.memberCursor--
		}
	case "down", "j":
		if m.model.memberCursor < len(members)-1 {
			m.model.memberCursor++
		}
	case "enter":
		if len(members) == 0 {
			return m, nil, true
		}
		member := members[m.model.memberCursor]
		m.openModelStage(member)
		return m, m.ensureCatalogue(), true
	}
	return m, nil, true
}

func (m roomsModel) handleModelStageKey(msg tea.KeyPressMsg, key string) (roomsModel, tea.Cmd, bool) {
	options := m.filteredModels()
	switch key {
	case "esc":
		// Back to the member list rather than out of the flow: choosing a
		// different member is the common next thought.
		m.model = modelPicker{active: true, stage: modelStageMember, memberCursor: m.memberCursorOf(m.model.handle), supported: m.model.supported}
		return m, nil, true
	case "up":
		if m.model.cursor > 0 {
			m.model.cursor--
		}
		return m, nil, true
	case "down":
		if m.model.cursor < len(options)-1 {
			m.model.cursor++
		}
		return m, nil, true
	case "ctrl+u":
		m.model.filter = ""
		m.model.cursor = 0
		return m, nil, true
	case "backspace":
		if m.model.filter != "" {
			r := []rune(m.model.filter)
			m.model.filter = string(r[:len(r)-1])
			m.model.cursor = 0
		}
		return m, nil, true
	case "enter":
		if supported := m.model.supported; supported != nil && !*supported {
			m.model.err = errNoMemberModel
			return m, nil, true
		}
		if len(options) == 0 {
			m.model.err = fmt.Errorf("no model to pick — the gateway reported an empty catalogue, use /model @%s <name>", m.model.handle)
			return m, nil, true
		}
		member, ok := m.memberByHandle(m.model.handle)
		if !ok {
			m.model.err = fmt.Errorf("no member @%s in this room", m.model.handle)
			return m, nil, true
		}
		model := options[m.model.cursor]
		return m, m.setMemberModel(member, model), true
	}
	if t := msg.Text; t != "" && !strings.HasPrefix(key, "ctrl+") && !strings.HasPrefix(key, "alt+") {
		m.model.filter += t
		m.model.cursor = 0
	}
	return m, nil, true
}

// errNoMemberModel is the refusal shown when the gateway has no switching
// method. It names the alternative rather than leaving the user stuck.
var errNoMemberModel = fmt.Errorf(
	"this gateway cannot switch a member's model: groups.capabilities does not list %s "+
		"(the hosted-room protocol has no such method, and a roster is frozen once the room exists). "+
		"A room whose members use a different model has to be seated again from the rooms list (n)",
	rooms.MemberModelMethod)

// filteredModels is the catalogue narrowed by the typed filter.
func (m roomsModel) filteredModels() []string {
	return rooms.FilterModels(m.model.catalogue, m.model.filter)
}

func (m roomsModel) memberByHandle(handle string) (rooms.Member, bool) {
	for _, member := range m.roster() {
		if rooms.HandleFor(member.Profile) == handle || member.Handle == handle {
			return member, true
		}
	}
	return rooms.Member{}, false
}

func (m roomsModel) memberCursorOf(handle string) int {
	for i, member := range m.roster() {
		if rooms.HandleFor(member.Profile) == handle || member.Handle == handle {
			return i
		}
	}
	return 0
}

// ── results ──────────────────────────────────────────────────────────────────

func (m *roomsModel) applyModels(msg roomsModelsMsg) {
	m.model.loading = false
	if msg.err != nil {
		m.model.err = msg.err
		return
	}
	m.model.catalogue = msg.catalogue
	m.model.supported = msg.supported
	if len(msg.catalogue) == 0 {
		m.model.err = fmt.Errorf("the gateway reported no selectable models — use /model @handle <name>")
	}
}

// applyModelSet records an accepted switch, or reports the refusal and leaves
// the previous model in place (the UI's rollback: the roster is only updated on
// success, so nothing has to be undone).
func (m *roomsModel) applyModelSet(msg roomsModelSetMsg) {
	if msg.err != nil {
		m.model.err = fmt.Errorf("gateway refused the model change for @%s: %w", msg.handle, msg.err)
		m.status = fmt.Sprintf("model of @%s unchanged (gateway refused %q)", msg.handle, msg.model)
		return
	}
	if m.memberModels == nil {
		m.memberModels = map[string]string{}
	}
	m.memberModels[msg.handle] = msg.model
	if m.openRoom != nil {
		members := make([]rooms.Member, len(m.openRoom.Members))
		copy(members, m.openRoom.Members)
		for i := range members {
			if rooms.HandleFor(members[i].Profile) == msg.handle || members[i].Handle == msg.handle {
				members[i] = rooms.WithMemberModel(members[i], msg.model)
			}
		}
		room := *m.openRoom
		room.Members = members
		m.openRoom = &room
	}
	m.model.err = nil
	m.model.current = msg.model
	m.model = modelPicker{}
	m.status = fmt.Sprintf("model of @%s seated on %s (this room session only; profile untouched)",
		msg.handle, msg.seat.SeatedLabel())
}

// openRoomMembers was a helper for a slice copy that no longer exists; the
// roster the view shows is m.roster(), which also falls back to the open room.

// ── rendering ────────────────────────────────────────────────────────────────

// modelPickerView renders the overlay replacing the transcript while it is
// open: a member list first, then the model list with the live filter.
func (m roomsModel) modelPickerView() string {
	var b strings.Builder
	// The picker replaces the transcript, so its list has to leave room for the
	// view's own chrome: the header (2 lines), the picker's title/subtitle
	// (3), its scroll indicators (2), the filter line (2) and the help line (2).
	// The margin is deliberate — a picker that scrolls the hint off the screen
	// hides the keys the user needs to leave it.
	budget := m.height - 16
	if budget < 3 {
		budget = 3
	}
	switch m.model.stage {
	case modelStageMember:
		b.WriteString(assistantPrefixStyle.Render("Wybierz członka pokoju") + "\n")
		b.WriteString(statusStyle.Render("model obowiązuje tylko w tej sesji pokoju — profil i jego config zostają nietknięte") + "\n\n")
		members := m.roster()
		if len(members) == 0 {
			b.WriteString(statusStyle.Render("ten pokój nie ma rosteru") + "\n")
			break
		}
		start, end, above, below := windowRows(len(members), m.model.memberCursor, budget)
		if above > 0 {
			b.WriteString(statusStyle.Render(fmt.Sprintf("  ↑ %d more", above)) + "\n")
		}
		for i := start; i < end; i++ {
			cursor := "  "
			if i == m.model.memberCursor {
				cursor = completionMenuHighlightStyle.Render("› ")
			}
			member := members[i]
			handle := rooms.HandleFor(member.Profile)
			b.WriteString(fmt.Sprintf("%s@%-20s %s\n", cursor, handle,
				statusStyle.Render(rooms.ModelLabel(member))))
		}
		if below > 0 {
			b.WriteString(statusStyle.Render(fmt.Sprintf("  ↓ %d more", below)) + "\n")
		}
	default:
		current := m.model.current
		if current == "" {
			current = "profile default"
		}
		b.WriteString(assistantPrefixStyle.Render("Model dla @"+m.model.handle) + "\n")
		b.WriteString(statusStyle.Render("teraz: "+current+" · zmiana na czas tej sesji pokoju (profil nietknięty)") + "\n\n")
		switch {
		case m.model.loading:
			b.WriteString(statusStyle.Render("wczytuję listę modeli z gatewaya…") + "\n")
		case m.model.supported != nil && !*m.model.supported:
			b.WriteString(errorStyle.Render("ten gateway nie wspiera zmiany modelu członka") + "\n")
			b.WriteString(statusStyle.Render(errNoMemberModel.Error()) + "\n")
		default:
			options := m.filteredModels()
			if len(m.model.catalogue) == 0 {
				b.WriteString(statusStyle.Render("gateway nie zwrócił listy modeli — wpisz nazwę ręcznie: /model @"+m.model.handle+" <nazwa>") + "\n")
			}
			start, end, above, below := windowRows(len(options), m.model.cursor, budget)
			if above > 0 {
				b.WriteString(statusStyle.Render(fmt.Sprintf("  ↑ %d more", above)) + "\n")
			}
			for i := start; i < end; i++ {
				cursor := "  "
				if i == m.model.cursor {
					cursor = completionMenuHighlightStyle.Render("› ")
				}
				marker := ""
				if options[i] == m.model.current {
					marker = statusStyle.Render("  (obecny)")
				}
				b.WriteString(fmt.Sprintf("%s%s%s\n", cursor, options[i], marker))
			}
			if below > 0 {
				b.WriteString(statusStyle.Render(fmt.Sprintf("  ↓ %d more", below)) + "\n")
			}
			if len(m.model.catalogue) > 0 && len(options) == 0 {
				b.WriteString(statusStyle.Render("brak modelu pasującego do filtra — backspace/ctrl+u czyści") + "\n")
			}
			b.WriteString("\n" + statusStyle.Render("filtr: "+m.model.filter+"▌") + "\n")
		}
	}
	if m.model.err != nil {
		b.WriteString("\n" + errorStyle.Render("error: "+m.model.err.Error()) + "\n")
	}
	b.WriteString("\n" + helpStyle.Render("↑/↓ wybór · enter zatwierdź · wpisz aby filtrować · ctrl+u czyści filtr · esc wstecz"))
	return b.String()
}
