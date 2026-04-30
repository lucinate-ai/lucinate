// Package hermes is a Lucinate backend for Nous Research's Hermes
// Agent (https://github.com/nousresearch/hermes-agent). Hermes is
// stateful server-side: each profile owns its own SOUL, sessions,
// memories, and runs an OpenAI-shaped API on its own port. So unlike
// the OpenAI backend (which treats the remote as a stateless chat-
// completions sink and keeps client-side IDENTITY/SOUL/history), this
// backend is a thin client over Hermes' /v1/responses surface:
//
//   - Connection ↔ Hermes profile, 1:1. The profile is the agent.
//     ListAgents returns one synthetic entry; CreateAgent is rejected.
//   - ChatSend posts to /v1/responses with conversation:"<connID>",
//     letting Hermes maintain the dialog server-side. Streaming reads
//     the typed SSE events and emits chat-delta / chat-final events
//     in the existing Lucinate protocol shape.
//   - ChatHistory walks back the previous_response_id chain from a
//     locally-tracked last response ID. There's no list-by-
//     conversation endpoint upstream; the walk is bounded to a small
//     number of entries to keep first-load latency tolerable.
//
// HTTP/SSE/event-emission plumbing comes from internal/backend/
// httpcommon — the OpenAI backend uses the same primitives, applied
// against a different request/response shape.
package hermes

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
	"github.com/lucinate-ai/lucinate/internal/config"
)

// DefaultBaseURL is the loopback URL Hermes binds when API_SERVER_HOST
// is unset, with the /v1 suffix the OpenAI-compatible endpoints live
// under.
const DefaultBaseURL = "http://127.0.0.1:8642/v1"

// historyWalkLimit is the maximum number of previous_response_id hops
// the history fetch will follow on first load. The user accepted the
// 100-entry server-side LRU cap; we further bound this read because
// each hop is a separate round-trip and the chat view only needs the
// recent past for context. Larger walks slow first-render.
const historyWalkLimit = 3

// syntheticAgentID is the fixed agent identifier used because Hermes
// connections expose a single agent (the connected profile). The TUI
// uses this as both agent ID and session key.
const syntheticAgentID = "hermes"

// Options bundles the per-connection configuration the Backend needs.
type Options struct {
	ConnectionID   string
	BaseURL        string
	APIKey         string
	DefaultModel   string
	ConnectTimeout time.Duration

	// HTTPClient lets tests inject a fake transport.
	HTTPClient *http.Client
}

// Backend implements backend.Backend by translating /v1/responses
// SSE streams into protocol.ChatEvent messages. Server-side state
// (chain of responses, named conversation) is the source of truth;
// the only client-side persistence is a per-connection lastResponseID
// pointer for history walk-back.
type Backend struct {
	opts    Options
	http    *httpcommon.Client
	emitter *httpcommon.EventEmitter

	mu               sync.Mutex
	runs             map[string]context.CancelFunc
	runsWG           sync.WaitGroup // tracks in-flight runStream goroutines
	lastResponseID   string         // shadow of the on-disk pointer
	profileModelOnce sync.Once
	profileModel     string // discovered via /v1/models on first Connect
	prompts          *promptLog
}

