package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

func newSlashTestModel() *chatModel {
	vp := viewport.New()
	return &chatModel{
		viewport:  vp,
		backend:   newFakeBackend(),
		agentName: "test",
		width:     80,
		height:    30,
		messages: []chatMessage{
			{role: "user", content: "hello"},
			{role: "assistant", content: "hi there"},
		},
	}
}

// newSlashTabTestModel produces a chatModel with a real textarea so the
// completion-menu Tab tests can drive Update with key events. The
// baseline newSlashTestModel leaves textarea zero-valued, which is fine
// for the slash-command dispatch tests but not for cursor-aware code.
func newSlashTabTestModel() *chatModel {
	ta := textarea.New()
	ta.Focus()
	ta.SetWidth(80)
	ta.SetHeight(inputHeight)
	return &chatModel{
		viewport:           viewport.New(),
		textarea:           ta,
		backend:            newFakeBackend(),
		agentName:          "test",
		width:              80,
		height:             30,
		baseViewportHeight: 20,
	}
}

func TestSlashCommand_Quit(t *testing.T) {
	m := newSlashTestModel()
	handled, cmd := m.handleSlashCommand("/quit")
	if !handled {
		t.Fatal("expected /quit to be handled")
	}
	if cmd == nil {
		t.Fatal("expected a quit cmd")
	}
}

func TestSlashCommand_Exit(t *testing.T) {
	m := newSlashTestModel()
	handled, cmd := m.handleSlashCommand("/exit")
	if !handled {
		t.Fatal("expected /exit to be handled")
	}
	if cmd == nil {
		t.Fatal("expected a quit cmd")
	}
}

func TestSlashCommand_Agents(t *testing.T) {
	m := newSlashTestModel()
	handled, cmd := m.handleSlashCommand("/agents")
	if !handled {
		t.Fatal("expected /agents to be handled")
	}
	if cmd == nil {
		t.Fatal("expected a goBackMsg cmd")
	}
	msg := cmd()
	if _, ok := msg.(goBackMsg); !ok {
		t.Errorf("expected goBackMsg, got %T", msg)
	}
}

func TestSlashCommand_Agent_BareReturnsToAgentPicker(t *testing.T) {
	m := newSlashTestModel()
	handled, cmd := m.handleSlashCommand("/agent")
	if !handled {
		t.Fatal("expected /agent to be handled")
	}
	if cmd == nil {
		t.Fatal("expected /agent to return a goBackMsg cmd")
	}
	if _, ok := cmd().(goBackMsg); !ok {
		t.Errorf("expected goBackMsg, got %T", cmd())
	}
}

func TestSlashCommand_Agent_NoMatchReturnsFailureMsg(t *testing.T) {
	m := newSlashTestModel()
	handled, cmd := m.handleSlashCommand("/agent nope")
	if !handled {
		t.Fatal("expected /agent <name> to be handled")
	}
	if cmd == nil {
		t.Fatal("expected /agent <name> to return a switch cmd")
	}
	msg, ok := cmd().(agentSwitchFailedMsg)
	if !ok {
		t.Fatalf("expected agentSwitchFailedMsg, got %T", cmd())
	}
	if msg.err == nil || !strings.Contains(msg.err.Error(), "no agent matching") {
		t.Errorf("expected no-match error, got %v", msg.err)
	}
}

func TestSlashCommand_Clear(t *testing.T) {
	m := newSlashTestModel()
	handled, cmd := m.handleSlashCommand("/clear")
	if !handled {
		t.Fatal("expected /clear to be handled")
	}
	if cmd != nil {
		t.Error("expected nil cmd from /clear")
	}
	if len(m.messages) != 0 {
		t.Errorf("expected 0 messages after /clear, got %d", len(m.messages))
	}
}

func TestSlashCommand_Help(t *testing.T) {
	m := newSlashTestModel()
	initialCount := len(m.messages)

	handled, cmd := m.handleSlashCommand("/help")
	if !handled {
		t.Fatal("expected /help to be handled")
	}
	if cmd != nil {
		t.Error("expected nil cmd from /help")
	}
	if len(m.messages) != initialCount+1 {
		t.Errorf("expected %d messages after /help, got %d", initialCount+1, len(m.messages))
	}
	last := m.messages[len(m.messages)-1]
	if last.role != "system" {
		t.Errorf("help message role = %q, want system", last.role)
	}

	// Every advertised command should appear in the help text.
	for _, want := range []string{"/quit", "/exit", "/agents", "/clear", "/model", "/sessions", "/stats", "/skills", "/help"} {
		if !strings.Contains(last.content, want) {
			t.Errorf("/help text missing %q\ngot: %s", want, last.content)
		}
	}
}

