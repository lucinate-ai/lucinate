package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	// http.Client with no timeout (streaming responses are
	// arbitrarily long; per-call ctx cancels are the bound).
	HTTPClient *http.Client
}

// Backend implements backend.Backend by translating /v1/chat/completions
// SSE streams into protocol.ChatEvent messages. Agent state lives in
// a per-connection AgentStore on disk (agent ≡ session, 1:1).
type Backend struct {
	opts   Options
	store  *AgentStore
	events chan protocol.Event
	http   *http.Client

	mu     sync.Mutex
	apiKey string                          // mutable; auth modal can rewrite
	runs   map[string]context.CancelFunc   // active runs by run ID
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
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Backend{
		opts:   opts,
		store:  store,
		events: make(chan protocol.Event, 64),
		http:   httpClient,
		apiKey: opts.APIKey,
		runs:   map[string]context.CancelFunc{},
	}, nil
}

// Connect verifies the endpoint by hitting /v1/models. A 401 / 403
// surfaces as the canonical "api key required" error so the TUI's
// connecting view routes it to the API-key modal.
func (b *Backend) Connect(ctx context.Context) error {
	req, err := b.newRequest(ctx, http.MethodGet, "/models", nil)
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
	close(b.events)
	return nil
}

// Events returns the event channel.
func (b *Backend) Events() <-chan protocol.Event { return b.events }

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
		identity = DefaultIdentity
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
func (b *Backend) ChatSend(ctx context.Context, sessionKey, message, idemKey string) (*protocol.ChatSendResult, error) {
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

	if err := b.store.AppendMessage(sessionKey, Message{Role: "user", Content: message}); err != nil {
		return nil, fmt.Errorf("persist user message: %w", err)
	}

	runID := fmt.Sprintf("run-%d", time.Now().UnixNano())
	streamCtx, cancel := context.WithCancel(context.Background())
	b.mu.Lock()
	b.runs[runID] = cancel
	b.mu.Unlock()

	go b.runStream(streamCtx, runID, sessionKey, model)

	return &protocol.ChatSendResult{RunID: runID}, nil
}

// runStream performs the HTTP request, parses the SSE stream, and
// emits chat-delta / chat-final events. Errors during the run are
// surfaced as a state="error" event so the chat view's existing
// error rendering applies.
func (b *Backend) runStream(ctx context.Context, runID, sessionKey, model string) {
	defer func() {
		b.mu.Lock()
		delete(b.runs, runID)
		b.mu.Unlock()
	}()

	// Build the message list: system prompt (if any), then full history.
	body := chatRequest{Model: model, Stream: true}
	if sysPrompt := b.store.SystemPrompt(sessionKey); sysPrompt != "" {
		body.Messages = append(body.Messages, chatRequestMessage{Role: "system", Content: sysPrompt})
	}
	history, _ := b.store.LoadHistory(sessionKey, 0)
	for _, msg := range history {
		if msg.Role == "system" {
			continue // system prompt is reconstructed from Identity/Soul each turn
		}
		body.Messages = append(body.Messages, chatRequestMessage{Role: msg.Role, Content: msg.Content})
	}

	req, err := b.newRequest(ctx, http.MethodPost, "/chat/completions", body)
	if err != nil {
		b.emitError(runID, sessionKey, err.Error())
		return
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := b.http.Do(req)
	if err != nil {
		b.emitError(runID, sessionKey, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		b.emitError(runID, sessionKey, fmt.Sprintf("api key required (HTTP %d)", resp.StatusCode))
		return
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		b.emitError(runID, sessionKey, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw))))
		return
	}

	var assistant strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}
		var ev streamChunk
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		if len(ev.Choices) == 0 {
			continue
		}
		delta := ev.Choices[0].Delta.Content
		if delta == "" {
			continue
		}
		assistant.WriteString(delta)
		b.emitDelta(runID, sessionKey, assistant.String())
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
		b.emitError(runID, sessionKey, err.Error())
		return
	}

	final := assistant.String()
	if final != "" {
		_ = b.store.AppendMessage(sessionKey, Message{Role: "assistant", Content: final, Model: model})
	}
	b.emitFinal(runID, sessionKey, final)
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
	b.emitAborted(runID, sessionKey)
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
		Role    string  `json:"role"`
		Content []block `json:"content"`
	}
	out := struct {
		Messages []historyMsg `json:"messages"`
	}{}
	for _, msg := range msgs {
		if msg.Role == "system" {
			continue
		}
		out.Messages = append(out.Messages, historyMsg{
			Role:    msg.Role,
			Content: []block{{Type: "text", Text: msg.Content}},
		})
	}
	return json.Marshal(out)
}

// ModelsList queries the upstream /v1/models endpoint and returns
// the result in the gateway's protocol.ModelsListResult shape.
func (b *Backend) ModelsList(ctx context.Context) (*protocol.ModelsListResult, error) {
	req, err := b.newRequest(ctx, http.MethodGet, "/models", nil)
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
		AuthRecovery: backend.AuthRecoveryAPIKey,
	}
}

// --- APIKeyAuth ---

func (b *Backend) StoreAPIKey(key string) error {
	b.mu.Lock()
	b.apiKey = key
	b.mu.Unlock()
	return nil
}

// --- internals ---

// newRequest constructs an HTTP request with the auth header
// pre-populated. body, when non-nil, is JSON-encoded.
func (b *Backend) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	u := strings.TrimRight(b.opts.BaseURL, "/") + "/" + strings.TrimLeft(path, "/")
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	b.mu.Lock()
	key := b.apiKey
	b.mu.Unlock()
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return req, nil
}

func (b *Backend) emitDelta(runID, sessionKey, full string) {
	payload, _ := json.Marshal(protocol.ChatEvent{
		State:      "delta",
		RunID:      runID,
		SessionKey: sessionKey,
		Message:    json.RawMessage(mustJSON(full)),
	})
	b.send(protocol.Event{EventName: protocol.EventChat, Payload: payload})
}

func (b *Backend) emitFinal(runID, sessionKey, full string) {
	final := struct {
		Role    string   `json:"role"`
		Content []map[string]string `json:"content"`
	}{
		Role:    "assistant",
		Content: []map[string]string{{"type": "text", "text": full}},
	}
	finalRaw, _ := json.Marshal(final)
	payload, _ := json.Marshal(protocol.ChatEvent{
		State:      "final",
		RunID:      runID,
		SessionKey: sessionKey,
		Message:    finalRaw,
	})
	b.send(protocol.Event{EventName: protocol.EventChat, Payload: payload})
}

func (b *Backend) emitError(runID, sessionKey, msg string) {
	payload, _ := json.Marshal(protocol.ChatEvent{
		State:        "error",
		RunID:        runID,
		SessionKey:   sessionKey,
		ErrorMessage: msg,
	})
	b.send(protocol.Event{EventName: protocol.EventChat, Payload: payload})
}

func (b *Backend) emitAborted(runID, sessionKey string) {
	payload, _ := json.Marshal(protocol.ChatEvent{
		State:      "aborted",
		RunID:      runID,
		SessionKey: sessionKey,
	})
	b.send(protocol.Event{EventName: protocol.EventChat, Payload: payload})
}

func (b *Backend) send(ev protocol.Event) {
	defer func() {
		_ = recover() // events channel may be closed during teardown
	}()
	select {
	case b.events <- ev:
	default:
		// Drop on full channel — same policy as the OpenClaw client.
	}
}

func mustJSON(s string) string {
	buf, _ := json.Marshal(s)
	return string(buf)
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