// New constructs a Backend. BaseURL defaults to the loopback Hermes
// API server. ConnectionID is required so the local last-response-id
// pointer can be scoped per connection.
func New(opts Options) (*Backend, error) {
	if opts.ConnectionID == "" {
		return nil, fmt.Errorf("hermes backend: ConnectionID is required")
	}
	if opts.BaseURL == "" {
		opts.BaseURL = DefaultBaseURL
	}
	httpClient, err := httpcommon.NewClient(httpcommon.Options{
		BaseURL:        opts.BaseURL,
		APIKey:         opts.APIKey,
		HTTPClient:     opts.HTTPClient,
		ConnectTimeout: opts.ConnectTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("hermes backend: %w", err)
	}
	prompts, err := newPromptLog(opts.ConnectionID)
	if err != nil {
		return nil, fmt.Errorf("hermes backend: prompt log: %w", err)
	}
	b := &Backend{
		opts:    opts,
		http:    httpClient,
		emitter: httpcommon.NewEventEmitter(64),
		runs:    map[string]context.CancelFunc{},
		prompts: prompts,
	}
	if id, err := readLastResponseID(opts.ConnectionID); err == nil {
		b.lastResponseID = id
	}
	return b, nil
}

// Connect verifies the endpoint by hitting /v1/models and caches the
// discovered profile name as the synthetic agent's model.
func (b *Backend) Connect(ctx context.Context) error {
	models, err := b.fetchModels(ctx)
	if err != nil {
		return err
	}
	b.profileModelOnce.Do(func() {
		if len(models) > 0 {
			b.profileModel = models[0]
		}
	})
	return nil
}

// Close cancels in-flight runs, waits for their goroutines to flush
// any final disk writes, then closes the events channel. Without the
// wait, abort + teardown can race the prompt-log append and leave the
// per-test HOME tempdir non-empty when t.Cleanup tries to remove it.
func (b *Backend) Close() error {
	b.mu.Lock()
	for _, cancel := range b.runs {
		cancel()
	}
	b.runs = nil
	b.mu.Unlock()
	b.runsWG.Wait()
	b.emitter.Close()
	return nil
}

// Events returns the event channel.
func (b *Backend) Events() <-chan protocol.Event { return b.emitter.Channel() }

// Supervise emits a single connected transition and blocks. Hermes is
// a request/response HTTP service with no long-lived link to babysit.
func (b *Backend) Supervise(ctx context.Context, notify func(client.ConnState)) {
	notify(client.ConnState{Status: client.StatusConnected})
	<-ctx.Done()
}

// ListAgents returns a single synthetic entry — the connected Hermes
// profile is the agent. The TUI shows it in the agent picker so the
// existing "pick an agent then chat" flow still works without forking
// a Hermes-specific path.
func (b *Backend) ListAgents(ctx context.Context) (*protocol.AgentsListResult, error) {
	model := b.opts.DefaultModel
	if model == "" {
		model = b.profileModel
	}
	summary := protocol.AgentSummary{
		ID:   syntheticAgentID,
		Name: profileDisplayName(model),
	}
	if model != "" {
		summary.Model = &protocol.AgentSummaryModel{Primary: model}
	}
	return &protocol.AgentsListResult{
		Agents:    []protocol.AgentSummary{summary},
		DefaultID: syntheticAgentID,
		MainKey:   syntheticAgentID,
	}, nil
}

// CreateAgent rejects: Hermes profiles are configured server-side
// (`hermes profile create`) and are not user-creatable from a chat
// client. The TUI should hide its "new agent" button via the
// AgentManagement capability flag, but rejecting here is the
// belt-and-braces guard if that path is ever bypassed.
func (b *Backend) CreateAgent(ctx context.Context, params backend.CreateAgentParams) error {
	return fmt.Errorf("agents are not user-creatable on Hermes connections; configure profiles with `hermes profile create`")
}

// SessionsList returns one synthetic session for the synthetic agent.
// Last-message and updatedAt are best-effort: we hit the last
// response we know about for the title preview, but if there's no
// pointer yet we return an empty session so the TUI's session picker
// renders a clean "ready to chat" state.
func (b *Backend) SessionsList(ctx context.Context, agentID string) (json.RawMessage, error) {
	if agentID != syntheticAgentID {
		return json.RawMessage(`{"sessions":[]}`), nil
	}
	entry := map[string]any{
		"key":   syntheticAgentID,
		"title": "Hermes",
	}
	b.mu.Lock()
	last := b.lastResponseID
	b.mu.Unlock()
	if last != "" {
		if r, err := b.fetchResponse(ctx, last); err == nil {
			entry["lastMessage"] = r.text()
			entry["updatedAt"] = r.CreatedAt * 1000
		}
	}
	return json.Marshal(map[string]any{"sessions": []any{entry}})
}

// CreateSession is a no-op — there's only one session per Hermes
// connection (the synthetic agent's). Returns the agent ID so the TUI
// uses it consistently as the session key.
func (b *Backend) CreateSession(ctx context.Context, agentID, key string) (string, error) {
	if agentID != syntheticAgentID {
		return "", fmt.Errorf("agent not found: %s", agentID)
	}
	return syntheticAgentID, nil
}

// SessionDelete clears the local last-response pointer and prompts
// log so the next chat turn starts a fresh Hermes conversation.
// Server-side history is left alone — Hermes' SQLite store ages out
// via its own LRU.
func (b *Backend) SessionDelete(ctx context.Context, sessionKey string) error {
	b.mu.Lock()
	b.lastResponseID = ""
	b.mu.Unlock()
	if err := clearLastResponseID(b.opts.ConnectionID); err != nil {
		return err
	}
	return b.prompts.Clear()
}

// ChatSend posts the user message to /v1/responses chained off the
// last response ID we got back. Hermes maintains chain continuity
// server-side, so we don't resend history each turn; clearing the
// local last-response pointer (via /reset / SessionDelete) yields a
// fresh chain on the next turn.
func (b *Backend) ChatSend(ctx context.Context, sessionKey string, params backend.ChatSendParams) (*protocol.ChatSendResult, error) {
	if sessionKey != syntheticAgentID {
		return nil, fmt.Errorf("session not found: %s", sessionKey)
	}
	model := b.opts.DefaultModel
	if model == "" {
		model = b.profileModel
	}
	if model == "" {
		return nil, fmt.Errorf("no model configured for Hermes connection")
	}

	runID := fmt.Sprintf("run-%d", time.Now().UnixNano())
	streamCtx, cancel := context.WithCancel(context.Background())
	b.mu.Lock()
	b.runs[runID] = cancel
	b.mu.Unlock()

	b.runsWG.Add(1)
	go func() {
		defer b.runsWG.Done()
		b.runStream(streamCtx, runID, sessionKey, model, params.Message)
	}()
	return &protocol.ChatSendResult{RunID: runID}, nil
}

// runStream issues the streaming POST and parses Hermes' typed SSE
// events. We accumulate `response.output_text.delta` content and emit
// it as Lucinate chat-delta events, then close the run on
// `response.completed`.
func (b *Backend) runStream(ctx context.Context, runID, sessionKey, model, userMessage string) {
	defer func() {
		b.mu.Lock()
		delete(b.runs, runID)
		b.mu.Unlock()
	}()

	// Chain via previous_response_id rather than a named conversation
	// so /reset (which clears lastResponseID) actually starts a fresh
	// server-side chat. A named conversation would be remembered on
	// the Hermes side regardless of local state.
	b.mu.Lock()
	prev := b.lastResponseID
	b.mu.Unlock()
	body := responsesRequest{
		Model:              model,
		Input:              userMessage,
		Stream:             true,
		Store:              true,
		PreviousResponseID: prev,
	}

	req, err := b.http.NewRequest(ctx, http.MethodPost, "/responses", body)
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

	var (
		assistant      strings.Builder
		newResponseID  string
		streamErrorMsg string
	)
	scanErr := httpcommon.ScanSSE(resp.Body, func(payload string) bool {
		var ev responsesEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return false
		}
		switch ev.Type {
		case "response.created":
			if ev.Response != nil {
				newResponseID = ev.Response.ID
			}
		case "response.output_text.delta":
			if ev.Delta != "" {
				assistant.WriteString(ev.Delta)
				b.emitter.EmitChatDelta(runID, sessionKey, assistant.String())
			}
		case "response.completed":
			if ev.Response != nil && ev.Response.ID != "" {
				newResponseID = ev.Response.ID
			}
			return true
		case "response.failed", "error":
			streamErrorMsg = ev.errorMessage()
			return true
		}
		return false
	})
	if scanErr != nil {
		b.emitter.EmitChatError(runID, sessionKey, scanErr.Error())
		return
	}
	if streamErrorMsg != "" {
		b.emitter.EmitChatError(runID, sessionKey, streamErrorMsg)
		return
	}

	if newResponseID != "" {
		b.mu.Lock()
		b.lastResponseID = newResponseID
		b.mu.Unlock()
		_ = writeLastResponseID(b.opts.ConnectionID, newResponseID)
		_ = b.prompts.Append(newResponseID, userMessage, time.Now().UnixMilli())
	}
	b.emitter.EmitChatFinal(runID, sessionKey, assistant.String())
}