func TestSlashCommand_Help_MentionsSkillsWhenLoaded(t *testing.T) {
	m := newSlashTestModel()
	m.skills = []agentSkill{{Name: "greet", Description: "say hi", Body: "hello"}}

	m.handleSlashCommand("/help")
	last := m.messages[len(m.messages)-1]
	if !strings.Contains(last.content, "1 agent skill") {
		t.Errorf("expected help to mention skill count, got: %s", last.content)
	}
}

func TestSlashCommand_Stats_NotLoaded(t *testing.T) {
	m := newSlashTestModel()
	initialCount := len(m.messages)

	handled, cmd := m.handleSlashCommand("/stats")
	if !handled {
		t.Fatal("expected /stats to be handled")
	}
	if cmd == nil {
		t.Error("expected a loadStats cmd when stats are nil")
	}
	if len(m.messages) != initialCount+1 {
		t.Fatalf("expected %d messages, got %d", initialCount+1, len(m.messages))
	}
	last := m.messages[len(m.messages)-1]
	if last.role != "system" || !strings.Contains(last.content, "not yet loaded") {
		t.Errorf("unexpected system message: %+v", last)
	}
}

func TestSlashCommand_Stats_Loaded(t *testing.T) {
	m := newSlashTestModel()
	m.stats = &sessionStats{
		inputTokens:       100,
		outputTokens:      200,
		totalCost:         0.42,
		totalMessages:     3,
		userMessages:      2,
		assistantMessages: 1,
	}

	handled, cmd := m.handleSlashCommand("/stats")
	if !handled {
		t.Fatal("expected /stats to be handled")
	}
	if cmd != nil {
		t.Error("expected nil cmd when stats already loaded")
	}
	last := m.messages[len(m.messages)-1]
	if last.role != "system" {
		t.Errorf("expected system role, got %q", last.role)
	}
	if !strings.Contains(last.content, "Input") || !strings.Contains(last.content, "Total") {
		t.Errorf("expected stats table in message, got: %s", last.content)
	}
}

func TestSlashCommand_Skills_Empty(t *testing.T) {
	m := newSlashTestModel()
	handled, cmd := m.handleSlashCommand("/skills")
	if !handled {
		t.Fatal("expected /skills to be handled")
	}
	if cmd != nil {
		t.Error("expected nil cmd from /skills")
	}
	last := m.messages[len(m.messages)-1]
	if last.role != "system" || !strings.Contains(last.content, "No agent skills found") {
		t.Errorf("expected empty-skills message, got: %+v", last)
	}
}

func TestSlashCommand_Skills_Populated(t *testing.T) {
	m := newSlashTestModel()
	m.skills = []agentSkill{
		{Name: "greet", Description: "say hi", Body: "hello"},
		{Name: "summarise", Description: "condense text", Body: "summary"},
	}
	handled, _ := m.handleSlashCommand("/skills")
	if !handled {
		t.Fatal("expected /skills to be handled")
	}
	last := m.messages[len(m.messages)-1]
	for _, want := range []string{"/greet", "say hi", "/summarise", "condense text"} {
		if !strings.Contains(last.content, want) {
			t.Errorf("expected %q in skills listing\ngot: %s", want, last.content)
		}
	}
}

func TestSlashCommand_Model_BareEmitsHint(t *testing.T) {
	m := newSlashTestModel()
	handled, cmd := m.handleSlashCommand("/model")
	if !handled {
		t.Fatal("expected /model to be handled")
	}
	if cmd != nil {
		t.Error("expected bare /model to return no cmd (inline error only)")
	}
	last := m.messages[len(m.messages)-1]
	if last.role != "system" || !strings.Contains(last.errMsg, "/models") {
		t.Errorf("expected hint pointing at /models, got: %+v", last)
	}
}

