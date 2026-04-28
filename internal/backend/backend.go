// Package backend defines the abstraction the TUI uses to talk to a
// chat service. The first implementation wraps the OpenClaw gateway
// client (internal/backend/openclaw); the second implementation
// targets OpenAI-compatible HTTP servers (internal/backend/openai),
// translating /v1/chat/completions SSE into the same event shape so
// the chat view does not need to know which backend it is talking to.
//
// The lingua franca is github.com/a3tai/openclaw-go/protocol —
// AgentSummary, ChatEvent, etc. All backends translate their native
// protocol into these types. Capabilities not exposed by every
// backend (gateway health, remote exec, server-side compact, thinking
// level, server-side usage) are surfaced through optional sub-
// interfaces (StatusBackend, ExecBackend, ...) the TUI type-asserts
// against; missing capabilities degrade gracefully with a clear
// "not available on this connection" message.
package backend

import (
	"context"
	"encoding/json"

	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/client"
)

// Backend is the core surface every chat service must implement.
//
// Connect/Close own the network lifecycle; the TUI's connection
// driver pumps Events and runs Supervise. Agents, sessions, and chat
// turns map 1:1 to OpenClaw concepts. ModelsList and SessionPatchModel
// drive the /model command. Backends that don't track usage / status /
// thinking / exec implement only the optional sub-interfaces.
type Backend interface {
	// Connect establishes the underlying transport. The TUI calls
	// this from the connecting view; auth-recovery modals route
	// through the AuthRecovery sub-interfaces.
	Connect(ctx context.Context) error

	// Close tears down the transport. Called by the connection
	// driver when the program exits or the user switches connection.
	Close() error

	// Events returns a channel of gateway events. The chat view
	// consumes protocol.ChatEvent (delta/final/error/aborted) and
	// the exec helpers consume protocol.ExecFinished /
	// protocol.ExecApprovalResolvedEvent / protocol.ExecDenied.
	// Backends without exec semantics simply never emit those event
	// names.
	Events() <-chan protocol.Event

	// Supervise watches reconnect state. Backends without a
	// long-lived transport (HTTP-only) emit a single "connected"
	// transition and then block until ctx is cancelled.
	Supervise(ctx context.Context, notify func(client.ConnState))

	// ListAgents returns the configured agents. For local-agent
	// backends the result is built from the per-connection store on
	// disk.
	ListAgents(ctx context.Context) (*protocol.AgentsListResult, error)

	// CreateAgent provisions a new agent. The fields used vary by
	// backend type: OpenClaw uses Name+Workspace; OpenAI-compat uses
	// Name+Identity+Soul+Model. Backends ignore fields they don't
	// understand. The TUI's create-agent form switches its rendered
	// fields based on the connection type to populate this struct
	// correctly.
	CreateAgent(ctx context.Context, params CreateAgentParams) error

	// SessionsList returns the raw protocol.SessionsListResult JSON
	// for the given agent so the existing TUI session browser keeps
	// working unchanged.
	SessionsList(ctx context.Context, agentID string) (json.RawMessage, error)

	// CreateSession creates or resumes a session and returns the
	// resolved key. For agent ≡ session 1:1 backends the key equals
	// the agent ID.
	CreateSession(ctx context.Context, agentID, key string) (string, error)

	// SessionDelete removes a session and its transcript.
	SessionDelete(ctx context.Context, sessionKey string) error

	// ChatSend posts a user message and returns the run ID. Streaming
	// deltas arrive on Events. The Skills field of params is the
	// catalog the model should be aware of for this session;
	// backends present it in whatever shape their wire protocol
	// expects (OpenClaw gets System:-prefixed lines on the first
	// turn so the gateway can lift them into a system block; OpenAI
	// gets a real role:system message rebuilt each turn).
	ChatSend(ctx context.Context, sessionKey string, params ChatSendParams) (*protocol.ChatSendResult, error)

	// ChatAbort cancels an in-flight run.
	ChatAbort(ctx context.Context, sessionKey, runID string) error

	// ChatHistory returns recent messages for the session in the
	// gateway's protocol.ChatHistoryResult JSON shape.
	ChatHistory(ctx context.Context, sessionKey string, limit int) (json.RawMessage, error)

	// ModelsList returns the available models for the /model picker.
	ModelsList(ctx context.Context) (*protocol.ModelsListResult, error)

	// SessionPatchModel switches the model for a session.
	SessionPatchModel(ctx context.Context, sessionKey, modelID string) error

	// Capabilities reports which optional features this backend
	// supports. The TUI uses this to gate menu items and slash
	// commands so unsupported features render a clear status message
	// instead of a confusing error.
	Capabilities() Capabilities
}

