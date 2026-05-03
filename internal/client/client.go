package client

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/a3tai/openclaw-go/gateway"
	"github.com/a3tai/openclaw-go/identity"
	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// IdentityStore abstracts persistence of the device keypair and device token.
//
// The default filesystem implementation is provided by the openclaw-go
// identity package (*identity.Store satisfies this interface). Alternative
// implementations can be supplied via NewWithIdentityStore so the gateway
// client logic stays decoupled from any particular storage backend.
type IdentityStore interface {
	LoadOrGenerate() (*identity.Identity, error)
	LoadDeviceToken() string
	SaveDeviceToken(token string) error
	ClearDeviceToken() error
	Reset() error
}

// Client wraps the gateway SDK client and bridges events to a channel
// for consumption by the bubbletea event loop.
type Client struct {
	mu     sync.RWMutex
	gw     *gateway.Client
	events chan protocol.Event
	cfg    *config.Config
	store  IdentityStore

	// connectTimeout, when non-zero, is forwarded to the gateway SDK
	// as WithConnectTimeout so the WebSocket handshake gives up at the
	// user-configured deadline. Applies to both initial Connect and
	// each Reconnect attempt.
	connectTimeout time.Duration
}

// SetConnectTimeout sets the WebSocket connect/handshake deadline used
// for every (re)dial. A zero or negative value lets the SDK use its
// own default. Safe to call before any Connect/Reconnect; the value is
// read on each dial.
func (c *Client) SetConnectTimeout(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d <= 0 {
		c.connectTimeout = 0
		return
	}
	c.connectTimeout = d
}

// New creates a new client from the given config, using the default
// per-endpoint filesystem identity store at ~/.lucinate/identity/<host>/.
func New(cfg *config.Config) (*Client, error) {
	identityDir, err := identityDirForEndpoint(cfg.GatewayURL)
	if err != nil {
		return nil, fmt.Errorf("identity dir: %w", err)
	}

	store, err := identity.NewStore(identityDir)
	if err != nil {
		return nil, fmt.Errorf("identity store: %w", err)
	}

	return NewWithIdentityStore(cfg, store), nil
}

// NewWithIdentityStore creates a new client using a caller-supplied identity
// store. This entry point lets embedders persist the device keypair somewhere
// other than the default filesystem location (for example, in tests, or in
// alternative host environments).
func NewWithIdentityStore(cfg *config.Config, store IdentityStore) *Client {
	return &Client{
		events: make(chan protocol.Event, 256),
		cfg:    cfg,
		store:  store,
	}
}

// identityDirForEndpoint returns a per-endpoint identity directory
// under <data-dir>/identity/<host_port>/. This keeps keys and device
// tokens isolated per gateway so switching endpoints doesn't
// overwrite them.
func identityDirForEndpoint(gatewayURL string) (string, error) {
	root, err := config.DataDir()
	if err != nil {
		return "", fmt.Errorf("data dir: %w", err)
	}

	u, err := url.Parse(gatewayURL)
	if err != nil {
		return "", fmt.Errorf("parse gateway URL: %w", err)
	}

	key := sanitiseHost(u.Host)
	if key == "" {
		return "", fmt.Errorf("gateway URL has no host: %s", gatewayURL)
	}

	return filepath.Join(root, "identity", key), nil
}