func TestSlashCommand_Models_ReturnsPickerCmd(t *testing.T) {
	m := newSlashTestModel()
	handled, cmd := m.handleSlashCommand("/models")
	if !handled {
		t.Fatal("expected /models to be handled")
	}
	if cmd == nil {
		t.Fatal("expected /models to return a picker cmd")
	}
	if _, ok := cmd().(showModelPickerMsg); !ok {
		t.Errorf("expected showModelPickerMsg, got %T", cmd())
	}
}

func TestSlashCommand_Model_SwitchReturnsCmd(t *testing.T) {
	m := newSlashTestModel()
	handled, cmd := m.handleSlashCommand("/model sonnet")
	if !handled {
		t.Fatal("expected /model <name> to be handled")
	}
	if cmd == nil {
		t.Error("expected /model <name> to return a switch cmd")
	}
}

func TestSlashCommand_SkillActivation_DelegatesToSendPath(t *testing.T) {
	m := newSlashTestModel()
	m.skills = []agentSkill{{Name: "greet", Description: "say hi", Body: "Greet the user warmly."}}
	initialCount := len(m.messages)

	handled, cmd := m.handleSlashCommand("/greet")
	if handled || cmd != nil {
		t.Fatalf("expected /greet to be delegated (false, nil), got handled=%v cmd=%v", handled, cmd)
	}
	if len(m.messages) != initialCount {
		t.Errorf("expected no message changes from delegation, got %d new", len(m.messages)-initialCount)
	}
}

func TestSlashCommand_SkillActivation_CaseInsensitive(t *testing.T) {
	m := newSlashTestModel()
	m.skills = []agentSkill{{Name: "Greet", Description: "say hi", Body: "hi"}}

	handled, cmd := m.handleSlashCommand("/GREET")
	if handled || cmd != nil {
		t.Fatalf("expected /GREET to be delegated (case-insensitive), got handled=%v cmd=%v", handled, cmd)
	}
}

func TestSlashCommand_SkillWithProse_Delegates(t *testing.T) {
	m := newSlashTestModel()
	m.skills = []agentSkill{{Name: "greet", Description: "say hi", Body: "hi"}}

	handled, cmd := m.handleSlashCommand("/greet user warmly")
	if handled || cmd != nil {
		t.Fatalf("expected /greet-with-prose to be delegated, got handled=%v cmd=%v", handled, cmd)
	}
}

func TestSlashCommand_Unknown(t *testing.T) {
	m := newSlashTestModel()
	initialCount := len(m.messages)

	handled, cmd := m.handleSlashCommand("/foobar")
	if !handled {
		t.Fatal("expected unknown slash command to be handled")
	}
	if cmd != nil {
		t.Error("expected nil cmd from unknown command")
	}
	if len(m.messages) != initialCount+1 {
		t.Fatalf("expected %d messages, got %d", initialCount+1, len(m.messages))
	}
	if m.messages[len(m.messages)-1].errMsg == "" {
		t.Error("expected error message for unknown command")
	}
}

func TestSlashCommand_NotACommand(t *testing.T) {
	m := newSlashTestModel()
	handled, cmd := m.handleSlashCommand("hello world")
	if handled {
		t.Error("regular text should not be handled as a command")
	}
	if cmd != nil {
		t.Error("expected nil cmd for regular text")
	}
}

func TestSlashCommand_CaseInsensitive(t *testing.T) {
	m := newSlashTestModel()
	handled, _ := m.handleSlashCommand("/QUIT")
	if !handled {
		t.Error("slash commands should be case-insensitive")
	}
	handled, _ = m.handleSlashCommand("/Help")
	if !handled {
		t.Error("slash commands should be case-insensitive")
	}
}

func TestCompleteSlashCommand(t *testing.T) {
	m := newSlashTestModel()
	tests := []struct {
		prefix string
		want   string
	}{
		{"/h", "/help"},
		{"/he", "/help"},
		{"/help", "/help"},
		{"/q", "/quit"},
		{"/c", "/cancel"},
		{"/e", "/exit"},
		{"/a", "/agents"},
		{"/agents", "/agents"},
		{"/b", ""},
		{"/z", ""},
		{"/", "/commands"},
		{"/H", "/help"},
	}
	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			got := m.completeSlashCommand(tt.prefix)
			if got != tt.want {
				t.Errorf("completeSlashCommand(%q) = %q, want %q", tt.prefix, got, tt.want)
			}
		})
	}
}

