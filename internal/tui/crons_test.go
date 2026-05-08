package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/a3tai/openclaw-go/protocol"
)

func newTestCronsModel(t *testing.T) (cronsModel, *fakeBackend) {
	t.Helper()
	fake := newFakeBackend()
	m := newCronsModel(fake, "agent-1", "Scout", false, nil, false)
	m.setSize(120, 40)
	return m, fake
}

// openDetail drives the picker through its "enter + run-log payload"
// transition into the detail substate so tests that need to start
// in detail don't have to repeat the two-step plumbing. The post-
// loading-state change made enter alone insufficient — the detail
// substate is gated on cronRunsLoadedMsg arriving.
func openDetail(t *testing.T, m cronsModel) cronsModel {
	t.Helper()
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m, _ = m.Update(cronRunsLoadedMsg{jobID: m.selectedID})
	if m.subset != cronSubDetail {
		t.Fatalf("openDetail: expected detail substate, got %v", m.subset)
	}
	return m
}

func sampleJobs() []protocol.CronJob {
	enabled := true
	return []protocol.CronJob{
		{
			ID:            "job-1",
			Name:          "Daily report",
			AgentID:       "agent-1",
			Enabled:       enabled,
			SessionTarget: "isolated",
			WakeMode:      "now",
			Schedule:      protocol.CronSchedule{Kind: "cron", Expr: "0 9 * * *", Tz: "UTC"},
			Payload:       protocol.CronPayload{Kind: "agentTurn", Text: "Generate report."},
		},
		{
			ID:            "job-2",
			Name:          "Other agent thing",
			AgentID:       "agent-2",
			Enabled:       enabled,
			SessionTarget: "main",
			WakeMode:      "next-heartbeat",
			Schedule:      protocol.CronSchedule{Kind: "cron", Expr: "*/15 * * * *"},
			Payload:       protocol.CronPayload{Kind: "agentTurn", Text: "Tick."},
		},
	}
}

func TestCronsLoadedMsg_PopulatesList(t *testing.T) {
	m, _ := newTestCronsModel(t)
	m, _ = m.Update(cronsLoadedMsg{jobs: sampleJobs()})
	if m.loading {
		t.Error("expected loading=false after cronsLoadedMsg")
	}
	// filter is agent-1 by default → only job-1 should be shown.
	if got := len(m.list.Items()); got != 1 {
		t.Fatalf("expected 1 item after filtering, got %d", got)
	}
	item, ok := m.list.SelectedItem().(cronItem)
	if !ok || item.job.ID != "job-1" {
		t.Errorf("expected job-1 to be selected, got %+v", m.list.SelectedItem())
	}
}

func TestCronsLoadedMsg_Error(t *testing.T) {
	m, _ := newTestCronsModel(t)
	m, _ = m.Update(cronsLoadedMsg{err: errString("gateway down")})
	if m.err == nil {
		t.Error("expected err to be set")
	}
	if m.loading {
		t.Error("expected loading=false even on error")
	}
}

func TestCronsKey_A_TogglesAllAgentsFilter(t *testing.T) {
	m, _ := newTestCronsModel(t)
	m, _ = m.Update(cronsLoadedMsg{jobs: sampleJobs()})
	if got := len(m.list.Items()); got != 1 {
		t.Fatalf("expected filtered list to start with 1 item, got %d", got)
	}
	m, _ = m.handleListKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if got := len(m.list.Items()); got != 2 {
		t.Errorf("expected 2 items after toggling all-agents, got %d", got)
	}
	m, _ = m.handleListKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if got := len(m.list.Items()); got != 1 {
		t.Errorf("expected back to 1 item after toggling off, got %d", got)
	}
}

