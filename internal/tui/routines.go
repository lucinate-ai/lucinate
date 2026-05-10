package tui

import (
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lucinate-ai/lucinate/internal/routines"
)

// routinesSubState selects which sub-view of the routines manager renders.
type routinesSubState int

const (
	routinesSubList routinesSubState = iota
	routinesSubDetail
	routinesSubForm
	routinesSubConfirmDelete
)

// Form field indices. Header fields occupy 0..fieldStepStart-1; each
// element of form.steps occupies fieldStepStart + i.
const (
	fieldName = iota
	fieldMode
	fieldLog
	fieldStepStart
)

// routineItem wraps a Routine for the bubbles list.
type routineItem struct {
	routine routines.Routine
}

func (i routineItem) FilterValue() string { return i.routine.Name }

// routineDelegate renders each list row as two lines: the name and a
// secondary chip line with mode + step count.
type routineDelegate struct{}

func (d routineDelegate) Height() int                             { return 2 }
func (d routineDelegate) Spacing() int                            { return 1 }
func (d routineDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d routineDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	r, ok := item.(routineItem)
	if !ok {
		return
	}
	name := r.routine.Name
	var titleLine string
	if index == m.Index() {
		titleLine = lipgloss.NewStyle().Foreground(accent).Bold(true).
			Render("> " + name)
	} else {
		titleLine = "  " + name
	}
	mode := strings.ToUpper(string(r.routine.ResolvedMode()))
	steps := fmt.Sprintf("%d step", len(r.routine.Steps))
	if len(r.routine.Steps) != 1 {
		steps += "s"
	}
	chips := []string{
		chipStyle.Render(steps),
		chipStyle.Render(mode),
	}
	if r.routine.Frontmatter.Log != "" {
		chips = append(chips, chipStyle.Render("logs"))
	}
	subtitle := "  " + strings.Join(chips, " ")
	fmt.Fprint(w, titleLine+"\n"+subtitle)
}

// routinesModel is the /routines manager view.
type routinesModel struct {
	list   list.Model
	subset routinesSubState

	loading bool
	err     error

	routines []routines.Routine

	// Detail substate.
	selectedName string

	// Form substate.
	form routineForm

	// Confirm-delete substate.
	pendingDeleteName string

	hideHints       bool
	disableExitKeys bool
	width           int
	height          int
}

// routineForm holds in-progress form state for create + edit.
type routineForm struct {
	mode      string // "create" or "edit"
	editingID string // existing routine name when editing; "" when creating

	name  textinput.Model
	mFm   textinput.Model // mode field ("auto" / "manual")
	log   textinput.Model
	steps []textarea.Model

	focused int // 0=name, 1=mode, 2=log, 3+i=step i
	saving  bool
	err     error

	// pendingStepDelete is set when alt+delete is pressed on a step;
	// the form view replaces help with a y/n prompt until resolved.
	pendingStepDelete bool
	pendingDeleteIdx  int
}

// fieldCount returns the total focusable field count, including all
// steps. Used to wrap tab/shift+tab cycling.
func (f *routineForm) fieldCount() int { return fieldStepStart + len(f.steps) }

// stepIndex returns the index into form.steps for the focused field, or
// -1 when the focus is on a header field.
func (f *routineForm) stepIndex() int {
	if f.focused < fieldStepStart {
		return -1
	}
	return f.focused - fieldStepStart
}

func newRoutinesModel(hideHints bool, disableExitKeys bool) routinesModel {
	l := list.New(nil, routineDelegate{}, 0, 0)
	l.Title = "Routines"
	l.SetShowStatusBar(false)
	l.SetShowHelp(!hideHints)
	l.Styles.Title = headerStyle
	l.SetFilteringEnabled(false)
	if disableExitKeys {
		l.KeyMap.Quit.Unbind()
		l.KeyMap.ForceQuit.Unbind()
	}
	return routinesModel{
		list:            l,
		subset:          routinesSubList,
		loading:         true,
		hideHints:       hideHints,
		disableExitKeys: disableExitKeys,
	}
}