func TestSlashCommandHint(t *testing.T) {
	m := newSlashTestModel()
	tests := []struct {
		name       string
		input      string
		cursorByte int
		wantToken  string
		wantSuffix string
	}{
		{"prefix /h", "/h", 2, "/h", "elp"},
		{"prefix /he", "/he", 3, "/he", "lp"},
		{"exact /help", "/help", 5, "", ""},
		{"prefix /q", "/q", 2, "/q", "uit"},
		{"prefix /a", "/a", 2, "/a", "gents"},
		{"exact /agents", "/agents", 7, "", ""},
		{"unknown /z", "/z", 2, "", ""},
		{"plain text", "hello", 5, "", ""},
		{"after slash command + space", "/help foo", 9, "", ""},
		{"empty input", "", 0, "", ""},
		{"mid-message /h after space", "use /h", 6, "/h", "elp"},
		{"mid-message cursor in middle of token", "/he abc", 2, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotToken, gotSuffix := m.slashCommandHint(tt.input, tt.cursorByte)
			if gotToken != tt.wantToken || gotSuffix != tt.wantSuffix {
				t.Errorf("slashCommandHint(%q, %d) = (%q, %q), want (%q, %q)",
					tt.input, tt.cursorByte, gotToken, gotSuffix, tt.wantToken, tt.wantSuffix)
			}
		})
	}
}

func TestAgentNameHint(t *testing.T) {
	m := newSlashTestModel()
	m.agentNames = []string{"alpha", "Beta", "Gamma Two"}
	tests := []struct {
		name       string
		input      string
		cursorByte int
		wantToken  string
		wantSuffix string
	}{
		{"empty arg completes first", "/agent ", 7, "", "alpha"},
		{"prefix matches", "/agent al", 9, "al", "pha"},
		{"case-insensitive prefix", "/agent BE", 9, "BE", "ta"},
		{"matches name with space", "/agent Gam", 10, "Gam", "ma Two"},
		{"exact match no hint", "/agent alpha", 12, "", ""},
		{"no match", "/agent zzz", 10, "", ""},
		{"command without space", "/agent", 6, "", ""},
		{"unrelated command", "/agents", 7, "", ""},
		{"cursor mid-line", "/agent al ", 9, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotToken, gotSuffix := m.agentNameHint(tt.input, tt.cursorByte)
			if gotToken != tt.wantToken || gotSuffix != tt.wantSuffix {
				t.Errorf("agentNameHint(%q, %d) = (%q, %q), want (%q, %q)",
					tt.input, tt.cursorByte, gotToken, gotSuffix, tt.wantToken, tt.wantSuffix)
			}
		})
	}
}

func TestAgentNameHint_NoAgentsLoaded(t *testing.T) {
	m := newSlashTestModel()
	token, suffix := m.agentNameHint("/agent al", 9)
	if token != "" || suffix != "" {
		t.Errorf("expected no hint when agents are unloaded, got (%q, %q)", token, suffix)
	}
}

func TestSlashCommand_Config(t *testing.T) {
	m := newSlashTestModel()
	handled, cmd := m.handleSlashCommand("/config")
	if !handled {
		t.Fatal("expected /config to be handled")
	}
	if cmd == nil {
		t.Fatal("expected a cmd from /config")
	}
	msg := cmd()
	if _, ok := msg.(showConfigMsg); !ok {
		t.Errorf("expected showConfigMsg, got %T", msg)
	}
}

func TestSlashCommand_Connections(t *testing.T) {
	m := newSlashTestModel()
	handled, cmd := m.handleSlashCommand("/connections")
	if !handled {
		t.Fatal("expected /connections to be handled")
	}
	if cmd == nil {
		t.Fatal("expected a cmd from /connections")
	}
	if _, ok := cmd().(showConnectionsMsg); !ok {
		t.Errorf("expected showConnectionsMsg, got %T", cmd())
	}
}

func TestSlashCommand_Commands_ShowsHelp(t *testing.T) {
	m := newSlashTestModel()
	initialCount := len(m.messages)
	handled, cmd := m.handleSlashCommand("/commands")
	if !handled {
		t.Fatal("expected /commands to be handled")
	}
	if cmd != nil {
		t.Error("expected nil cmd from /commands")
	}
	if len(m.messages) != initialCount+1 {
		t.Fatalf("expected %d messages, got %d", initialCount+1, len(m.messages))
	}
	last := m.messages[len(m.messages)-1]
	if last.role != "system" || !strings.Contains(last.content, "/help") {
		t.Errorf("expected help content, got: %s", last.content)
	}
}

