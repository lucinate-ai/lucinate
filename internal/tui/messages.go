package tui

import (
	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// chatMessage represents a single message in the conversation.
type chatMessage struct {
	role          string // "user", "assistant", "system", "separator", or "tool"
	content       string
	raw           string // original markdown source when rendered is true; used to re-render on resize
	thinking      string // reasoning/intermediate thought content from the model
	streaming     bool
	awaitingDelta bool // true for the pre-response spinner placeholder, before any delta arrives
	errMsg        string
	rendered      bool  // true if content has been glamour-rendered (contains ANSI codes)
	timestampMs   int64 // unix millis; only used by "separator" rows to label resume time

	// Tool fields populated only when role == "tool".
	toolName     string
	toolCallID   string
	toolArgsLine string // single-line summary of the tool arguments
	toolState    string // "running", "success", "error"
	toolError    string // human-readable detail when toolState == "error"
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
	runID string
	err   error
}

// chatAbortMsg is returned after a cancel request is sent.
type chatAbortMsg struct {
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

// modelSwitchedMsg is returned after switching models.
type modelSwitchedMsg struct {
	modelID string
	err     error
}

// showModelPickerMsg signals the AppModel to switch to the model picker.
type showModelPickerMsg struct {
	sessionKey     string
	currentModelID string
}

// goBackFromModelPickerMsg signals the AppModel to return to the chat view.
type goBackFromModelPickerMsg struct{}

// modelsLoadedMsg is returned when the model list is fetched for the picker.
type modelsLoadedMsg struct {
	models []protocol.ModelChoice
	err    error
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

// agentSwitchFailedMsg signals that `/agent <name>` could not resolve or
// switch to an agent. Rendered inline in the chat view rather than bouncing
// the user back to the picker, so a typo doesn't lose chat context.
type agentSwitchFailedMsg struct {
	err error
}

// chatAgentNamesLoadedMsg delivers agent display names to the chat model
// for `/agent <TAB>` completion. Empty names slice on backend error is
// acceptable — completion silently degrades.
type chatAgentNamesLoadedMsg struct {
	names []string
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

// sessionSelectedMsg is returned when the user picks a session to
// restore. Most senders (the session browser) leave agentID empty and
// the AppModel falls back to the session view's known agentID;
// crossviews like the cron browser populate it explicitly so the chat
// model is constructed with the right agent context.
type sessionSelectedMsg struct {
	sessionKey string
	agentID    string
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

// gatewayStatusMsg is returned after fetching the gateway health.
type gatewayStatusMsg struct {
	health   *protocol.HealthEvent
	uptimeMs int64
	err      error
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

// connectAttemptMsg requests AppModel to begin a connect attempt for
// the given connection. Used both at startup (Initial connection) and
// when the user picks a connection from the picker.
type connectAttemptMsg struct {
	connection *config.Connection
}

// connectResultMsg carries the outcome of a Connect call. On success
// the new client is published to the app-layer driver via
// onClientChanged and the TUI advances to the agent picker. On
// recoverable auth errors AppModel transitions to an auth-modal
// sub-state. On other errors it returns to the connections picker
// with an error banner.
type connectResultMsg struct {
	connection *config.Connection
	backend    backend.Backend
	authNeed   authRecovery
	err        error
}

// authRecovery describes which auth-modal flow a connect error wants.
// "none" means no recovery offered (a generic error).
type authRecovery int

const (
	authRecoveryNone authRecovery = iota
	authRecoveryTokenMismatch
	authRecoveryTokenMissing
	authRecoveryAPIKey
	authRecoveryNotPaired
)

// authResolvedMsg is sent after the user has resolved an auth-recovery
// modal. The TUI re-runs Connect with whatever state the modal mutated
// (cleared token, reset identity, stored a new pre-shared token).
type authResolvedMsg struct {
	connection *config.Connection
	backend    backend.Backend
	cancelled  bool
}

// showConnectionsMsg signals the AppModel to switch to the connections
// picker (mid-session, via /connections).
type showConnectionsMsg struct{}

// connectionPickedMsg is emitted by the connections picker when the
// user chooses a connection. AppModel turns it into a connectAttemptMsg.
type connectionPickedMsg struct {
	connection *config.Connection
}

// connectionsChangedMsg is emitted by the connections picker after a
// CRUD operation. The TUI uses it to re-render and notify the embedder.
type connectionsChangedMsg struct{}

// ConnStateMsg carries a gateway connection-state transition from the
// reconnect supervisor into the bubbletea event loop. Exported so main.go
// can dispatch it via p.Send().
type ConnStateMsg struct {
	Status  client.ConnStatus
	Attempt int
	Err     error
}

// showCronsMsg signals the AppModel to switch to the cron browser.
// filterAgentID empty means "show jobs across all agents".
type showCronsMsg struct {
	filterAgentID string
	filterLabel   string // "all agents" or the agent's name; rendered in the header
}

// goBackFromCronsMsg signals the AppModel to return from the cron view.
type goBackFromCronsMsg struct{}

// cronsLoadedMsg is returned when the cron job list is fetched.
type cronsLoadedMsg struct {
	jobs []protocol.CronJob
	err  error
}

// cronRunsLoadedMsg is returned when run history for a specific job is fetched.
type cronRunsLoadedMsg struct {
	jobID   string
	entries []protocol.CronRunLogEntry
	err     error
}

// cronJobToggledMsg is returned after flipping a job's enabled flag.
type cronJobToggledMsg struct {
	jobID   string
	enabled bool
	err     error
}

// cronJobRanMsg is returned after a manual run-now request completes.
type cronJobRanMsg struct{ err error }

// cronJobSavedMsg is returned after creating or updating a cron job.
type cronJobSavedMsg struct {
	jobID string
	err   error
}

// cronJobRemovedMsg is returned after deleting a cron job.
type cronJobRemovedMsg struct {
	jobID string
	err   error
}

// updateCheckDoneMsg carries the outcome of the startup update check.
// It always fires when a check runs (so the AppModel can persist the
// new last-check timestamp on the main loop, never racing the config
// view's writer); Newer is true only when the user should see a badge.
type updateCheckDoneMsg struct {
	At         int64  // unix seconds when the check completed
	LatestSeen string // manifest version, or "" when the check returned nothing
	Newer      bool   // true iff badge should appear (also implies caller hasn't already seen Latest)
	URL        string // release URL when Newer is true
}