func (m routinesModel) Init() tea.Cmd { return loadRoutinesList() }

func (m *routinesModel) setSize(w, h int) {
	m.width = w
	m.height = h
	m.list.SetSize(w, h-2)
	if m.subset == routinesSubForm {
		m.sizeFormBody()
	}
}

// sizeFormBody adjusts the visible height of every step textarea to fit
// the available terminal vertical space. Header textinputs are always
// single-line; the remaining height is divided across the step
// textareas with a per-step floor of 3 lines so each step stays usable
// even when the form is long.
func (m *routinesModel) sizeFormBody() {
	if len(m.form.steps) == 0 {
		return
	}
	const perStepFloor = 3
	const reservedChrome = 14 // header + name/mode/log labels+inputs + help
	available := m.height - reservedChrome
	if available < perStepFloor*len(m.form.steps) {
		available = perStepFloor * len(m.form.steps)
	}
	per := available / len(m.form.steps)
	if per < perStepFloor {
		per = perStepFloor
	}
	if per > 8 {
		per = 8 // cap so a single tall textarea doesn't eat the form
	}
	width := m.width - 4
	if width < 20 {
		width = 20
	}
	for i := range m.form.steps {
		m.form.steps[i].SetHeight(per)
		m.form.steps[i].SetWidth(width)
	}
}

// routinesListLoadedMsg delivers the routines slice from disk.
type routinesListLoadedMsg struct {
	routines []routines.Routine
	err      error
}

// routineSavedMsg is dispatched after a save attempt.
type routineSavedMsg struct {
	name string
	err  error
}

// routineDeletedMsg is dispatched after a delete attempt.
type routineDeletedMsg struct {
	name string
	err  error
}

func loadRoutinesList() tea.Cmd {
	return func() tea.Msg {
		list, err := routines.List()
		return routinesListLoadedMsg{routines: list, err: err}
	}
}