func TestSlashCommand_Compact_SetsConfirmation(t *testing.T) {
	m := newSlashTestModel()
	initialCount := len(m.messages)

	handled, cmd := m.handleSlashCommand("/compact")
	if !handled {
		t.Fatal("expected /compact to be handled")
	}
	if cmd != nil {
		t.Error("expected nil cmd (waiting for confirmation)")
	}
	if m.pendingConfirm == nil {
		t.Fatal("expected pendingConfirm to be set")
	}
	if len(m.messages) != initialCount+1 {
		t.Fatalf("expected %d messages, got %d", initialCount+1, len(m.messages))
	}
	last := m.messages[len(m.messages)-1]
	if last.role != "system" || !strings.Contains(last.content, "y/n") {
		t.Errorf("expected confirmation prompt, got: %+v", last)
	}
}

func TestSlashCommand_Reset_SetsConfirmation(t *testing.T) {
	m := newSlashTestModel()
	initialCount := len(m.messages)

	handled, cmd := m.handleSlashCommand("/reset")
	if !handled {
		t.Fatal("expected /clear-session to be handled")
	}
	if cmd != nil {
		t.Error("expected nil cmd (waiting for confirmation)")
	}
	if m.pendingConfirm == nil {
		t.Fatal("expected pendingConfirm to be set")
	}
	if len(m.messages) != initialCount+1 {
		t.Fatalf("expected %d messages, got %d", initialCount+1, len(m.messages))
	}
	last := m.messages[len(m.messages)-1]
	if last.role != "system" || !strings.Contains(last.content, "y/n") {
		t.Errorf("expected confirmation prompt, got: %+v", last)
	}
}

// TestSlashCommand_Compact_SetsRunningStatus pins the runningStatus
// hookup that drives the spinner while compaction is in flight. The
// confirmation prompt itself is no-spinner; the running placeholder
// is appended only after the user confirms (covered separately in
// TestPendingConfirm_AppendsRunningStatusOnConfirm).
func TestSlashCommand_Compact_SetsRunningStatus(t *testing.T) {
	m := newSlashTestModel()
	if _, _ = m.handleSlashCommand("/compact"); m.pendingConfirm == nil {
		t.Fatal("expected pendingConfirm")
	}
	if m.pendingConfirm.runningStatus == "" {
		t.Errorf("expected /compact to set a runningStatus so the spinner ticks during the action")
	}
}

func TestSlashCommand_Reset_SetsRunningStatus(t *testing.T) {
	m := newSlashTestModel()
	if _, _ = m.handleSlashCommand("/reset"); m.pendingConfirm == nil {
		t.Fatal("expected pendingConfirm")
	}
	if m.pendingConfirm.runningStatus == "" {
		t.Errorf("expected /reset to set a runningStatus so the spinner ticks during the action")
	}
}

func TestSlashCommand_Help_IncludesNewCommands(t *testing.T) {
	m := newSlashTestModel()
	m.handleSlashCommand("/help")
	last := m.messages[len(m.messages)-1]
	for _, want := range []string{"/compact", "/reset"} {
		if !strings.Contains(last.content, want) {
			t.Errorf("/help text missing %q\ngot: %s", want, last.content)
		}
	}
}

func TestSlashCommand_Cancel_WhileSending(t *testing.T) {
	m := newSlashTestModel()
	m.sending = true
	m.runID = "run-1"
	m.pendingMessages = []string{"queued msg"}

	handled, cmd := m.handleSlashCommand("/cancel")
	if !handled {
		t.Fatal("expected /cancel to be handled")
	}
	if cmd == nil {
		t.Fatal("expected a cmd from /cancel while sending")
	}
	if len(m.pendingMessages) != 0 {
		t.Errorf("expected pending queue to be cleared, got %d", len(m.pendingMessages))
	}
}