// ChatAbort cancels the streaming context, which closes the SSE
// connection and triggers the aborted event. Hermes' Runs API has a
// dedicated /stop endpoint for tool-heavy turns, but for plain
// streaming chat the connection-close path is enough.
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

// ChatHistory walks the previous_response_id chain back from the
// stored last response ID, capped at historyWalkLimit hops because
// each hop is a separate round-trip and there's no list endpoint
// upstream. Returns the chain in user→assistant order so the TUI
// renders it as a normal transcript.
func (b *Backend) ChatHistory(ctx context.Context, sessionKey string, limit int) (json.RawMessage, error) {
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

	b.mu.Lock()
	cursor := b.lastResponseID
	b.mu.Unlock()

	walked := []*responseRecord{}
	for hops := 0; hops < historyWalkLimit && cursor != ""; hops++ {
		r, err := b.fetchResponse(ctx, cursor)
		if err != nil {
			break
		}
		walked = append(walked, r)
		cursor = r.PreviousResponseID
	}

	// Walked in reverse order (newest first); flip and emit
	// user-message + assistant-message pairs in chronological order.
	// Hermes' GET /v1/responses/{id} returns only the assistant
	// output, so we look up the user prompt from the local prompts
	// log keyed by response ID.
	for i := len(walked) - 1; i >= 0; i-- {
		r := walked[i]
		ts := r.CreatedAt * 1000 // seconds → ms
		userText := r.userInput()
		if userText == "" {
			if rec, ok, _ := b.prompts.Lookup(r.ID); ok {
				userText = rec.Prompt
			}
		}
		if userText != "" {
			out.Messages = append(out.Messages, historyMsg{
				Role:      "user",
				Content:   []block{{Type: "text", Text: userText}},
				Timestamp: ts,
			})
		}
		if assistant := r.text(); assistant != "" {
			out.Messages = append(out.Messages, historyMsg{
				Role:      "assistant",
				Content:   []block{{Type: "text", Text: assistant}},
				Timestamp: ts,
			})
		}
	}
	return json.Marshal(out)
}

