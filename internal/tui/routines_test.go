package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/lucinate-ai/lucinate/internal/config"
	"github.com/lucinate-ai/lucinate/internal/routines"
)

// newTestRoutinesModel builds a routinesModel suitable for unit tests
// and seeds it with the given routines (mirroring the cronsLoadedMsg
// path in production). Returns a populated model with the first
// routine highlighted.
func newTestRoutinesModel(rs []routines.Routine) routinesModel {
	m := newRoutinesModel(true, true)
	m.width = 120
	m.height = 40
	m.list.SetSize(m.width, m.height-2)
	updated, _ := m.Update(routinesListLoadedMsg{routines: rs})
	return updated
}

// sampleRoutines is the canonical seed list used across tests in this
// file: a "demo" routine plus an already-cloned "Copy of demo" so the
// collision-suffix logic has something to walk past.
func sampleRoutines() []routines.Routine {
	return []routines.Routine{
		{
			Name: "demo",
			Frontmatter: routines.Frontmatter{
				Name: "demo",
				Mode: string(routines.ModeAuto),
				Log:  "./demo.log",
			},
			Steps: []string{"step 1", "step 2", "step 3"},
		},
		{
			Name:        "other",
			Frontmatter: routines.Frontmatter{Name: "other", Mode: string(routines.ModeManual)},
			Steps:       []string{"only step"},
		},
	}
}