func TestSlashCommand_Cancel_WhileIdle(t *testing.T) {
	m := newSlashTestModel()
	m.sending = false
	initialCount := len(m.messages)

	handled, cmd := m.handleSlashCommand("/cancel")
	if !handled {
		t.Fatal("expected /cancel to be handled")
	}
	if cmd != nil {
		t.Error("expected nil cmd from /cancel when not sending")
	}
	if len(m.messages) != initialCount+1 {
		t.Fatalf("expected %d messages, got %d", initialCount+1, len(m.messages))
	}
	last := m.messages[len(m.messages)-1]
	if last.role != "system" || last.content != "Nothing to cancel." {
		t.Errorf("unexpected message: role=%q content=%q", last.role, last.content)
	}
}

func TestSlashCommand_Cancel_NoRunID(t *testing.T) {
	m := newSlashTestModel()
	m.sending = true
	m.runID = "" // sending but no runID yet

	handled, cmd := m.handleSlashCommand("/cancel")
	if !handled {
		t.Fatal("expected /cancel to be handled")
	}
	if cmd != nil {
		t.Error("expected nil cmd when sending but no runID")
	}
	last := m.messages[len(m.messages)-1]
	if last.content != "Nothing to cancel." {
		t.Errorf("expected 'Nothing to cancel.', got %q", last.content)
	}
}

func TestSlashCommand_Help_IncludesCancel(t *testing.T) {
	m := newSlashTestModel()
	m.handleSlashCommand("/help")
	last := m.messages[len(m.messages)-1]
	if !strings.Contains(last.content, "/cancel") {
		t.Errorf("/help text missing /cancel\ngot: %s", last.content)
	}
}

func TestEscKey_WhileSending_CancelsTurn(t *testing.T) {
	m := newSlashTestModel()
	m.sending = true
	m.runID = "run-1"
	m.pendingMessages = []string{"queued"}

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	if cmd == nil {
		t.Fatal("expected a cmd from Escape while sending")
	}
	if len(updated.pendingMessages) != 0 {
		t.Errorf("expected pending queue to be cleared, got %d", len(updated.pendingMessages))
	}
}

func TestEscKey_WhileIdle_NoOp(t *testing.T) {
	m := newSlashTestModel()
	m.sending = false
	initialCount := len(m.messages)

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	if cmd != nil {
		t.Error("expected nil cmd from Escape when not sending")
	}
	if len(updated.messages) != initialCount {
		t.Errorf("expected no new messages, got %d (was %d)", len(updated.messages), initialCount)
	}
}

func TestCompleteSlashCommand_IncludesSkills(t *testing.T) {
	m := newSlashTestModel()
	m.skills = []agentSkill{
		{Name: "greet", Description: "say hi"},
		{Name: "summarise", Description: "condense"},
	}

	// Built-in commands still take priority when they match.
	if got := m.completeSlashCommand("/s"); got != "/sessions" {
		t.Errorf("completeSlashCommand(%q) = %q, want %q", "/s", got, "/sessions")
	}
	// Skill name is reachable when no built-in prefix matches.
	if got := m.completeSlashCommand("/gr"); got != "/greet" {
		t.Errorf("completeSlashCommand(%q) = %q, want %q", "/gr", got, "/greet")
	}
	if got := m.completeSlashCommand("/sum"); got != "/summarise" {
		t.Errorf("completeSlashCommand(%q) = %q, want %q", "/sum", got, "/summarise")
	}
}

func tabKey() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: tea.KeyTab}
}

func shiftTabKey() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
}

func TestTabCompletion_LCPExtendsMultiCandidate(t *testing.T) {
	m := newSlashTabTestModel()
	m.skills = []agentSkill{{Name: "example"}, {Name: "exams"}}
	m.textarea.SetValue("/exa")
	m.textarea.CursorEnd()

	updated, _ := m.Update(tabKey())
	if got := updated.textarea.Value(); got != "/exam" {
		t.Errorf("after Tab: got %q, want %q", got, "/exam")
	}
	if updated.completion.cycling {
		t.Error("expected cycling=false after LCP extension")
	}
	if !updated.completion.visible {
		t.Error("expected menu visible after LCP extension")
	}
}

func TestTabCompletion_FullOnSingleCandidate(t *testing.T) {
	m := newSlashTabTestModel()
	m.textarea.SetValue("/qu")
	m.textarea.CursorEnd()

	updated, _ := m.Update(tabKey())
	if got := updated.textarea.Value(); got != "/quit" {
		t.Errorf("after Tab: got %q, want %q", got, "/quit")
	}
	if updated.completion.cycling {
		t.Error("expected cycling=false on single-candidate completion")
	}
}

