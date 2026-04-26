package client

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/a3tai/openclaw-go/gateway"
	"github.com/a3tai/openclaw-go/identity"
	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// Client wraps the gateway SDK client and bridges events to a channel
// for consumption by the bubbletea event loop.
type Client struct {
	gw     *gateway.Client
	events chan protocol.Event
	cfg    *config.Config
	store  *identity.Store
}

// New creates a new client from the given config.
func New(cfg *config.Config) (*Client, error) {
	identityDir, err := identityDirForEndpoint(cfg.GatewayURL)
	if err != nil {
		return nil, fmt.Errorf("identity dir: %w", err)
	}

	store, err := identity.NewStore(identityDir)
	if err != nil {
		return nil, fmt.Errorf("identity store: %w", err)
	}

	return &Client{
		events: make(chan protocol.Event, 256),
		cfg:    cfg,
		store:  store,
	}, nil
}

// identityDirForEndpoint returns a per-endpoint identity directory under
// ~/.lucinate/identity/<host_port>/.  This keeps keys and device tokens
// isolated per gateway so switching endpoints doesn't overwrite them.
func identityDirForEndpoint(gatewayURL string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home dir: %w", err)
	}

	u, err := url.Parse(gatewayURL)
	if err != nil {
		return "", fmt.Errorf("parse gateway URL: %w", err)
	}

	key := sanitiseHost(u.Host)
	if key == "" {
		return "", fmt.Errorf("gateway URL has no host: %s", gatewayURL)
	}

	return filepath.Join(home, ".lucinate", "identity", key), nil
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
	id, err := c.store.LoadOrGenerate()
	if err != nil {
		return fmt.Errorf("identity: %w", err)
	}

	deviceToken := c.store.LoadDeviceToken()

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
		gateway.WithOnEvent(func(ev protocol.Event) {
			select {
			case c.events <- ev:
			default:
				// drop event if channel is full
			}
		}),
	}

	// Include device identity for authentication.
	opts = append(opts, gateway.WithIdentity(id, deviceToken))

	c.gw = gateway.NewClient(opts...)

	if err := c.gw.Connect(ctx, c.cfg.WSURL); err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	// Save device token if issued.
	hello := c.gw.Hello()
	if hello != nil && hello.Auth != nil && hello.Auth.DeviceToken != "" {
		if err := c.store.SaveDeviceToken(hello.Auth.DeviceToken); err != nil {
			log.Printf("warning: failed to save device token: %v", err)
		}
	}

	return nil
}

// Events returns the channel of gateway events.
func (c *Client) Events() <-chan protocol.Event {
	return c.events
}

// ListAgents returns the list of available agents.
func (c *Client) ListAgents(ctx context.Context) (*protocol.AgentsListResult, error) {
	return c.gw.AgentsList(ctx)
}

