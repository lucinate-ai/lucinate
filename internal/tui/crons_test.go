package tui

import (
	"testing"

	"github.com/a3tai/openclaw-go/protocol"
	tea "charm.land/bubbletea/v2"
)

func newTestCronsModel(t *testing.T) (cronsModel, *fakeBackend) {
	t.Helper()
	fake := newFakeBackend()
	m := newCronsModel(fake, "agent-1", "Scout", false, nil)
	m.setSize(120, 40)
	return m, fake
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

func TestCronsKey_Enter_OpensDetail(t *testing.T) {
	m, _ := newTestCronsModel(t)
	m, _ = m.Update(cronsLoadedMsg{jobs: sampleJobs()})
	m, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.subset != cronSubDetail {
		t.Fatalf("expected substate=detail, got %v", m.subset)
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
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.subset != cronSubDetail {
		t.Fatal("setup: expected detail substate")
	}
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
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	f.payloadText.SetValue("Please generate today's report.")
	f.sessionTarget = "main"
	f.wakeMode = "now"
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
	if params.SessionTarget != "main" || params.WakeMode != "now" {
		t.Errorf("SessionTarget/WakeMode: %q/%q", params.SessionTarget, params.WakeMode)
	}
	if params.AgentID == nil || *params.AgentID != "agent-1" {
		t.Errorf("AgentID: %+v", params.AgentID)
	}
	if params.Enabled == nil || !*params.Enabled {
		t.Errorf("Enabled: %+v", params.Enabled)
	}
}

func TestCronsConfirmDelete_YesDispatchesRemove(t *testing.T) {
	m, fake := newTestCronsModel(t)
	m, _ = m.Update(cronsLoadedMsg{jobs: sampleJobs()})
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m, _ = m.handleKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m, _ = m.handleKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.subset != cronSubDetail {
		t.Errorf("expected return to detail after n, got %v", m.subset)
	}
	if m.pendingDeleteID != "" {
		t.Errorf("expected pendingDeleteID cleared, got %q", m.pendingDeleteID)
	}
}

func TestCronsTranscript_PicksMostRecentRunSession(t *testing.T) {
	m, _ := newTestCronsModel(t)
	jobs := sampleJobs()
	m, _ = m.Update(cronsLoadedMsg{jobs: jobs})
	m, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	// Seed runs as if loadRuns completed.
	m, _ = m.Update(cronRunsLoadedMsg{
		jobID:   "job-1",
		entries: []protocol.CronRunLogEntry{{SessionKey: "agent-1:cron:job-1:run-42"}},
	})
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: 'T', Text: "T"})
	if cmd == nil {
		t.Fatal("expected a cmd from T")
	}
	msg := cmd()
	sel, ok := msg.(sessionSelectedMsg)
	if !ok {
		t.Fatalf("expected sessionSelectedMsg, got %T", msg)
	}
	if sel.sessionKey != "agent-1:cron:job-1:run-42" {
		t.Errorf("sessionKey: %q", sel.sessionKey)
	}
	if sel.agentID != "agent-1" {
		t.Errorf("agentID: %q", sel.agentID)
	}
}
