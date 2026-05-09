package tui

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// cronItem wraps a CronJob for the bubbles list.
type cronItem struct {
	job protocol.CronJob
}

func (i cronItem) FilterValue() string {
	if i.job.Name != "" {
		return i.job.Name
	}
	return i.job.ID
}

// cronDelegate renders each cron job as two lines: a bold name (with the
// next-run relative time) and a row of status chips (session target,
// wake mode, agent, last-run badge).
type cronDelegate struct{}

func (d cronDelegate) Height() int                             { return 2 }
func (d cronDelegate) Spacing() int                            { return 1 }
func (d cronDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d cronDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	i, ok := item.(cronItem)
	if !ok {
		return
	}
	name := i.job.Name
	if name == "" {
		name = i.job.ID
	}
	relative := relativeNextRun(i.job.State.NextRunAtMs)

	var titleLine string
	if index == m.Index() {
		titleLine = lipgloss.NewStyle().Foreground(accent).Bold(true).
			Render(fmt.Sprintf("> %s", name))
	} else {
		titleLine = fmt.Sprintf("  %s", name)
	}
	if relative != "" {
		titleLine += "  " + lipgloss.NewStyle().Foreground(subtle).Render(relative)
	}

	chips := []string{
		chipStyle.Render(orDash(i.job.SessionTarget)),
		chipStyle.Render(wakeModeLabel(i.job.WakeMode)),
	}
	if i.job.AgentID != "" {
		chips = append(chips, chipStyle.Render("agent "+i.job.AgentID))
	}
	chips = append(chips, statusChip(i.job))

	subtitle := "  " + strings.Join(chips, " ")
	fmt.Fprint(w, titleLine+"\n"+subtitle)
}

// chipStyle is a neutral background tag used for sessionTarget / wake /
// agent chips on each list row.
var chipStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("#FFFFFF")).
	Background(lipgloss.Color("#3A3A3A")).
	Padding(0, 1)

// cronStatusOKStyle is the green badge for last-run==ok.
var cronStatusOKStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("#0A2A0A")).
	Background(lipgloss.Color("#5BC85B")).
	Bold(true).
	Padding(0, 1)

// cronStatusErrStyle is the red badge for last-run==error.
var cronStatusErrStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("#FFFFFF")).
	Background(errClr).
	Bold(true).
	Padding(0, 1)

// cronStatusDisabledStyle is the dim badge for disabled jobs.
var cronStatusDisabledStyle = lipgloss.NewStyle().
	Foreground(subtle).
	Background(lipgloss.Color("#2A2A2A")).
	Padding(0, 1)

func statusChip(job protocol.CronJob) string {
	if !job.Enabled {
		return cronStatusDisabledStyle.Render("disabled")
	}
	switch job.State.LastStatus {
	case "ok":
		return cronStatusOKStyle.Render("ok")
	case "error":
		return cronStatusErrStyle.Render("error")
	case "skipped":
		return chipStyle.Render("skipped")
	}
	return chipStyle.Render("idle")
}

