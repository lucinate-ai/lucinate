package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/backend/httpcommon"
	"github.com/lucinate-ai/lucinate/internal/client"
)

var (
	osRemove     = os.Remove
	osIsNotExist = os.IsNotExist
	filepathJoin = filepath.Join
)

// Options bundles the per-connection configuration the Backend needs.
// Constructed by main.go's backendFactory once it has resolved the
// API key (from secrets storage or the connection record itself).
type Options struct {
	// ConnectionID scopes the local agent store under
	// ~/.lucinate/agents/<ConnectionID>/. Required.
	ConnectionID string

	// BaseURL is the OpenAI-compatible endpoint root, e.g.
	// "http://localhost:11434/v1" for Ollama or
	// "https://api.openai.com/v1" for OpenAI proper. Required.
	BaseURL string

	// APIKey is sent as `Authorization: Bearer <key>`. Optional —
	// some endpoints (Ollama, vLLM without auth) accept anonymous
	// requests.
	APIKey string

	// DefaultModel seeds the model field on newly created agents
	// when the create-agent form does not specify one. Optional;
	// when blank the user picks at create time.
	DefaultModel string

	// HTTPClient lets tests inject a fake transport. Defaults to a
	// http.Client with no request-level timeout (streaming responses
	// are arbitrarily long; per-call ctx cancels are the bound). When
	// nil, ConnectTimeout is applied at the transport level so socket
	// dial and TLS handshake share the user-configured deadline
	// without bounding streaming reads.
	HTTPClient *http.Client

	// ConnectTimeout bounds TCP dial and TLS handshake on the default
	// HTTP transport. Zero leaves Go's defaults in place. Ignored when
	// HTTPClient is supplied (tests configure their own transport).
	ConnectTimeout time.Duration
}

// Backend implements backend.Backend by translating /v1/chat/completions
// SSE streams into protocol.ChatEvent messages. Agent state lives in
// a per-connection AgentStore on disk (agent ≡ session, 1:1).
//
// Shared HTTP/SSE/event-emission plumbing comes from httpcommon — see
// the package doc there for the rationale.
type Backend struct {
	opts    Options
	store   *AgentStore
	http    *httpcommon.Client
	emitter *httpcommon.EventEmitter

	mu   sync.Mutex
	runs map[string]context.CancelFunc // active runs by run ID
}

