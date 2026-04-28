package tui

import (
	"context"
	"encoding/json"

	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/client"
)

// fakeBackend is a minimal backend.Backend implementation used by the
// command/event unit tests. Every RPC returns zero values; the tests
// only care about the routing the chat-model performs around the
// backend, not what the backend actually does.
//
// fakeBackend deliberately implements every optional sub-interface so
// the slash-command tests for /compact, /think, /status etc. exercise
// the same code paths the OpenClaw backend takes. Backend-specific
// behaviour (e.g. capability gating) is covered by tests that swap in
// a stub implementing only the core Backend surface.
type fakeBackend struct {
	events chan protocol.Event
	caps   backend.Capabilities
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		events: make(chan protocol.Event, 1),
		caps: backend.Capabilities{
			GatewayStatus:  true,
			RemoteExec:     true,
			SessionCompact: true,
			Thinking:       true,
			SessionUsage:   true,
			AuthRecovery:   backend.AuthRecoveryDeviceToken,
		},
	}
}

func (f *fakeBackend) Connect(ctx context.Context) error  { return nil }
func (f *fakeBackend) Close() error                       { return nil }
func (f *fakeBackend) Events() <-chan protocol.Event      { return f.events }
func (f *fakeBackend) Supervise(ctx context.Context, notify func(client.ConnState)) {
	<-ctx.Done()
}

func (f *fakeBackend) ListAgents(ctx context.Context) (*protocol.AgentsListResult, error) {
	return &protocol.AgentsListResult{}, nil
}
func (f *fakeBackend) CreateAgent(ctx context.Context, params backend.CreateAgentParams) error {
	return nil
}
func (f *fakeBackend) SessionsList(ctx context.Context, agentID string) (json.RawMessage, error) {
	return json.RawMessage(`{"sessions":[]}`), nil
}
func (f *fakeBackend) CreateSession(ctx context.Context, agentID, key string) (string, error) {
	return key, nil
}
func (f *fakeBackend) SessionDelete(ctx context.Context, sessionKey string) error { return nil }
func (f *fakeBackend) ChatSend(ctx context.Context, sessionKey, message, idemKey string) (*protocol.ChatSendResult, error) {
	return &protocol.ChatSendResult{}, nil
}
func (f *fakeBackend) ChatAbort(ctx context.Context, sessionKey, runID string) error { return nil }
func (f *fakeBackend) ChatHistory(ctx context.Context, sessionKey string, limit int) (json.RawMessage, error) {
	return json.RawMessage(`{"messages":[]}`), nil
}
func (f *fakeBackend) ModelsList(ctx context.Context) (*protocol.ModelsListResult, error) {
	return &protocol.ModelsListResult{}, nil
}
func (f *fakeBackend) SessionPatchModel(ctx context.Context, sessionKey, modelID string) error {
	return nil
}
func (f *fakeBackend) Capabilities() backend.Capabilities { return f.caps }

// --- StatusBackend ---

func (f *fakeBackend) GatewayHealth(ctx context.Context) (*protocol.HealthEvent, error) {
	return &protocol.HealthEvent{}, nil
}
func (f *fakeBackend) HelloUptimeMs() int64 { return 0 }

// --- ExecBackend ---

func (f *fakeBackend) ExecRequest(ctx context.Context, command, sessionKey string) (*protocol.ExecApprovalRequestResult, error) {
	return &protocol.ExecApprovalRequestResult{}, nil
}
func (f *fakeBackend) ExecResolve(ctx context.Context, id, decision string) (*protocol.ExecApprovalResolveResult, error) {
	return &protocol.ExecApprovalResolveResult{}, nil
}

// --- CompactBackend ---

func (f *fakeBackend) SessionCompact(ctx context.Context, sessionKey string) error { return nil }

// --- ThinkingBackend ---

func (f *fakeBackend) SessionPatchThinking(ctx context.Context, sessionKey, level string) error {
	return nil
}

// --- UsageBackend ---

func (f *fakeBackend) SessionUsage(ctx context.Context, sessionKey string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

// --- DeviceTokenAuth ---

func (f *fakeBackend) StoreToken(token string) error { return nil }
func (f *fakeBackend) ClearToken() error             { return nil }
func (f *fakeBackend) ResetIdentity() error          { return nil }

// Compile-time assertions.
var (
	_ backend.Backend         = (*fakeBackend)(nil)
	_ backend.StatusBackend   = (*fakeBackend)(nil)
	_ backend.ExecBackend     = (*fakeBackend)(nil)
	_ backend.CompactBackend  = (*fakeBackend)(nil)
	_ backend.ThinkingBackend = (*fakeBackend)(nil)
	_ backend.UsageBackend    = (*fakeBackend)(nil)
	_ backend.DeviceTokenAuth = (*fakeBackend)(nil)
)