func wakeModeLabel(m string) string {
	switch m {
	case "now":
		return "now"
	case "next-heartbeat":
		return "heartbeat"
	}
	return orDash(m)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// relativeNextRun renders a "in 8h", "in 23m", "now" or "—" label for
// the next scheduled run. Past times render as "due".
func relativeNextRun(nextMs *int64) string {
	if nextMs == nil || *nextMs == 0 {
		return "—"
	}
	delta := time.Duration(*nextMs-time.Now().UnixMilli()) * time.Millisecond
	if delta <= 0 {
		return "due"
	}
	if delta < time.Minute {
		return "in <1m"
	}
	if delta < time.Hour {
		return fmt.Sprintf("in %dm", int(delta.Minutes()))
	}
	if delta < 24*time.Hour {
		return fmt.Sprintf("in %dh", int(delta.Hours()))
	}
	return fmt.Sprintf("in %dd", int(delta.Hours()/24))
}

// cronSubState selects which sub-view the model is rendering.
type cronSubState int

const (
	cronSubList cronSubState = iota
	cronSubDetail
	cronSubForm
	cronSubConfirmDelete
)

// cronFormField identifies a field on the create/edit form. The order
// here is the tab order rendered in the form view.
type cronFormField int

const (
	formName cronFormField = iota
	formDescription
	formCronExpr
	formTimezone
	formAgentID
	formModel
	formSessionTarget
	formWakeMode
	formPayloadText
	formDeliveryMode
	formDeliveryTarget
	formEnabled
	formFieldCount
)

// deliveryModes is the cycle order for the delivery-mode toggle in the
// form. "none" leaves the gateway silent after a run; "announce" posts
// to a channel (Slack/Telegram/...); "webhook" POSTs to an external URL.
var deliveryModes = []string{"none", "announce", "webhook"}

// cycleString advances current to the next entry in opts (wrapping to
// the start), returning the new value. Used by multi-state toggles in
// the cron form.
func cycleString(opts []string, current string) string {
	for i, o := range opts {
		if o == current {
			return opts[(i+1)%len(opts)]
		}
	}
	if len(opts) > 0 {
		return opts[0]
	}
	return current
}

// cronForm carries the in-progress create/edit-job form state. The form
// only supports the cron-style schedule kind and the agentTurn payload
// kind to avoid the brittleness of a TUI form modelling the union types
// the gateway exposes; jobs of other kinds open as read-only with a
// banner pointing the user to the gateway CLI.
type cronForm struct {
	mode      string // "create" or "edit"
	editingID string // job ID being edited; "" in create mode

	name           textinput.Model
	description    textinput.Model
	cronExpr       textinput.Model
	timezone       textinput.Model
	agentID        textinput.Model
	model          textinput.Model
	payloadText    textarea.Model
	deliveryTarget textinput.Model // channel for announce, URL for webhook

	sessionTarget string // "main" or "isolated"
	wakeMode      string // "next-heartbeat" or "now"
	deliveryMode  string // "none", "announce", "webhook"
	enabled       bool

	focused     cronFormField
	saving      bool
	err         error
	unsupported string // non-empty when the existing job's kind cannot be edited; rendered as a banner
}

// cronsModel is the cron browser view.
type cronsModel struct {
	list   list.Model
	cron   backend.CronBackend
	subset cronSubState

	// Filter state.
	filterAgentID string
	filterLabel   string
	showAllAgents bool
	jobs          []protocol.CronJob // unfiltered cache, refreshed by loadJobs

	// List loading.
	loading bool
	err     error

	// Detail substate.
	selectedID  string
	runs        []protocol.CronRunLogEntry
	runsLoading bool
	runsErr     error
	// running is set the moment "Run now" is triggered and cleared when
	// the gateway acknowledges. It exists so the detail view can render
	// a "triggering..." banner immediately, before the round-trip
	// completes — without it the user has no signal the keystroke landed.
	running bool
	// runStatus is a transient one-line ack rendered in the detail view
	// after cronJobRanMsg arrives ("Run triggered." / error). It is
	// cleared on the next refresh / navigation.
	runStatus string
	runFailed bool

	// Form substate.
	form cronForm

	// Confirm-delete substate.
	pendingDeleteID   string
	pendingDeleteName string

	hideHints  bool
	activeConn *config.Connection
	width      int
	height     int
}

func newCronsModel(cron backend.CronBackend, filterAgentID, filterLabel string, hideHints bool, activeConn *config.Connection, disableExitKeys bool) cronsModel {
	l := list.New(nil, cronDelegate{}, 0, 0)
	l.Title = "Cron"
	l.SetShowStatusBar(false)
	l.SetShowHelp(!hideHints)
	l.Styles.Title = headerStyle
	l.SetFilteringEnabled(false)
	if disableExitKeys {
		l.KeyMap.Quit.Unbind()
		l.KeyMap.ForceQuit.Unbind()
	}

	return cronsModel{
		list:          l,
		cron:          cron,
		subset:        cronSubList,
		filterAgentID: filterAgentID,
		filterLabel:   filterLabel,
		loading:       true,
		hideHints:     hideHints,
		activeConn:    activeConn,
	}
}

func (m cronsModel) Init() tea.Cmd { return m.loadJobs() }

func (m *cronsModel) setSize(w, h int) {
	m.width = w
	m.height = h
	m.list.SetSize(w, h-2)
	if m.subset == cronSubForm {
		m.sizePayloadTextarea()
	}
}

// loadJobs fetches the full cron-job list from the gateway. We cache
// the unfiltered slice so the agent-filter toggle can re-render
// without a round-trip.
func (m cronsModel) loadJobs() tea.Cmd {
	cron := m.cron
	return func() tea.Msg {
		result, err := cron.CronsList(context.Background(), protocol.CronListParams{
			Enabled: "all",
			SortBy:  "nextRunAtMs",
			SortDir: "asc",
		})
		if err != nil {
			return cronsLoadedMsg{err: err}
		}
		return cronsLoadedMsg{jobs: result.Jobs}
	}
}

// loadRuns fetches the run history for a single cron job. The server
// caps the entry count; we additionally render only the most recent 10
// in the detail view.
func (m cronsModel) loadRuns(jobID string) tea.Cmd {
	cron := m.cron
	limit := 10
	return func() tea.Msg {
		result, err := cron.CronRuns(context.Background(), protocol.CronRunsParams{
			Scope:   "job",
			ID:      jobID,
			Limit:   &limit,
			SortDir: "desc",
		})
		if err != nil {
			return cronRunsLoadedMsg{jobID: jobID, err: err}
		}
		return cronRunsLoadedMsg{jobID: jobID, entries: result.Entries}
	}
}

// applyFilter rebuilds the list items from the cached jobs slice
// using the current filter state. Kept as a separate step so the agent-
// toggle action doesn't have to re-fetch.
func (m *cronsModel) applyFilter() {
	want := m.filterAgentID
	if m.showAllAgents {
		want = ""
	}
	filtered := make([]protocol.CronJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		if want != "" && j.AgentID != want {
			continue
		}
		filtered = append(filtered, j)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		ai, bj := filtered[i].State.NextRunAtMs, filtered[j].State.NextRunAtMs
		switch {
		case ai != nil && bj != nil:
			return *ai < *bj
		case ai != nil:
			return true
		case bj != nil:
			return false
		default:
			return filtered[i].Name < filtered[j].Name
		}
	})
	items := make([]list.Item, len(filtered))
	for i, j := range filtered {
		items[i] = cronItem{job: j}
	}
	m.list.SetItems(items)
}

// findJob returns the cached job by ID, plus a bool indicator. Used by
// the detail / form / delete substates which keep only the ID across
// substate transitions.
func (m cronsModel) findJob(id string) (protocol.CronJob, bool) {
	for _, j := range m.jobs {
		if j.ID == id {
			return j, true
		}
	}
	return protocol.CronJob{}, false
}

