// Package openclaw adapts the existing OpenClaw gateway client to the
// backend.Backend interface. The adapter is a thin pass-through —
// every TUI call site that used to hold a *client.Client now holds a
// backend.Backend, and the OpenClaw concrete type is recovered via
// type assertion when the TUI needs gateway-only affordances
// (/status, !!, /compact, /think, /stats, device-token auth recovery).
package openclaw

import (
	"context"
	"encoding/json"

	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/client"
)

// Backend wraps a *client.Client. Constructed by the embedder's
// BackendFactory when the picked connection has type=openclaw.
type Backend struct {
	client *client.Client
}

// New wraps the given client. The caller still owns Connect / Close
// indirectly — both are forwarded through the Backend interface so
// the connection driver in app/app.go can drive the lifecycle without
// caring about the concrete type.
func New(c *client.Client) *Backend {
	return &Backend{client: c}
}

// Client exposes the underlying gateway client for tests and for the
// few places (auth-modal sub-state, TUI capability assertions) that
// still need the OpenClaw-specific surface.
func (b *Backend) Client() *client.Client { return b.client }

func (b *Backend) Connect(ctx context.Context) error { return b.client.Connect(ctx) }
func (b *Backend) Close() error                      { return b.client.Close() }
func (b *Backend) Events() <-chan protocol.Event     { return b.client.Events() }

func (b *Backend) Supervise(ctx context.Context, notify func(client.ConnState)) {
	b.client.Supervise(ctx, notify)
}

func (b *Backend) ListAgents(ctx context.Context) (*protocol.AgentsListResult, error) {
	return b.client.ListAgents(ctx)
}

func (b *Backend) CreateAgent(ctx context.Context, params backend.CreateAgentParams) error {
	return b.client.CreateAgent(ctx, params.Name, params.Workspace)
}

func (b *Backend) SessionsList(ctx context.Context, agentID string) (json.RawMessage, error) {
	return b.client.SessionsList(ctx, agentID)
}

func (b *Backend) CreateSession(ctx context.Context, agentID, key string) (string, error) {
	return b.client.CreateSession(ctx, agentID, key)
}

func (b *Backend) SessionDelete(ctx context.Context, sessionKey string) error {
	return b.client.SessionDelete(ctx, sessionKey)
}

func (b *Backend) ChatSend(ctx context.Context, sessionKey, message, idemKey string) (*protocol.ChatSendResult, error) {
	return b.client.ChatSend(ctx, sessionKey, message, idemKey)
}

func (b *Backend) ChatAbort(ctx context.Context, sessionKey, runID string) error {
	return b.client.ChatAbort(ctx, sessionKey, runID)
}

func (b *Backend) ChatHistory(ctx context.Context, sessionKey string, limit int) (json.RawMessage, error) {
	return b.client.ChatHistory(ctx, sessionKey, limit)
}

func (b *Backend) ModelsList(ctx context.Context) (*protocol.ModelsListResult, error) {
	return b.client.ModelsList(ctx)
}

func (b *Backend) SessionPatchModel(ctx context.Context, sessionKey, modelID string) error {
	return b.client.SessionPatchModel(ctx, sessionKey, modelID)
}

// Capabilities reports the full OpenClaw capability surface — every
// optional sub-interface is implemented below.
func (b *Backend) Capabilities() backend.Capabilities {
	return backend.Capabilities{
		GatewayStatus:  true,
		RemoteExec:     true,
		SessionCompact: true,
		Thinking:       true,
		SessionUsage:   true,
		AuthRecovery:   backend.AuthRecoveryDeviceToken,
	}
}

// --- StatusBackend ---

func (b *Backend) GatewayHealth(ctx context.Context) (*protocol.HealthEvent, error) {
	return b.client.GatewayHealth(ctx)
}

func (b *Backend) HelloUptimeMs() int64 { return b.client.HelloUptimeMs() }

// --- ExecBackend ---

func (b *Backend) ExecRequest(ctx context.Context, command, sessionKey string) (*protocol.ExecApprovalRequestResult, error) {
	return b.client.ExecRequest(ctx, command, sessionKey)
}

func (b *Backend) ExecResolve(ctx context.Context, id, decision string) (*protocol.ExecApprovalResolveResult, error) {
	return b.client.ExecResolve(ctx, id, decision)
}

// --- CompactBackend ---

func (b *Backend) SessionCompact(ctx context.Context, sessionKey string) error {
	return b.client.SessionCompact(ctx, sessionKey)
}

// --- ThinkingBackend ---

func (b *Backend) SessionPatchThinking(ctx context.Context, sessionKey, level string) error {
	return b.client.SessionPatchThinking(ctx, sessionKey, level)
}

// --- UsageBackend ---

func (b *Backend) SessionUsage(ctx context.Context, sessionKey string) (json.RawMessage, error) {
	return b.client.SessionUsage(ctx, sessionKey)
}

// --- DeviceTokenAuth ---

func (b *Backend) StoreToken(token string) error { return b.client.StoreToken(token) }
func (b *Backend) ClearToken() error             { return b.client.ClearToken() }
func (b *Backend) ResetIdentity() error          { return b.client.ResetIdentity() }

// Compile-time assertions that the wrapper implements every interface
// it claims to.
var (
	_ backend.Backend         = (*Backend)(nil)
	_ backend.StatusBackend   = (*Backend)(nil)
	_ backend.ExecBackend     = (*Backend)(nil)
	_ backend.CompactBackend  = (*Backend)(nil)
	_ backend.ThinkingBackend = (*Backend)(nil)
	_ backend.UsageBackend    = (*Backend)(nil)
	_ backend.DeviceTokenAuth = (*Backend)(nil)
)
