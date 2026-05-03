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

	// connectErr, when set, is returned from Connect so tests can
	// drive the connecting view's auth-modal recovery branches by
	// pre-seeding "gateway token mismatch" / "gateway token missing"
	// / "api key required" text.
	connectErr error

	// Recorded auth-modal calls — exposed to tests so they can assert
	// the right path was taken (clear vs reset vs store).
	storedToken    string
	storedAPIKey   string
	clearedToken   bool
	resetIdentity  bool

	// Recorded DeleteAgent calls so tests can assert keep-files /
	// delete-files routing.
	deletedAgents []backend.DeleteAgentParams
	deleteAgentErr error

	// createSessionHook, when non-nil, replaces the default
	// CreateSession behaviour so tests can drive timeout / error
	// paths without inventing a separate fake.
	createSessionHook func(ctx context.Context, agentID, key string) (string, error)

	// Cron RPC seams — tests pre-seed jobs / runs / errors and read
	// back the recorded calls through these fields.
	cronJobs        []protocol.CronJob
	cronRuns        []protocol.CronRunLogEntry
	cronListErr     error
	cronRunsErr     error
	lastCronAdd       *protocol.CronAddParams
	lastCronUpdate    *protocol.CronUpdateParams
	lastCronUpdateRaw map[string]any
	lastCronUpdateID  string
	lastCronRunID     string
	lastCronRunForce  bool
	cronRemoved       []string
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		events: make(chan protocol.Event, 1),
		caps: backend.Capabilities{
			GatewayStatus:   true,
			RemoteExec:      true,
			SessionCompact:  true,
			Thinking:        true,
			SessionUsage:    true,
			AuthRecovery:    backend.AuthRecoveryDeviceToken,
			AgentWorkspace:  true,
			AgentManagement: true,
			Cron:            true,
		},
	}
}

func (f *fakeBackend) Connect(ctx context.Context) error  { return f.connectErr }
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
func (f *fakeBackend) DeleteAgent(ctx context.Context, params backend.DeleteAgentParams) error {
	f.deletedAgents = append(f.deletedAgents, params)
	return f.deleteAgentErr
}
func (f *fakeBackend) SessionsList(ctx context.Context, agentID string) (json.RawMessage, error) {
	return json.RawMessage(`{"sessions":[]}`), nil
}
func (f *fakeBackend) CreateSession(ctx context.Context, agentID, key string) (string, error) {
	if f.createSessionHook != nil {
		return f.createSessionHook(ctx, agentID, key)
	}
	return key, nil
}
func (f *fakeBackend) SessionDelete(ctx context.Context, sessionKey string) error { return nil }
func (f *fakeBackend) ChatSend(ctx context.Context, sessionKey string, params backend.ChatSendParams) (*protocol.ChatSendResult, error) {
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

func (f *fakeBackend) StoreToken(token string) error { f.storedToken = token; return nil }
func (f *fakeBackend) ClearToken() error             { f.clearedToken = true; return nil }
func (f *fakeBackend) ResetIdentity() error          { f.resetIdentity = true; return nil }

// --- APIKeyAuth ---
//
// fakeBackend implements both DeviceTokenAuth and APIKeyAuth so a
// single fake can drive either modal flow; tests opt in by setting
// caps.AuthRecovery.

func (f *fakeBackend) StoreAPIKey(key string) error { f.storedAPIKey = key; return nil }

// --- CronBackend ---

func (f *fakeBackend) CronsList(ctx context.Context, params protocol.CronListParams) (*protocol.CronListResult, error) {
	if f.cronListErr != nil {
		return nil, f.cronListErr
	}
	return &protocol.CronListResult{Jobs: f.cronJobs, Total: len(f.cronJobs)}, nil
}

func (f *fakeBackend) CronRuns(ctx context.Context, params protocol.CronRunsParams) (*protocol.CronRunsResult, error) {
	if f.cronRunsErr != nil {
		return nil, f.cronRunsErr
	}
	return &protocol.CronRunsResult{Entries: f.cronRuns, Total: len(f.cronRuns)}, nil
}

func (f *fakeBackend) CronAdd(ctx context.Context, params protocol.CronAddParams) (json.RawMessage, error) {
	p := params
	f.lastCronAdd = &p
	return json.RawMessage(`{"id":"new-job"}`), nil
}

func (f *fakeBackend) CronUpdate(ctx context.Context, params protocol.CronUpdateParams) error {
	p := params
	f.lastCronUpdate = &p
	return nil
}

func (f *fakeBackend) CronUpdateRaw(ctx context.Context, jobID string, patch map[string]any) error {
	f.lastCronUpdateID = jobID
	f.lastCronUpdateRaw = patch
	return nil
}

func (f *fakeBackend) CronRemove(ctx context.Context, jobID string) error {
	f.cronRemoved = append(f.cronRemoved, jobID)
	return nil
}

func (f *fakeBackend) CronRun(ctx context.Context, jobID string, force bool) error {
	f.lastCronRunID = jobID
	f.lastCronRunForce = force
	return nil
}

// Compile-time assertions.
var (
	_ backend.Backend         = (*fakeBackend)(nil)
	_ backend.StatusBackend   = (*fakeBackend)(nil)
	_ backend.ExecBackend     = (*fakeBackend)(nil)
	_ backend.CompactBackend  = (*fakeBackend)(nil)
	_ backend.ThinkingBackend = (*fakeBackend)(nil)
	_ backend.UsageBackend    = (*fakeBackend)(nil)
	_ backend.DeviceTokenAuth = (*fakeBackend)(nil)
	_ backend.APIKeyAuth      = (*fakeBackend)(nil)
	_ backend.CronBackend     = (*fakeBackend)(nil)
)
