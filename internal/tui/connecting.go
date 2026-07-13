package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lucinate-ai/lucinate/internal/backend"
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
	subStatePairingRequired
)

// connectingModel handles the in-progress connect attempt and the
// auth-recovery modals. It is a transient view: success transitions to
// viewSelect, cancellation/resolution feeds back through messages on
// AppModel.
type connectingModel struct {
	connection *config.Connection
	backend    backend.Backend
	subState   connectingSubState
	authNeed   authRecovery // which recovery flow is active in subStateAuthTokenPrompt
	hideHints  bool

	tokenInput textinput.Model
	authErr    error

	// resolving is set once the user commits a recovery choice (submit
	// token, clear/reset identity, retry pairing) and the store+connect
	// round-trip is dispatched. While set, the modal ignores further
	// input so a double-tap can't dispatch the mutation twice. Reset on
	// re-entry (enterAuthModal) when a retry lands back on this modal.
	resolving bool

	width  int
	height int

	// body wraps the scrollable middle of each modal so the pinned
	// title at the top and pinned help line at the bottom always stay
	// visible on short terminals. Shared across the three modal
	// substates because only one is active at a time.
	body viewport.Model
}

func newConnectingModel(conn *config.Connection, hideHints bool) connectingModel {
	return connectingModel{
		connection: conn,
		subState:   subStateDialing,
		hideHints:  hideHints,
		body:       viewport.New(),
	}
}

// enterAuthModal switches the model into the appropriate recovery
// sub-state. AppModel calls this when runConnect returned a
// recoverable auth error.
func (m *connectingModel) enterAuthModal(conn *config.Connection, b backend.Backend, need authRecovery, err error) {
	m.connection = conn
	m.backend = b
	m.authErr = err
	m.authNeed = need
	// A retry that failed again re-enters this same model instance;
	// clear the in-flight guard so the reopened modal is interactive.
	m.resolving = false
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
	case authRecoveryAPIKey:
		m.subState = subStateAuthTokenPrompt
		ti := textinput.New()
		ti.Placeholder = "api key"
		ti.CharLimit = 512
		ti.Focus()
		m.tokenInput = ti
	case authRecoveryNotPaired:
		m.subState = subStatePairingRequired
	}
	m.body.SetYOffset(0)
}

func (m connectingModel) Init() tea.Cmd {
	return nil
}

