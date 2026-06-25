package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/a3tai/openclaw-go/gateway"
	"github.com/a3tai/openclaw-go/identity"
	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// ErrNotConnected is returned by RPC methods when the gateway client is
// not currently attached — either before the first Connect, after Close,
// or while the supervisor is between Reconnect attempts (the underlying
// transport is rebuilt from scratch, so c.gw is briefly nil during each
// retry). Surfaces as a clean error in the TUI's chat-send error path
// instead of a nil-pointer panic.
var ErrNotConnected = errors.New("gateway not connected")

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

// Bootstrap performs the setup-code node→operator handoff so a brand-new
// device can be established without a manual pairing approval. It connects
// once as a node presenting the bootstrap token (minted by `openclaw qr`),
// which the gateway silently approves for the setup-code profile, then
// persists the bounded operator device token returned in hello-ok. After
// Bootstrap returns, an ordinary Connect authenticates with that token.
//
// It is a no-op-free one-shot: the temporary node connection is closed
// before returning, and the persisted token is the operator credential, not
// the node one.
func (c *Client) Bootstrap(ctx context.Context, bootstrapToken string) error {
	if strings.TrimSpace(bootstrapToken) == "" {
		return fmt.Errorf("bootstrap: empty token")
	}
	id, err := c.store.LoadOrGenerate()
	if err != nil {
		return fmt.Errorf("bootstrap: identity: %w", err)
	}

	c.mu.RLock()
	connectTimeout := c.connectTimeout
	c.mu.RUnlock()

	opts := []gateway.Option{
		gateway.WithClientInfo(protocol.ClientInfo{
			ID:       protocol.ClientIDNodeHost,
			Version:  "0.1.0",
			Platform: "go",
			Mode:     protocol.ClientModeNode,
		}),
		gateway.WithRole(protocol.RoleNode),
		// Node-role bootstrap carries no operator scopes; the gateway mints
		// the bounded operator token (and its scopes) from the setup-code
		// profile and returns it in the hello-ok handoff list.
		gateway.WithScopes(),
		gateway.WithBootstrapToken(bootstrapToken),
		gateway.WithIdentity(id, ""),
	}
	if connectTimeout > 0 {
		opts = append(opts, gateway.WithConnectTimeout(connectTimeout))
	}

	gw := gateway.NewClient(opts...)
	if err := gw.Connect(ctx, c.cfg.WSURL); err != nil {
		return fmt.Errorf("bootstrap connect: %w", err)
	}
	defer gw.Close()

	token := operatorTokenFromHello(gw.Hello())
	if token == "" {
		return fmt.Errorf("bootstrap: gateway issued no operator token")
	}
	if err := c.store.SaveDeviceToken(token); err != nil {
		return fmt.Errorf("bootstrap: save device token: %w", err)
	}
	return nil
}