// sanitiseHost converts a host or host:port into a filesystem-safe directory
// name.  Colons (from the port separator) are replaced with underscores; any
// other characters that are not alphanumeric, '.', '-', or '_' are dropped.
func sanitiseHost(host string) string {
	host = strings.ReplaceAll(host, ":", "_")
	var b strings.Builder
	for _, r := range host {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Connect establishes a WebSocket connection to the gateway.
func (c *Client) Connect(ctx context.Context) error {
	return c.dial(ctx)
}

// Reconnect tears down the current gateway client and dials a fresh one.
// The events channel is preserved so existing TUI consumers keep working.
func (c *Client) Reconnect(ctx context.Context) error {
	c.mu.Lock()
	old := c.gw
	c.gw = nil
	c.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	return c.dial(ctx)
}

// dial loads identity, builds options, and performs the SDK handshake.
//
// First-time pairing is handled inline: if the bootstrap connect
// presented no device token (the device key signature alone got us
// past the gateway's NOT_PAIRED gate after an admin approval) and the
// gateway issued a fresh token in hello-ok, the bootstrap client is
// closed and a second dial is performed with the new token. The
// OpenClaw protocol expects scoped operations to run on a connection
// that authenticated with the token at connect time; staying on the
// bootstrap connection causes subsequent RPCs (sessions.create in
// particular) to silently stall.
func (c *Client) dial(ctx context.Context) error {
	requestedToken := c.store.LoadDeviceToken()

	gw, err := c.dialOnce(ctx)
	if err != nil {
		return err
	}

	issued := ""
	if hello := gw.Hello(); hello != nil && hello.Auth != nil {
		issued = hello.Auth.DeviceToken
	}
	if issued != "" {
		if err := c.store.SaveDeviceToken(issued); err != nil {
			log.Printf("warning: failed to save device token: %v", err)
		}
	}

	if requestedToken == "" && issued != "" {
		_ = gw.Close()
		gw2, err := c.dialOnce(ctx)
		if err != nil {
			return fmt.Errorf("post-pair re-dial: %w", err)
		}
		gw = gw2
	}

	c.mu.Lock()
	c.gw = gw
	c.mu.Unlock()
	return nil
}

// dialOnce performs a single SDK handshake, picking up the latest
// device token from the store on each call so a re-dial after a
// first-time pairing presents the freshly-issued token.
func (c *Client) dialOnce(ctx context.Context) (*gateway.Client, error) {
	opts, err := c.buildOptions()
	if err != nil {
		return nil, err
	}
	gw := gateway.NewClient(opts...)
	if err := gw.Connect(ctx, c.cfg.WSURL); err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return gw, nil
}

// buildOptions assembles the gateway SDK options for a connection attempt.
// Called on every (re)connect so any newly-saved device token is picked up.
func (c *Client) buildOptions() ([]gateway.Option, error) {
	id, err := c.store.LoadOrGenerate()
	if err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}

	deviceToken := c.store.LoadDeviceToken()

	c.mu.RLock()
	connectTimeout := c.connectTimeout
	c.mu.RUnlock()

	opts := []gateway.Option{
		gateway.WithToken(deviceToken),
		gateway.WithClientInfo(protocol.ClientInfo{
			ID:       protocol.ClientIDCLI,
			Version:  "0.1.0",
			Platform: "go",
			Mode:     protocol.ClientModeCLI,
		}),
		gateway.WithRole(protocol.RoleOperator),
		gateway.WithScopes(protocol.ScopeOperatorRead, protocol.ScopeOperatorWrite, protocol.ScopeOperatorAdmin, protocol.ScopeOperatorApprovals),
		gateway.WithCaps(protocol.ClientCapToolEvents),
		gateway.WithOnEvent(func(ev protocol.Event) {
			select {
			case c.events <- ev:
			default:
				// drop event if channel is full
			}
		}),
		gateway.WithIdentity(id, deviceToken),
	}
	if connectTimeout > 0 {
		opts = append(opts, gateway.WithConnectTimeout(connectTimeout))
	}
	return opts, nil
}

// Done returns a channel that is closed when the current gateway connection
// terminates (clean close, network drop, or gateway restart). Returns a
// pre-closed channel if there is no active connection.
func (c *Client) Done() <-chan struct{} {
	c.mu.RLock()
	gw := c.gw
	c.mu.RUnlock()
	if gw == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return gw.Done()
}

// Events returns the channel of gateway events.
func (c *Client) Events() <-chan protocol.Event {
	return c.events
}

// gw returns the current gateway client under read-lock. Callers must
// handle nil (returned only before the first Connect succeeds).
func (c *Client) currentGW() *gateway.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.gw
}

// ListAgents returns the list of available agents.
func (c *Client) ListAgents(ctx context.Context) (*protocol.AgentsListResult, error) {
	return c.currentGW().AgentsList(ctx)
}

// DeleteAgent removes an agent via the gateway API. deleteFiles is
// the user's explicit choice from the confirm view: when true, the
// gateway also wipes the agent's workspace files; when false, only
// bindings drop and the workspace stays on disk so the user can
// reuse it. The result payload (ok / removed bindings count) is
// discarded — callers care only about success vs failure.
func (c *Client) DeleteAgent(ctx context.Context, agentID string, deleteFiles bool) error {
	gw := c.currentGW()
	flag := deleteFiles
	if _, err := gw.AgentsDelete(ctx, protocol.AgentsDeleteParams{
		AgentID:     agentID,
		DeleteFiles: &flag,
	}); err != nil {
		return fmt.Errorf("agents delete: %w", err)
	}
	return nil
}

