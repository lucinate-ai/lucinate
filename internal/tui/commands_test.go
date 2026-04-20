package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
)

func newSlashTestModel() *chatModel {
	vp := viewport.New()
	return &chatModel{
		viewport:  vp,
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

func TestSlashCommand_Model_ListReturnsCmd(t *testing.T) {
	m := newSlashTestModel()
	handled, cmd := m.handleSlashCommand("/model")
	if !handled {
		t.Fatal("expected /model to be handled")
	}
	if cmd == nil {
		t.Error("expected /model to return a list cmd")
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

func TestSlashCommand_SkillActivation(t *testing.T) {
	m := newSlashTestModel()
	m.skills = []agentSkill{{Name: "greet", Description: "say hi", Body: "Greet the user warmly."}}
	initialCount := len(m.messages)

	handled, cmd := m.handleSlashCommand("/greet")
	if !handled {
		t.Fatal("expected /greet to be handled as skill activation")
	}
	if cmd == nil {
		t.Error("expected sendMessage cmd from skill activation")
	}
	if !m.sending {
		t.Error("expected m.sending to be true after skill activation")
	}
	if len(m.messages) != initialCount+1 {
		t.Fatalf("expected %d messages, got %d", initialCount+1, len(m.messages))
	}
	last := m.messages[len(m.messages)-1]
	if last.role != "user" || last.content != "/greet" {
		t.Errorf("expected user message %q, got role=%q content=%q", "/greet", last.role, last.content)
	}
}

func TestSlashCommand_SkillActivation_CaseInsensitive(t *testing.T) {
	m := newSlashTestModel()
	m.skills = []agentSkill{{Name: "Greet", Description: "say hi", Body: "hi"}}

	handled, cmd := m.handleSlashCommand("/GREET")
	if !handled {
		t.Fatal("expected skill activation to be case-insensitive")
	}
	if cmd == nil {
		t.Error("expected a sendMessage cmd")
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
		{"/c", "/clear"},
		{"/e", "/exit"},
		{"/a", "/agents"},
		{"/agents", "/agents"},
		{"/b", ""},
		{"/z", ""},
		{"/", "/agents"},
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
		input string
		want  string
	}{
		{"/h", "elp"},
		{"/he", "lp"},
		{"/help", ""},
		{"/q", "uit"},
		{"/a", "gents"},
		{"/agents", ""},
		{"/z", ""},
		{"hello", ""},
		{"/help foo", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := m.slashCommandHint(tt.input)
			if got != tt.want {
				t.Errorf("slashCommandHint(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
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