// CreateAgent provisions a new agent via the gateway API and seeds an
// IDENTITY.md file for it.
func (c *Client) CreateAgent(ctx context.Context, name, workspace string) error {
	result, err := c.gw.AgentsCreate(ctx, protocol.AgentsCreateParams{
		Name:      name,
		Workspace: workspace,
	})
	if err != nil {
		return fmt.Errorf("agents create: %w", err)
	}

	// Seed IDENTITY.md so the agent has a name.
	identity := fmt.Sprintf("# Identity\n\nName: %s\n", name)
	if _, err := c.gw.AgentsFilesSet(ctx, protocol.AgentsFilesSetParams{
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
	return c.gw.SessionsList(ctx, protocol.SessionsListParams{
		AgentID:              agentID,
		IncludeDerivedTitles: &includeTitles,
		IncludeLastMessage:   &includeLastMsg,
	})
}

// CreateSession creates or resumes a session for the given agent and returns
// the gateway-assigned session key.
func (c *Client) CreateSession(ctx context.Context, agentID, key string) (string, error) {
	raw, err := c.gw.SessionsCreate(ctx, protocol.SessionsCreateParams{
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
	return c.gw.ChatSend(ctx, protocol.ChatSendParams{
		SessionKey:     sessionKey,
		Message:        message,
		IdempotencyKey: idemKey,
	})
}

// ChatHistory retrieves recent chat history for a session.
func (c *Client) ChatHistory(ctx context.Context, sessionKey string, limit int) (json.RawMessage, error) {
	return c.gw.ChatHistory(ctx, protocol.ChatHistoryParams{
		SessionKey: sessionKey,
		Limit:      &limit,
	})
}

// SessionUsage retrieves usage data for a session.
func (c *Client) SessionUsage(ctx context.Context, sessionKey string) (json.RawMessage, error) {
	includeContext := true
	return c.gw.SessionsUsage(ctx, protocol.SessionsUsageParams{
		Key:                  sessionKey,
		IncludeContextWeight: &includeContext,
	})
}

// ModelsList returns the available models.
func (c *Client) ModelsList(ctx context.Context) (*protocol.ModelsListResult, error) {
	return c.gw.ModelsList(ctx)
}

// SessionPatchModel changes the model for a session.
func (c *Client) SessionPatchModel(ctx context.Context, sessionKey, modelID string) error {
	return c.gw.SessionsPatch(ctx, protocol.SessionsPatchParams{
		Key:   sessionKey,
		Model: &modelID,
	})
}

// SessionPatchThinking sets the thinking level for a session.
func (c *Client) SessionPatchThinking(ctx context.Context, sessionKey, level string) error {
	return c.gw.SessionsPatch(ctx, protocol.SessionsPatchParams{
		Key:           sessionKey,
		ThinkingLevel: &level,
	})
}

// ExecRequest submits a command for execution on the gateway host.
// TwoPhase is set so the gateway returns immediately with status "accepted"
// and the decision arrives asynchronously via an exec.approval.resolved event.
func (c *Client) ExecRequest(ctx context.Context, command, sessionKey string) (*protocol.ExecApprovalRequestResult, error) {
	twoPhase := true
	return c.gw.ExecApprovalRequest(ctx, protocol.ExecApprovalRequestParams{
		Command:    command,
		SessionKey: &sessionKey,
		TwoPhase:   &twoPhase,
	})
}

// ExecResolve approves or denies a pending exec approval.
func (c *Client) ExecResolve(ctx context.Context, id, decision string) (*protocol.ExecApprovalResolveResult, error) {
	return c.gw.ExecApprovalResolve(ctx, protocol.ExecApprovalResolveParams{
		ID:       id,
		Decision: decision,
	})
}

// ChatAbort aborts a running chat turn.
func (c *Client) ChatAbort(ctx context.Context, sessionKey, runID string) error {
	return c.gw.ChatAbort(ctx, protocol.ChatAbortParams{
		SessionKey: sessionKey,
		RunID:      runID,
	})
}

// SessionCompact compacts (summarises) the session context.
func (c *Client) SessionCompact(ctx context.Context, sessionKey string) error {
	return c.gw.SessionsCompact(ctx, protocol.SessionsCompactParams{Key: sessionKey})
}

// SessionDelete deletes a session and its transcript.
func (c *Client) SessionDelete(ctx context.Context, sessionKey string) error {
	deleteTranscript := true
	return c.gw.SessionsDelete(ctx, protocol.SessionsDeleteParams{
		Key:              sessionKey,
		DeleteTranscript: &deleteTranscript,
	})
}

// GatewayHealth retrieves the gateway health snapshot.
func (c *Client) GatewayHealth(ctx context.Context) (*protocol.HealthEvent, error) {
	raw, err := c.gw.Health(ctx)
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
	if h := c.gw.Hello(); h != nil {
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

// GW returns the underlying gateway client (for direct RPC access).
func (c *Client) GW() *gateway.Client { return c.gw }

// Close closes the gateway connection.
func (c *Client) Close() error {
	if c.gw != nil {
		return c.gw.Close()
	}
	return nil
}