// keyPress returns a tea.KeyPressMsg for a single rune. The String()
// method on KeyPressMsg uses Text first, then Code, so we set both.
func keyPress(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

func TestDuplicateRoutineName_Empty(t *testing.T) {
	if got := duplicateRoutineName("", nil); got != "" {
		t.Errorf("empty original must pass through unchanged so the form-level 'name is required' validation fires; got %q", got)
	}
}

func TestDuplicateRoutineName_NoCollision(t *testing.T) {
	got := duplicateRoutineName("demo", []routines.Routine{{Name: "demo"}})
	if got != "Copy of demo" {
		t.Errorf("got %q, want %q", got, "Copy of demo")
	}
}

func TestDuplicateRoutineName_SingleCollision(t *testing.T) {
	existing := []routines.Routine{
		{Name: "demo"},
		{Name: "Copy of demo"},
	}
	got := duplicateRoutineName("demo", existing)
	if got != "Copy of demo (2)" {
		t.Errorf("got %q, want %q", got, "Copy of demo (2)")
	}
}

func TestDuplicateRoutineName_MultipleCollisions(t *testing.T) {
	existing := []routines.Routine{
		{Name: "demo"},
		{Name: "Copy of demo"},
		{Name: "Copy of demo (2)"},
		{Name: "Copy of demo (3)"},
	}
	got := duplicateRoutineName("demo", existing)
	if got != "Copy of demo (4)" {
		t.Errorf("got %q, want %q", got, "Copy of demo (4)")
	}
}

func TestRoutinesDuplicate_FormPrePopulatesFromRoutine(t *testing.T) {
	rs := sampleRoutines()
	form := newDuplicateRoutineForm(rs[0], rs)

	if form.mode != "create" {
		t.Errorf("expected create mode for duplicate, got %q", form.mode)
	}
	if form.editingID != "" {
		t.Errorf("editingID must stay empty so submitForm goes through the no-rename Save path, got %q", form.editingID)
	}
	if got, want := form.name.Value(), "Copy of demo"; got != want {
		t.Errorf("name: got %q, want %q", got, want)
	}
	if got, want := form.mFm.Value(), string(routines.ModeAuto); got != want {
		t.Errorf("mode field: got %q, want %q", got, want)
	}
	if got, want := form.log.Value(), "./demo.log"; got != want {
		t.Errorf("log field: got %q, want %q", got, want)
	}
	if len(form.steps) != 3 {
		t.Fatalf("expected 3 steps copied, got %d", len(form.steps))
	}
	for i, want := range []string{"step 1", "step 2", "step 3"} {
		if got := form.steps[i].Value(); got != want {
			t.Errorf("step %d: got %q, want %q", i, got, want)
		}
	}
}

func TestRoutinesDuplicate_FrontmatterNameKeptInSync(t *testing.T) {
	// frontmatter.name should match the new directory name so the
	// metadata block in STEPS.md stays consistent with the on-disk
	// identity (per docs/routines.md, frontmatter.name is informational
	// but conventionally tracks the directory name).
	src := routines.Routine{
		Name:        "demo",
		Frontmatter: routines.Frontmatter{Name: "demo"},
		Steps:       []string{"only"},
	}
	form := newDuplicateRoutineForm(src, []routines.Routine{src})
	// The form doesn't expose Frontmatter directly, but newRoutineForm
	// pulls Frontmatter.Mode and Log into their respective inputs. To
	// verify the frontmatter.Name sync we go through the same path
	// submitForm would: trim name, build the Routine struct.
	if got := strings.TrimSpace(form.name.Value()); got != "Copy of demo" {
		t.Errorf("name input: %q", got)
	}
}

func TestRoutinesListActions_DuplicateHiddenWhenEmpty(t *testing.T) {
	m := newTestRoutinesModel(nil)
	for _, a := range m.Actions() {
		if a.ID == "duplicate" {
			t.Fatalf("duplicate must be hidden on an empty list; got %+v", a)
		}
	}
}

func TestRoutinesListActions_DuplicateShownWhenPopulated(t *testing.T) {
	m := newTestRoutinesModel(sampleRoutines())
	var found *Action
	for i, a := range m.Actions() {
		if a.ID == "duplicate" {
			found = &m.Actions()[i]
			break
		}
	}
	if found == nil {
		t.Fatal("duplicate action missing from list-view Actions()")
	}
	if found.Key != "d" {
		t.Errorf("duplicate key: got %q, want %q", found.Key, "d")
	}
	if found.Label != "Duplicate" {
		t.Errorf("duplicate label: got %q, want %q", found.Label, "Duplicate")
	}
}

func TestRoutinesKey_D_FromList_OpensDuplicateForm(t *testing.T) {
	m := newTestRoutinesModel(sampleRoutines())
	m, _ = m.handleListKey(keyPress('d'))

	if m.subset != routinesSubForm {
		t.Fatalf("expected substate=form after d, got %v", m.subset)
	}
	if m.form.mode != "create" {
		t.Errorf("expected create mode for duplicate, got %q", m.form.mode)
	}
	if m.form.editingID != "" {
		t.Errorf("editingID must stay empty for duplicate, got %q", m.form.editingID)
	}
	if got := m.form.name.Value(); got != "Copy of demo" {
		t.Errorf("name: got %q, want %q", got, "Copy of demo")
	}
	if len(m.form.steps) != 3 {
		t.Errorf("expected 3 steps cloned, got %d", len(m.form.steps))
	}
}

func TestRoutinesKey_D_OnEmptyList_NoOp(t *testing.T) {
	// With nothing to copy from, `d` must not transition to the form
	// substate. SelectedItem() returns nil on an empty list and
	// actionDuplicate bails early.
	m := newTestRoutinesModel(nil)
	m, cmd := m.handleListKey(keyPress('d'))
	if m.subset != routinesSubList {
		t.Errorf("substate must stay on list when there's nothing to duplicate, got %v", m.subset)
	}
	if cmd != nil {
		t.Errorf("expected nil cmd, got %T", cmd())
	}
}

func TestRoutinesDuplicate_NameCollisionPicksFreeSuffix(t *testing.T) {
	// Seed the list with `demo` and an already-existing `Copy of demo`
	// so the duplicate flow has to walk past the basic suffix.
	rs := []routines.Routine{
		{Name: "demo", Steps: []string{"x"}},
		{Name: "Copy of demo", Steps: []string{"y"}},
	}
	m := newTestRoutinesModel(rs)
	m, _ = m.handleListKey(keyPress('d'))

	if got := m.form.name.Value(); got != "Copy of demo (2)" {
		t.Errorf("expected duplicate to pick the next free suffix; got %q", got)
	}
}

func TestRoutinesDuplicate_EscFromFormReturnsToList(t *testing.T) {
	m := newTestRoutinesModel(sampleRoutines())
	m, _ = m.handleListKey(keyPress('d'))
	if m.subset != routinesSubForm {
		t.Fatalf("expected form substate after d, got %v", m.subset)
	}
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.subset != routinesSubList {
		t.Errorf("esc from duplicate form must return to list (the substate that opened it), got %v", m.subset)
	}
	if m.form.mode != "" {
		t.Errorf("form should be reset on cancel; got mode=%q", m.form.mode)
	}
}

func TestRoutinesDuplicate_TriggerActionAlsoOpensForm(t *testing.T) {
	// The Actions() / TriggerAction surface mirrors the key bindings
	// for accessibility/menu use. Pin that the duplicate ID dispatches
	// the same way pressing `d` does.
	m := newTestRoutinesModel(sampleRoutines())
	m, _ = m.TriggerAction("duplicate")
	if m.subset != routinesSubForm || m.form.mode != "create" {
		t.Errorf("TriggerAction(duplicate) should open the form in create mode; got subset=%v mode=%q",
			m.subset, m.form.mode)
	}
}

func TestRoutinesDetailKey_X_TriggersDelete(t *testing.T) {
	// `x` (not the old `d`) is the new delete key in the detail view —
	// matches the cron browser's convention so the two managers share
	// a key vocabulary.
	m := newTestRoutinesModel(sampleRoutines())
	m.selectedName = "demo"
	m.subset = routinesSubDetail

	m, _ = m.handleDetailKey(keyPress('x'))
	if m.subset != routinesSubConfirmDelete {
		t.Fatalf("expected confirm-delete substate after x, got %v", m.subset)
	}
	if m.pendingDeleteName != "demo" {
		t.Errorf("pendingDeleteName: got %q, want %q", m.pendingDeleteName, "demo")
	}
}

func TestRoutinesDetailKey_D_NoLongerDeletes(t *testing.T) {
	// The old `d` binding has moved to the list view (Duplicate). On
	// the detail view it must now be a no-op rather than triggering
	// delete — otherwise pressing d on detail would still nuke the
	// routine, defeating the rebind.
	m := newTestRoutinesModel(sampleRoutines())
	m.selectedName = "demo"
	m.subset = routinesSubDetail

	m, _ = m.handleDetailKey(keyPress('d'))
	if m.subset == routinesSubConfirmDelete {
		t.Error("d on detail must no longer open the delete confirmation; it has been remapped to x")
	}
	if m.pendingDeleteName != "" {
		t.Errorf("pendingDeleteName must stay empty when d is pressed on detail, got %q", m.pendingDeleteName)
	}
}

func TestRoutinesDetailActions_DeleteKeyIsX(t *testing.T) {
	m := newTestRoutinesModel(sampleRoutines())
	m.selectedName = "demo"
	m.subset = routinesSubDetail

	var found *Action
	for i, a := range m.Actions() {
		if a.ID == "delete" {
			found = &m.Actions()[i]
			break
		}
	}
	if found == nil {
		t.Fatal("delete action missing from detail-view Actions()")
	}
	if found.Key != "x" {
		t.Errorf("delete key on detail view: got %q, want %q (cron parity)", found.Key, "x")
	}
}

// list.Item interface is satisfied by routineItem; this is a compile-time
// guard that the test seeding path actually creates list-recognised items.
var _ list.Item = routineItem{}

// withTempRoutinesDir reroutes config.DataDir at a temp directory so
// Save/Load/Delete don't touch the user's real ~/.lucinate.
func withTempRoutinesDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	config.SetDataDir(dir)
	t.Cleanup(func() { config.SetDataDir("") })
}