func TestTabCompletion_CycleAtLCP(t *testing.T) {
	m := newSlashTabTestModel()
	m.skills = []agentSkill{{Name: "example"}, {Name: "exams"}}
	m.textarea.SetValue("/exa")
	m.textarea.CursorEnd()

	// First Tab: LCP extension to /exam.
	updated, _ := m.Update(tabKey())
	if got := updated.textarea.Value(); got != "/exam" {
		t.Fatalf("Tab #1: got %q, want %q", got, "/exam")
	}

	// Second Tab: at LCP, start cycling, pick first candidate.
	updated, _ = updated.Update(tabKey())
	if got := updated.textarea.Value(); got != "/example" {
		t.Fatalf("Tab #2: got %q, want %q", got, "/example")
	}
	if !updated.completion.cycling || updated.completion.cycleIndex != 0 {
		t.Errorf("Tab #2: expected cycling=true index=0, got cycling=%v index=%d", updated.completion.cycling, updated.completion.cycleIndex)
	}

	// Third Tab: advance cycle.
	updated, _ = updated.Update(tabKey())
	if got := updated.textarea.Value(); got != "/exams" {
		t.Fatalf("Tab #3: got %q, want %q", got, "/exams")
	}

	// Fourth Tab: wrap around.
	updated, _ = updated.Update(tabKey())
	if got := updated.textarea.Value(); got != "/example" {
		t.Fatalf("Tab #4 (wrap): got %q, want %q", got, "/example")
	}
}

func TestTabCompletion_ShiftTabCyclesBack(t *testing.T) {
	m := newSlashTabTestModel()
	m.skills = []agentSkill{{Name: "example"}, {Name: "exams"}}
	m.textarea.SetValue("/exam")
	m.textarea.CursorEnd()

	// Tab from LCP enters cycle and selects /example.
	updated, _ := m.Update(tabKey())
	if got := updated.textarea.Value(); got != "/example" {
		t.Fatalf("Tab: got %q, want %q", got, "/example")
	}

	// Shift+Tab from index 0 wraps to last (/exams).
	updated, _ = updated.Update(shiftTabKey())
	if got := updated.textarea.Value(); got != "/exams" {
		t.Errorf("Shift+Tab: got %q, want %q", got, "/exams")
	}
}

func TestTabCompletion_NonTabResetsCycle(t *testing.T) {
	m := newSlashTabTestModel()
	m.skills = []agentSkill{{Name: "example"}, {Name: "exams"}}
	m.textarea.SetValue("/exam")
	m.textarea.CursorEnd()

	// Enter cycle.
	updated, _ := m.Update(tabKey())
	if !updated.completion.cycling {
		t.Fatal("expected to be cycling after Tab at LCP")
	}

	// Any printable keypress should reset cycle state via refreshCompletionMenu.
	updated, _ = updated.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if updated.completion.cycling {
		t.Error("expected cycling=false after non-Tab keypress")
	}
}

func TestCompletionMenu_HidesWhenTokenBroken(t *testing.T) {
	m := newSlashTabTestModel()
	m.textarea.SetValue("/h")
	m.textarea.CursorEnd()

	// Trigger a refresh by typing 'e' — textarea becomes "/he" with cursor
	// at end, and "/he" is still a prefix of /help so the menu opens.
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if !updated.completion.visible {
		t.Fatalf("expected menu visible at /he, got value=%q visible=%v", updated.textarea.Value(), updated.completion.visible)
	}

	// Typing a space breaks the slash-token boundary — cursor sits after
	// whitespace, so findSlashTokenAt returns ok=false.
	updated, _ = updated.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	if updated.completion.visible {
		t.Errorf("expected menu hidden after space breaks the token, value=%q", updated.textarea.Value())
	}
}

func TestTabCompletion_NoCandidatesIsNoOp(t *testing.T) {
	m := newSlashTabTestModel()
	m.textarea.SetValue("/zzz")
	m.textarea.CursorEnd()

	updated, _ := m.Update(tabKey())
	if got := updated.textarea.Value(); got != "/zzz" {
		t.Errorf("expected textarea unchanged, got %q", got)
	}
}

