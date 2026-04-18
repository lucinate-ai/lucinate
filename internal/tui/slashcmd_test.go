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
	// Execute the cmd and check the message type.
	msg := cmd()
	if _, ok := msg.(goBackMsg); !ok {
		t.Errorf("expected goBackMsg, got %T", msg)
	}
}

func TestSlashCommand_Clear(t *testing.T) {
	m := newSlashTestModel()
	if len(m.messages) != 2 {
		t.Fatalf("precondition: expected 2 messages, got %d", len(m.messages))
	}

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
	// Should have added a separator + help message.
	if len(m.messages) != initialCount+2 {
		t.Errorf("expected %d messages after /help, got %d", initialCount+2, len(m.messages))
	}
	lastMsg := m.messages[len(m.messages)-1]
	if lastMsg.role != "assistant" {
		t.Errorf("help message role = %q, want assistant", lastMsg.role)
	}
	if lastMsg.content == "" {
		t.Error("help message content should not be empty")
	}
}

func TestSlashCommand_Unknown(t *testing.T) {
	m := newSlashTestModel()
	initialCount := len(m.messages)

	handled, cmd := m.handleSlashCommand("/foobar")
	if !handled {
		t.Fatal("expected unknown slash command to be handled (with error)")
	}
	if cmd != nil {
		t.Error("expected nil cmd from unknown command")
	}
	if len(m.messages) != initialCount+1 {
		t.Fatalf("expected %d messages, got %d", initialCount+1, len(m.messages))
	}
	lastMsg := m.messages[len(m.messages)-1]
	if lastMsg.errMsg == "" {
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