// CreateAgent provisions a new agent via the gateway API and seeds an
// IDENTITY.md file for it.
func (c *Client) CreateAgent(ctx context.Context, name, workspace string) error {
	gw := c.currentGW()
	result, err := gw.AgentsCreate(ctx, protocol.AgentsCreateParams{
		Name:      name,
		Workspace: workspace,
	})
	if err != nil {
		return fmt.Errorf("agents create: %w", err)
	}

	// Seed IDENTITY.md so the agent has a name.
	identity := fmt.Sprintf("# Identity\n\nName: %s\n", name)
	if _, err := gw.AgentsFilesSet(ctx, protocol.AgentsFilesSetParams{
		AgentID: result.AgentID,
		Name:    "IDENTITY.md",
		Content: identity,
	}); err != nil {
		// Non-fatal: agent is created but identity file may need manual setup.
		log.Printf("warning: failed to seed IDENTITY.md: %v", err)
	}

	return nil
}

// SessionsList lists sessions for the given agent.
func (c *Client) SessionsList(ctx context.Context, agentID string) (json.RawMessage, error) {
	includeTitles := true
	includeLastMsg := true
	return c.currentGW().SessionsList(ctx, protocol.SessionsListParams{
		AgentID:              agentID,
		IncludeDerivedTitles: &includeTitles,
		IncludeLastMessage:   &includeLastMsg,
	})
}

// CreateSession creates or resumes a session for the given agent and returns
// the gateway-assigned session key.
func (c *Client) CreateSession(ctx context.Context, agentID, key string) (string, error) {
	raw, err := c.currentGW().SessionsCreate(ctx, protocol.SessionsCreateParams{
		Key:     key,
		AgentID: agentID,
	})
	if err != nil {
		return "", fmt.Errorf("sessions create: %w", err)
	}
	var result struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("parse session result: %w", err)
	}
	return result.Key, nil
}

// ChatSend sends a chat message and returns the initial ack.
func (c *Client) ChatSend(ctx context.Context, sessionKey, message, idemKey string) (*protocol.ChatSendResult, error) {
	return c.currentGW().ChatSend(ctx, protocol.ChatSendParams{
		SessionKey:     sessionKey,
		Message:        message,
		IdempotencyKey: idemKey,
	})
}

// ChatHistory retrieves recent chat history for a session.
func (c *Client) ChatHistory(ctx context.Context, sessionKey string, limit int) (json.RawMessage, error) {
	return c.currentGW().ChatHistory(ctx, protocol.ChatHistoryParams{
		SessionKey: sessionKey,
		Limit:      &limit,
	})
}

// SessionUsage retrieves usage data for a session.
func (c *Client) SessionUsage(ctx context.Context, sessionKey string) (json.RawMessage, error) {
	includeContext := true
	return c.currentGW().SessionsUsage(ctx, protocol.SessionsUsageParams{
		Key:                  sessionKey,
		IncludeContextWeight: &includeContext,
	})
}

// ModelsList returns the available models.
func (c *Client) ModelsList(ctx context.Context) (*protocol.ModelsListResult, error) {
	return c.currentGW().ModelsList(ctx)
}

// SessionPatchModel changes the model for a session.
func (c *Client) SessionPatchModel(ctx context.Context, sessionKey, modelID string) error {
	return c.currentGW().SessionsPatch(ctx, protocol.SessionsPatchParams{
		Key:   sessionKey,
		Model: &modelID,
	})
}

// SessionPatchThinking sets the thinking level for a session.
func (c *Client) SessionPatchThinking(ctx context.Context, sessionKey, level string) error {
	return c.currentGW().SessionsPatch(ctx, protocol.SessionsPatchParams{
		Key:           sessionKey,
		ThinkingLevel: &level,
	})
}

// ExecRequest submits a command for execution on the gateway host.
// TwoPhase is set so the gateway returns immediately with status "accepted"
// and the decision arrives asynchronously via an exec.approval.resolved event.
func (c *Client) ExecRequest(ctx context.Context, command, sessionKey string) (*protocol.ExecApprovalRequestResult, error) {
	twoPhase := true
	return c.currentGW().ExecApprovalRequest(ctx, protocol.ExecApprovalRequestParams{
		Command:    command,
		SessionKey: &sessionKey,
		TwoPhase:   &twoPhase,
	})
}

// ExecResolve approves or denies a pending exec approval.
func (c *Client) ExecResolve(ctx context.Context, id, decision string) (*protocol.ExecApprovalResolveResult, error) {
	return c.currentGW().ExecApprovalResolve(ctx, protocol.ExecApprovalResolveParams{
		ID:       id,
		Decision: decision,
	})
}

// ChatAbort aborts a running chat turn.
func (c *Client) ChatAbort(ctx context.Context, sessionKey, runID string) error {
	return c.currentGW().ChatAbort(ctx, protocol.ChatAbortParams{
		SessionKey: sessionKey,
		RunID:      runID,
	})
}

