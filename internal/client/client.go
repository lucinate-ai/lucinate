package client

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/a3tai/openclaw-go/gateway"
	"github.com/a3tai/openclaw-go/identity"
	"github.com/a3tai/openclaw-go/protocol"

	"github.com/outofcoffee/repclaw/internal/config"
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
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("user home dir: %w", err)
	}

	identityDir := filepath.Join(home, ".openclaw-go", "identity")
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

// Connect establishes a WebSocket connection to the gateway.
func (c *Client) Connect(ctx context.Context) error {
	id, err := c.store.LoadOrGenerate()
	if err != nil {
		return fmt.Errorf("identity: %w", err)
	}

	deviceToken := c.store.LoadDeviceToken()
	token := c.cfg.Token
	if deviceToken != "" {
		token = deviceToken
	}

	opts := []gateway.Option{
		gateway.WithToken(token),
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

// CreateAgent provisions a standalone agent by reading the current gateway
// config, appending the new agent to agents.list, and applying the updated
// config. It also seeds an IDENTITY.md file for the new agent.
func (c *Client) CreateAgent(ctx context.Context, name, workspace string) error {
	agentDir := "~/.openclaw/agents/" + name + "/agent"

	// Fetch current config to get the base hash and existing agents list.
	configRaw, err := c.gw.ConfigGet(ctx)
	if err != nil {
		return fmt.Errorf("config get: %w", err)
	}

	// Parse the response to extract hash and existing agents.list.
	var configResp struct {
		Hash   string          `json:"hash"`
		Config json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(configRaw, &configResp); err != nil {
		return fmt.Errorf("parse config response: %w", err)
	}

	// Parse the config object to read the existing agents.list.
	var cfg map[string]any
	if err := json.Unmarshal(configResp.Config, &cfg); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	// Extract existing agents.list (may not exist yet).
	var agentsList []any
	if agents, ok := cfg["agents"].(map[string]any); ok {
		if list, ok := agents["list"].([]any); ok {
			agentsList = list
		}
	}

	// Append the new agent entry.
	newAgent := map[string]any{
		"id":        name,
		"name":      name,
		"workspace": workspace,
		"agentDir":  agentDir,
	}
	agentsList = append(agentsList, newAgent)

	// Build the patch with the full updated agents.list (arrays replace).
	patch := map[string]any{
		"agents": map[string]any{
			"list": agentsList,
		},
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal config patch: %w", err)
	}

	if err := c.gw.ConfigPatch(ctx, protocol.ConfigPatchParams{
		Raw:      string(raw),
		BaseHash: configResp.Hash,
		Note:     fmt.Sprintf("add agent %q", name),
	}); err != nil {
		return fmt.Errorf("config patch: %w", err)
	}

	// Seed IDENTITY.md so the agent has a name.
	identity := fmt.Sprintf("# Identity\n\nName: %s\n", name)
	if _, err := c.gw.AgentsFilesSet(ctx, protocol.AgentsFilesSetParams{
		AgentID: name,
		Name:    "IDENTITY.md",
		Content: identity,
	}); err != nil {
		// Non-fatal: agent is created but identity file may need manual setup.
		log.Printf("warning: failed to seed IDENTITY.md: %v", err)
	}

	return nil
}

// CreateSession creates a new session for the given agent and returns the
// gateway-assigned session key.
func (c *Client) CreateSession(ctx context.Context, agentID string) (string, error) {
	raw, err := c.gw.SessionsCreate(ctx, protocol.SessionsCreateParams{
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

// GW returns the underlying gateway client (for direct RPC access).
func (c *Client) GW() *gateway.Client { return c.gw }

// Close closes the gateway connection.
func (c *Client) Close() error {
	if c.gw != nil {
		return c.gw.Close()
	}
	return nil
}