func TestCronsKey_Enter_FreezesUntilRunsLoad(t *testing.T) {
	m, _ := newTestCronsModel(t)
	m, _ = m.Update(cronsLoadedMsg{jobs: sampleJobs()})
	m, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	// Enter alone freezes the list and holds the user on a loading
	// line; the detail substate is gated on the run-log payload.
	if !m.selecting {
		t.Error("expected selecting=true after enter")
	}
	if m.subset == cronSubDetail {
		t.Errorf("detail substate should not flip until cronRunsLoadedMsg, got %v", m.subset)
	}
	if m.selectedID != "job-1" {
		t.Errorf("expected selectedID=job-1, got %q", m.selectedID)
	}
	if cmd == nil {
		t.Fatal("expected a runs-load command from enter")
	}
	msg := cmd()
	if _, ok := msg.(cronRunsLoadedMsg); !ok {
		t.Errorf("expected cronRunsLoadedMsg, got %T", msg)
	}

	// Delivering the payload drops the freeze and reveals detail.
	m, _ = m.Update(cronRunsLoadedMsg{jobID: "job-1"})
	if m.selecting {
		t.Error("expected selecting=false after run-log payload")
	}
	if m.subset != cronSubDetail {
		t.Errorf("expected detail substate after payload, got %v", m.subset)
	}
}

func TestCronsSelecting_BlocksNavigation(t *testing.T) {
	m, _ := newTestCronsModel(t)
	// Multi-job list (no agent filter) so navigation has somewhere
	// to go.
	m.filterAgentID = ""
	m, _ = m.Update(cronsLoadedMsg{jobs: sampleJobs()})
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.selecting {
		t.Fatal("setup: expected selecting=true after enter")
	}
	startIdx := m.list.Index()

	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	if m.list.Index() != startIdx {
		t.Errorf("list index moved while selecting: was %d, now %d", startIdx, m.list.Index())
	}
	if cmd != nil {
		t.Errorf("expected no cmd while selecting, got %T", cmd)
	}
}

func TestCronsSelecting_HidesActions(t *testing.T) {
	m, _ := newTestCronsModel(t)
	m, _ = m.Update(cronsLoadedMsg{jobs: sampleJobs()})
	if len(m.Actions()) == 0 {
		t.Fatal("setup: expected actions before enter")
	}
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.Actions()) != 0 {
		t.Errorf("expected no actions while selecting, got %+v", m.Actions())
	}
}

func TestCronsSelecting_ViewRendersLoading(t *testing.T) {
	m, _ := newTestCronsModel(t)
	m, _ = m.Update(cronsLoadedMsg{jobs: sampleJobs()})
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	view := m.View()
	if !contains(view, "Loading Daily report") {
		t.Errorf("expected loading line naming the picked job, got:\n%s", view)
	}
}

func TestCronsSelecting_ErrorClearsFreezeAndSurfaces(t *testing.T) {
	m, _ := newTestCronsModel(t)
	m, _ = m.Update(cronsLoadedMsg{jobs: sampleJobs()})
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m, _ = m.Update(cronRunsLoadedMsg{jobID: "job-1", err: errString("network down")})
	if m.selecting {
		t.Error("expected selecting=false even on error so the picker isn't stranded")
	}
	if m.subset != cronSubDetail {
		t.Errorf("expected detail substate so the user sees the run-log error, got %v", m.subset)
	}
	if m.runsErr == nil {
		t.Error("expected runsErr to be surfaced on the detail page")
	}
}

func TestCronsKey_Esc_FromList_GoesBack(t *testing.T) {
	m, _ := newTestCronsModel(t)
	m, _ = m.Update(cronsLoadedMsg{jobs: sampleJobs()})
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("expected a cmd from esc on list")
	}
	if _, ok := cmd().(goBackFromCronsMsg); !ok {
		t.Errorf("expected goBackFromCronsMsg, got %T", cmd())
	}
}

func TestCronsKey_Esc_FromDetail_ReturnsToList(t *testing.T) {
	m, _ := newTestCronsModel(t)
	m, _ = m.Update(cronsLoadedMsg{jobs: sampleJobs()})
	m = openDetail(t, m)
	m, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.subset != cronSubList {
		t.Errorf("expected substate=list after esc from detail, got %v", m.subset)
	}
	if cmd != nil {
		// esc inside the detail view shouldn't escape to the parent.
		if msg := cmd(); msg != nil {
			if _, ok := msg.(goBackFromCronsMsg); ok {
				t.Errorf("esc from detail should not bubble to goBackFromCronsMsg, got %T", msg)
			}
		}
	}
}