func (m connectingModel) Update(msg tea.Msg) (connectingModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		return m.handleKey(key)
	}
	if m.subState == subStateAuthTokenPrompt {
		if m.resolving {
			// Submit is in flight; freeze the field.
			return m, nil
		}
		var cmd tea.Cmd
		m.tokenInput, cmd = m.tokenInput.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m connectingModel) handleKey(msg tea.KeyPressMsg) (connectingModel, tea.Cmd) {
	if m.resolving {
		// A recovery choice is already committed and its store+connect
		// round-trip is in flight; ignore further input until the
		// result lands (success transitions away, a repeat failure
		// re-enters via enterAuthModal which clears the flag).
		return m, nil
	}
	switch m.subState {
	case subStatePairingRequired:
		switch msg.String() {
		case "enter", "r":
			b := m.backend
			conn := m.connection
			m.resolving = true
			return m, func() tea.Msg {
				return authResolvedMsg{connection: conn, backend: b}
			}
		case "esc", "q":
			b := m.backend
			conn := m.connection
			return m, func() tea.Msg {
				return authResolvedMsg{connection: conn, backend: b, cancelled: true}
			}
		}
		return m, nil

	case subStateAuthMismatchPrompt:
		switch msg.String() {
		case "1", "enter":
			b := m.backend
			conn := m.connection
			m.resolving = true
			return m, func() tea.Msg {
				if auth, ok := b.(backend.DeviceTokenAuth); ok {
					if err := auth.ClearToken(); err != nil {
						return connectResultMsg{connection: conn, err: fmt.Errorf("clear token: %w", err)}
					}
				}
				return authResolvedMsg{connection: conn, backend: b}
			}
		case "2":
			b := m.backend
			conn := m.connection
			m.resolving = true
			return m, func() tea.Msg {
				if auth, ok := b.(backend.DeviceTokenAuth); ok {
					if err := auth.ResetIdentity(); err != nil {
						return connectResultMsg{connection: conn, err: fmt.Errorf("reset identity: %w", err)}
					}
				}
				return authResolvedMsg{connection: conn, backend: b}
			}
		case "esc", "3", "q":
			b := m.backend
			conn := m.connection
			return m, func() tea.Msg {
				return authResolvedMsg{connection: conn, backend: b, cancelled: true}
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
			b := m.backend
			conn := m.connection
			need := m.authNeed
			m.resolving = true
			return m, func() tea.Msg {
				// Dispatch on the modal's recovery type rather than
				// on backend interface assertion: a single backend
				// can implement both DeviceTokenAuth and APIKeyAuth
				// (the OpenClaw fake does), and a type-switch would
				// always pick the first arm regardless of which
				// flow the user is actually in.
				switch need {
				case authRecoveryAPIKey:
					if auth, ok := b.(backend.APIKeyAuth); ok {
						if err := auth.StoreAPIKey(token); err != nil {
							return connectResultMsg{connection: conn, err: fmt.Errorf("store api key: %w", err)}
						}
					}
				default:
					if auth, ok := b.(backend.DeviceTokenAuth); ok {
						if err := auth.StoreToken(token); err != nil {
							return connectResultMsg{connection: conn, err: fmt.Errorf("store token: %w", err)}
						}
					}
				}
				return authResolvedMsg{connection: conn, backend: b}
			}
		case "esc":
			b := m.backend
			conn := m.connection
			return m, func() tea.Msg {
				return authResolvedMsg{connection: conn, backend: b, cancelled: true}
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
	case subStatePairingRequired:
		return []Action{
			{ID: "pairing-retry", Label: "Retry", Key: "enter"},
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
	case "pairing-retry":
		return m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	m.sizeModalBody()
}

// sizeModalBody sizes the body viewport used by the three auth modals
// so the title and help/footer line stay pinned and the body scrolls
// when the terminal is too short to fit the full text.
func (m *connectingModel) sizeModalBody() {
	const titleLines = 3
	const footerLines = 2
	const minBodyHeight = 5

	bodyH := m.height - titleLines - footerLines
	if bodyH < minBodyHeight {
		bodyH = minBodyHeight
	}
	bodyW := m.width
	if bodyW < 1 {
		bodyW = 1
	}
	m.body.SetWidth(bodyW)
	m.body.SetHeight(bodyH)
}

// scrollBodyTo nudges the body viewport so the line range [start, end]
// stays within view; used by viewAuthToken to keep the token textinput
// reachable on short terminals.
func (m *connectingModel) scrollBodyTo(start, end, totalLines int) {
	height := m.body.Height()
	if height <= 0 || totalLines == 0 {
		return
	}
	offset := m.body.YOffset()
	switch {
	case start < offset:
		offset = start
	case end >= offset+height:
		offset = end - height + 1
	}
	if offset < 0 {
		offset = 0
	}
	maxOffset := totalLines - height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	m.body.SetYOffset(offset)
}

func (m connectingModel) View() string {
	switch m.subState {
	case subStateAuthMismatchPrompt:
		return m.viewAuthMismatch()
	case subStateAuthTokenPrompt:
		return m.viewAuthToken()
	case subStatePairingRequired:
		return m.viewPairingRequired()
	}
	name := "gateway"
	if m.connection != nil {
		name = m.connection.Name
	}
	return fmt.Sprintf("\n  Connecting to %s...\n", name)
}

func (m connectingModel) viewAuthMismatch() string {
	titleLine := "\n" + headerStyle.Render(" Auth recovery ") + "\n"

	var body strings.Builder
	lineCount := 0
	writeLine := func(s string) {
		body.WriteString(s)
		body.WriteString("\n")
		lineCount += strings.Count(s, "\n") + 1
	}
	if m.authErr != nil {
		writeLine(errorStyle.Render(fmt.Sprintf("  %v", m.authErr)))
		writeLine("")
	}
	writeLine("  The stored device token was rejected by the gateway.")
	writeLine("")
	writeLine("  1) Clear stored token and retry  (recommended)")
	writeLine("  2) Reset full identity and retry")
	writeLine("  Esc) Cancel")

	if m.height <= 0 {
		return titleLine + body.String()
	}
	m.body.SetContent(body.String())
	return lipgloss.JoinVertical(lipgloss.Left, titleLine, m.body.View())
}

func (m connectingModel) viewPairingRequired() string {
	titleLine := "\n" + headerStyle.Render(" Pairing required ") + "\n"

	var body strings.Builder
	lineCount := 0
	writeLine := func(s string) {
		body.WriteString(s)
		body.WriteString("\n")
		lineCount += strings.Count(s, "\n") + 1
	}
	if m.authErr != nil {
		wrapped := wordWrap(m.authErr.Error(), max(m.width-2, 20))
		writeLine("  " + errorStyle.Render(indentMultiline(wrapped, "  ")))
		writeLine("")
	}
	writeLine("  This device hasn't been paired with the gateway yet.")
	writeLine("  An administrator must approve it before you can connect.")
	writeLine("")
	writeLine("  On the gateway host, run:")
	writeLine("")
	writeLine("    openclaw devices approve --latest")
	writeLine("")
	writeLine("  This previews the pending request and prints the exact")
	writeLine("  approve command. Run that command, then press Enter to retry.")

	footer := helpStyle.Render("  Enter: retry | Esc: cancel")
	if m.resolving {
		footer = statusStyle.Render("  Retrying...")
	}

	if m.height <= 0 {
		return titleLine + body.String() + "\n" + footer
	}
	m.body.SetContent(body.String())
	return lipgloss.JoinVertical(lipgloss.Left, titleLine, m.body.View(), footer)
}

func (m connectingModel) viewAuthToken() string {
	titleLine := "\n" + headerStyle.Render(" Auth token required ") + "\n"

	var body strings.Builder
	lineCount := 0
	writeLine := func(s string) {
		body.WriteString(s)
		body.WriteString("\n")
		lineCount += strings.Count(s, "\n") + 1
	}
	if m.authErr != nil {
		writeLine(errorStyle.Render(fmt.Sprintf("  %v", m.authErr)))
		writeLine("")
	}
	writeLine("  This gateway requires a pre-shared auth token.")
	writeLine("  Ask your gateway operator if you don't have one.")
	writeLine("")
	writeLine("  Token:")
	tokenStart := lineCount
	writeLine("  " + m.tokenInput.View())
	tokenEnd := lineCount - 1

	footer := helpStyle.Render("  Enter: submit | Esc: cancel")
	if m.resolving {
		footer = statusStyle.Render("  Submitting...")
	}

	if m.height <= 0 {
		return titleLine + body.String() + "\n" + footer
	}
	m.body.SetContent(body.String())
	m.scrollBodyTo(tokenStart, tokenEnd, lineCount)
	return lipgloss.JoinVertical(lipgloss.Left, titleLine, m.body.View(), footer)
}