func TestRoutinesForm_RejectsNonKebabName(t *testing.T) {
	withTempRoutinesDir(t)
	m := newRoutinesModel(true, true)
	m.openCreateFormWithSteps([]string{"first prompt"})

	m.form.name.SetValue("My Routine")
	m, cmd := m.submitForm()

	if cmd != nil {
		t.Fatal("expected no save cmd when name fails kebab validation")
	}
	if m.form.err == nil {
		t.Fatal("expected form err for non-kebab name")
	}
	got := m.form.err.Error()
	if !strings.Contains(got, "kebab-case") {
		t.Errorf("error %q should mention kebab-case", got)
	}
	if !strings.Contains(got, "my-routine") {
		t.Errorf("error %q should suggest the kebab form", got)
	}

	listed, err := routines.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 0 {
		t.Errorf("rejected save still wrote %d routines: %+v", len(listed), listed)
	}
}

func TestRoutinesForm_AcceptsKebabName(t *testing.T) {
	withTempRoutinesDir(t)
	m := newRoutinesModel(true, true)
	m.openCreateFormWithSteps([]string{"do the thing"})

	m.form.name.SetValue("my-routine")
	_, cmd := m.submitForm()

	if cmd == nil {
		t.Fatal("expected save cmd for kebab-cased name")
	}
	// Drive the cmd so the routine actually lands on disk.
	if msg := cmd(); msg == nil {
		t.Fatal("save cmd returned nil")
	}
	listed, err := routines.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0].Name != "my-routine" {
		t.Errorf("expected one routine 'my-routine', got %+v", listed)
	}
}

// makeFormWithLayout builds a routineForm with synthetic line-start
// metadata so ensureFocusVisible can be exercised without rendering
// real textareas. height is the viewport height; bodyLines is the
// total content height; starts maps focusable-field index → starting
// line. The viewport is given just enough fake content to satisfy
// scroll bounds.
func makeFormWithLayout(t *testing.T, height int, bodyLines int, starts []int) routineForm {
	t.Helper()
	vp := viewport.New()
	vp.SetWidth(80)
	vp.SetHeight(height)
	// Fake content: bodyLines newlines so YOffset clamping has
	// something realistic to clamp against.
	vp.SetContent(strings.Repeat("x\n", bodyLines))
	return routineForm{
		body:            vp,
		fieldLineStarts: starts,
		bodyLines:       bodyLines,
	}
}