func (m routinesModel) Update(msg tea.Msg) (routinesModel, tea.Cmd) {
	switch msg := msg.(type) {
	case routinesListLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.routines = msg.routines
		items := make([]list.Item, len(msg.routines))
		for i, r := range msg.routines {
			items[i] = routineItem{routine: r}
		}
		m.list.SetItems(items)
		return m, nil

	case routineSavedMsg:
		if msg.err != nil {
			m.form.err = msg.err
			m.form.saving = false
			return m, nil
		}
		m.subset = routinesSubList
		m.form = routineForm{}
		m.loading = true
		return m, tea.Batch(loadRoutinesList(), func() tea.Msg { return routinesChangedMsg{} })

	case routineDeletedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.subset = routinesSubList
		m.selectedName = ""
		m.loading = true
		return m, tea.Batch(loadRoutinesList(), func() tea.Msg { return routinesChangedMsg{} })

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	if m.subset == routinesSubList {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m routinesModel) handleKey(msg tea.KeyPressMsg) (routinesModel, tea.Cmd) {
	switch m.subset {
	case routinesSubList:
		return m.handleListKey(msg)
	case routinesSubDetail:
		return m.handleDetailKey(msg)
	case routinesSubForm:
		return m.handleFormKey(msg)
	case routinesSubConfirmDelete:
		return m.handleConfirmKey(msg)
	}
	return m, nil
}

func (m routinesModel) handleListKey(msg tea.KeyPressMsg) (routinesModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m, func() tea.Msg { return goBackFromRoutinesMsg{} }
	case "n":
		m.form = newRoutineForm("", routines.Routine{})
		m.subset = routinesSubForm
		m.sizeFormBody()
		return m, nil
	case "d":
		return m.actionDuplicate()
	case "enter":
		item, ok := m.list.SelectedItem().(routineItem)
		if !ok {
			return m, nil
		}
		m.selectedName = item.routine.Name
		m.subset = routinesSubDetail
		return m, nil
	case "up", "k", "down", "j":
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m routinesModel) handleDetailKey(msg tea.KeyPressMsg) (routinesModel, tea.Cmd) {
	switch msg.String() {
	case "esc", "left", "h":
		m.subset = routinesSubList
		m.selectedName = ""
		return m, nil
	case "e":
		r, ok := m.findRoutine(m.selectedName)
		if !ok {
			return m, nil
		}
		m.form = newRoutineForm(r.Name, r)
		m.subset = routinesSubForm
		m.sizeFormBody()
		return m, nil
	case "x":
		m.pendingDeleteName = m.selectedName
		m.subset = routinesSubConfirmDelete
		return m, nil
	}
	return m, nil
}

func (m routinesModel) handleConfirmKey(msg tea.KeyPressMsg) (routinesModel, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "y":
		name := m.pendingDeleteName
		m.pendingDeleteName = ""
		return m, func() tea.Msg {
			err := routines.Delete(name)
			return routineDeletedMsg{name: name, err: err}
		}
	case "n", "esc":
		m.pendingDeleteName = ""
		m.subset = routinesSubDetail
		return m, nil
	}
	return m, nil
}

func (m routinesModel) handleFormKey(msg tea.KeyPressMsg) (routinesModel, tea.Cmd) {
	// Resolve a pending step-delete confirmation first — every other key
	// is interpreted relative to the prompt while it's open.
	if m.form.pendingStepDelete {
		switch strings.ToLower(msg.String()) {
		case "y":
			m.deleteStep(m.form.pendingDeleteIdx)
			m.form.pendingStepDelete = false
			return m, nil
		case "n", "esc":
			m.form.pendingStepDelete = false
			return m, nil
		}
		return m, nil
	}

	switch msg.String() {
	case "esc":
		m.form = routineForm{}
		m.subset = routinesSubList
		return m, nil
	case "tab":
		n := m.form.fieldCount()
		if n > 0 {
			m.form.focused = (m.form.focused + 1) % n
			m.refocus()
		}
		return m, nil
	case "shift+tab":
		n := m.form.fieldCount()
		if n > 0 {
			m.form.focused = (m.form.focused - 1 + n) % n
			m.refocus()
		}
		return m, nil
	// alt+s is the primary save key; ctrl+s is kept as an alias defensively
	// because some terminals interpret ctrl+s as XOFF and never deliver it.
	case "alt+s", "ctrl+s":
		return m.submitForm()
	case "alt+up":
		if idx := m.form.stepIndex(); idx >= 0 {
			m.insertStep(idx, "")
			m.form.focused = fieldStepStart + idx // focus the new (above) step
			m.refocus()
			m.sizeFormBody()
		}
		return m, nil
	case "alt+down":
		if idx := m.form.stepIndex(); idx >= 0 {
			m.insertStep(idx+1, "")
			m.form.focused = fieldStepStart + idx + 1 // focus the new (below) step
			m.refocus()
			m.sizeFormBody()
		}
		return m, nil
	case "alt+delete", "alt+backspace":
		if idx := m.form.stepIndex(); idx >= 0 {
			m.form.pendingStepDelete = true
			m.form.pendingDeleteIdx = idx
		}
		return m, nil
	}

	var cmd tea.Cmd
	switch m.form.focused {
	case fieldName:
		m.form.name, cmd = m.form.name.Update(msg)
	case fieldMode:
		m.form.mFm, cmd = m.form.mFm.Update(msg)
	case fieldLog:
		m.form.log, cmd = m.form.log.Update(msg)
	default:
		idx := m.form.stepIndex()
		if idx >= 0 && idx < len(m.form.steps) {
			m.form.steps[idx], cmd = m.form.steps[idx].Update(msg)
		}
	}
	return m, cmd
}

// insertStep splices a fresh textarea at idx, shifting subsequent steps
// down. Caller is responsible for setting form.focused afterward.
func (m *routinesModel) insertStep(idx int, value string) {
	if idx < 0 {
		idx = 0
	}
	if idx > len(m.form.steps) {
		idx = len(m.form.steps)
	}
	ta := newStepTextarea(value)
	m.form.steps = append(m.form.steps, textarea.Model{})
	copy(m.form.steps[idx+1:], m.form.steps[idx:])
	m.form.steps[idx] = ta
}

// deleteStep removes the step at idx. If the result would be empty, a
// fresh blank step is inserted so the form always has at least one
// step to type into.
func (m *routinesModel) deleteStep(idx int) {
	if idx < 0 || idx >= len(m.form.steps) {
		return
	}
	m.form.steps = append(m.form.steps[:idx], m.form.steps[idx+1:]...)
	if len(m.form.steps) == 0 {
		m.form.steps = []textarea.Model{newStepTextarea("")}
	}
	// Keep focus near the deleted spot. Cap to last step.
	maxFocus := m.form.fieldCount() - 1
	if m.form.focused > maxFocus {
		m.form.focused = maxFocus
	}
	m.refocus()
	m.sizeFormBody()
}

func (m *routinesModel) refocus() {
	m.form.name.Blur()
	m.form.mFm.Blur()
	m.form.log.Blur()
	for i := range m.form.steps {
		m.form.steps[i].Blur()
	}
	switch m.form.focused {
	case fieldName:
		m.form.name.Focus()
	case fieldMode:
		m.form.mFm.Focus()
	case fieldLog:
		m.form.log.Focus()
	default:
		idx := m.form.stepIndex()
		if idx >= 0 && idx < len(m.form.steps) {
			m.form.steps[idx].Focus()
		}
	}
}

func (m routinesModel) submitForm() (routinesModel, tea.Cmd) {
	name := strings.TrimSpace(m.form.name.Value())
	if name == "" {
		m.form.err = fmt.Errorf("name is required")
		return m, nil
	}
	mode := strings.ToLower(strings.TrimSpace(m.form.mFm.Value()))
	if mode != "" && mode != string(routines.ModeAuto) && mode != string(routines.ModeManual) {
		m.form.err = fmt.Errorf("mode must be 'auto' or 'manual'")
		return m, nil
	}
	steps := make([]string, 0, len(m.form.steps))
	for _, ta := range m.form.steps {
		if v := strings.TrimSpace(ta.Value()); v != "" {
			steps = append(steps, v)
		}
	}
	r := routines.Routine{
		Name: name,
		Frontmatter: routines.Frontmatter{
			Name: name,
			Mode: mode,
			Log:  strings.TrimSpace(m.form.log.Value()),
		},
		Steps: steps,
	}
	if len(r.Steps) == 0 {
		m.form.err = fmt.Errorf("at least one non-empty step is required")
		return m, nil
	}
	if m.form.mode == "edit" && m.form.editingID != "" && m.form.editingID != name {
		// Rename: delete old then save new. Save first to ensure no data loss
		// on partial failure.
		oldName := m.form.editingID
		m.form.saving = true
		return m, func() tea.Msg {
			if err := routines.Save(r); err != nil {
				return routineSavedMsg{name: name, err: err}
			}
			_ = routines.Delete(oldName)
			return routineSavedMsg{name: name}
		}
	}
	m.form.saving = true
	return m, func() tea.Msg {
		err := routines.Save(r)
		return routineSavedMsg{name: name, err: err}
	}
}

// actionDuplicate opens the form pre-populated from the highlighted
// list item, but in create mode so submission goes through the plain
// routines.Save path (no rename, no overwrite-then-delete). Triggered
// from the list substate ("d"); the source routine is read from
// m.list.SelectedItem rather than m.selectedName, which is only set
// after entering the detail view.
//
// Mirrors crons.actionDuplicate (internal/tui/crons.go) — the cron
// browser established this pattern; routines follow the same shape so
// the UX is consistent across the two managers.
func (m routinesModel) actionDuplicate() (routinesModel, tea.Cmd) {
	item, ok := m.list.SelectedItem().(routineItem)
	if !ok {
		return m, nil
	}
	m.form = newDuplicateRoutineForm(item.routine, m.routines)
	m.subset = routinesSubForm
	m.sizeFormBody()
	return m, nil
}

// newDuplicateRoutineForm pre-populates a create-mode form from src.
// The name is uniquified against existing so the initial value already
// passes the duplicate-name check and never collides with an on-disk
// directory; editingID stays "" so submitForm goes through the plain
// create path — no rename, no overwrite, no delete-of-original.
//
// Frontmatter.Name is set to the duplicated name so the metadata in
// STEPS.md stays in sync with the directory identity. Frontmatter.Log
// is copied verbatim — if it's a relative path the user can change it
// in the form before saving so the duplicate doesn't share the
// original's log file.
func newDuplicateRoutineForm(src routines.Routine, existing []routines.Routine) routineForm {
	cloned := src
	cloned.Name = duplicateRoutineName(src.Name, existing)
	cloned.Frontmatter.Name = cloned.Name
	return newRoutineForm("", cloned)
}

// duplicateRoutineName returns "Copy of X" if that slot is free,
// otherwise "Copy of X (N)" with the smallest N ≥ 2 that does not
// collide with any existing routine. Empty original passes through so
// the form-level "name is required" check catches it (mirrors
// crons.duplicateName).
//
// Routines are name-keyed (the directory under ~/.lucinate/routines/<name>
// is the identity), so collision avoidance is required — unlike cron
// jobs, two routines cannot share a name. The unbounded loop is bounded
// in practice by len(existing); a non-colliding suffix is always
// reachable.
func duplicateRoutineName(original string, existing []routines.Routine) string {
	if original == "" {
		return ""
	}
	taken := make(map[string]bool, len(existing))
	for _, r := range existing {
		taken[r.Name] = true
	}
	candidate := "Copy of " + original
	if !taken[candidate] {
		return candidate
	}
	for i := 2; ; i++ {
		c := fmt.Sprintf("Copy of %s (%d)", original, i)
		if !taken[c] {
			return c
		}
	}
}

func newRoutineForm(editingID string, existing routines.Routine) routineForm {
	name := textinput.New()
	name.Placeholder = "routine-name"
	name.SetValue(existing.Name)
	name.Focus()

	mFm := textinput.New()
	mFm.Placeholder = "manual"
	mFm.SetValue(existing.Frontmatter.Mode)

	log := textinput.New()
	log.Placeholder = "./routine.log (optional)"
	log.SetValue(existing.Frontmatter.Log)

	steps := make([]textarea.Model, 0, len(existing.Steps))
	for _, s := range existing.Steps {
		steps = append(steps, newStepTextarea(s))
	}
	if len(steps) == 0 {
		// Always show at least one step textarea so the user has an
		// obvious place to start typing on a fresh routine.
		steps = append(steps, newStepTextarea(""))
	}

	mode := "create"
	if editingID != "" {
		mode = "edit"
	}
	return routineForm{
		mode:      mode,
		editingID: editingID,
		name:      name,
		mFm:       mFm,
		log:       log,
		steps:     steps,
		focused:   fieldName,
	}
}

// newStepTextarea constructs a textarea pre-configured for routine
// steps: no line numbers, alt+enter for newline (the chat textarea
// uses the same convention), and the seed value applied.
func newStepTextarea(value string) textarea.Model {
	ta := textarea.New()
	ta.Placeholder = "step content (alt+enter for newline)"
	ta.SetValue(value)
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.KeyMap.InsertNewline.SetKeys("alt+enter")
	return ta
}

func (m routinesModel) findRoutine(name string) (routines.Routine, bool) {
	for _, r := range m.routines {
		if r.Name == name {
			return r, true
		}
	}
	return routines.Routine{}, false
}

// Actions returns the top-level discoverable actions for the active subset.
func (m routinesModel) Actions() []Action {
	switch m.subset {
	case routinesSubList:
		var actions []Action
		actions = append(actions, Action{ID: "new", Label: "New routine", Key: "n"})
		// Duplicate is a list-only action — same shape as the cron browser.
		// Hidden when the list is empty so the menu doesn't advertise an
		// action that has no source row to copy from.
		if len(m.routines) > 0 {
			actions = append(actions, Action{ID: "duplicate", Label: "Duplicate", Key: "d"})
		}
		actions = append(actions, Action{ID: "back", Label: "Back to chat", Key: "esc"})
		return actions
	case routinesSubDetail:
		return []Action{
			{ID: "edit", Label: "Edit", Key: "e"},
			// Delete uses `x` to match the cron detail view. Lower-case `d`
			// is reserved for "duplicate" on the list view; keeping `d` for
			// delete here would overload it across substates and conflict
			// with that pattern. See docs/key-conventions.md.
			{ID: "delete", Label: "Delete", Key: "x"},
			{ID: "back", Label: "Back", Key: "esc"},
		}
	case routinesSubForm:
		return []Action{
			{ID: "save", Label: "Save", Key: "alt+s"},
			{ID: "insert-above", Label: "Insert step above", Key: "alt+up"},
			{ID: "insert-below", Label: "Insert step below", Key: "alt+down"},
			{ID: "delete-step", Label: "Delete step", Key: "alt+delete"},
			{ID: "cancel", Label: "Cancel", Key: "esc"},
		}
	case routinesSubConfirmDelete:
		return []Action{
			{ID: "confirm-delete", Label: "Delete", Key: "y"},
			{ID: "cancel-delete", Label: "Cancel", Key: "n"},
		}
	}
	return nil
}

// TriggerAction routes view-level commands. Mirrors the ID space declared
// by Actions().
func (m routinesModel) TriggerAction(id string) (routinesModel, tea.Cmd) {
	switch id {
	case "new":
		m.form = newRoutineForm("", routines.Routine{})
		m.subset = routinesSubForm
		m.sizeFormBody()
		return m, nil
	case "edit":
		r, ok := m.findRoutine(m.selectedName)
		if !ok {
			return m, nil
		}
		m.form = newRoutineForm(r.Name, r)
		m.subset = routinesSubForm
		m.sizeFormBody()
		return m, nil
	case "duplicate":
		return m.actionDuplicate()
	case "delete":
		m.pendingDeleteName = m.selectedName
		m.subset = routinesSubConfirmDelete
		return m, nil
	case "confirm-delete":
		name := m.pendingDeleteName
		m.pendingDeleteName = ""
		return m, func() tea.Msg {
			err := routines.Delete(name)
			return routineDeletedMsg{name: name, err: err}
		}
	case "cancel-delete":
		m.pendingDeleteName = ""
		m.subset = routinesSubDetail
		return m, nil
	case "save":
		return m.submitForm()
	case "insert-above":
		if idx := m.form.stepIndex(); idx >= 0 {
			m.insertStep(idx, "")
			m.form.focused = fieldStepStart + idx
			m.refocus()
			m.sizeFormBody()
		}
		return m, nil
	case "insert-below":
		if idx := m.form.stepIndex(); idx >= 0 {
			m.insertStep(idx+1, "")
			m.form.focused = fieldStepStart + idx + 1
			m.refocus()
			m.sizeFormBody()
		}
		return m, nil
	case "delete-step":
		if idx := m.form.stepIndex(); idx >= 0 {
			m.form.pendingStepDelete = true
			m.form.pendingDeleteIdx = idx
		}
		return m, nil
	case "cancel":
		m.form = routineForm{}
		m.subset = routinesSubList
		return m, nil
	case "back":
		switch m.subset {
		case routinesSubDetail:
			m.subset = routinesSubList
			return m, nil
		default:
			return m, func() tea.Msg { return goBackFromRoutinesMsg{} }
		}
	}
	return m, nil
}

func (m routinesModel) View() string {
	switch m.subset {
	case routinesSubList:
		return m.viewList()
	case routinesSubDetail:
		return m.viewDetail()
	case routinesSubForm:
		return m.viewForm()
	case routinesSubConfirmDelete:
		return m.viewConfirmDelete()
	}
	return ""
}

func (m routinesModel) viewList() string {
	if m.loading {
		return statusStyle.Render("Loading routines...")
	}
	if m.err != nil {
		return errorStyle.Render(fmt.Sprintf("error: %v", m.err))
	}
	if len(m.routines) == 0 {
		body := emptyHistoryStyle.Render("No routines yet — press n to create one.")
		hint := helpStyle.Render(renderActionHints(m.Actions()))
		return lipgloss.JoinVertical(lipgloss.Left,
			headerStyle.Width(m.width).Render(" Routines"),
			body,
			hint)
	}
	hint := ""
	if !m.hideHints {
		hint = helpStyle.Render(renderActionHints(m.Actions()))
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.list.View(), hint)
}

func (m routinesModel) viewDetail() string {
	r, ok := m.findRoutine(m.selectedName)
	if !ok {
		return errorStyle.Render("routine not found")
	}
	var b strings.Builder
	b.WriteString(headerStyle.Width(m.width).Render(" Routines · " + r.Name))
	b.WriteString("\n\n")
	mode := strings.ToUpper(string(r.ResolvedMode()))
	b.WriteString(statusStyle.Render(fmt.Sprintf("mode: %s    steps: %d", mode, len(r.Steps))))
	b.WriteString("\n")
	if r.Frontmatter.Log != "" {
		b.WriteString(statusStyle.Render("log: " + r.Frontmatter.Log))
		b.WriteString("\n")
	}
	b.WriteString(statusStyle.Render("path: " + r.Path))
	b.WriteString("\n\n")
	for i, step := range r.Steps {
		b.WriteString(lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("Step %d", i+1)))
		b.WriteString("\n")
		b.WriteString(step)
		b.WriteString("\n\n")
	}
	if !m.hideHints {
		b.WriteString(helpStyle.Render(renderActionHints(m.Actions())))
	}
	return b.String()
}