func TestCronsKey_T_TogglesEnabled(t *testing.T) {
	m, fake := newTestCronsModel(t)
	m, _ = m.Update(cronsLoadedMsg{jobs: sampleJobs()})
	m = openDetail(t, m)
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: 't', Text: "t"})
	if cmd == nil {
		t.Fatal("expected a cmd from toggle")
	}
	msg := cmd()
	tm, ok := msg.(cronJobToggledMsg)
	if !ok {
		t.Fatalf("expected cronJobToggledMsg, got %T", msg)
	}
	if tm.jobID != "job-1" {
		t.Errorf("expected jobID=job-1, got %q", tm.jobID)
	}
	if tm.enabled != false {
		t.Errorf("expected enabled flipped to false, got %v", tm.enabled)
	}
	if fake.lastCronUpdate == nil {
		t.Fatal("expected CronUpdate to be invoked")
	}
	if fake.lastCronUpdate.Patch.Enabled == nil || *fake.lastCronUpdate.Patch.Enabled {
		t.Errorf("expected patch.Enabled=false, got %+v", fake.lastCronUpdate.Patch.Enabled)
	}
}

func TestCronsForm_RefusesUnsupportedScheduleKind(t *testing.T) {
	job := protocol.CronJob{
		ID:            "weird",
		Name:          "Weird",
		AgentID:       "agent-1",
		Enabled:       true,
		SessionTarget: "isolated",
		WakeMode:      "now",
		Schedule:      protocol.CronSchedule{Kind: "every"},
		Payload:       protocol.CronPayload{Kind: "agentTurn", Text: "X"},
	}
	form, banner := newEditForm(job)
	if banner == "" {
		t.Fatal("expected unsupported banner for schedule.kind=every")
	}
	if form.editingID != "weird" {
		t.Errorf("expected editingID=weird, got %q", form.editingID)
	}
}

func TestCronsForm_BuildsCronAddParams(t *testing.T) {
	f := newCreateForm()
	f.name.SetValue("Daily report")
	f.description.SetValue("Generate a report")
	f.cronExpr.SetValue("0 9 * * *")
	f.timezone.SetValue("Europe/London")
	f.agentID.SetValue("agent-1")
	f.model.SetValue("claude-opus-4-7")
	f.payloadText.SetValue("Please generate today's report.")
	f.sessionTarget = "main"
	f.wakeMode = "now"
	f.deliveryMode = "announce"
	f.deliveryTarget.SetValue("slack:#alerts")
	f.enabled = true

	params := buildAddParams(f)
	if params.Name != "Daily report" {
		t.Errorf("Name: %q", params.Name)
	}
	if params.Schedule.Kind != "cron" || params.Schedule.Expr != "0 9 * * *" || params.Schedule.Tz != "Europe/London" {
		t.Errorf("Schedule: %+v", params.Schedule)
	}
	if params.Payload.Kind != "agentTurn" || params.Payload.Text != "Please generate today's report." {
		t.Errorf("Payload: %+v", params.Payload)
	}
	if params.Payload.Model != "claude-opus-4-7" {
		t.Errorf("Payload.Model: %q", params.Payload.Model)
	}
	if params.SessionTarget != "main" || params.WakeMode != "now" {
		t.Errorf("SessionTarget/WakeMode: %q/%q", params.SessionTarget, params.WakeMode)
	}
	if params.AgentID == nil || *params.AgentID != "agent-1" {
		t.Errorf("AgentID: %+v", params.AgentID)
	}
	if params.Enabled == nil || !*params.Enabled {
		t.Errorf("Enabled: %+v", params.Enabled)
	}
	if params.Delivery == nil || params.Delivery.Mode != "announce" || params.Delivery.Channel != "slack:#alerts" {
		t.Errorf("Delivery: %+v", params.Delivery)
	}
}

func TestCronsForm_BuildDelivery_Webhook(t *testing.T) {
	f := newCreateForm()
	f.deliveryMode = "webhook"
	f.deliveryTarget.SetValue("https://example.com/hook")
	d := buildDelivery(f)
	if d == nil || d.Mode != "webhook" || d.To != "https://example.com/hook" {
		t.Errorf("expected webhook delivery, got %+v", d)
	}
}