// SessionCompact compacts (summarises) the session context.
func (c *Client) SessionCompact(ctx context.Context, sessionKey string) error {
	return c.currentGW().SessionsCompact(ctx, protocol.SessionsCompactParams{Key: sessionKey})
}

// SessionDelete deletes a session and its transcript.
func (c *Client) SessionDelete(ctx context.Context, sessionKey string) error {
	deleteTranscript := true
	return c.currentGW().SessionsDelete(ctx, protocol.SessionsDeleteParams{
		Key:              sessionKey,
		DeleteTranscript: &deleteTranscript,
	})
}

// GatewayHealth retrieves the gateway health snapshot.
func (c *Client) GatewayHealth(ctx context.Context) (*protocol.HealthEvent, error) {
	raw, err := c.currentGW().Health(ctx)
	if err != nil {
		return nil, fmt.Errorf("health: %w", err)
	}
	var h protocol.HealthEvent
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, fmt.Errorf("parse health: %w", err)
	}
	return &h, nil
}

// HelloUptimeMs returns the gateway uptime in milliseconds from the connect
// handshake, or 0 if not connected.
func (c *Client) HelloUptimeMs() int64 {
	gw := c.currentGW()
	if gw == nil {
		return 0
	}
	if h := gw.Hello(); h != nil {
		return h.Snapshot.UptimeMs
	}
	return 0
}

// ClearToken removes the stored device token so the next Connect call
// will authenticate without a cached token.
func (c *Client) ClearToken() error {
	return c.store.ClearDeviceToken()
}

// ResetIdentity removes all stored identity data (keypair and device token)
// so the next Connect call will register as a new device.
func (c *Client) ResetIdentity() error {
	return c.store.Reset()
}

// StoreToken saves a new device token. Use this after clearing a stale token
// to seed the gateway auth token before retrying the connection.
func (c *Client) StoreToken(token string) error {
	return c.store.SaveDeviceToken(token)
}

// CronsList lists cron jobs on the gateway.
func (c *Client) CronsList(ctx context.Context, params protocol.CronListParams) (*protocol.CronListResult, error) {
	return c.currentGW().CronList(ctx, params)
}

// CronRuns retrieves the run history for a cron job (or all jobs).
func (c *Client) CronRuns(ctx context.Context, params protocol.CronRunsParams) (*protocol.CronRunsResult, error) {
	return c.currentGW().CronRuns(ctx, params)
}

// CronAdd creates a new cron job.
func (c *Client) CronAdd(ctx context.Context, params protocol.CronAddParams) (json.RawMessage, error) {
	return c.currentGW().CronAdd(ctx, params)
}

// CronUpdate updates an existing cron job.
func (c *Client) CronUpdate(ctx context.Context, params protocol.CronUpdateParams) error {
	return c.currentGW().CronUpdate(ctx, params)
}

// CronUpdateRaw updates an existing cron job using a raw patch map.
// This bypasses the protocol.CronJobPatch struct so callers can express
// "explicitly clear this string field" — the typed struct uses
// `omitempty` on string fields, which makes an empty value
// indistinguishable from "field not provided" once it hits the wire.
// The form-edit flow uses this so clearing the model or description
// fields actually persists.
func (c *Client) CronUpdateRaw(ctx context.Context, jobID string, patch map[string]any) error {
	resp, err := c.currentGW().Send(ctx, string(protocol.MethodCronUpdate), map[string]any{
		"id":    jobID,
		"patch": patch,
	})
	if err != nil {
		return fmt.Errorf("cron update: %w", err)
	}
	if !resp.OK && resp.Error != nil {
		return fmt.Errorf("cron update: %s: %s", resp.Error.Code, resp.Error.Message)
	}
	return nil
}

// CronRemove deletes a cron job.
func (c *Client) CronRemove(ctx context.Context, jobID string) error {
	return c.currentGW().CronRemove(ctx, protocol.CronRemoveParams{ID: jobID})
}

// CronRun manually triggers a cron job. When force is true, the job runs
// regardless of its schedule; otherwise it only runs if currently due.
func (c *Client) CronRun(ctx context.Context, jobID string, force bool) error {
	mode := "due"
	if force {
		mode = "force"
	}
	return c.currentGW().CronRun(ctx, protocol.CronRunParams{ID: jobID, Mode: mode})
}

// GW returns the underlying gateway client (for direct RPC access).
// May return nil if no connection has been established yet, or briefly
// during a reconnect cycle.
func (c *Client) GW() *gateway.Client { return c.currentGW() }

// Close closes the gateway connection.
func (c *Client) Close() error {
	c.mu.Lock()
	gw := c.gw
	c.gw = nil
	c.mu.Unlock()
	if gw != nil {
		return gw.Close()
	}
	return nil
}
