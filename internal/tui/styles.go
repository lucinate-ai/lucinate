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

	// Status / info text.
	statusStyle = lipgloss.NewStyle().
			Foreground(subtle)

	// Error text.
	errorStyle = lipgloss.NewStyle().
			Foreground(errClr).
			Bold(true)

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

	// Help text.
	helpStyle = lipgloss.NewStyle().
			Foreground(subtle)
)