func TestCronsForm_BuildDelivery_NoneReturnsNil(t *testing.T) {
	f := newCreateForm()
	if d := buildDelivery(f); d != nil {
		t.Errorf("expected nil for mode=none, got %+v", d)
	}
}

func TestCronsForm_BuildJobPatchMap_ClearsModelAndDescription(t *testing.T) {
	f := newCreateForm()
	f.editingID = "job-1"
	f.mode = "edit"
	f.name.SetValue("Renamed")
	// description and model are intentionally left empty to simulate
	// the user clearing them in the edit form.
	f.cronExpr.SetValue("0 9 * * *")
	f.timezone.SetValue("UTC")
	f.agentID.SetValue("agent-1")
	f.payloadText.SetValue("Do thing.")
	f.sessionTarget = "isolated"
	f.wakeMode = "now"
	f.deliveryMode = "none"
	f.enabled = true

	patch := buildJobPatchMap(f)

	if got := patch["description"]; got != "" {
		t.Errorf("expected description to be present and empty, got %#v", got)
	}
	payload, ok := patch["payload"].(map[string]any)
	if !ok {
		t.Fatalf("expected payload map, got %T", patch["payload"])
	}
	if got, ok := payload["model"]; !ok {
		t.Errorf("expected payload.model key to be present in patch")
	} else if got != "" {
		t.Errorf("expected payload.model='', got %#v", got)
	}
	delivery, ok := patch["delivery"].(map[string]any)
	if !ok {
		t.Fatalf("expected delivery map, got %T", patch["delivery"])
	}
	if delivery["mode"] != "none" {
		t.Errorf("expected delivery.mode=none, got %#v", delivery["mode"])
	}
}

func TestCronsForm_EditSubmitUsesRawUpdate(t *testing.T) {
	m, fake := newTestCronsModel(t)
	jobs := []protocol.CronJob{{
		ID:            "job-1",
		Name:          "Old name",
		AgentID:       "agent-1",
		Enabled:       true,
		SessionTarget: "isolated",
		WakeMode:      "now",
		Schedule:      protocol.CronSchedule{Kind: "cron", Expr: "0 9 * * *"},
		Payload:       protocol.CronPayload{Kind: "agentTurn", Text: "old text", Model: "haiku-4-5"},
	}}
	m, _ = m.Update(cronsLoadedMsg{jobs: jobs})
	m = openDetail(t, m)
	m, _ = m.handleKey(tea.KeyPressMsg{Code: 'e', Text: "e"}) // open edit form
	if m.subset != cronSubForm {
		t.Fatalf("expected form substate, got %v", m.subset)
	}
	// Clear the model value, simulating the user emptying the field.
	m.form.model.SetValue("")

	_, cmd := m.submitForm()
	if cmd == nil {
		t.Fatal("expected a save cmd")
	}
	cmd()

	if fake.lastCronUpdateID != "job-1" {
		t.Errorf("lastCronUpdateID: %q", fake.lastCronUpdateID)
	}
	payload, ok := fake.lastCronUpdateRaw["payload"].(map[string]any)
	if !ok {
		t.Fatalf("expected payload map in raw patch, got %T", fake.lastCronUpdateRaw["payload"])
	}
	if model, present := payload["model"]; !present {
		t.Errorf("expected payload.model key on the wire even when empty")
	} else if model != "" {
		t.Errorf("expected payload.model=\"\", got %#v", model)
	}
}

func TestCronsForm_PrePopulatesModelAndDelivery(t *testing.T) {
	job := protocol.CronJob{
		ID:            "job-1",
		Name:          "Foo",
		Enabled:       true,
		SessionTarget: "isolated",
		WakeMode:      "now",
		Schedule:      protocol.CronSchedule{Kind: "cron", Expr: "0 9 * * *"},
		Payload:       protocol.CronPayload{Kind: "agentTurn", Text: "hi", Model: "haiku-4-5"},
		Delivery:      &protocol.CronDelivery{Mode: "announce", Channel: "slack:#general"},
	}
	form, banner := newEditForm(job)
	if banner != "" {
		t.Fatalf("unexpected unsupported banner: %q", banner)
	}
	if got := form.model.Value(); got != "haiku-4-5" {
		t.Errorf("model: %q", got)
	}
	if form.deliveryMode != "announce" {
		t.Errorf("deliveryMode: %q", form.deliveryMode)
	}
	if got := form.deliveryTarget.Value(); got != "slack:#general" {
		t.Errorf("deliveryTarget: %q", got)
	}
}

