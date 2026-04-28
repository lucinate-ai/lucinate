package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lucinate-ai/lucinate/internal/config"
)

func TestConnectingModel_DialingView(t *testing.T) {
	conn := &config.Connection{Name: "home", URL: "https://home.example.com"}
	m := newConnectingModel(conn, false)
	if !strings.Contains(m.View(), "Connecting to home") {
		t.Errorf("dialing view missing connection name, got:\n%s", m.View())
	}
}

func TestConnectingModel_AuthMismatchModalChoices(t *testing.T) {
	conn := &config.Connection{Name: "home", URL: "https://home.example.com"}
	m := newConnectingModel(conn, false)
	m.enterAuthModal(conn, nil, authRecoveryTokenMismatch, errors.New("gateway token mismatch"))

	if m.subState != subStateAuthMismatchPrompt {
		t.Fatalf("subState = %v, want auth-mismatch", m.subState)
	}
	if !strings.Contains(m.View(), "stored device token was rejected") {
		t.Errorf("view missing recovery prompt:\n%s", m.View())
	}
	actions := m.Actions()
	if len(actions) != 3 {
		t.Errorf("expected 3 actions in auth-mismatch modal, got %d", len(actions))
	}
}

func TestConnectingModel_AuthTokenPromptShowsInput(t *testing.T) {
	conn := &config.Connection{Name: "home", URL: "https://home.example.com"}
	m := newConnectingModel(conn, false)
	m.enterAuthModal(conn, nil, authRecoveryTokenMissing, errors.New("gateway token missing"))

	if m.subState != subStateAuthTokenPrompt {
		t.Fatalf("subState = %v, want auth-token", m.subState)
	}
	if !m.wantsInput() {
		t.Error("token prompt should report wantsInput=true")
	}
	if !strings.Contains(m.View(), "pre-shared auth token") {
		t.Errorf("view missing token prompt:\n%s", m.View())
	}
}

func TestConnectingModel_AuthCancelEmitsResolvedCancelled(t *testing.T) {
	conn := &config.Connection{Name: "home", URL: "https://home.example.com"}
	m := newConnectingModel(conn, false)
	m.enterAuthModal(conn, nil, authRecoveryTokenMismatch, errors.New("gateway token mismatch"))

	_, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected cmd from cancel")
	}
	msg := cmd()
	resolved, ok := msg.(authResolvedMsg)
	if !ok {
		t.Fatalf("expected authResolvedMsg, got %T", msg)
	}
	if !resolved.cancelled {
		t.Error("cancel should set Cancelled=true")
	}
}
