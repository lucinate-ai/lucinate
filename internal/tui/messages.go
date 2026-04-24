package tui

import (
	"github.com/a3tai/openclaw-go/protocol"

	"github.com/outofcoffee/repclaw/internal/config"
)

// chatMessage represents a single message in the conversation.
type chatMessage struct {
	role      string // "user", "assistant", "system", or "separator"
	content   string
	streaming bool
	errMsg    string
	rendered  bool // true if content has been glamour-rendered (contains ANSI codes)
}

// sessionStats holds token usage stats for display.
type sessionStats struct {
	inputTokens       int
	outputTokens      int
	cacheRead         int
	cacheWrite        int
	totalCost         float64
	inputCost         float64
	outputCost        float64
	cacheReadCost     float64
	cacheWriteCost    float64
	totalMessages     int
	userMessages      int
	assistantMessages int
}

// historyLoadedMsg is returned when chat history is fetched.
type historyLoadedMsg struct {
	messages []chatMessage
	err      error
}

// historyRefreshMsg replaces all messages with fresh history after a response completes.
type historyRefreshMsg struct {
	messages []chatMessage
	err      error
}

// chatSentMsg is returned after a message is sent.
type chatSentMsg struct {
	err error
}

// statsLoadedMsg is returned when session usage stats are fetched.
type statsLoadedMsg struct {
	stats *sessionStats
	err   error
}

// GatewayEventMsg wraps a gateway event for the bubbletea loop.
// Exported so main.go can send events via p.Send().
type GatewayEventMsg protocol.Event

// goBackMsg signals the AppModel to return to agent selection.
type goBackMsg struct{}

// modelListMsg is returned when the model list is fetched.
type modelListMsg struct {
	models []protocol.ModelChoice
	err    error
}

// modelSwitchedMsg is returned after switching models.
type modelSwitchedMsg struct {
	modelID string
	err     error
}

// execSubmittedMsg signals the exec request was submitted (output comes via events).
type execSubmittedMsg struct {
	err error
}

// localExecFinishedMsg carries the result of a locally executed command.
type localExecFinishedMsg struct {
	output   string
	exitCode int
	err      error
}

// agentCreatedMsg is returned when an agent is created via the API.
type agentCreatedMsg struct {
	name string
	err  error
}

// sessionCreatedMsg is returned when a session is created for a non-default agent.
type sessionCreatedMsg struct {
	sessionKey string
	agentID    string
	agentName  string
	modelID    string
	err        error
}

// skillsDiscoveredMsg is returned when skill discovery completes.
type skillsDiscoveredMsg struct {
	skills []agentSkill
}

// showSessionsMsg signals the AppModel to switch to the session browser.
type showSessionsMsg struct {
	agentID   string
	agentName string
	modelID   string
	mainKey   string
}

// sessionSelectedMsg is returned when the user picks a session to restore.
type sessionSelectedMsg struct {
	sessionKey string
	agentName  string
	modelID    string
}

// goBackFromSessionsMsg signals the AppModel to return to the chat view.
type goBackFromSessionsMsg struct{}

// sessionsLoadedMsg is returned when the session list is fetched.
type sessionsLoadedMsg struct {
	sessions []sessionItem
	err      error
}

// newSessionCreatedMsg is returned when a new session is created from the browser.
type newSessionCreatedMsg struct {
	sessionKey string
	agentName  string
	modelID    string
	err        error
}

// showConfigMsg signals the AppModel to switch to the config view.
type showConfigMsg struct{}

// goBackFromConfigMsg signals the AppModel to return to the chat view from config.
type goBackFromConfigMsg struct{}

// prefsUpdatedMsg carries updated preferences after a config toggle.
type prefsUpdatedMsg struct {
	prefs config.Preferences
}

// thinkingChangedMsg is returned after changing the thinking level.
type thinkingChangedMsg struct {
	level string
	err   error
}

// sessionCompactedMsg is returned after compacting a session.
type sessionCompactedMsg struct{ err error }

// sessionClearedMsg is returned after clearing (deleting) a session.
type sessionClearedMsg struct {
	err           error
	newSessionKey string
}

// spinnerTickMsg advances the streaming-response placeholder animation.
type spinnerTickMsg struct{}