// ModelsList returns the upstream /v1/models response in protocol
// shape. Hermes advertises one entry — the profile name — but we
// keep the list behaviour symmetrical with OpenAI for the model
// picker UI.
func (b *Backend) ModelsList(ctx context.Context) (*protocol.ModelsListResult, error) {
	models, err := b.fetchModels(ctx)
	if err != nil {
		return nil, err
	}
	out := &protocol.ModelsListResult{}
	for _, m := range models {
		out.Models = append(out.Models, protocol.ModelChoice{ID: m, Name: m})
	}
	return out, nil
}

// SessionPatchModel is a no-op error: the model is pinned in the
// Hermes profile config (`model.default` in cli-config.yaml). Runtime
// override would be possible per-request via the OpenAI-style `model`
// field, but Hermes still routes inference using the profile's
// configured upstream — changing the request field is misleading.
func (b *Backend) SessionPatchModel(ctx context.Context, sessionKey, modelID string) error {
	return fmt.Errorf("model is configured in the Hermes profile, not at runtime; edit ~/.hermes/profiles/<name>/config.yaml on the host")
}

// Capabilities reports the trimmed Hermes feature surface: API-key
// auth, no agent management, no remote exec, no compact, etc.
func (b *Backend) Capabilities() backend.Capabilities {
	return backend.Capabilities{
		AuthRecovery:    backend.AuthRecoveryAPIKey,
		AgentManagement: false,
	}
}

// --- APIKeyAuth ---

func (b *Backend) StoreAPIKey(key string) error {
	b.http.SetAPIKey(key)
	return nil
}

// --- internals ---

// fetchModels hits /v1/models and returns the discovered IDs.
func (b *Backend) fetchModels(ctx context.Context) ([]string, error) {
	req, err := b.http.NewRequest(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("api key required (HTTP %d)", resp.StatusCode)
	}
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
	out := make([]string, 0, len(raw.Data))
	for _, m := range raw.Data {
		out = append(out, m.ID)
	}
	return out, nil
}