// New constructs a Backend. Connect() doesn't perform the network
// handshake yet — it's done lazily on the first request — but the
// constructor validates the local agent store path so wiring errors
// surface before the TUI transitions to the chat view.
func New(opts Options) (*Backend, error) {
	if opts.ConnectionID == "" {
		return nil, fmt.Errorf("openai backend: ConnectionID is required")
	}
	if opts.BaseURL == "" {
		return nil, fmt.Errorf("openai backend: BaseURL is required")
	}
	store, err := NewAgentStore(opts.ConnectionID)
	if err != nil {
		return nil, fmt.Errorf("openai backend: %w", err)
	}
	httpClient, err := httpcommon.NewClient(httpcommon.Options{
		BaseURL:        opts.BaseURL,
		APIKey:         opts.APIKey,
		HTTPClient:     opts.HTTPClient,
		ConnectTimeout: opts.ConnectTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("openai backend: %w", err)
	}
	return &Backend{
		opts:    opts,
		store:   store,
		http:    httpClient,
		emitter: httpcommon.NewEventEmitter(64),
		runs:    map[string]context.CancelFunc{},
	}, nil
}

// Connect verifies the endpoint by hitting /v1/models. A 401 / 403
// surfaces as the canonical "api key required" error so the TUI's
// connecting view routes it to the API-key modal.
func (b *Backend) Connect(ctx context.Context) error {
	req, err := b.http.NewRequest(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return err
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("api key required (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("connect: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// Close drains the events channel and cancels any in-flight runs.
func (b *Backend) Close() error {
	b.mu.Lock()
	for _, cancel := range b.runs {
		cancel()
	}
	b.runs = nil
	b.mu.Unlock()
	b.emitter.Close()
	return nil
}

// Events returns the event channel.
func (b *Backend) Events() <-chan protocol.Event { return b.emitter.Channel() }

// Supervise emits a single "connected" transition and blocks. The
// HTTP backend has no long-lived connection to supervise; reconnect
// state is meaningless. Until ctx is cancelled the chat view sees
// the steady-state badge (no "reconnecting" or "disconnected"
// banner).
func (b *Backend) Supervise(ctx context.Context, notify func(client.ConnState)) {
	notify(client.ConnState{Status: client.StatusConnected})
	<-ctx.Done()
}

// ListAgents enumerates the local store and returns the result in
// the gateway's protocol shape so the existing TUI agent picker
// renders without changes.
func (b *Backend) ListAgents(ctx context.Context) (*protocol.AgentsListResult, error) {
	metas, err := b.store.List()
	if err != nil {
		return nil, err
	}
	out := &protocol.AgentsListResult{}
	for _, meta := range metas {
		modelID := meta.Model
		if modelID == "" {
			modelID = b.opts.DefaultModel
		}
		summary := protocol.AgentSummary{
			ID:   meta.ID,
			Name: meta.Name,
		}
		if modelID != "" {
			summary.Model = &protocol.AgentSummaryModel{Primary: modelID}
		}
		out.Agents = append(out.Agents, summary)
	}
	if len(out.Agents) > 0 {
		out.DefaultID = out.Agents[0].ID
		out.MainKey = out.Agents[0].ID
	}
	return out, nil
}

// CreateAgent provisions an agent on disk. The TUI populates
// Identity/Soul/Model from the create-agent form (with Default*
// constants as initial values), or accepts the defaults when no
// custom values are supplied.
func (b *Backend) CreateAgent(ctx context.Context, params backend.CreateAgentParams) error {
	identity := params.Identity
	if identity == "" {
		identity = DefaultIdentity(params.Name)
	}
	soul := params.Soul
	if soul == "" {
		soul = DefaultSoul
	}
	model := params.Model
	if model == "" {
		model = b.opts.DefaultModel
	}
	_, err := b.store.Create(params.Name, identity, soul, model)
	return err
}

// SessionsList returns a single-entry "session list" so the existing
// TUI session browser keeps working: agent ≡ session 1:1, the
// session key equals the agent ID.
func (b *Backend) SessionsList(ctx context.Context, agentID string) (json.RawMessage, error) {
	meta, err := b.store.LoadMeta(agentID)
	if err != nil {
		return json.RawMessage(`{"sessions":[]}`), nil
	}
	last := ""
	if msgs, _ := b.store.LoadHistory(agentID, 1); len(msgs) > 0 {
		last = msgs[len(msgs)-1].Content
	}
	entry := map[string]any{
		"key":         meta.ID,
		"title":       meta.Name,
		"updatedAt":   meta.UpdatedAt.UnixMilli(),
		"lastMessage": last,
	}
	wrap := map[string]any{"sessions": []any{entry}}
	return json.Marshal(wrap)
}

// CreateSession is a no-op for OpenAI: the agent is the session.
// Returns the agentID as the session key so subsequent ChatSend /
// ChatHistory calls round-trip the right value.
func (b *Backend) CreateSession(ctx context.Context, agentID, key string) (string, error) {
	if _, err := b.store.LoadMeta(agentID); err != nil {
		return "", fmt.Errorf("agent not found: %s", agentID)
	}
	return agentID, nil
}

// SessionDelete clears the agent's transcript by deleting the
// directory; the TUI's /reset flow follows up with CreateSession,
// which will fail because the agent no longer exists. To match the
// OpenClaw behaviour ("clear history, keep the agent"), we instead
// truncate history.jsonl and leave the rest of the directory intact.
func (b *Backend) SessionDelete(ctx context.Context, sessionKey string) error {
	dir := b.store.AgentDir(sessionKey)
	return removeFile(dir, "history.jsonl")
}

// ChatSend posts the user's message + persisted transcript to
// /v1/chat/completions with stream=true. Deltas arrive on the events
// channel as protocol.ChatEvent (state=delta), and a final event
// closes the run. The user message is appended to history.jsonl
// before the request goes out so a mid-stream crash doesn't lose it.
func (b *Backend) ChatSend(ctx context.Context, sessionKey string, params backend.ChatSendParams) (*protocol.ChatSendResult, error) {
	meta, err := b.store.LoadMeta(sessionKey)
	if err != nil {
		return nil, fmt.Errorf("agent not found: %s", sessionKey)
	}
	model := meta.Model
	if model == "" {
		model = b.opts.DefaultModel
	}
	if model == "" {
		return nil, fmt.Errorf("no model configured for agent %q (run /model to pick one)", sessionKey)
	}

	if err := b.store.AppendMessage(sessionKey, Message{Role: "user", Content: params.Message}); err != nil {
		return nil, fmt.Errorf("persist user message: %w", err)
	}

	runID := fmt.Sprintf("run-%d", time.Now().UnixNano())
	streamCtx, cancel := context.WithCancel(context.Background())
	b.mu.Lock()
	b.runs[runID] = cancel
	b.mu.Unlock()

	go b.runStream(streamCtx, runID, sessionKey, model, params.Skills)

	return &protocol.ChatSendResult{RunID: runID}, nil
}

// runStream performs the HTTP request, parses the SSE stream, and
// emits chat-delta / chat-final events. Errors during the run are
// surfaced as a state="error" event so the chat view's existing
// error rendering applies.
func (b *Backend) runStream(ctx context.Context, runID, sessionKey, model string, skills []backend.SkillCatalogEntry) {
	defer func() {
		b.mu.Lock()
		delete(b.runs, runID)
		b.mu.Unlock()
	}()

	// Build the message list: identity/soul system prompt, then a
	// separate system message advertising any local skills, then
	// the full conversation history. Both system messages are
	// reconstructed each turn so user edits to IDENTITY.md /
	// SOUL.md and changes to the discovered skill set take effect
	// immediately on the next request.
	body := chatRequest{Model: model, Stream: true}
	if sysPrompt := b.store.SystemPrompt(sessionKey); sysPrompt != "" {
		body.Messages = append(body.Messages, chatRequestMessage{Role: "system", Content: sysPrompt})
	}
	if catalog := skillCatalogSystemMessage(skills); catalog != "" {
		body.Messages = append(body.Messages, chatRequestMessage{Role: "system", Content: catalog})
	}
	history, _ := b.store.LoadHistory(sessionKey, 0)
	for _, msg := range history {
		if msg.Role == "system" {
			continue // system prompt is reconstructed from Identity/Soul each turn
		}
		body.Messages = append(body.Messages, chatRequestMessage{Role: msg.Role, Content: msg.Content})
	}

	req, err := b.http.NewRequest(ctx, http.MethodPost, "/chat/completions", body)
	if err != nil {
		b.emitter.EmitChatError(runID, sessionKey, err.Error())
		return
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := b.http.Do(req)
	if err != nil {
		b.emitter.EmitChatError(runID, sessionKey, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		b.emitter.EmitChatError(runID, sessionKey, fmt.Sprintf("api key required (HTTP %d)", resp.StatusCode))
		return
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		b.emitter.EmitChatError(runID, sessionKey, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw))))
		return
	}

	var assistant strings.Builder
	scanErr := httpcommon.ScanSSE(resp.Body, func(payload string) bool {
		if payload == "[DONE]" {
			return true
		}
		var ev streamChunk
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return false
		}
		if len(ev.Choices) == 0 {
			return false
		}
		delta := ev.Choices[0].Delta.Content
		if delta == "" {
			return false
		}
		assistant.WriteString(delta)
		b.emitter.EmitChatDelta(runID, sessionKey, assistant.String())
		return false
	})
	if scanErr != nil {
		b.emitter.EmitChatError(runID, sessionKey, scanErr.Error())
		return
	}

	final := assistant.String()
	if final != "" {
		_ = b.store.AppendMessage(sessionKey, Message{Role: "assistant", Content: final, Model: model})
	}
	b.emitter.EmitChatFinal(runID, sessionKey, final)
}

// ChatAbort cancels the in-flight run if any.
func (b *Backend) ChatAbort(ctx context.Context, sessionKey, runID string) error {
	b.mu.Lock()
	cancel, ok := b.runs[runID]
	delete(b.runs, runID)
	b.mu.Unlock()
	if !ok {
		return nil
	}
	cancel()
	b.emitter.EmitChatAborted(runID, sessionKey)
	return nil
}

// ChatHistory loads history from the local store and renders it in
// the gateway's protocol.ChatHistoryResult JSON shape so the chat
// view's existing parser keeps working unchanged.
func (b *Backend) ChatHistory(ctx context.Context, sessionKey string, limit int) (json.RawMessage, error) {
	msgs, err := b.store.LoadHistory(sessionKey, limit)
	if err != nil {
		return nil, err
	}
	type block struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type historyMsg struct {
		Role      string  `json:"role"`
		Content   []block `json:"content"`
		Timestamp int64   `json:"timestamp,omitempty"`
	}
	out := struct {
		Messages []historyMsg `json:"messages"`
	}{}
	for _, msg := range msgs {
		if msg.Role == "system" {
			continue
		}
		var ts int64
		if !msg.Time.IsZero() {
			ts = msg.Time.UnixMilli()
		}
		out.Messages = append(out.Messages, historyMsg{
			Role:      msg.Role,
			Content:   []block{{Type: "text", Text: msg.Content}},
			Timestamp: ts,
		})
	}
	return json.Marshal(out)
}

// ModelsList queries the upstream /v1/models endpoint and returns
// the result in the gateway's protocol.ModelsListResult shape.
func (b *Backend) ModelsList(ctx context.Context) (*protocol.ModelsListResult, error) {
	req, err := b.http.NewRequest(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("models list: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var raw struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}
	out := &protocol.ModelsListResult{}
	for _, m := range raw.Data {
		out.Models = append(out.Models, protocol.ModelChoice{ID: m.ID, Name: m.ID})
	}
	return out, nil
}

// SessionPatchModel updates the agent's stored model for future turns.
func (b *Backend) SessionPatchModel(ctx context.Context, sessionKey, modelID string) error {
	return b.store.SetModel(sessionKey, modelID)
}

// Capabilities reports the trimmed feature set: no gateway status,
// no remote exec, no server-side compact (issue #76 covers the
// future local summarisation pass), no thinking, no usage.
func (b *Backend) Capabilities() backend.Capabilities {
	return backend.Capabilities{
		AuthRecovery:    backend.AuthRecoveryAPIKey,
		AgentManagement: true,
	}
}

// --- APIKeyAuth ---

func (b *Backend) StoreAPIKey(key string) error {
	b.http.SetAPIKey(key)
	return nil
}

// --- internals ---

// skillCatalogSystemMessage formats the local skill catalog as a
// real role:system message body. Returns the empty string when no
// catalog entries are present so the caller can skip emitting a
// blank system block.
func skillCatalogSystemMessage(skills []backend.SkillCatalogEntry) string {
	var entries strings.Builder
	for _, s := range skills {
		if s.Name == "" {
			continue
		}
		entries.WriteString(fmt.Sprintf("  - %s: %s\n", s.Name, s.Description))
	}
	if entries.Len() == 0 {
		return ""
	}
	return "Available agent skills (the user activates one with /skill-name):\n" + entries.String()
}

// chatRequest mirrors the OpenAI-compatible request body (subset).
type chatRequest struct {
	Model    string               `json:"model"`
	Messages []chatRequestMessage `json:"messages"`
	Stream   bool                 `json:"stream"`
}

type chatRequestMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

// removeFile deletes a single file inside a directory, ignoring
// "does not exist" errors so /reset works even on a fresh agent.
func removeFile(dir, name string) error {
	if err := osRemove(filepathJoin(dir, name)); err != nil && !osIsNotExist(err) {
		return err
	}
	return nil
}
