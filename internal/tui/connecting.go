package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// connectingSubState distinguishes the plain "connecting…" spinner
// from the modal flows that recover from auth errors. Each modal
// owns its own input affordances; the state machine is small so
// keeping them as a single view (rather than separate model files)
// avoids a lot of pass-through plumbing.
type connectingSubState int

const (
	subStateDialing connectingSubState = iota
	subStateAuthMismatchPrompt
	subStateAuthTokenPrompt
)

// connectingModel handles the in-progress connect attempt and the
// auth-recovery modals. It is a transient view: success transitions to
// viewSelect, cancellation/resolution feeds back through messages on
// AppModel.
type connectingModel struct {
	connection *config.Connection
	client     *client.Client
	subState   connectingSubState
	hideHints  bool

	tokenInput textinput.Model
	authErr    error

	width  int
	height int
}

func newConnectingModel(conn *config.Connection, hideHints bool) connectingModel {
	return connectingModel{
		connection: conn,
		subState:   subStateDialing,
		hideHints:  hideHints,
	}
}

// enterAuthModal switches the model into the appropriate recovery
// sub-state. AppModel calls this when runConnect returned a
// recoverable auth error.
func (m *connectingModel) enterAuthModal(conn *config.Connection, c *client.Client, need authRecovery, err error) {
	m.connection = conn
	m.client = c
	m.authErr = err
	switch need {
	case authRecoveryTokenMismatch:
		m.subState = subStateAuthMismatchPrompt
	case authRecoveryTokenMissing:
		m.subState = subStateAuthTokenPrompt
		ti := textinput.New()
		ti.Placeholder = "auth token"
		ti.CharLimit = 256
		ti.Focus()
		m.tokenInput = ti
	}
}

func (m connectingModel) Init() tea.Cmd {
	return nil
}

func (m connectingModel) Update(msg tea.Msg) (connectingModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		return m.handleKey(key)
	}
	if m.subState == subStateAuthTokenPrompt {
		var cmd tea.Cmd
		m.tokenInput, cmd = m.tokenInput.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m connectingModel) handleKey(msg tea.KeyPressMsg) (connectingModel, tea.Cmd) {
	switch m.subState {
	case subStateAuthMismatchPrompt:
		switch msg.String() {
		case "1", "enter":
			c := m.client
			conn := m.connection
			return m, func() tea.Msg {
				if err := c.ClearToken(); err != nil {
					return connectResultMsg{connection: conn, err: fmt.Errorf("clear token: %w", err)}
				}
				return authResolvedMsg{connection: conn, client: c}
			}
		case "2":
			c := m.client
			conn := m.connection
			return m, func() tea.Msg {
				if err := c.ResetIdentity(); err != nil {
					return connectResultMsg{connection: conn, err: fmt.Errorf("reset identity: %w", err)}
				}
				return authResolvedMsg{connection: conn, client: c}
			}
		case "esc", "3", "q":
			c := m.client
			conn := m.connection
			return m, func() tea.Msg {
				return authResolvedMsg{connection: conn, client: c, cancelled: true}
			}
		}
		return m, nil

	case subStateAuthTokenPrompt:
		switch msg.String() {
		case "enter":
			token := strings.TrimSpace(m.tokenInput.Value())
			if token == "" {
				return m, nil
			}
			c := m.client
			conn := m.connection
			return m, func() tea.Msg {
				if err := c.StoreToken(token); err != nil {
					return connectResultMsg{connection: conn, err: fmt.Errorf("store token: %w", err)}
				}
				return authResolvedMsg{connection: conn, client: c}
			}
		case "esc":
			c := m.client
			conn := m.connection
			return m, func() tea.Msg {
				return authResolvedMsg{connection: conn, client: c, cancelled: true}
			}
		}
		var cmd tea.Cmd
		m.tokenInput, cmd = m.tokenInput.Update(msg)
		return m, cmd
	}
	return m, nil
}

// Actions returns view-level commands. The dialing state has none —
// the only way out is success, error, or Ctrl+C. Modal sub-states
// surface their resolution choices through Actions so native
// platform embedders get buttons.
func (m connectingModel) Actions() []Action {
	switch m.subState {
	case subStateAuthMismatchPrompt:
		return []Action{
			{ID: "auth-clear-retry", Label: "Clear token & retry", Key: "1"},
			{ID: "auth-reset-identity", Label: "Reset identity", Key: "2"},
			{ID: "auth-cancel", Label: "Cancel", Key: "esc"},
		}
	case subStateAuthTokenPrompt:
		return []Action{
			{ID: "auth-cancel", Label: "Cancel", Key: "esc"},
		}
	}
	return nil
}

// TriggerAction lets embedders invoke modal choices without forging
// keystrokes.
func (m connectingModel) TriggerAction(id string) (connectingModel, tea.Cmd) {
	switch id {
	case "auth-clear-retry":
		return m.handleKey(tea.KeyPressMsg{Code: '1', Text: "1"})
	case "auth-reset-identity":
		return m.handleKey(tea.KeyPressMsg{Code: '2', Text: "2"})
	case "auth-cancel":
		return m.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	}
	return m, nil
}

// wantsInput reports whether the embedder should surface its on-screen
// keyboard. Only the token prompt has a focused text input.
func (m connectingModel) wantsInput() bool {
	return m.subState == subStateAuthTokenPrompt
}

func (m *connectingModel) setSize(w, h int) {
	m.width = w
	m.height = h
}

func (m connectingModel) View() string {
	switch m.subState {
	case subStateAuthMismatchPrompt:
		return m.viewAuthMismatch()
	case subStateAuthTokenPrompt:
		return m.viewAuthToken()
	}
	name := "gateway"
	if m.connection != nil {
		name = m.connection.Name
	}
	return fmt.Sprintf("\n  Connecting to %s...\n", name)
}

func (m connectingModel) viewAuthMismatch() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(headerStyle.Render(" Auth recovery "))
	b.WriteString("\n\n")
	if m.authErr != nil {
		b.WriteString(errorStyle.Render(fmt.Sprintf("  %v", m.authErr)))
		b.WriteString("\n\n")
	}
	b.WriteString("  The stored device token was rejected by the gateway.\n\n")
	b.WriteString("  1) Clear stored token and retry  (recommended)\n")
	b.WriteString("  2) Reset full identity and retry\n")
	b.WriteString("  Esc) Cancel\n")
	return b.String()
}

func (m connectingModel) viewAuthToken() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(headerStyle.Render(" Auth token required "))
	b.WriteString("\n\n")
	if m.authErr != nil {
		b.WriteString(errorStyle.Render(fmt.Sprintf("  %v", m.authErr)))
		b.WriteString("\n\n")
	}
	b.WriteString("  This gateway requires a pre-shared auth token.\n")
	b.WriteString("  Ask your gateway operator if you don't have one.\n\n")
	b.WriteString("  Token:\n")
	b.WriteString("  " + m.tokenInput.View() + "\n\n")
	b.WriteString(helpStyle.Render("  Enter: submit | Esc: cancel"))
	b.WriteString("\n")
	return b.String()
}