func (m cronsModel) Update(msg tea.Msg) (cronsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case cronsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.jobs = msg.jobs
		m.applyFilter()
		return m, nil

	case cronRunsLoadedMsg:
		if msg.jobID != m.selectedID {
			return m, nil
		}
		m.runsLoading = false
		if msg.err != nil {
			m.runsErr = msg.err
			return m, nil
		}
		m.runsErr = nil
		m.runs = msg.entries
		return m, nil

	case cronJobToggledMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		// Reload to pick up the new state and any next-run shift.
		m.loading = true
		return m, m.loadJobs()

	case cronJobRanMsg:
		m.running = false
		if msg.err != nil {
			m.runFailed = true
			m.runStatus = fmt.Sprintf("Run failed: %v", msg.err)
			return m, nil
		}
		m.runFailed = false
		m.runStatus = "Run triggered."
		m.runsLoading = true
		var refreshRuns tea.Cmd
		if m.selectedID != "" {
			refreshRuns = m.loadRuns(m.selectedID)
		}
		m.loading = true
		return m, tea.Batch(m.loadJobs(), refreshRuns)

	case cronJobSavedMsg:
		if msg.err != nil {
			m.form.err = msg.err
			m.form.saving = false
			return m, nil
		}
		// Return to the list and refresh.
		m.subset = cronSubList
		m.form = cronForm{}
		m.loading = true
		return m, m.loadJobs()

	case cronJobRemovedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.subset = cronSubList
		m.selectedID = ""
		m.runs = nil
		m.loading = true
		return m, m.loadJobs()

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	if m.subset == cronSubList {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m cronsModel) handleKey(msg tea.KeyPressMsg) (cronsModel, tea.Cmd) {
	switch m.subset {
	case cronSubList:
		return m.handleListKey(msg)
	case cronSubDetail:
		return m.handleDetailKey(msg)
	case cronSubForm:
		return m.handleFormKey(msg)
	case cronSubConfirmDelete:
		return m.handleConfirmKey(msg)
	}
	return m, nil
}

func (m cronsModel) handleListKey(msg tea.KeyPressMsg) (cronsModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k", "down", "j":
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	case "enter":
		if m.loading || m.err != nil {
			return m, nil
		}
		item, ok := m.list.SelectedItem().(cronItem)
		if !ok {
			return m, nil
		}
		m.selectedID = item.job.ID
		m.subset = cronSubDetail
		m.runs = nil
		m.runsErr = nil
		m.runsLoading = true
		m.runStatus = ""
		m.runFailed = false
		m.running = false
		return m, m.loadRuns(item.job.ID)
	}
	for _, a := range m.Actions() {
		if a.Key == msg.String() {
			return m.TriggerAction(a.ID)
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m cronsModel) handleDetailKey(msg tea.KeyPressMsg) (cronsModel, tea.Cmd) {
	for _, a := range m.Actions() {
		if a.Key == msg.String() {
			return m.TriggerAction(a.ID)
		}
	}
	return m, nil
}

func (m cronsModel) handleConfirmKey(msg tea.KeyPressMsg) (cronsModel, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "y":
		jobID := m.pendingDeleteID
		cron := m.cron
		m.pendingDeleteID = ""
		m.pendingDeleteName = ""
		m.subset = cronSubList
		return m, func() tea.Msg {
			err := cron.CronRemove(context.Background(), jobID)
			return cronJobRemovedMsg{jobID: jobID, err: err}
		}
	case "n", "esc":
		m.pendingDeleteID = ""
		m.pendingDeleteName = ""
		m.subset = cronSubDetail
		return m, nil
	}
	return m, nil
}

// -----------------------------------------------------------------------------
// Actions / TriggerAction
// -----------------------------------------------------------------------------

func (m cronsModel) Actions() []Action {
	switch m.subset {
	case cronSubList:
		return m.listActions()
	case cronSubDetail:
		return m.detailActions()
	case cronSubForm:
		// The form's tab/enter/esc are intrinsic form controls, not
		// discoverable view-level commands, so they don't appear here.
		return nil
	case cronSubConfirmDelete:
		return []Action{
			{ID: "confirm-delete", Label: "Delete", Key: "y"},
			{ID: "cancel-delete", Label: "Cancel", Key: "n"},
		}
	}
	return nil
}

func (m cronsModel) listActions() []Action {
	var actions []Action
	if !m.loading && m.err == nil {
		actions = append(actions, Action{ID: "new", Label: "New job", Key: "n"})
		if len(m.list.Items()) > 0 {
			actions = append(actions, Action{ID: "duplicate", Label: "Duplicate", Key: "d"})
		}
	}
	if m.filterAgentID != "" {
		label := "Show all agents"
		if m.showAllAgents {
			label = "Filter by agent"
		}
		actions = append(actions, Action{ID: "toggle-agent-filter", Label: label, Key: "a"})
	}
	actions = append(actions, Action{ID: "refresh", Label: "Refresh", Key: "r"})
	actions = append(actions, Action{ID: "back", Label: "Back", Key: "esc"})
	if m.err != nil {
		actions = append(actions, Action{ID: "retry", Label: "Retry", Key: "R"})
	}
	return actions
}

func (m cronsModel) detailActions() []Action {
	job, ok := m.findJob(m.selectedID)
	var actions []Action
	if ok {
		// Bound to "!" rather than "R" because the case-sensitive pair
		// (R=run, r=refresh) was easy to misfire — terminals that
		// don't report shift on letter keys would land on refresh
		// when the user expected run.
		actions = append(actions, Action{ID: "run", Label: "Run now", Key: "!"})
		toggleLabel := "Disable"
		if !job.Enabled {
			toggleLabel = "Enable"
		}
		actions = append(actions, Action{ID: "toggle", Label: toggleLabel, Key: "t"})
		actions = append(actions, Action{ID: "edit", Label: "Edit", Key: "e"})
		actions = append(actions, Action{ID: "delete", Label: "Delete", Key: "x"})
		if hasTranscriptContent(job, m.runs) {
			actions = append(actions, Action{ID: "transcript", Label: "Transcript", Key: "T"})
		}
	}
	actions = append(actions, Action{ID: "refresh", Label: "Refresh", Key: "r"})
	actions = append(actions, Action{ID: "back", Label: "Back", Key: "esc"})
	return actions
}

func (m cronsModel) TriggerAction(id string) (cronsModel, tea.Cmd) {
	switch id {
	case "back":
		return m.actionBack()
	case "refresh":
		return m.actionRefresh()
	case "retry":
		if m.err == nil {
			return m, nil
		}
		m.loading = true
		m.err = nil
		return m, m.loadJobs()
	case "toggle-agent-filter":
		m.showAllAgents = !m.showAllAgents
		m.applyFilter()
		return m, nil
	case "new":
		m.form = newCreateForm()
		m.sizePayloadTextarea()
		m.subset = cronSubForm
		return m, m.form.name.Focus()
	case "duplicate":
		return m.actionDuplicate()
	case "run":
		return m.actionRun()
	case "toggle":
		return m.actionToggle()
	case "edit":
		return m.actionEdit()
	case "delete":
		return m.actionDelete()
	case "transcript":
		return m.actionTranscript()
	case "confirm-delete":
		return m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	case "cancel-delete":
		return m.handleConfirmKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	}
	return m, nil
}

func (m cronsModel) actionBack() (cronsModel, tea.Cmd) {
	switch m.subset {
	case cronSubList:
		return m, func() tea.Msg { return goBackFromCronsMsg{} }
	case cronSubDetail:
		m.subset = cronSubList
		return m, nil
	case cronSubForm:
		// Form esc returns to whichever substate opened it.
		if m.form.editingID != "" {
			m.subset = cronSubDetail
		} else {
			m.subset = cronSubList
		}
		m.form = cronForm{}
		return m, nil
	}
	return m, nil
}

func (m cronsModel) actionRefresh() (cronsModel, tea.Cmd) {
	switch m.subset {
	case cronSubList:
		m.loading = true
		return m, m.loadJobs()
	case cronSubDetail:
		if m.selectedID == "" {
			return m, nil
		}
		m.runsLoading = true
		m.loading = true
		m.runStatus = ""
		m.runFailed = false
		return m, tea.Batch(m.loadJobs(), m.loadRuns(m.selectedID))
	}
	return m, nil
}

func (m cronsModel) actionRun() (cronsModel, tea.Cmd) {
	job, ok := m.findJob(m.selectedID)
	if !ok {
		return m, nil
	}
	if m.running {
		// Swallow repeat presses while the previous trigger is still
		// in flight — otherwise the gateway would receive duplicate
		// run-now requests for a single keystroke burst.
		return m, nil
	}
	m.running = true
	m.runStatus = ""
	m.runFailed = false
	cron := m.cron
	id := job.ID
	return m, func() tea.Msg {
		err := cron.CronRun(context.Background(), id, true)
		return cronJobRanMsg{err: err}
	}
}

func (m cronsModel) actionToggle() (cronsModel, tea.Cmd) {
	job, ok := m.findJob(m.selectedID)
	if !ok {
		return m, nil
	}
	cron := m.cron
	newEnabled := !job.Enabled
	id := job.ID
	return m, func() tea.Msg {
		err := cron.CronUpdate(context.Background(), protocol.CronUpdateParams{
			ID: id,
			Patch: protocol.CronJobPatch{
				Enabled: &newEnabled,
			},
		})
		return cronJobToggledMsg{jobID: id, enabled: newEnabled, err: err}
	}
}

func (m cronsModel) actionEdit() (cronsModel, tea.Cmd) {
	job, ok := m.findJob(m.selectedID)
	if !ok {
		return m, nil
	}
	form, err := newEditForm(job)
	if err != "" {
		// Render the form anyway so the user sees the explanation; the
		// save action is suppressed while the unsupported banner is up.
		form.unsupported = err
	}
	m.form = form
	m.sizePayloadTextarea()
	m.subset = cronSubForm
	return m, m.form.name.Focus()
}

// actionDuplicate opens the form pre-populated from the highlighted
// list item, but in create mode so submission goes through CronAdd.
// Triggered from the list substate ("d"); the source job is read from
// m.list.SelectedItem rather than m.selectedID, which is only set
// after entering the detail view.
func (m cronsModel) actionDuplicate() (cronsModel, tea.Cmd) {
	item, ok := m.list.SelectedItem().(cronItem)
	if !ok {
		return m, nil
	}
	form, banner := newDuplicateForm(item.job)
	if banner != "" {
		form.unsupported = banner
	}
	m.form = form
	m.sizePayloadTextarea()
	m.subset = cronSubForm
	return m, m.form.name.Focus()
}

// sizePayloadTextarea applies the current view width to the payload
// textarea. Called whenever the form is (re)constructed because each
// new form has a fresh textarea.Model with default sizing.
func (m *cronsModel) sizePayloadTextarea() {
	w := m.width - 6
	if w < 20 {
		w = 20
	}
	m.form.payloadText.SetWidth(w)
}

func (m cronsModel) actionDelete() (cronsModel, tea.Cmd) {
	job, ok := m.findJob(m.selectedID)
	if !ok {
		return m, nil
	}
	m.pendingDeleteID = job.ID
	m.pendingDeleteName = displayJobName(job)
	m.subset = cronSubConfirmDelete
	return m, nil
}

func (m cronsModel) actionTranscript() (cronsModel, tea.Cmd) {
	job, ok := m.findJob(m.selectedID)
	if !ok {
		return m, nil
	}
	if !hasTranscriptContent(job, m.runs) {
		return m, nil
	}
	runs := append([]protocol.CronRunLogEntry(nil), m.runs...)
	return m, func() tea.Msg {
		return cronTranscriptMsg{
			job:       job,
			runs:      runs,
			agentName: job.AgentID,
		}
	}
}

// hasTranscriptContent reports whether the cron has any run-log content
// worth rendering — a payload alone isn't enough; we want at least one
// run with a summary or error message to show alongside it.
func hasTranscriptContent(job protocol.CronJob, runs []protocol.CronRunLogEntry) bool {
	if cronPayloadText(job) == "" && len(runs) == 0 {
		return false
	}
	for _, r := range runs {
		if r.Summary != "" || r.Error != "" || r.DeliveryError != "" {
			return true
		}
	}
	return false
}

// cronPayloadText returns the human-authored prompt for an agentTurn
// cron job, falling back across the union fields the gateway uses.
func cronPayloadText(job protocol.CronJob) string {
	if t := strings.TrimSpace(job.Payload.Text); t != "" {
		return t
	}
	return strings.TrimSpace(job.Payload.Message)
}

// -----------------------------------------------------------------------------
// Form
// -----------------------------------------------------------------------------

func newCreateForm() cronForm {
	f := cronForm{
		mode:          "create",
		sessionTarget: "isolated",
		wakeMode:      "next-heartbeat",
		deliveryMode:  "none",
		enabled:       true,
		focused:       formName,
	}
	f.name = textinput.New()
	f.name.CharLimit = 80
	f.description = textinput.New()
	f.description.CharLimit = 200
	f.cronExpr = textinput.New()
	f.cronExpr.Placeholder = "0 8 * * *"
	f.cronExpr.CharLimit = 64
	f.timezone = textinput.New()
	f.timezone.Placeholder = "Europe/London"
	f.timezone.CharLimit = 64
	f.agentID = textinput.New()
	f.agentID.CharLimit = 64
	f.model = textinput.New()
	f.model.Placeholder = "leave blank to use the agent default"
	f.model.CharLimit = 128
	f.payloadText = textarea.New()
	f.payloadText.CharLimit = 4000
	f.payloadText.SetHeight(6)
	f.payloadText.ShowLineNumbers = false
	f.payloadText.Prompt = ""
	f.payloadText.Placeholder = "Read /home/pete/projects/cron-jobs/example.md and follow the instructions."
	f.deliveryTarget = textinput.New()
	f.deliveryTarget.CharLimit = 256
	return f
}

// newEditForm pre-populates a form from an existing job. If the job's
// schedule or payload kind is one the TUI form does not model, the
// returned banner string is non-empty and the caller should disable the
// save path.
func newEditForm(job protocol.CronJob) (cronForm, string) {
	f, unsupported := populateFormFromJob(job)
	f.mode = "edit"
	f.editingID = job.ID
	f.name.SetValue(job.Name)
	if unsupported != "" {
		unsupported = strings.Replace(unsupported, "{action}", "Edit", 1)
	}
	return f, unsupported
}

// newDuplicateForm pre-populates a create-mode form from an existing
// job. Like the edit form it copies every field the TUI models, but
// leaves editingID empty so submission goes through CronAdd rather than
// CronUpdate. The name is prefixed with "Copy of " so the duplicate is
// distinguishable in the list before the user edits it.
func newDuplicateForm(job protocol.CronJob) (cronForm, string) {
	f, unsupported := populateFormFromJob(job)
	// mode stays "create" and editingID stays "" so submitForm -> CronAdd.
	f.name.SetValue(duplicateName(job.Name))
	if unsupported != "" {
		unsupported = strings.Replace(unsupported, "{action}", "Duplicate", 1)
	}
	return f, unsupported
}

// populateFormFromJob copies every form-modelled field off a job into a
// fresh create-mode form. Shared between the edit and duplicate flows
// because the only differences between them are mode/editingID and the
// name handling. The unsupported banner uses a "{action}" placeholder
// the caller swaps in so the wording matches whichever flow opened the
// form.
func populateFormFromJob(job protocol.CronJob) (cronForm, string) {
	f := newCreateForm()
	f.description.SetValue(job.Description)
	f.cronExpr.SetValue(job.Schedule.Expr)
	f.timezone.SetValue(job.Schedule.Tz)
	f.agentID.SetValue(job.AgentID)
	f.model.SetValue(job.Payload.Model)
	f.payloadText.SetValue(job.Payload.Text)
	if f.payloadText.Value() == "" {
		f.payloadText.SetValue(job.Payload.Message)
	}
	if job.SessionTarget != "" {
		f.sessionTarget = job.SessionTarget
	}
	if job.WakeMode != "" {
		f.wakeMode = job.WakeMode
	}
	if job.Delivery != nil && job.Delivery.Mode != "" {
		f.deliveryMode = job.Delivery.Mode
		switch job.Delivery.Mode {
		case "webhook":
			f.deliveryTarget.SetValue(job.Delivery.To)
		default:
			f.deliveryTarget.SetValue(job.Delivery.Channel)
		}
	}
	f.enabled = job.Enabled

	var unsupported string
	if job.Schedule.Kind != "" && job.Schedule.Kind != "cron" {
		unsupported = fmt.Sprintf("{action} not supported for schedule kind %q. Use the openclaw CLI.", job.Schedule.Kind)
	}
	if job.Payload.Kind != "" && job.Payload.Kind != "agentTurn" && unsupported == "" {
		unsupported = fmt.Sprintf("{action} not supported for payload kind %q. Use the openclaw CLI.", job.Payload.Kind)
	}
	return f, unsupported
}

// duplicateName builds the cloned job's name. Empty names are passed
// through so the form-level "name is required" validation catches the
// case rather than producing a spurious "Copy of " placeholder.
func duplicateName(original string) string {
	if original == "" {
		return ""
	}
	return "Copy of " + original
}

func (m cronsModel) handleFormKey(msg tea.KeyPressMsg) (cronsModel, tea.Cmd) {
	if m.form.saving {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		return m.actionBack()
	case "tab":
		m.form.advanceFocus(+1)
		return m, m.form.refocus()
	case "shift+tab":
		m.form.advanceFocus(-1)
		return m, m.form.refocus()
	case "enter":
		// In the payload textarea, Enter inserts a newline; submission
		// uses Ctrl+S (or Alt+Enter) so multi-line bodies are easy.
		if m.form.focused == formPayloadText {
			var cmd tea.Cmd
			m.form.payloadText, cmd = m.form.payloadText.Update(msg)
			return m, cmd
		}
		return m.submitForm()
	case "ctrl+s", "alt+enter":
		return m.submitForm()
	case "space", " ":
		// Toggle controls are bound to space when focused. Text fields
		// and the payload textarea fall through to receive the literal
		// space character.
		switch m.form.focused {
		case formSessionTarget:
			if m.form.sessionTarget == "main" {
				m.form.sessionTarget = "isolated"
			} else {
				m.form.sessionTarget = "main"
			}
			return m, nil
		case formWakeMode:
			if m.form.wakeMode == "now" {
				m.form.wakeMode = "next-heartbeat"
			} else {
				m.form.wakeMode = "now"
			}
			return m, nil
		case formDeliveryMode:
			m.form.deliveryMode = cycleString(deliveryModes, m.form.deliveryMode)
			return m, nil
		case formEnabled:
			m.form.enabled = !m.form.enabled
			return m, nil
		}
	}
	// Forward to the payload textarea or whichever textinput is focused.
	if m.form.focused == formPayloadText {
		var cmd tea.Cmd
		m.form.payloadText, cmd = m.form.payloadText.Update(msg)
		return m, cmd
	}
	field := m.form.activeInput()
	if field == nil {
		return m, nil
	}
	var cmd tea.Cmd
	*field, cmd = field.Update(msg)
	return m, cmd
}

// activeInput returns a pointer to the textinput currently focused, or
// nil if the focus is on the payload textarea (handled separately) or
// a non-text control (toggles / checkbox).
func (f *cronForm) activeInput() *textinput.Model {
	switch f.focused {
	case formName:
		return &f.name
	case formDescription:
		return &f.description
	case formCronExpr:
		return &f.cronExpr
	case formTimezone:
		return &f.timezone
	case formAgentID:
		return &f.agentID
	case formModel:
		return &f.model
	case formDeliveryTarget:
		return &f.deliveryTarget
	}
	return nil
}

// advanceFocus moves the focus marker by dir (+1 forward, -1 back),
// wrapping around the field list.
func (f *cronForm) advanceFocus(dir int) {
	next := int(f.focused) + dir
	if next < 0 {
		next = int(formFieldCount) - 1
	}
	if next >= int(formFieldCount) {
		next = 0
	}
	f.focused = cronFormField(next)
}

// refocus reapplies bubbletea focus to the active text input (and
// blurs the others). Returns the focus tea.Cmd so the cursor blinks.
func (f *cronForm) refocus() tea.Cmd {
	f.name.Blur()
	f.description.Blur()
	f.cronExpr.Blur()
	f.timezone.Blur()
	f.agentID.Blur()
	f.model.Blur()
	f.deliveryTarget.Blur()
	f.payloadText.Blur()
	if f.focused == formPayloadText {
		return f.payloadText.Focus()
	}
	if input := f.activeInput(); input != nil {
		return input.Focus()
	}
	return nil
}

func (m cronsModel) submitForm() (cronsModel, tea.Cmd) {
	if m.form.unsupported != "" {
		// User must back out and use the CLI; we refuse to save a
		// truncated representation rather than silently mutate the job.
		return m, nil
	}
	if m.form.name.Value() == "" {
		m.form.err = fmt.Errorf("name is required")
		return m, nil
	}
	if m.form.cronExpr.Value() == "" {
		m.form.err = fmt.Errorf("cron expression is required")
		return m, nil
	}
	m.form.err = nil
	m.form.saving = true
	cron := m.cron
	form := m.form

	if form.mode == "edit" {
		patch := buildJobPatchMap(form)
		jobID := form.editingID
		return m, func() tea.Msg {
			err := cron.CronUpdateRaw(context.Background(), jobID, patch)
			return cronJobSavedMsg{jobID: jobID, err: err}
		}
	}

	params := buildAddParams(form)
	return m, func() tea.Msg {
		_, err := cron.CronAdd(context.Background(), params)
		return cronJobSavedMsg{err: err}
	}
}

func buildAddParams(f cronForm) protocol.CronAddParams {
	params := protocol.CronAddParams{
		Name:          f.name.Value(),
		Description:   f.description.Value(),
		SessionTarget: f.sessionTarget,
		WakeMode:      f.wakeMode,
		Schedule: protocol.CronSchedule{
			Kind: "cron",
			Expr: f.cronExpr.Value(),
			Tz:   f.timezone.Value(),
		},
		Payload: protocol.CronPayload{
			Kind:  "agentTurn",
			Text:  f.payloadText.Value(),
			Model: f.model.Value(),
		},
		Delivery: buildDelivery(f),
	}
	if v := f.agentID.Value(); v != "" {
		params.AgentID = &v
	}
	enabled := f.enabled
	params.Enabled = &enabled
	return params
}

// buildJobPatchMap renders the form as a wire-level map for
// CronUpdateRaw. We avoid the typed CronJobPatch here because every
// string field on it is `omitempty` — once Go marshals the struct,
// "" becomes indistinguishable from "not provided", and the gateway
// silently retains the prior value. Sending a literal map preserves
// the user's intent to clear a field.
func buildJobPatchMap(f cronForm) map[string]any {
	patch := map[string]any{
		"name":          f.name.Value(),
		"description":   f.description.Value(),
		"sessionTarget": f.sessionTarget,
		"wakeMode":      f.wakeMode,
		"schedule": map[string]any{
			"kind": "cron",
			"expr": f.cronExpr.Value(),
			"tz":   f.timezone.Value(),
		},
		"payload": map[string]any{
			"kind":  "agentTurn",
			"text":  f.payloadText.Value(),
			"model": f.model.Value(),
		},
		"agentId": f.agentID.Value(),
		"enabled": f.enabled,
	}
	if d := buildDelivery(f); d != nil {
		patch["delivery"] = map[string]any{
			"mode":    d.Mode,
			"channel": d.Channel,
			"to":      d.To,
		}
	} else {
		// mode=none clears delivery on the wire too.
		patch["delivery"] = map[string]any{"mode": "none"}
	}
	return patch
}

// buildDelivery turns the form's deliveryMode + deliveryTarget into a
// *CronDelivery, or nil when the user picked "none". For "announce" the
// target is the channel; for "webhook" it's the destination URL.
func buildDelivery(f cronForm) *protocol.CronDelivery {
	switch f.deliveryMode {
	case "announce":
		return &protocol.CronDelivery{
			Mode:    "announce",
			Channel: f.deliveryTarget.Value(),
		}
	case "webhook":
		return &protocol.CronDelivery{
			Mode: "webhook",
			To:   f.deliveryTarget.Value(),
		}
	}
	return nil
}

// -----------------------------------------------------------------------------
// View
// -----------------------------------------------------------------------------

func (m cronsModel) View() string {
	banner := renderConnectionBanner(m.activeConn)
	switch m.subset {
	case cronSubList:
		return banner + m.viewList()
	case cronSubDetail:
		return banner + m.viewDetail()
	case cronSubForm:
		return banner + m.viewForm()
	case cronSubConfirmDelete:
		return banner + m.viewConfirm()
	}
	return banner
}

func (m cronsModel) viewList() string {
	if m.loading {
		return "\n  Loading cron jobs...\n"
	}
	hints := ""
	if !m.hideHints {
		hints = helpStyle.Render(renderActionHints(m.Actions()))
	}
	if m.err != nil {
		var b strings.Builder
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("  Error: %v", m.err)))
		b.WriteString("\n\n")
		b.WriteString(hints)
		b.WriteString("\n")
		return b.String()
	}
	header := headerStyle.Render(" Cron — " + m.headerLabel() + " ")
	if len(m.list.Items()) == 0 {
		var b strings.Builder
		b.WriteString("\n")
		b.WriteString(header)
		b.WriteString("\n\n")
		b.WriteString("  No cron jobs found.\n\n")
		b.WriteString(hints)
		b.WriteString("\n")
		return b.String()
	}
	return m.list.View() + "\n" + hints
}

