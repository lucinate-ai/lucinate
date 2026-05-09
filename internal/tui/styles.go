package tui

import "charm.land/lipgloss/v2"

var (
	// Colours — using dark theme values.
	subtle  = lipgloss.Color("#5C5C5C")
	accent  = lipgloss.Color("#AD8CFF")
	userClr = lipgloss.Color("#48CAE4")
	errClr      = lipgloss.Color("#FF6B6B")
	execClr     = lipgloss.Color("#FFB74D")
	localExcClr = lipgloss.Color("#66BB6A")

	// Header bar.
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(accent).
			Padding(0, 1)

	// User message prefix.
	userPrefixStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(userClr)

	// Assistant message prefix.
	assistantPrefixStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(accent)

	// Streaming cursor.
	cursorStyle = lipgloss.NewStyle().
			Foreground(accent).
			Bold(true)

	// Input area border.
	inputBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(subtle).
				Padding(0, 1)

	// Thinking content body (reasoning blocks from the model).
	thinkingBodyStyle = lipgloss.NewStyle().
			Foreground(subtle)

	// Status / info text.
	statusStyle = lipgloss.NewStyle().
			Foreground(subtle)

	// Empty-history placeholder — brighter than statusStyle so it stands
	// out against the empty conversation pane.
	emptyHistoryStyle = lipgloss.NewStyle().
				Foreground(accent).
				Italic(true)

	// Error text.
	errorStyle = lipgloss.NewStyle().
			Foreground(errClr).
			Bold(true)

	// Connection-status badge styles, sized to read against the purple
	// header background where the badge is rendered.
	headerBadgeWarnStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#1A0033")).
				Background(accent)
	headerBadgeErrStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(lipgloss.Color("#B00020")).
				Padding(0, 1)

	// Input area border for exec mode.
	execBorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(execClr).
			Padding(0, 1)

	// Exec command prefix style (remote).
	execPrefixStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(execClr)

	// Input area border for local exec mode.
	localExecBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(localExcClr).
				Padding(0, 1)

	// Local exec command prefix style.
	localExecPrefixStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(localExcClr)

	// Pending (queued) message prefix — dimmed, italic shadow of the user style.
	pendingPrefixStyle = lipgloss.NewStyle().
				Italic(true).
				Faint(true).
				Foreground(userClr)

	// Pending (queued) message body — dimmed italic to match prefix.
	pendingBodyStyle = lipgloss.NewStyle().
				Italic(true).
				Faint(true)

	// Help text.
	helpStyle = lipgloss.NewStyle().
			Foreground(subtle)

	// Completion-menu highlight — applied to the row at the current
	// cycle index when Tab is cycling through candidates.
	completionMenuHighlightStyle = lipgloss.NewStyle().
					Foreground(accent).
					Bold(true)

	// Connection status bar — a thin dim row above non-chat views
	// telling the user which connection is in scope. The chat view
	// folds the same info into its own header bar.
	connBannerStyle = lipgloss.NewStyle().
			Foreground(subtle)

	// Tool-card styles. Running reuses the muted status colour; success
	// borrows the local-exec green so the visual language matches "this
	// finished cleanly"; error falls through to errorStyle.
	toolRunningStyle = lipgloss.NewStyle().
				Foreground(subtle)
	toolSuccessStyle = lipgloss.NewStyle().
				Foreground(localExcClr)
	toolNameStyle = lipgloss.NewStyle().
			Bold(true)
)