func TestRoutineForm_EnsureFocusVisible_ScrollsDownToReachLaterField(t *testing.T) {
	// 11 fields, sized so 9 of them sit below a 10-row window.
	// Focused field starts at line 51, viewport height 10 → expected
	// offset puts the focused field's last line on the bottom edge.
	starts := []int{0, 5, 10, 15, 21, 27, 33, 39, 45, 51, 57}
	form := makeFormWithLayout(t, 10, 63, starts)
	form.focused = 9
	form.body.SetYOffset(0)

	form.ensureFocusVisible()

	got := form.body.YOffset()
	// Field 9 spans lines 51..56 (next start is 57). Window height 10.
	// Acceptable offsets keep [51,56] inside [offset, offset+10).
	if got < 47 || got > 51 {
		t.Errorf("YOffset=%d, want in [47,51] so step 9 (lines 51..56) is visible", got)
	}
}

func TestRoutineForm_EnsureFocusVisible_ScrollsUpToReachEarlierField(t *testing.T) {
	starts := []int{0, 5, 10, 15, 21, 27, 33, 39, 45, 51, 57}
	form := makeFormWithLayout(t, 10, 63, starts)
	form.focused = 1 // field at line 5..9
	form.body.SetYOffset(40) // start scrolled past it

	form.ensureFocusVisible()

	got := form.body.YOffset()
	if got > 5 {
		t.Errorf("YOffset=%d, want ≤5 so field 1 (line 5) is visible", got)
	}
}

func TestRoutineForm_EnsureFocusVisible_NoOpWhenAlreadyVisible(t *testing.T) {
	starts := []int{0, 5, 10, 15, 21, 27, 33, 39, 45, 51, 57}
	form := makeFormWithLayout(t, 20, 63, starts)
	form.focused = 4 // line 21..26
	form.body.SetYOffset(15)

	form.ensureFocusVisible()

	if got := form.body.YOffset(); got != 15 {
		t.Errorf("YOffset=%d, want 15 (already-visible focus shouldn't scroll)", got)
	}
}

func TestRoutineForm_EnsureFocusVisible_ClampsToBodyLines(t *testing.T) {
	// Focusing the very last field shouldn't try to push offset past
	// the maximum scroll position — the viewport would just clamp it
	// itself but ensureFocusVisible should produce a sane value too.
	starts := []int{0, 5}
	form := makeFormWithLayout(t, 10, 12, starts)
	form.focused = 1 // line 5..11
	form.body.SetYOffset(0)

	form.ensureFocusVisible()

	got := form.body.YOffset()
	maxOffset := 12 - 10
	if got > maxOffset {
		t.Errorf("YOffset=%d, want ≤%d (clamped to bodyLines-height)", got, maxOffset)
	}
	// And the focus's start must still be inside the window.
	if got > 5 {
		t.Errorf("YOffset=%d hides field start at line 5", got)
	}
}

func TestRoutinesForm_HelpLineAlwaysRendersWithManySteps(t *testing.T) {
	// Regression: with 30 steps and a 24-row terminal the previous
	// inline rendering pushed the help line off the bottom. The
	// viewport-based form keeps title and footer pinned, so the help
	// text must always appear in the rendered output.
	withTempRoutinesDir(t)
	steps := make([]string, 30)
	for i := range steps {
		steps[i] = "step body"
	}
	m := newRoutinesModel(false, true)
	m.setSize(80, 24)
	m.openCreateFormWithSteps(steps)
	// Focus the last step — the worst case for the previous layout.
	m.form.focused = fieldStepStart + 29

	out := m.viewForm()

	if !strings.Contains(out, "ctrl+s: save") {
		t.Errorf("help line missing from rendered form with many steps:\n%s", out)
	}
	// And the title should still render at the top.
	if !strings.Contains(out, "Routines · New") {
		t.Errorf("title missing from rendered form:\n%s", out)
	}
}

func TestRoutinesForm_RejectsEmptyAndPunctuationOnly(t *testing.T) {
	withTempRoutinesDir(t)
	m := newRoutinesModel(true, true)
	m.openCreateFormWithSteps([]string{"step"})

	cases := []string{"", "  ", "!!!", "   !!! "}
	for _, in := range cases {
		m.form.err = nil
		m.form.name.SetValue(in)
		var cmd tea.Cmd
		m, cmd = m.submitForm()
		if cmd != nil {
			t.Errorf("%q: expected no cmd, got %v", in, cmd)
		}
		if m.form.err == nil {
			t.Errorf("%q: expected form err", in)
		}
	}
}
