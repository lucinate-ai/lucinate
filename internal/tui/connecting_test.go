package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lucinate-ai/lucinate/internal/backend"
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

func TestConnectingModel_AuthMismatchClearTokenCallsBackend(t *testing.T) {
	conn := &config.Connection{Name: "home", URL: "https://home.example.com"}
	fake := newFakeBackend()
	m := newConnectingModel(conn, false)
	m.enterAuthModal(conn, fake, authRecoveryTokenMismatch, errors.New("gateway token mismatch"))

	// "1" / Enter → clear token & retry.
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: '1', Text: "1"})
	if cmd == nil {
		t.Fatal("expected cmd from clear-token choice")
	}
	msg := cmd()
	if !fake.clearedToken {
		t.Error("expected ClearToken to be called")
	}
	resolved, ok := msg.(authResolvedMsg)
	if !ok {
		t.Fatalf("expected authResolvedMsg, got %T", msg)
	}
	if resolved.cancelled {
		t.Error("clear-token should not be a cancellation")
	}
}

func TestConnectingModel_AuthMismatchResetIdentityCallsBackend(t *testing.T) {
	conn := &config.Connection{Name: "home", URL: "https://home.example.com"}
	fake := newFakeBackend()
	m := newConnectingModel(conn, false)
	m.enterAuthModal(conn, fake, authRecoveryTokenMismatch, errors.New("gateway token mismatch"))

	_, cmd := m.handleKey(tea.KeyPressMsg{Code: '2', Text: "2"})
	if cmd == nil {
		t.Fatal("expected cmd from reset-identity choice")
	}
	cmd()
	if !fake.resetIdentity {
		t.Error("expected ResetIdentity to be called")
	}
}

func TestConnectingModel_TokenPromptStoresOnDeviceAuth(t *testing.T) {
	conn := &config.Connection{Name: "home", URL: "https://home.example.com"}
	fake := newFakeBackend()
	m := newConnectingModel(conn, false)
	m.enterAuthModal(conn, fake, authRecoveryTokenMissing, errors.New("gateway token missing"))

	m.tokenInput.SetValue("pre-shared-token")
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd from Enter")
	}
	cmd()
	if fake.storedToken != "pre-shared-token" {
		t.Errorf("StoreToken not called with submitted value, got %q", fake.storedToken)
	}
}

func TestConnectingModel_APIKeyPromptStoresOnAPIKeyAuth(t *testing.T) {
	conn := &config.Connection{Name: "openai", URL: "http://localhost:11434/v1"}
	fake := newFakeBackend()
	fake.caps.AuthRecovery = backend.AuthRecoveryAPIKey
	m := newConnectingModel(conn, false)
	m.enterAuthModal(conn, fake, authRecoveryAPIKey, errors.New("api key required"))

	if !strings.Contains(m.View(), "pre-shared auth token") {
		// The shared token-prompt copy is reused; the modal still
		// renders the same prompt regardless of recovery type.
		t.Logf("token prompt copy: %q", m.View())
	}

	m.tokenInput.SetValue("sk-test-123")
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd from Enter")
	}
	cmd()
	if fake.storedAPIKey != "sk-test-123" {
		t.Errorf("StoreAPIKey not called with submitted value, got %q", fake.storedAPIKey)
	}
	if fake.storedToken != "" {
		t.Errorf("StoreToken should not be called for API-key flow, got %q", fake.storedToken)
	}
}

func TestConnectingModel_TokenPromptIgnoresEmpty(t *testing.T) {
	conn := &config.Connection{Name: "home", URL: "https://home.example.com"}
	fake := newFakeBackend()
	m := newConnectingModel(conn, false)
	m.enterAuthModal(conn, fake, authRecoveryTokenMissing, errors.New("gateway token missing"))

	// Empty input + Enter — no cmd, no store.
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Errorf("expected no cmd for empty submit, got %T", cmd)
	}
	if fake.storedToken != "" {
		t.Errorf("StoreToken should not be called on empty submit, got %q", fake.storedToken)
	}
}

func TestConnectingModel_PairingRequiredShowsInstructions(t *testing.T) {
	conn := &config.Connection{Name: "home", URL: "https://home.example.com"}
	m := newConnectingModel(conn, false)
	m.enterAuthModal(conn, nil, authRecoveryNotPaired, errors.New("connect: hello: connect rejected: NOT_PAIRED: pairing required"))

	if m.subState != subStatePairingRequired {
		t.Fatalf("subState = %v, want pairing-required", m.subState)
	}
	view := m.View()
	if !strings.Contains(view, "Pairing required") {
		t.Errorf("view missing pairing-required header:\n%s", view)
	}
	if !strings.Contains(view, "openclaw devices approve --latest") {
		t.Errorf("view missing approve command hint:\n%s", view)
	}
	if m.wantsInput() {
		t.Error("pairing-required modal should not request a text input")
	}
	actions := m.Actions()
	if len(actions) != 2 {
		t.Errorf("expected 2 actions on pairing-required modal, got %d", len(actions))
	}
}

func TestConnectingModel_PairingRequiredWrapsLongError(t *testing.T) {
	conn := &config.Connection{Name: "home", URL: "https://home.example.com"}
	m := newConnectingModel(conn, false)
	m.setSize(50, 24)
	longErr := errors.New("connect: hello: connect rejected: NOT_PAIRED: pairing required: device is not approved by the gateway administrator yet")
	m.enterAuthModal(conn, nil, authRecoveryNotPaired, longErr)

	view := m.View()
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, longErr.Error()) {
			t.Errorf("error rendered on a single line without wrapping at width=50:\n%s", view)
		}
	}
	// Sanity-check that fragments of the error did still make it in.
	if !strings.Contains(view, "NOT_PAIRED") || !strings.Contains(view, "administrator yet") {
		t.Errorf("wrapped error lost content:\n%s", view)
	}
}

func TestConnectingModel_PairingRequiredEnterRetries(t *testing.T) {
	conn := &config.Connection{Name: "home", URL: "https://home.example.com"}
	fake := newFakeBackend()
	m := newConnectingModel(conn, false)
	m.enterAuthModal(conn, fake, authRecoveryNotPaired, errors.New("NOT_PAIRED"))

	_, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd from Enter on pairing-required modal")
	}
	resolved, ok := cmd().(authResolvedMsg)
	if !ok {
		t.Fatalf("expected authResolvedMsg, got %T", cmd())
	}
	if resolved.cancelled {
		t.Error("retry should not be a cancellation")
	}
	if resolved.backend != fake {
		t.Error("retry should reuse the same backend")
	}
}

func TestConnectingModel_PairingRequiredEscCancels(t *testing.T) {
	conn := &config.Connection{Name: "home", URL: "https://home.example.com"}
	m := newConnectingModel(conn, false)
	m.enterAuthModal(conn, nil, authRecoveryNotPaired, errors.New("NOT_PAIRED"))

	_, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected cmd from Esc on pairing-required modal")
	}
	resolved, ok := cmd().(authResolvedMsg)
	if !ok {
		t.Fatalf("expected authResolvedMsg, got %T", cmd())
	}
	if !resolved.cancelled {
		t.Error("Esc should set Cancelled=true")
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
