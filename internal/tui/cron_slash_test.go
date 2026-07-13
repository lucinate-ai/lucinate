package tui

import (
	"strings"
	"testing"

	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
)

// nonCronBackend embeds the backend.Backend interface (left nil) so it
// satisfies chatModel.backend without implementing backend.CronBackend —
// exercising the "/cron not available on this connection" branch. None of
// the embedded methods are called on that path.
type nonCronBackend struct{ backend.Backend }

func dupNameJobs() []protocol.CronJob {
	return []protocol.CronJob{
		{ID: "job-a", Name: "dup", Schedule: protocol.CronSchedule{Kind: "cron", Expr: "0 9 * * *"}},
		{ID: "job-b", Name: "dup", Schedule: protocol.CronSchedule{Kind: "cron", Expr: "0 10 * * *"}},
	}
}

func TestMatchCronJobs(t *testing.T) {
	jobs := sampleJobs() // job-1 "Daily report" / agent-1, job-2 "Other agent thing" / agent-2

	tests := []struct {
		name    string
		query   string
		wantIDs []string
	}{
		{"exact name", "Daily report", []string{"job-1"}},
		{"exact name case-insensitive", "daily report", []string{"job-1"}},
		{"exact id", "job-2", []string{"job-2"}},
		{"substring on name", "other agent", []string{"job-2"}},
		{"substring on id", "job-", []string{"job-1", "job-2"}},
		{"no match", "nope", nil},
		{"empty query", "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchCronJobs(jobs, tc.query)
			var ids []string
			for _, j := range got {
				ids = append(ids, j.ID)
			}
			if strings.Join(ids, ",") != strings.Join(tc.wantIDs, ",") {
				t.Errorf("matchCronJobs(%q) = %v, want %v", tc.query, ids, tc.wantIDs)
			}
		})
	}

	// Duplicate names are not unique, so tier 1 returns both — the caller
	// detects ambiguity.
	if got := matchCronJobs(dupNameJobs(), "dup"); len(got) != 2 {
		t.Errorf("expected 2 matches for duplicate name, got %d", len(got))
	}
}

func TestSlashCommand_Cron_BareEmitsError(t *testing.T) {
	m := newSlashTestModel()
	handled, cmd := m.handleSlashCommand("/cron")
	if !handled {
		t.Fatal("expected /cron to be handled")
	}
	if cmd != nil {
		t.Error("expected bare /cron to return no cmd (inline error only)")
	}
	last := m.messages[len(m.messages)-1]
	if last.role != "system" || !strings.Contains(last.errMsg, "/crons") {
		t.Errorf("expected error pointing at /crons, got: %+v", last)
	}
}

func TestSlashCommand_Cron_NotAvailableWithoutCronBackend(t *testing.T) {
	m := newSlashTestModel()
	m.backend = nonCronBackend{}
	handled, cmd := m.handleSlashCommand("/cron Daily report")
	if !handled {
		t.Fatal("expected /cron to be handled")
	}
	if cmd != nil {
		t.Error("expected no cmd when cron is unavailable")
	}
	last := m.messages[len(m.messages)-1]
	if last.role != "system" || !strings.Contains(last.errMsg, "not available") {
		t.Errorf("expected 'not available' error, got: %+v", last)
	}
}

func TestSlashCommand_Cron_ReturnsResolveCmd(t *testing.T) {
	m := newSlashTestModel()
	m.backend.(*fakeBackend).cronJobs = sampleJobs()

	handled, cmd := m.handleSlashCommand("/cron Daily report")
	if !handled {
		t.Fatal("expected /cron <name> to be handled")
	}
	if cmd == nil {
		t.Fatal("expected a resolve cmd")
	}
	// Resolution is async — the confirmation must NOT be set synchronously
	// (unlike /compact, which sets pendingConfirm immediately).
	if m.pendingConfirm != nil {
		t.Error("expected pendingConfirm to be nil until the resolve msg is handled")
	}
	msg, ok := cmd().(cronResolveMsg)
	if !ok {
		t.Fatalf("expected cronResolveMsg, got %T", cmd())
	}
	if msg.err != nil {
		t.Fatalf("unexpected resolve error: %v", msg.err)
	}
	if len(msg.matches) != 1 || msg.matches[0].ID != "job-1" {
		t.Errorf("expected single match job-1, got %+v", msg.matches)
	}
}

func TestCronResolveMsg_SingleMatchSetsConfirm(t *testing.T) {
	m := newSlashTestModel()
	initial := len(m.messages)

	updated, cmd := m.Update(cronResolveMsg{query: "Daily report", matches: sampleJobs()[:1]})
	if cmd != nil {
		t.Error("expected nil cmd (waiting for confirmation)")
	}
	if updated.pendingConfirm == nil {
		t.Fatal("expected pendingConfirm to be set for a single match")
	}
	if !strings.Contains(updated.pendingConfirm.prompt, "Daily report") ||
		!strings.Contains(updated.pendingConfirm.prompt, "y/n") {
		t.Errorf("prompt missing name or y/n: %q", updated.pendingConfirm.prompt)
	}
	if updated.pendingConfirm.runningStatus == "" {
		t.Error("expected a runningStatus so the spinner ticks during the run")
	}
	if len(updated.messages) != initial+1 {
		t.Errorf("expected the prompt appended as a system row, got %d messages", len(updated.messages))
	}
	last := updated.messages[len(updated.messages)-1]
	if last.role != "system" || !strings.Contains(last.content, "y/n") {
		t.Errorf("expected confirmation prompt row, got: %+v", last)
	}
}