func (m routinesModel) viewForm() string {
	var b strings.Builder
	title := " Routines · New"
	if m.form.mode == "edit" {
		title = " Routines · Edit " + m.form.editingID
	}
	b.WriteString(headerStyle.Width(m.width).Render(title))
	b.WriteString("\n\n")

	row := func(label string, field string) {
		b.WriteString(lipgloss.NewStyle().Bold(true).Render(label))
		b.WriteString("\n")
		b.WriteString(field)
		b.WriteString("\n\n")
	}
	row("Name", m.form.name.View())
	row("Mode (auto|manual)", m.form.mFm.View())
	row("Log path (optional)", m.form.log.View())

	for i, ta := range m.form.steps {
		marker := "  "
		if m.form.stepIndex() == i {
			marker = "▶ "
		}
		label := lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("%sStep %d", marker, i+1))
		b.WriteString(label)
		b.WriteString("\n")
		b.WriteString(ta.View())
		b.WriteString("\n\n")
	}

	if m.form.err != nil {
		b.WriteString(errorStyle.Render(m.form.err.Error()))
		b.WriteString("\n\n")
	}
	if m.form.pendingStepDelete {
		prompt := fmt.Sprintf("Delete step %d? (y/n)", m.form.pendingDeleteIdx+1)
		b.WriteString(errorStyle.Render(prompt))
		return b.String()
	}
	if !m.hideHints {
		b.WriteString(helpStyle.Render(" alt+s: save · alt+↑/↓: insert step · alt+delete: remove step · tab: next field · esc: cancel"))
	}
	return b.String()
}

func (m routinesModel) viewConfirmDelete() string {
	prompt := fmt.Sprintf("Delete routine %q? This removes the directory and all steps.", m.pendingDeleteName)
	hints := helpStyle.Render(" y: confirm · n: cancel")
	return lipgloss.JoinVertical(lipgloss.Left,
		headerStyle.Width(m.width).Render(" Routines · Delete"),
		"",
		prompt,
		"",
		hints,
	)
}