// ChatSendParams bundles the variable inputs to a chat turn so the
// signature stays stable as new fields appear (skills today, tool
// schemas tomorrow). The chat layer fills these — backends decide
// how to render them on the wire.
type ChatSendParams struct {
	// Message is the user's typed prompt, exactly as it should be
	// stored in the visible transcript. The chat layer never wraps
	// it with System:-prefix kludges any more; that's a backend
	// concern.
	Message string

	// IdempotencyKey is forwarded to backends that dedupe on the
	// wire (OpenClaw). Backends that don't need it ignore the
	// field.
	IdempotencyKey string

	// Skills is the local skill catalog the model should know
	// about. Empty when no skills were discovered. Backends format
	// it appropriately — see the Backend.ChatSend doc comment.
	Skills []SkillCatalogEntry
}

// SkillCatalogEntry is one entry in the skill catalog presented to
// the model as a list of available capabilities.
type SkillCatalogEntry struct {
	Name        string
	Description string
}

// CreateAgentParams is the kitchen-sink parameter struct for
// Backend.CreateAgent. Fields are optional per backend type — the
// TUI populates the subset relevant to the active connection.
type CreateAgentParams struct {
	// Name is the human-friendly label for the agent. Required for
	// every backend.
	Name string

	// Workspace is the OpenClaw agent workspace filesystem path.
	// OpenClaw-only.
	Workspace string

	// Identity is the IDENTITY.md body for local-agent backends —
	// who the agent is, role, persona. OpenAI-compat.
	Identity string

	// Soul is the SOUL.md body for local-agent backends — tone,
	// values, working style. OpenAI-compat.
	Soul string

	// Model is the model ID the new agent should default to.
	// OpenAI-compat (driven by /v1/models discovery on the connection).
	Model string
}

// Capabilities flags which optional sub-interfaces a backend
// implements. The TUI checks these before exposing /status, /compact,
// /think, /stats, and the !! remote exec affordance.
type Capabilities struct {
	GatewayStatus  bool
	RemoteExec     bool
	SessionCompact bool
	Thinking       bool
	SessionUsage   bool
	AuthRecovery   AuthRecovery

	// AgentWorkspace indicates the backend's CreateAgent uses the
	// Workspace field of CreateAgentParams (filesystem path the
	// agent operates in). The TUI's create-agent form renders the
	// workspace field only for backends that opt in. Local-agent
	// backends (OpenAI-compat) leave this false because Identity /
	// Soul / Model carry the agent's configuration instead.
	AgentWorkspace bool
}

// AuthRecovery selects the auth-modal flow the connecting view runs
// when a connect attempt returns a recoverable auth error.
type AuthRecovery int

const (
	// AuthRecoveryNone disables the modal — auth errors bounce the
	// user back to the connections picker with an error banner.
	AuthRecoveryNone AuthRecovery = iota

	// AuthRecoveryDeviceToken is the OpenClaw flow: clear stored
	// token / reset identity / store a pre-shared token.
	AuthRecoveryDeviceToken

	// AuthRecoveryAPIKey is the OpenAI-compat flow: prompt for an
	// API key on 401/403.
	AuthRecoveryAPIKey
)

// StatusBackend exposes the gateway health endpoint behind /status.
type StatusBackend interface {
	GatewayHealth(ctx context.Context) (*protocol.HealthEvent, error)
	HelloUptimeMs() int64
}

// ExecBackend exposes the !! remote-exec affordance.
type ExecBackend interface {
	ExecRequest(ctx context.Context, command, sessionKey string) (*protocol.ExecApprovalRequestResult, error)
	ExecResolve(ctx context.Context, id, decision string) (*protocol.ExecApprovalResolveResult, error)
}

// CompactBackend exposes server-side context compaction behind
// /compact. Local-agent backends without server-side compaction omit
// this and the TUI shows a "not available" hint.
type CompactBackend interface {
	SessionCompact(ctx context.Context, sessionKey string) error
}

// ThinkingBackend exposes the per-session thinking-level switch
// behind /think.
type ThinkingBackend interface {
	SessionPatchThinking(ctx context.Context, sessionKey, level string) error
}

// UsageBackend exposes session usage stats behind /stats.
type UsageBackend interface {
	SessionUsage(ctx context.Context, sessionKey string) (json.RawMessage, error)
}

// DeviceTokenAuth is the recovery surface used by OpenClaw when the
// gateway returns a token-mismatch / token-missing error.
type DeviceTokenAuth interface {
	StoreToken(token string) error
	ClearToken() error
	ResetIdentity() error
}

// APIKeyAuth is the recovery surface used by OpenAI-compatible
// backends when the upstream returns 401/403.
type APIKeyAuth interface {
	StoreAPIKey(key string) error
}