// fetchResponse retrieves a single response by ID. Used by ChatHistory
// to walk back the chain and by SessionsList to populate the
// last-message preview.
func (b *Backend) fetchResponse(ctx context.Context, id string) (*responseRecord, error) {
	req, err := b.http.NewRequest(ctx, http.MethodGet, "/responses/"+id, nil)
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
		return nil, fmt.Errorf("get response: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var r responseRecord
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &r, nil
}

// profileDisplayName picks the visible label for the synthetic agent.
// Falls back to "Hermes" when the model wasn't discovered.
func profileDisplayName(model string) string {
	if model == "" {
		return "Hermes"
	}
	return model
}

// --- request/response types ---

type responsesRequest struct {
	Model              string `json:"model"`
	Input              string `json:"input"`
	Stream             bool   `json:"stream"`
	Store              bool   `json:"store,omitempty"`
	Conversation       string `json:"conversation,omitempty"`
	PreviousResponseID string `json:"previous_response_id,omitempty"`
}

// responsesEvent matches the SSE payload Hermes emits on
// /v1/responses streams. The fields are sparse on purpose —
// different event types populate different subsets.
type responsesEvent struct {
	Type     string            `json:"type"`
	Response *responseRecord   `json:"response,omitempty"`
	Delta    string            `json:"delta,omitempty"`
	Error    *responseErrorObj `json:"error,omitempty"`
	Message  string            `json:"message,omitempty"`
}

func (e responsesEvent) errorMessage() string {
	if e.Error != nil && e.Error.Message != "" {
		return e.Error.Message
	}
	if e.Message != "" {
		return e.Message
	}
	return "stream error"
}

type responseErrorObj struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// responseRecord is the subset of the Responses API "response" object
// we care about for chain walking and content extraction.
type responseRecord struct {
	ID                 string           `json:"id"`
	Status             string           `json:"status"`
	CreatedAt          int64            `json:"created_at"`
	Model              string           `json:"model"`
	PreviousResponseID string           `json:"previous_response_id"`
	Input              json.RawMessage  `json:"input,omitempty"`
	Output             []responseOutput `json:"output,omitempty"`
}

type responseOutput struct {
	Type    string                  `json:"type"`
	Role    string                  `json:"role"`
	Content []responseOutputContent `json:"content,omitempty"`
}

type responseOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// text returns the assistant's reply text from the response output,
// concatenating any output_text content blocks.
func (r *responseRecord) text() string {
	var b strings.Builder
	for _, o := range r.Output {
		if o.Role != "" && o.Role != "assistant" {
			continue
		}
		for _, c := range o.Content {
			if c.Type == "output_text" {
				b.WriteString(c.Text)
			}
		}
	}
	return b.String()
}

// userInput returns the user's input string for this response. The
// API accepts either a raw string or a structured array; we handle
// both shapes for chain walking.
func (r *responseRecord) userInput() string {
	if len(r.Input) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(r.Input, &s); err == nil {
		return s
	}
	var arr []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(r.Input, &arr); err != nil {
		return ""
	}
	var b strings.Builder
	for _, item := range arr {
		if item.Text != "" {
			b.WriteString(item.Text)
			continue
		}
		for _, c := range item.Content {
			if c.Type == "input_text" || c.Type == "text" {
				b.WriteString(c.Text)
			}
		}
	}
	return b.String()
}

// --- last-response-id pointer persistence ---

// lastResponseIDPath returns ~/.lucinate/hermes/<connID>/last_response_id.
// Stored separately from the OpenAI agent store: we don't share a
// directory layout because the data semantics are different.
func lastResponseIDPath(connID string) (string, error) {
	dir, err := config.DataDir()
	if err != nil {
		return "", err
	}
	scoped := filepath.Join(dir, "hermes", connID)
	if err := os.MkdirAll(scoped, 0700); err != nil {
		return "", err
	}
	return filepath.Join(scoped, "last_response_id"), nil
}

func readLastResponseID(connID string) (string, error) {
	path, err := lastResponseIDPath(connID)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func writeLastResponseID(connID, id string) error {
	path, err := lastResponseIDPath(connID)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(id), 0600)
}

func clearLastResponseID(connID string) error {
	path, err := lastResponseIDPath(connID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