func TestCronsConfirmDelete_YesDispatchesRemove(t *testing.T) {
	m, fake := newTestCronsModel(t)
	m, _ = m.Update(cronsLoadedMsg{jobs: sampleJobs()})
	m = openDetail(t, m)
	m, _ = m.handleKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if m.subset != cronSubConfirmDelete {
		t.Fatalf("expected confirm substate, got %v", m.subset)
	}
	if m.pendingDeleteID != "job-1" {
		t.Errorf("pendingDeleteID: %q", m.pendingDeleteID)
	}
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("expected a cmd from y")
	}
	msg := cmd()
	if rm, ok := msg.(cronJobRemovedMsg); !ok {
		t.Errorf("expected cronJobRemovedMsg, got %T", msg)
	} else if rm.jobID != "job-1" {
		t.Errorf("expected removed jobID=job-1, got %q", rm.jobID)
	}
	if len(fake.cronRemoved) != 1 || fake.cronRemoved[0] != "job-1" {
		t.Errorf("expected fake.cronRemoved=[job-1], got %+v", fake.cronRemoved)
	}
}

func TestCronsConfirmDelete_NoCancels(t *testing.T) {
	m, _ := newTestCronsModel(t)
	m, _ = m.Update(cronsLoadedMsg{jobs: sampleJobs()})
	m = openDetail(t, m)
	m, _ = m.handleKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m, _ = m.handleKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.subset != cronSubDetail {
		t.Errorf("expected return to detail after n, got %v", m.subset)
	}
	if m.pendingDeleteID != "" {
		t.Errorf("expected pendingDeleteID cleared, got %q", m.pendingDeleteID)
	}
}

func TestCronsTranscript_DispatchesRunLogContent(t *testing.T) {
	m, _ := newTestCronsModel(t)
	jobs := sampleJobs()
	m, _ = m.Update(cronsLoadedMsg{jobs: jobs})
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	// Seed runs (newest first, matching cron.runs sortDir=desc) as if
	// loadRuns completed — including a summary so the transcript action
	// is offered and dispatched with the run-log payload.
	m, _ = m.Update(cronRunsLoadedMsg{
		jobID: "job-1",
		entries: []protocol.CronRunLogEntry{
			{Status: "ok", Summary: "Newest run output."},
			{Status: "error", Error: "boom"},
		},
	})
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: 'T', Text: "T"})
	if cmd == nil {
		t.Fatal("expected a cmd from T")
	}
	msg := cmd()
	sel, ok := msg.(cronTranscriptMsg)
	if !ok {
		t.Fatalf("expected cronTranscriptMsg, got %T", msg)
	}
	if sel.job.ID != "job-1" {
		t.Errorf("job.ID: %q", sel.job.ID)
	}
	if sel.agentName != "agent-1" {
		t.Errorf("agentName: %q", sel.agentName)
	}
	if len(sel.runs) != 2 {
		t.Errorf("expected 2 runs forwarded, got %d", len(sel.runs))
	}
}

func TestCronsTranscript_HiddenWhenNoRunOutput(t *testing.T) {
	m, _ := newTestCronsModel(t)
	jobs := sampleJobs()
	m, _ = m.Update(cronsLoadedMsg{jobs: jobs})
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	// Runs exist but none carry summary or error — no useful content.
	m, _ = m.Update(cronRunsLoadedMsg{
		jobID:   "job-1",
		entries: []protocol.CronRunLogEntry{{Status: "skipped"}},
	})
	for _, a := range m.Actions() {
		if a.ID == "transcript" {
			t.Fatalf("transcript action should be hidden when no run carries summary/error; got %+v", a)
		}
	}
}
