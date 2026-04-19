package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
)

func newSlashTestModel() *chatModel {
	vp := viewport.New(80, 20)
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

func TestSlashCommand_Back(t *testing.T) {
	m := newSlashTestModel()
	handled, cmd := m.handleSlashCommand("/back")
	if !handled {
		t.Fatal("expected /back to be handled")
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
	if m.messages[len(m.messages)-1].role != "system" {
		t.Errorf("help message role = %q, want system", m.messages[len(m.messages)-1].role)
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
		{"/b", "/back"},
		{"/c", "/clear"},
		{"/e", "/exit"},
		{"/z", ""},
		{"/", "/back"},
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
