package tui

import (
	"strings"
	"testing"

	"github.com/lucinate-ai/lucinate/internal/backend"
)

// nonRoomsBackend reports no room capability — the shape of an OpenClaw or
// OpenAI-compatible connection. Rooms are a Hermes-gateway surface: without
// this gate the view dialled `<url>/api/ws` on backends that have no such
// endpoint and reported a transport error instead of the real reason.
type nonRoomsBackend struct{ backend.Backend }

func (nonRoomsBackend) Capabilities() backend.Capabilities { return backend.Capabilities{} }

func roomsCapableBackend() backend.Backend {
	return roomsCapBackend{}
}

type roomsCapBackend struct{ backend.Backend }

func (roomsCapBackend) Capabilities() backend.Capabilities {
	return backend.Capabilities{Rooms: true}
}

func TestSlashCommand_RoomsOnANonHermesConnectionIsRefused(t *testing.T) {
	m := newTestChatModel()
	m.backend = nonRoomsBackend{}

	handled, cmd := m.handleSlashCommand("/rooms")
	if !handled {
		t.Fatal("expected /rooms to be handled")
	}
	if cmd != nil {
		t.Fatalf("a refused /rooms must not navigate, got %T", cmd())
	}
	if !hasSystemMessage(m.messages, "not available on this connection") {
		t.Fatalf("expected a not-available notice, got %+v", m.messages)
	}
}

func TestSlashCommand_RoomsOnAHermesConnectionNavigates(t *testing.T) {
	m := newTestChatModel()
	m.backend = roomsCapableBackend()

	handled, cmd := m.handleSlashCommand("/rooms")
	if !handled {
		t.Fatal("expected /rooms to be handled")
	}
	if cmd == nil {
		t.Fatal("expected a navigation command")
	}
	if _, ok := cmd().(showRoomsMsg); !ok {
		t.Fatalf("expected showRoomsMsg, got %T", cmd())
	}
}

func hasSystemMessage(msgs []chatMessage, substr string) bool {
	for _, msg := range msgs {
		if msg.role == "system" && (strings.Contains(msg.content, substr) || strings.Contains(msg.errMsg, substr)) {
			return true
		}
	}
	return false
}