func TestAgentTabCompletion_LCPExtendsMultiCandidate(t *testing.T) {
	m := newSlashTabTestModel()
	m.agentNames = []string{"main", "mail"}
	m.textarea.SetValue("/agent ma")
	m.textarea.CursorEnd()

	updated, _ := m.Update(tabKey())
	if got := updated.textarea.Value(); got != "/agent mai" {
		t.Errorf("after Tab: got %q, want %q", got, "/agent mai")
	}
	if updated.completion.cycling {
		t.Error("expected cycling=false after LCP extension")
	}
}

func TestAgentTabCompletion_FullOnSingleCandidate(t *testing.T) {
	m := newSlashTabTestModel()
	m.agentNames = []string{"alpha", "beta"}
	m.textarea.SetValue("/agent al")
	m.textarea.CursorEnd()

	updated, _ := m.Update(tabKey())
	if got := updated.textarea.Value(); got != "/agent alpha" {
		t.Errorf("after Tab: got %q, want %q", got, "/agent alpha")
	}
}

func TestAgentTabCompletion_CycleAtLCP(t *testing.T) {
	m := newSlashTabTestModel()
	m.agentNames = []string{"main", "mail"}
	m.textarea.SetValue("/agent mai")
	m.textarea.CursorEnd()

	// First Tab from LCP enters cycle, picks first.
	updated, _ := m.Update(tabKey())
	if got := updated.textarea.Value(); got != "/agent main" {
		t.Fatalf("Tab #1: got %q, want %q", got, "/agent main")
	}
	// Second Tab advances cycle.
	updated, _ = updated.Update(tabKey())
	if got := updated.textarea.Value(); got != "/agent mail" {
		t.Fatalf("Tab #2: got %q, want %q", got, "/agent mail")
	}
	// Third Tab wraps.
	updated, _ = updated.Update(tabKey())
	if got := updated.textarea.Value(); got != "/agent main" {
		t.Fatalf("Tab #3 (wrap): got %q, want %q", got, "/agent main")
	}
}

func TestAgentTabCompletion_ShiftTabCyclesBack(t *testing.T) {
	m := newSlashTabTestModel()
	m.agentNames = []string{"main", "mail"}
	m.textarea.SetValue("/agent mai")
	m.textarea.CursorEnd()

	updated, _ := m.Update(tabKey())
	if got := updated.textarea.Value(); got != "/agent main" {
		t.Fatalf("Tab: got %q, want %q", got, "/agent main")
	}
	updated, _ = updated.Update(shiftTabKey())
	if got := updated.textarea.Value(); got != "/agent mail" {
		t.Errorf("Shift+Tab from index 0 should wrap, got %q", got)
	}
}

func TestAgentTabCompletion_PreservesOriginalCasing(t *testing.T) {
	m := newSlashTabTestModel()
	m.agentNames = []string{"Alpha", "Beta", "Gamma Two"}
	m.textarea.SetValue("/agent be")
	m.textarea.CursorEnd()

	// Single match — full completion preserves original casing.
	updated, _ := m.Update(tabKey())
	if got := updated.textarea.Value(); got != "/agent Beta" {
		t.Errorf("expected original casing preserved, got %q", got)
	}
}

func TestAgentTabCompletion_MenuVisibleWhileTyping(t *testing.T) {
	m := newSlashTabTestModel()
	m.agentNames = []string{"main", "mail"}
	m.textarea.SetValue("/agent ma")
	m.textarea.CursorEnd()

	// A keypress that leaves the textarea in the agent-arg context
	// should populate the menu via refreshCompletionMenu.
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	if !updated.completion.visible {
		t.Fatalf("expected menu visible at /agent mai, got value=%q visible=%v", updated.textarea.Value(), updated.completion.visible)
	}
	if len(updated.completion.candidates) != 2 {
		t.Errorf("expected 2 candidates, got %d: %v", len(updated.completion.candidates), updated.completion.candidates)
	}
}

func TestLongestCommonPrefixFold(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"empty", nil, ""},
		{"single preserves casing", []string{"Beta"}, "Beta"},
		{"mixed case converges", []string{"Beta", "Beach"}, "Be"},
		{"diverging case", []string{"main", "Mail"}, "mai"},
		{"first casing wins", []string{"BEta", "beach"}, "BE"},
		{"no common prefix", []string{"alpha", "Beta"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := longestCommonPrefixFold(tt.in); got != tt.want {
				t.Errorf("longestCommonPrefixFold(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