func (m cronsModel) headerLabel() string {
	if m.showAllAgents || m.filterAgentID == "" {
		return "all agents"
	}
	if m.filterLabel != "" {
		return m.filterLabel
	}
	return m.filterAgentID
}

func (m cronsModel) viewDetail() string {
	job, ok := m.findJob(m.selectedID)
	if !ok {
		return "\n  Job no longer available.\n"
	}
	hints := ""
	if !m.hideHints {
		hints = helpStyle.Render(renderActionHints(m.Actions()))
	}
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(headerStyle.Render(" Cron · " + displayJobName(job) + " "))
	if !job.Enabled {
		b.WriteString("  ")
		b.WriteString(cronStatusDisabledStyle.Render("disabled"))
	}
	b.WriteString("\n\n")

	switch {
	case m.running:
		b.WriteString(statusStyle.Render("  Triggering run..."))
		b.WriteString("\n\n")
	case m.runFailed && m.runStatus != "":
		b.WriteString(errorStyle.Render("  " + m.runStatus))
		b.WriteString("\n\n")
	case m.runStatus != "":
		b.WriteString(statusStyle.Render("  " + m.runStatus))
		b.WriteString("\n\n")
	}

	rows := [][2]string{
		{"Schedule", formatSchedule(job.Schedule)},
		{"Description", orDash(job.Description)},
		{"Agent", orDash(job.AgentID)},
		{"Model", orDash(job.Payload.Model)},
		{"Session", orDash(job.SessionTarget)},
		{"Wake", wakeModeLabel(job.WakeMode)},
		{"Delivery", formatDeliveryOrNone(job.Delivery)},
		{"Next run", formatAbsTime(job.State.NextRunAtMs)},
		{"Last run", formatLastRun(job.State)},
	}
	for _, r := range rows {
		b.WriteString(fmt.Sprintf("  %-12s  %s\n", r[0], r[1]))
	}
	b.WriteString("\n")
	b.WriteString("  Payload:\n")
	payload := job.Payload.Text
	if payload == "" {
		payload = job.Payload.Message
	}
	if payload == "" {
		payload = "—"
	}
	for _, line := range strings.Split(payload, "\n") {
		b.WriteString("    " + line + "\n")
	}

	b.WriteString("\n")
	b.WriteString(headerStyle.Render(" Run history "))
	b.WriteString("\n")
	switch {
	case m.runsLoading:
		b.WriteString("  Loading...\n")
	case m.runsErr != nil:
		b.WriteString(errorStyle.Render(fmt.Sprintf("  Error: %v", m.runsErr)))
		b.WriteString("\n")
	case len(m.runs) == 0:
		b.WriteString("  No run log entries yet.\n")
	default:
		for _, r := range m.runs {
			b.WriteString("  " + formatRunLogEntry(r) + "\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(hints)
	b.WriteString("\n")
	return b.String()
}

func (m cronsModel) viewForm() string {
	var b strings.Builder
	b.WriteString("\n")
	title := " New cron job "
	if m.form.mode == "edit" {
		title = " Edit cron job "
	}
	b.WriteString(headerStyle.Render(title))
	b.WriteString("\n\n")

	if m.form.unsupported != "" {
		b.WriteString(errorStyle.Render("  " + m.form.unsupported))
		b.WriteString("\n\n")
	}

	rows := []struct {
		label string
		field cronFormField
		view  string
	}{
		{"Name", formName, m.form.name.View()},
		{"Description", formDescription, m.form.description.View()},
		{"Cron expression", formCronExpr, m.form.cronExpr.View()},
		{"Timezone", formTimezone, m.form.timezone.View()},
		{"Agent ID (optional)", formAgentID, m.form.agentID.View()},
		{"Model (optional)", formModel, m.form.model.View()},
		{"Session target", formSessionTarget, toggleView(m.form.sessionTarget, "main", "isolated")},
		{"Wake mode", formWakeMode, toggleView(m.form.wakeMode, "next-heartbeat", "now")},
		{"Payload (agent turn text)", formPayloadText, m.form.payloadText.View()},
		{"Delivery mode", formDeliveryMode, cycleView(deliveryModes, m.form.deliveryMode)},
		{deliveryTargetLabel(m.form.deliveryMode), formDeliveryTarget, m.form.deliveryTarget.View()},
		{"Enabled", formEnabled, checkboxView(m.form.enabled)},
	}
	for _, r := range rows {
		marker := "  "
		if r.field == m.form.focused {
			marker = lipgloss.NewStyle().Foreground(accent).Bold(true).Render("> ")
		}
		b.WriteString(marker + r.label + "\n")
		b.WriteString("    " + r.view + "\n\n")
	}

	if m.form.err != nil {
		b.WriteString(errorStyle.Render(fmt.Sprintf("  Error: %v", m.form.err)))
		b.WriteString("\n\n")
	}
	if m.form.saving {
		b.WriteString(statusStyle.Render("  Saving..."))
		b.WriteString("\n")
	} else {
		hint := "  Tab/Shift+Tab: navigate | Space: toggle | Enter: save (newline in payload) | Ctrl+S: save anywhere | Esc: cancel"
		b.WriteString(helpStyle.Render(hint))
		b.WriteString("\n")
	}
	return b.String()
}

func (m cronsModel) viewConfirm() string {
	var b strings.Builder
	b.WriteString("\n\n")
	b.WriteString(headerStyle.Render(" Delete cron job "))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  Delete %q? This cannot be undone.\n\n", m.pendingDeleteName))
	b.WriteString(helpStyle.Render("  y: confirm  ·  n / esc: cancel"))
	b.WriteString("\n")
	return b.String()
}

// -----------------------------------------------------------------------------
// Formatting helpers
// -----------------------------------------------------------------------------

func displayJobName(job protocol.CronJob) string {
	if job.Name != "" {
		return job.Name
	}
	return job.ID
}

func toggleView(current, opt1, opt2 string) string {
	render := func(label string) string {
		if label == current {
			return lipgloss.NewStyle().Foreground(accent).Bold(true).Render("[" + label + "]")
		}
		return statusStyle.Render(" " + label + " ")
	}
	return render(opt1) + " " + render(opt2)
}

// cycleView is toggleView for an N-state field (the delivery-mode
// cycle). The current option is highlighted; non-active options are
// rendered dim.
func cycleView(opts []string, current string) string {
	parts := make([]string, len(opts))
	for i, o := range opts {
		if o == current {
			parts[i] = lipgloss.NewStyle().Foreground(accent).Bold(true).Render("[" + o + "]")
		} else {
			parts[i] = statusStyle.Render(" " + o + " ")
		}
	}
	return strings.Join(parts, " ")
}

// deliveryTargetLabel returns the input label that matches the current
// delivery mode. "none" still shows the field but disabled-looking so
// the layout stays stable as the user cycles modes.
func deliveryTargetLabel(mode string) string {
	switch mode {
	case "announce":
		return "Channel (e.g. slack:#alerts)"
	case "webhook":
		return "Webhook URL"
	}
	return "Delivery target (unused while mode=none)"
}

func checkboxView(checked bool) string {
	box := "[ ]"
	if checked {
		box = "[x]"
	}
	return box + " enabled"
}

func formatSchedule(s protocol.CronSchedule) string {
	switch s.Kind {
	case "cron":
		out := "cron " + s.Expr
		if s.Tz != "" {
			out += " (" + s.Tz + ")"
		}
		return out
	case "every":
		if s.EveryMs != nil {
			return "every " + formatDuration(int64(*s.EveryMs))
		}
		return "every —"
	case "at":
		return "at " + s.At
	}
	return orDash(s.Kind)
}

func formatAbsTime(ms *int64) string {
	if ms == nil || *ms == 0 {
		return "—"
	}
	t := time.UnixMilli(*ms).Local()
	return t.Format("02 Jan 2006 15:04:05")
}

func formatLastRun(s protocol.CronJobState) string {
	if s.LastRunAtMs == nil || *s.LastRunAtMs == 0 {
		return "—"
	}
	t := time.UnixMilli(*s.LastRunAtMs).Local().Format("02 Jan 2006 15:04:05")
	if s.LastStatus != "" {
		return t + " · " + s.LastStatus
	}
	return t
}

func formatDelivery(d *protocol.CronDelivery) string {
	if d == nil || d.Mode == "" {
		return ""
	}
	out := d.Mode
	if d.Channel != "" {
		out += " (" + d.Channel + ")"
	}
	if d.To != "" {
		out += " → " + d.To
	}
	return out
}

// formatDeliveryOrNone returns "none" instead of an empty string when
// the job has no delivery configured, so the detail view's row layout
// stays consistent.
func formatDeliveryOrNone(d *protocol.CronDelivery) string {
	if s := formatDelivery(d); s != "" {
		return s
	}
	return "none"
}

func formatRunLogEntry(r protocol.CronRunLogEntry) string {
	when := "—"
	if r.RunAtMs != nil {
		when = time.UnixMilli(*r.RunAtMs).Local().Format("02 Jan 15:04:05")
	}
	parts := []string{when}
	if r.Status != "" {
		parts = append(parts, r.Status)
	}
	if r.DurationMs != nil {
		parts = append(parts, formatDuration(*r.DurationMs))
	}
	if r.Summary != "" {
		summary := r.Summary
		if len(summary) > 60 {
			summary = summary[:57] + "..."
		}
		parts = append(parts, summary)
	} else if r.Error != "" {
		errMsg := r.Error
		if len(errMsg) > 60 {
			errMsg = errMsg[:57] + "..."
		}
		parts = append(parts, errMsg)
	}
	return strings.Join(parts, " · ")
}