// operatorTokenFromHello extracts the operator-role device token from a
// bootstrap hello-ok, whether it arrived as the primary issued token or in
// the per-role handoff list.
func operatorTokenFromHello(hello *protocol.HelloOK) string {
	if hello == nil || hello.Auth == nil {
		return ""
	}
	if hello.Auth.Role == string(protocol.RoleOperator) && hello.Auth.DeviceToken != "" {
		return hello.Auth.DeviceToken
	}
	for _, dt := range hello.Auth.DeviceTokens {
		if dt.Role == string(protocol.RoleOperator) && dt.DeviceToken != "" {
			return dt.DeviceToken
		}
	}
	return ""
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
			slog.Warn("failed to save device token", "err", err)
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

// defaultOperatorScopes is the operator scope set requested unless overridden.
var defaultOperatorScopes = []protocol.Scope{
	protocol.ScopeOperatorRead,
	protocol.ScopeOperatorWrite,
	protocol.ScopeOperatorAdmin,
	protocol.ScopeOperatorApprovals,
}

// operatorScopes returns the operator scopes to request on connect.
// OPENCLAW_OPERATOR_SCOPES (comma-separated, e.g.
// "operator.read,operator.write,operator.approvals") overrides the default —
// needed when the device token is bounded to a subset, such as a setup-code
// bootstrap credential that cannot carry operator.admin. Requesting scopes
// beyond what the token grants is rejected as a scope mismatch.
func operatorScopes() []protocol.Scope {
	raw := strings.TrimSpace(os.Getenv("OPENCLAW_OPERATOR_SCOPES"))
	if raw == "" {
		return defaultOperatorScopes
	}
	var scopes []protocol.Scope
	for _, s := range strings.Split(raw, ",") {
		if s = strings.TrimSpace(s); s != "" {
			scopes = append(scopes, protocol.Scope(s))
		}
	}
	if len(scopes) == 0 {
		return defaultOperatorScopes
	}
	return scopes
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
		gateway.WithScopes(operatorScopes()...),
		gateway.WithCaps(protocol.ClientCapToolEvents),
		gateway.WithOnEvent(func(ev protocol.Event) {
			select {
			case c.events <- ev:
			default:
				// Drop the event if the channel is full. The TUI consumer
				// is too slow or stuck; without this log a dropped error
				// event would silently hang the next turn.
				slog.Warn("gateway event channel full, dropped event", "name", ev.EventName, "payload_len", len(ev.Payload))
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

// currentGW returns the current gateway client under read-lock, or
// ErrNotConnected when none is attached (before the first Connect, after
// Close, or briefly between Reconnect attempts while the supervisor
// retries a failed dial). Callers MUST propagate the error rather than
// dereferencing the gw — Reconnect nils gw out before re-dialing so a
// dial failure (e.g. tunnel drop) leaves it nil for the full backoff
// window, and a stray dereference there is what panics the TUI.
func (c *Client) currentGW() (*gateway.Client, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.gw == nil {
		return nil, ErrNotConnected
	}
	return c.gw, nil
}

// defaultRPCTimeout bounds any gateway RPC whose caller did not already
// set a deadline. It exists because a silently-dropped transport (a
// tunnel that vanishes without a TCP close) leaves the SDK's request
// loop waiting on a response that never arrives: the write succeeds into
// the kernel send buffer, but the gateway is gone, and the SDK's internal
// "connection closed" signal only fires once the OS TCP timeout lapses
// minutes later. Without a deadline the calling goroutine — a chat send,
// the post-send history refresh, a stats poll — would block for that
// whole window. The bound turns the silent hang into a prompt error the
// TUI can surface, while staying well above any healthy round-trip.
const defaultRPCTimeout = 30 * time.Second

// rpc is the single funnel every gateway RPC method goes through. It
// returns the live gateway client (or ErrNotConnected) together with a
// context bounded by defaultRPCTimeout, unless the caller already set its
// own deadline. The returned cancel must always be called.
func (c *Client) rpc(ctx context.Context) (*gateway.Client, context.Context, context.CancelFunc, error) {
	gw, err := c.currentGW()
	if err != nil {
		return nil, nil, nil, err
	}
	if _, ok := ctx.Deadline(); ok {
		return gw, ctx, func() {}, nil
	}
	cctx, cancel := context.WithTimeout(ctx, defaultRPCTimeout)
	return gw, cctx, cancel, nil
}

// ListAgents returns the list of available agents.
func (c *Client) ListAgents(ctx context.Context) (*protocol.AgentsListResult, error) {
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return gw.AgentsList(ctx)
}

// grantedScopes returns the operator scopes the gateway granted this
// connection at the hello handshake, or nil if not connected or the gateway
// reported none. The granted set is the intersection of the scopes requested
// on connect and those the device token actually carries, so it can be
// narrower than defaultOperatorScopes.
func (c *Client) grantedScopes() []string {
	gw, err := c.currentGW()
	if err != nil {
		return nil
	}
	if h := gw.Hello(); h != nil && h.Auth != nil {
		return h.Auth.Scopes
	}
	return nil
}

// requireAdminScope fails fast, before an admin-only RPC, when the connection
// was demonstrably granted a scope set without operator.admin. The most common
// cause is OPENCLAW_OPERATOR_SCOPES bounding the requested scopes below admin —
// for example a leftover integration-test .env picked up by godotenv from the
// working directory — so the message points there. When the granted set is
// unknown (older gateway, no hello auth) it returns nil and lets the RPC
// surface the gateway's own error, which adminScopeHint then annotates.
func (c *Client) requireAdminScope(op string) error {
	scopes := c.grantedScopes()
	if len(scopes) == 0 {
		return nil
	}
	for _, s := range scopes {
		if s == string(protocol.ScopeOperatorAdmin) {
			return nil
		}
	}
	return fmt.Errorf("%s requires the operator.admin scope, but this connection was granted only [%s]. "+
		"Check that OPENCLAW_OPERATOR_SCOPES is not bounding scopes below admin (e.g. a leftover .env from the integration tests), then reconnect",
		op, strings.Join(scopes, ", "))
}

// adminScopeHint annotates a gateway "missing scope: operator.admin" error with
// the likely local cause so the raw protocol error is not the only thing the
// user sees. It is a fallback for when requireAdminScope could not pre-empt the
// failure (the granted scope set was unknown at call time).
func adminScopeHint(err error) error {
	if err == nil || !strings.Contains(err.Error(), string(protocol.ScopeOperatorAdmin)) {
		return err
	}
	return fmt.Errorf("%w (check that OPENCLAW_OPERATOR_SCOPES is not bounding scopes below admin, e.g. a leftover .env from the integration tests, then reconnect)", err)
}

// DeleteAgent removes an agent via the gateway API. deleteFiles is
// the user's explicit choice from the confirm view: when true, the
// gateway also wipes the agent's workspace files; when false, only
// bindings drop and the workspace stays on disk so the user can
// reuse it. The result payload (ok / removed bindings count) is
// discarded — callers care only about success vs failure.
func (c *Client) DeleteAgent(ctx context.Context, agentID string, deleteFiles bool) error {
	if err := c.requireAdminScope("agents delete"); err != nil {
		return err
	}
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	flag := deleteFiles
	if _, err := gw.AgentsDelete(ctx, protocol.AgentsDeleteParams{
		AgentID:     agentID,
		DeleteFiles: &flag,
	}); err != nil {
		return adminScopeHint(fmt.Errorf("agents delete: %w", err))
	}
	return nil
}

// CreateAgent provisions a new agent via the gateway API and seeds an
// IDENTITY.md file for it.
func (c *Client) CreateAgent(ctx context.Context, name, workspace string) error {
	if err := c.requireAdminScope("agents create"); err != nil {
		return err
	}
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	result, err := gw.AgentsCreate(ctx, protocol.AgentsCreateParams{
		Name:      name,
		Workspace: workspace,
	})
	if err != nil {
		return adminScopeHint(fmt.Errorf("agents create: %w", err))
	}

	// Seed IDENTITY.md so the agent has a name.
	identity := fmt.Sprintf("# Identity\n\nName: %s\n", name)
	if _, err := gw.AgentsFilesSet(ctx, protocol.AgentsFilesSetParams{
		AgentID: result.AgentID,
		Name:    "IDENTITY.md",
		Content: identity,
	}); err != nil {
		// Non-fatal: agent is created but identity file may need manual setup.
		slog.Warn("failed to seed IDENTITY.md", "err", err)
	}

	return nil
}

// SessionsList lists sessions for the given agent.
func (c *Client) SessionsList(ctx context.Context, agentID string) (json.RawMessage, error) {
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	includeTitles := true
	includeLastMsg := true
	return gw.SessionsList(ctx, protocol.SessionsListParams{
		AgentID:              agentID,
		IncludeDerivedTitles: &includeTitles,
		IncludeLastMessage:   &includeLastMsg,
	})
}

// CreateSession creates or resumes a session for the given agent and returns
// the gateway-assigned session key.
func (c *Client) CreateSession(ctx context.Context, agentID, key string) (string, error) {
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return "", err
	}
	defer cancel()
	raw, err := gw.SessionsCreate(ctx, protocol.SessionsCreateParams{
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
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return gw.ChatSend(ctx, protocol.ChatSendParams{
		SessionKey:     sessionKey,
		Message:        message,
		IdempotencyKey: idemKey,
	})
}

// ChatHistory retrieves recent chat history for a session.
func (c *Client) ChatHistory(ctx context.Context, sessionKey string, limit int) (json.RawMessage, error) {
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return gw.ChatHistory(ctx, protocol.ChatHistoryParams{
		SessionKey: sessionKey,
		Limit:      &limit,
	})
}

// SessionUsage retrieves usage data for a session.
func (c *Client) SessionUsage(ctx context.Context, sessionKey string) (json.RawMessage, error) {
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	includeContext := true
	return gw.SessionsUsage(ctx, protocol.SessionsUsageParams{
		Key:                  sessionKey,
		IncludeContextWeight: &includeContext,
	})
}

// ModelsList returns the available models.
func (c *Client) ModelsList(ctx context.Context) (*protocol.ModelsListResult, error) {
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return gw.ModelsList(ctx)
}

// SessionPatchModel changes the model for a session.
func (c *Client) SessionPatchModel(ctx context.Context, sessionKey, modelID string) error {
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	return gw.SessionsPatch(ctx, protocol.SessionsPatchParams{
		Key:   sessionKey,
		Model: &modelID,
	})
}

// SessionPatchThinking sets the thinking level for a session.
func (c *Client) SessionPatchThinking(ctx context.Context, sessionKey, level string) error {
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	return gw.SessionsPatch(ctx, protocol.SessionsPatchParams{
		Key:           sessionKey,
		ThinkingLevel: &level,
	})
}

// ExecRequest submits a command for execution on the gateway host.
// TwoPhase is set so the gateway returns immediately with status "accepted"
// and the decision arrives asynchronously via an exec.approval.resolved event.
func (c *Client) ExecRequest(ctx context.Context, command, sessionKey string) (*protocol.ExecApprovalRequestResult, error) {
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	twoPhase := true
	return gw.ExecApprovalRequest(ctx, protocol.ExecApprovalRequestParams{
		Command:    command,
		SessionKey: &sessionKey,
		TwoPhase:   &twoPhase,
	})
}

// ExecResolve approves or denies a pending exec approval.
func (c *Client) ExecResolve(ctx context.Context, id, decision string) (*protocol.ExecApprovalResolveResult, error) {
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return gw.ExecApprovalResolve(ctx, protocol.ExecApprovalResolveParams{
		ID:       id,
		Decision: decision,
	})
}

// ChatAbort aborts a running chat turn.
func (c *Client) ChatAbort(ctx context.Context, sessionKey, runID string) error {
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	return gw.ChatAbort(ctx, protocol.ChatAbortParams{
		SessionKey: sessionKey,
		RunID:      runID,
	})
}

// SessionCompact compacts (summarises) the session context.
func (c *Client) SessionCompact(ctx context.Context, sessionKey string) error {
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	return gw.SessionsCompact(ctx, protocol.SessionsCompactParams{Key: sessionKey})
}

// SessionDelete deletes a session and its transcript.
func (c *Client) SessionDelete(ctx context.Context, sessionKey string) error {
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	deleteTranscript := true
	return gw.SessionsDelete(ctx, protocol.SessionsDeleteParams{
		Key:              sessionKey,
		DeleteTranscript: &deleteTranscript,
	})
}

// GatewayHealth retrieves the gateway health snapshot.
func (c *Client) GatewayHealth(ctx context.Context) (*protocol.HealthEvent, error) {
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	raw, err := gw.Health(ctx)
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
	gw, err := c.currentGW()
	if err != nil {
		return 0
	}
	if h := gw.Hello(); h != nil {
		return h.Snapshot.UptimeMs
	}
	return 0
}

// HelloServerVersion returns the gateway's reported version string from the
// connect handshake, or "" if not connected or unknown.
func (c *Client) HelloServerVersion() string {
	gw, err := c.currentGW()
	if err != nil {
		return ""
	}
	if h := gw.Hello(); h != nil {
		return h.Server.Version
	}
	return ""
}

// HelloProtocol returns the protocol (API) version negotiated with the gateway
// during the connect handshake, or 0 if not connected/unknown.
func (c *Client) HelloProtocol() int {
	gw, err := c.currentGW()
	if err != nil {
		return 0
	}
	if h := gw.Hello(); h != nil {
		return h.Protocol
	}
	return 0
}

// GatewayURL returns the configured gateway WebSocket URL.
func (c *Client) GatewayURL() string {
	if c.cfg == nil {
		return ""
	}
	return c.cfg.WSURL
}

// HasDeviceToken reports whether a device token is currently stored
// for this client's gateway endpoint.
func (c *Client) HasDeviceToken() bool {
	return c.store != nil && c.store.LoadDeviceToken() != ""
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
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return gw.CronList(ctx, params)
}

// CronRuns retrieves the run history for a cron job (or all jobs).
func (c *Client) CronRuns(ctx context.Context, params protocol.CronRunsParams) (*protocol.CronRunsResult, error) {
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return gw.CronRuns(ctx, params)
}

// CronAdd creates a new cron job.
func (c *Client) CronAdd(ctx context.Context, params protocol.CronAddParams) (json.RawMessage, error) {
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return gw.CronAdd(ctx, params)
}

// CronUpdate updates an existing cron job.
func (c *Client) CronUpdate(ctx context.Context, params protocol.CronUpdateParams) error {
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	return gw.CronUpdate(ctx, params)
}

// CronUpdateRaw updates an existing cron job using a raw patch map.
// This bypasses the protocol.CronJobPatch struct so callers can express
// "explicitly clear this string field" — the typed struct uses
// `omitempty` on string fields, which makes an empty value
// indistinguishable from "field not provided" once it hits the wire.
// The form-edit flow uses this so clearing the model or description
// fields actually persists.
func (c *Client) CronUpdateRaw(ctx context.Context, jobID string, patch map[string]any) error {
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	resp, err := gw.Send(ctx, string(protocol.MethodCronUpdate), map[string]any{
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
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	return gw.CronRemove(ctx, protocol.CronRemoveParams{ID: jobID})
}

// CronRun manually triggers a cron job. When force is true, the job runs
// regardless of its schedule; otherwise it only runs if currently due.
func (c *Client) CronRun(ctx context.Context, jobID string, force bool) error {
	gw, ctx, cancel, err := c.rpc(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	mode := "due"
	if force {
		mode = "force"
	}
	return gw.CronRun(ctx, protocol.CronRunParams{ID: jobID, Mode: mode})
}

// GW returns the underlying gateway client (for direct RPC access).
// May return nil if no connection has been established yet, or briefly
// during a reconnect cycle — callers must handle the nil case (the TUI's
// capability-assertion sites already do).
func (c *Client) GW() *gateway.Client {
	gw, _ := c.currentGW()
	return gw
}

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
