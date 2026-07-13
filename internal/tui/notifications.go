package tui

import "charm.land/lipgloss/v2"

// notification is a single ephemeral row rendered above the input box.
// The text is shown verbatim with statusStyle (or errorStyle when isError
// is true). Notifications survive history refreshes (because they live
// outside m.messages) and are cleared when the user submits an input —
// the working assumption is that any state worth showing in a
// notification has been read or no longer applies once the user types
// their next message.
type notification struct {
	text    string
	isError bool
}

// notify queues an informational notification.
func (m *chatModel) notify(text string) {
	if text == "" {
		return
	}
	m.notifications = append(m.notifications, notification{text: text})
	m.applyLayout()
}

// notifyError queues an error-styled notification.
func (m *chatModel) notifyError(text string) {
	if text == "" {
		return
	}
	m.notifications = append(m.notifications, notification{text: text, isError: true})
	m.applyLayout()
}

// clearNotifications drops every pending notification. Called when the
// user submits an input.
func (m *chatModel) clearNotifications() {
	if len(m.notifications) == 0 {
		return
	}
	m.notifications = nil
	m.applyLayout()
}

// renderInfoNotifications returns the styled block of pending informational
// notifications (e.g. "copied … to clipboard"), or "" when there are none.
// These render at the top of the view, just below the header — see the View
// layout in chat.go for the full region order.
func (m *chatModel) renderInfoNotifications() string {
	return m.renderNotificationsMatching(false)
}

// renderErrorNotifications returns the styled block of pending error
// notifications, or "" when there are none. These render at the very bottom,
// below the queued-message footer and directly above the input, so a failure
// surfaces next to where the user is about to act on it.
func (m *chatModel) renderErrorNotifications() string {
	return m.renderNotificationsMatching(true)
}

// renderNotificationsMatching returns the styled, multi-line block of pending
// notifications whose isError flag equals wantError, sized to width, or ""
// when none match. Each row uses statusStyle (or errorStyle for is-error
// rows) and is padded to the chat width so the styling reads as a coherent
// band. Info and error notifications render in separate regions (top vs
// bottom), so they are filtered rather than rendered as one block.
func (m *chatModel) renderNotificationsMatching(wantError bool) string {
	rows := make([]string, 0, len(m.notifications))
	for _, n := range m.notifications {
		if n.isError != wantError {
			continue
		}
		style := statusStyle
		if n.isError {
			style = errorStyle
		}
		rows = append(rows, style.Width(m.width).Render(" "+n.text))
	}
	if len(rows) == 0 {
		return ""
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}
