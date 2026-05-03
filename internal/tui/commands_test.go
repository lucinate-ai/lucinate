package tui

import (
	"strings"
	"testing"

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