func TestCronResolveMsg_NoMatch(t *testing.T) {
	m := newSlashTestModel()
	updated, _ := m.Update(cronResolveMsg{query: "ghost", matches: nil})
	if updated.pendingConfirm != nil {
		t.Error("expected no confirmation on zero matches")
	}
	last := updated.messages[len(updated.messages)-1]
	if last.role != "system" || !strings.Contains(last.errMsg, "no cron job matching") {
		t.Errorf("expected no-match error, got: %+v", last)
	}
}

func TestCronResolveMsg_Ambiguous(t *testing.T) {
	m := newSlashTestModel()
	updated, _ := m.Update(cronResolveMsg{query: "dup", matches: dupNameJobs()})
	if updated.pendingConfirm != nil {
		t.Error("expected no confirmation on ambiguous match")
	}
	last := updated.messages[len(updated.messages)-1]
	if last.role != "system" || last.errMsg == "" {
		t.Fatalf("expected ambiguity error row, got: %+v", last)
	}
	for _, id := range []string{"job-a", "job-b"} {
		if !strings.Contains(last.errMsg, id) {
			t.Errorf("ambiguity error should list id %q, got: %s", id, last.errMsg)
		}
	}
}

func TestCronResolveMsg_ListError(t *testing.T) {
	m := newSlashTestModel()
	updated, _ := m.Update(cronResolveMsg{query: "x", err: errString("gateway down")})
	if updated.pendingConfirm != nil {
		t.Error("expected no confirmation on list error")
	}
	last := updated.messages[len(updated.messages)-1]
	if last.role != "system" || !strings.Contains(last.errMsg, "cron lookup failed") {
		t.Errorf("expected lookup-failed error, got: %+v", last)
	}
}

func TestCron_ConfirmRunsJob(t *testing.T) {
	m := newSlashTestModel()
	fake := m.backend.(*fakeBackend)

	updated, _ := m.Update(cronResolveMsg{query: "Daily report", matches: sampleJobs()[:1]})
	if updated.pendingConfirm == nil {
		t.Fatal("expected pendingConfirm")
	}

	// Simulate the user confirming with `y`: run the stored action.
	msg := updated.pendingConfirm.action()()
	if fake.lastCronRunID != "job-1" {
		t.Errorf("expected CronRun on job-1, got %q", fake.lastCronRunID)
	}
	if !fake.lastCronRunForce {
		t.Error("expected force=true for an on-demand run")
	}
	ran, ok := msg.(chatCronRanMsg)
	if !ok {
		t.Fatalf("expected chatCronRanMsg, got %T", msg)
	}
	if ran.err != nil {
		t.Errorf("unexpected run error: %v", ran.err)
	}
	if ran.jobName != "Daily report" {
		t.Errorf("expected jobName 'Daily report', got %q", ran.jobName)
	}

	// Feeding the result back reports success.
	done, _ := updated.Update(ran)
	last := done.messages[len(done.messages)-1]
	if last.role != "system" || !strings.Contains(last.content, "Triggered") {
		t.Errorf("expected 'Triggered' outcome row, got: %+v", last)
	}
}

func TestChatCronRanMsg_FailureShowsError(t *testing.T) {
	m := newSlashTestModel()
	updated, _ := m.Update(chatCronRanMsg{jobName: "Daily report", err: errString("boom")})
	last := updated.messages[len(updated.messages)-1]
	if last.role != "system" || !strings.Contains(last.errMsg, "cron run failed") {
		t.Errorf("expected 'cron run failed' error, got: %+v", last)
	}
}

func TestSlashCommand_Help_IncludesCron(t *testing.T) {
	m := newSlashTestModel()
	m.handleSlashCommand("/help")
	last := m.messages[len(m.messages)-1]
	if !strings.Contains(last.content, "/cron <job name>") {
		t.Errorf("/help text missing /cron entry\ngot: %s", last.content)
	}
}

func TestFindCronArgAt(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		cursor     int
		wantOK     bool
		wantPrefix string
	}{
		{"typing a name", "/cron dai", len("/cron dai"), true, "dai"},
		{"empty arg after space", "/cron ", len("/cron "), true, ""},
		{"plural /crons excluded", "/crons dai", len("/crons dai"), false, ""},
		{"cursor mid-line", "/cron dai", 3, false, ""},
		{"not a cron line", "hello", len("hello"), false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start, prefix, ok := findCronArgAt(tc.value, tc.cursor)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok {
				if prefix != tc.wantPrefix {
					t.Errorf("prefix = %q, want %q", prefix, tc.wantPrefix)
				}
				if start != len("/cron ") {
					t.Errorf("start = %d, want %d", start, len("/cron "))
				}
			}
		})
	}
}

func TestMatchingCronNames(t *testing.T) {
	m := newSlashTestModel()
	m.cronNames = []string{"Daily report", "Deploy", "Other"}

	if got := m.matchingCronNames("d"); len(got) != 2 {
		t.Errorf("prefix 'd' expected 2 matches, got %v", got)
	}
	if got := m.matchingCronNames(""); len(got) != 3 {
		t.Errorf("empty prefix should match all, got %v", got)
	}
	if got := m.matchingCronNames("xyz"); got != nil {
		t.Errorf("no-match prefix should return nil, got %v", got)
	}

	m.cronNames = nil
	if got := m.matchingCronNames("d"); got != nil {
		t.Errorf("no loaded names should return nil, got %v", got)
	}
}
