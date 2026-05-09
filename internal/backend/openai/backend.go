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

// DeleteAgent removes an agent from disk. When DeleteFiles is true
// the agent directory is wiped (os.RemoveAll); when false the
// directory is moved to <root>/.archive/<id>-<unixts>/ so the user
// can recover IDENTITY.md, SOUL.md and history.jsonl from disk
// later. Returns a wrapped error if the agent doesn't exist so a
// stale UI doesn't silent-succeed.
func (b *Backend) DeleteAgent(ctx context.Context, params backend.DeleteAgentParams) error {
	if _, err := b.store.LoadMeta(params.AgentID); err != nil {
		return fmt.Errorf("agent not found: %s: %w", params.AgentID, err)
	}
	if params.DeleteFiles {
		return b.store.Delete(params.AgentID)
	}
	return b.store.Archive(params.AgentID)
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
		if msg.Role == "system" && !msg.Summary {
			// system prompt is reconstructed from Identity/Soul each
			// turn; summaries written by /compact are forwarded.
			continue
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
		if msg.Role == "system" && !msg.Summary {
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
// no remote exec, no thinking, no usage. SessionCompact is supported
// via a local summarisation pass (see SessionCompact below).
func (b *Backend) Capabilities() backend.Capabilities {
	return backend.Capabilities{
		SessionCompact:  true,
		AuthRecovery:    backend.AuthRecoveryAPIKey,
		AgentManagement: true,
	}
}

// --- CompactBackend ---

// compactKeepTail is the number of most-recent messages /compact
// preserves verbatim. Anything older is summarised into a single
// system-role digest. Two full turns (user/assistant pairs) keep the
// model anchored on the immediate context while still giving the
// summary something meaningful to absorb.
const compactKeepTail = 4

// compactMinHistory is the minimum number of stored messages required
// before /compact will run a summarisation pass. Below this the saving
// is too small to be worth a model round-trip.
const compactMinHistory = compactKeepTail + 2

// compactSystemPrompt is the instruction sent to the model when
// summarising older turns. The output replaces those messages on disk
// as a single system-role entry, so the prompt asks for a dense,
// self-contained recap rather than chat-style prose.
const compactSystemPrompt = `You are compacting an ongoing chat transcript so the conversation can continue with a smaller context window. Produce a dense factual summary of the transcript provided, preserving:
- the user's goals and any open questions
- decisions, conclusions, and code or data the assistant produced
- any constraints, preferences, or facts the user established

Write the summary as a single passage in the third person ("the user asked …", "the assistant explained …"). Do not address the user directly, do not include greetings, and do not invent information. Output only the summary text.`

// compactUserPreamble introduces the transcript dump in the single
// role: user message that drives the summarisation. The transcript
// must arrive embedded in a user turn — forwarding it as the literal
// user/assistant message sequence makes the request end on
// role: assistant, which OpenAI-compatible servers (Ollama, vLLM,
// llama.cpp) interpret as "the conversation is complete" and either
// return an empty completion or generate a follow-up turn instead of
// the summary we asked for.
const compactUserPreamble = "Below is the transcript to summarise. Output only the summary text.\n\n"

// SessionCompact runs a local summarisation pass: load the agent's
// transcript, ask the configured model to summarise everything except
// the last few turns, and rewrite history.jsonl with the summary as a
// single system-role message followed by the preserved tail. The
// summary is forwarded on every subsequent turn (see runStream's
// Summary check) so the model retains the context at a fraction of the
// original token cost.
//
// Implements the local-summarisation pass described in issue #76 — the
// OpenAI-compatible backend has no gateway-side compact, so the
// equivalent behaviour is built here.
func (b *Backend) SessionCompact(ctx context.Context, sessionKey string) error {
	meta, err := b.store.LoadMeta(sessionKey)
	if err != nil {
		return fmt.Errorf("agent not found: %s", sessionKey)
	}
	model := meta.Model
	if model == "" {
		model = b.opts.DefaultModel
	}
	if model == "" {
		return fmt.Errorf("no model configured for agent %q (run /model to pick one)", sessionKey)
	}

	history, err := b.store.LoadHistory(sessionKey, 0)
	if err != nil {
		return fmt.Errorf("load history: %w", err)
	}
	if len(history) < compactMinHistory {
		// Nothing meaningful to compact yet — leave the transcript
		// untouched. Returning nil keeps /compact a no-op-success in
		// the chat view rather than a confusing error.
		return nil
	}
	cutoff := len(history) - compactKeepTail
	older := history[:cutoff]
	tail := history[cutoff:]

	summary, err := b.summarise(ctx, model, older)
	if err != nil {
		return err
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return fmt.Errorf("compact: model returned an empty summary")
	}

	rewritten := make([]Message, 0, 1+len(tail))
	rewritten = append(rewritten, Message{
		Role:    "system",
		Content: "Summary of earlier conversation:\n" + summary,
		Time:    time.Now().UTC(),
		Summary: true,
	})
	rewritten = append(rewritten, tail...)
	return b.store.RewriteHistory(sessionKey, rewritten)
}

// summarise issues a /v1/chat/completions request with the compaction
// prompt and returns the assistant's reply text. The older transcript
// is dumped into a single role: user message — including any prior
// summary produced by an earlier /compact, so the new digest folds
// the old one in rather than dropping it.
//
// Streaming is used here for the same reason it's used for regular
// chat sends: some OpenAI-compatible servers return an empty
// `message.content` on the non-streaming path while the streamed
// `delta.content` deltas produce the actual answer. Accumulating
// deltas is the well-trodden path that works across the whole
// compatibility matrix; the events channel is intentionally not
// touched so /compact stays invisible in the chat transcript.
//
// The transcript is rendered as a labelled text block inside one
// user turn rather than a sequence of user/assistant messages because
// the messages array must end with role: user for the model to
// generate a reply — ending on role: assistant (which our older slice
// usually does) tells Ollama/vLLM/llama.cpp the conversation is done
// and the model returns an empty completion or a stray follow-up.
func (b *Backend) summarise(ctx context.Context, model string, older []Message) (string, error) {
	transcript := renderTranscriptForCompact(older)
	if transcript == "" {
		return "", nil
	}
	body := chatRequest{Model: model, Stream: true}
	body.Messages = append(body.Messages,
		chatRequestMessage{Role: "system", Content: compactSystemPrompt},
		chatRequestMessage{Role: "user", Content: compactUserPreamble + transcript},
	)

	req, err := b.http.NewRequest(ctx, http.MethodPost, "/chat/completions", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := b.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("compact: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("api key required (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("compact: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var summary strings.Builder
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
		summary.WriteString(ev.Choices[0].Delta.Content)
		return false
	})
	if scanErr != nil {
		return "", fmt.Errorf("compact: %w", scanErr)
	}
	return summary.String(), nil
}

// --- APIKeyAuth ---

func (b *Backend) StoreAPIKey(key string) error {
	b.http.SetAPIKey(key)
	return nil
}

// --- internals ---

// renderTranscriptForCompact dumps the older slice of history as a
// labelled plain-text block ("user: …" / "assistant: …" / "summary: …")
// for embedding inside the single role: user message that drives the
// compaction. Earlier summaries written by a previous /compact are
// included so the new digest folds in detail accumulated across
// passes; non-summary system messages are filtered out defensively.
// Returns an empty string when nothing remains after filtering, so
// the caller can short-circuit a wasted round-trip.
func renderTranscriptForCompact(older []Message) string {
	var b strings.Builder
	for _, msg := range older {
		role := msg.Role
		if role == "system" {
			if !msg.Summary {
				continue
			}
			role = "summary"
		}
		if msg.Content == "" {
			continue
		}
		b.WriteString(role)
		b.WriteString(": ")
		b.WriteString(msg.Content)
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

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

// Compile-time assertions that Backend implements every interface it
// claims to. Sub-interfaces beyond the core Backend live behind the
// type assertions in the TUI's slash-command handlers — keeping these
// here means a refactor that drops a method gets caught at build time.
var (
	_ backend.Backend        = (*Backend)(nil)
	_ backend.CompactBackend = (*Backend)(nil)
	_ backend.APIKeyAuth     = (*Backend)(nil)
)

// removeFile deletes a single file inside a directory, ignoring
// "does not exist" errors so /reset works even on a fresh agent.
func removeFile(dir, name string) error {
	if err := osRemove(filepathJoin(dir, name)); err != nil && !osIsNotExist(err) {
		return err
	}
	return nil
}
