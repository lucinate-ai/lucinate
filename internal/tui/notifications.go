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

// renderNotifications returns the styled, multi-line block of pending
// notifications, sized to width, or "" when there are none. Each row
// uses statusStyle (or errorStyle for is-error rows) and is padded to
// the chat width so the styling reads as a coherent band above the
// input.
func (m *chatModel) renderNotifications() string {
	if len(m.notifications) == 0 {
		return ""
	}
	rows := make([]string, 0, len(m.notifications))
	for _, n := range m.notifications {
		style := statusStyle
		if n.isError {
			style = errorStyle
		}
		rows = append(rows, style.Width(m.width).Render(" "+n.text))
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}
