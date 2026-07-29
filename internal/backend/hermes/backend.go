// Package hermes is a Lucinate backend for Nous Research's Hermes
// Agent (https://github.com/nousresearch/hermes-agent). It speaks the
// tui_gateway JSON-RPC protocol over a WebSocket to the `hermes
// dashboard` gateway (endpoint /api/ws, default port 9119) — the same
// protocol upstream's desktop app and web dashboard use.
//
// Hermes is stateful server-side: each profile owns its own SOUL,
// sessions, and memories, so this backend stays thin and lets the
// gateway be the source of truth:
//
//   - Connection ↔ Hermes profile, 1:1. The profile is the agent.
//     ListAgents returns one synthetic entry; CreateAgent is rejected.
//   - Sessions, history, usage, compaction, and abort are all real
//     gateway RPCs (session.*, prompt.submit) — no client-side state
//     files or history reconstruction.
//   - Server event notifications (message.*, tool.*, thinking.*) are
//     mapped to protocol events by the Translator in translate.go;
//     the golden payload shapes live in testdata/events/.
//
// The generic JSON-RPC/WebSocket client lives in the rpc subpackage.
package hermes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/backend/hermes/rpc"
	"github.com/lucinate-ai/lucinate/internal/client"
)

// DefaultBaseURL is the local `hermes dashboard` gateway. Note this is
// not the legacy API server (`gateway run`, port 8642) — that process
// does not serve /api/ws.
const DefaultBaseURL = "http://localhost:9119"

// syntheticAgentID is the fixed agent identifier used because Hermes
// connections expose a single agent (the connected profile).
const syntheticAgentID = "hermes"

// defaultConnectTimeout bounds the dial + gateway.ready handshake when
// the caller didn't configure one.
const defaultConnectTimeout = 10 * time.Second

// legacyEndpointHint is the migration error for connections still
// pointing at the pre-WebSocket Hermes API server. There is no silent
// auto-migration: the user has to switch the server process they run.
const legacyEndpointHint = "this connection points at the legacy Hermes API server; " +
	"Hermes connections now use the `hermes dashboard` gateway — run `hermes dashboard`, " +
	"edit the connection URL (default http://localhost:9119), and paste the gateway token"

// Options bundles the per-connection configuration the Backend needs.
type Options struct {
	ConnectionID string
	// BaseURL is the HTTP(S) base of the `hermes dashboard` gateway;
	// the /api/ws WebSocket URL is derived from it.
	BaseURL string
	// APIKey is the gateway session token (HERMES_DASHBOARD_SESSION_TOKEN),
	// stored in the same per-connection secret slot the old bearer key
	// used so the existing auth-modal plumbing carries over.
	APIKey         string
	ConnectTimeout time.Duration
}

// session tracks the two id-spaces the gateway exposes: the short live
// handle (used by prompt.submit / session.history / session.usage /
// session.interrupt during a connection) and the timestamped stored id
// (the one session.list reports, and the resume key across
// reconnects). Empty sessions aren't listed until they hold a message.
type session struct {
	live      string
	stored    string
	preambled bool // skills catalogue already prepended on turn 1
}

// Backend implements backend.Backend over the tui_gateway protocol.
type Backend struct {
	opts Options

	events chan protocol.Event

	mu       sync.Mutex
	cli      *rpc.Client
	tr       *Translator
	token    string
	model    string              // active model reported by the gateway
	profile  string              // profile name from session.create info
	sessions map[string]*session // TUI-facing session key → session
	active   string              // key of the most recently used session
	closed   bool
}

// New constructs a Backend. BaseURL defaults to the local dashboard
// gateway.
func New(opts Options) (*Backend, error) {
	if opts.ConnectionID == "" {
		return nil, fmt.Errorf("hermes backend: ConnectionID is required")
	}
	if opts.BaseURL == "" {
		opts.BaseURL = DefaultBaseURL
	}
	if opts.ConnectTimeout <= 0 {
		opts.ConnectTimeout = defaultConnectTimeout
	}
	return &Backend{
		opts:     opts,
		events:   make(chan protocol.Event, 64),
		tr:       newTranslator(),
		token:    opts.APIKey,
		sessions: make(map[string]*session),
	}, nil
}

// wsURL derives the ws(s)://…/api/ws?token=… endpoint from the
// connection's HTTP base URL.
func (b *Backend) wsURL() (string, error) {
	u, err := url.Parse(b.opts.BaseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base URL: %w", err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "ws", "wss":
		// already a websocket scheme
	default:
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	u.Path = "/api/ws"
	b.mu.Lock()
	token := b.token
	b.mu.Unlock()
	if token != "" {
		q := u.Query()
		q.Set("token", token)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// isLegacyEndpoint recognises URLs still pointing at the pre-WebSocket
// API server: the old default port, or the /v1 path prefix the
// OpenAI-compatible surface lived under.
func isLegacyEndpoint(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	if u.Port() == "8642" {
		return true
	}
	return strings.Contains(u.Path, "/v1")
}

// Connect dials the gateway WebSocket, waits for the gateway.ready
// handshake, then loads the agent snapshot and active model. A refused
// upgrade with HTTP 403 is the gateway's auth rejection and maps to
// the canonical api-key-required error so the connecting view opens
// the token modal.
func (b *Backend) Connect(ctx context.Context) error {
	if isLegacyEndpoint(b.opts.BaseURL) {
		return errors.New(legacyEndpointHint)
	}
	cli, err := b.dial(ctx)
	if err != nil {
		return err
	}

	b.mu.Lock()
	b.cli = cli
	b.mu.Unlock()
	go b.pump(cli)

	// Best-effort metadata: the connection is usable without either.
	callCtx, cancel := context.WithTimeout(ctx, b.opts.ConnectTimeout)
	defer cancel()
	var agents struct {
		Processes []json.RawMessage `json:"processes"`
	}
	_ = cli.Call(callCtx, "agents.list", nil, &agents)
	var models struct {
		Model string `json:"model"`
	}
	if err := cli.Call(callCtx, "model.options", nil, &models); err == nil && models.Model != "" {
		b.mu.Lock()
		b.model = models.Model
		b.mu.Unlock()
	}
	return nil
}

// dial opens the WebSocket and consumes frames until gateway.ready.
// The handshake read happens before the pump starts, so no frame is
// lost between the two.
func (b *Backend) dial(ctx context.Context) (*rpc.Client, error) {
	wsURL, err := b.wsURL()
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, b.opts.ConnectTimeout)
	defer cancel()
	cli, err := rpc.Dial(dialCtx, wsURL, http.Header{})
	if err != nil {
		var ue *rpc.UpgradeError
		if errors.As(err, &ue) && (ue.StatusCode == http.StatusForbidden || ue.StatusCode == http.StatusUnauthorized) {
			return nil, fmt.Errorf("api key required (HTTP %d)", ue.StatusCode)
		}
		if errors.As(err, &ue) && ue.StatusCode == http.StatusNotFound && isLegacyEndpoint(b.opts.BaseURL) {
			return nil, errors.New(legacyEndpointHint)
		}
		return nil, fmt.Errorf("hermes gateway: %w", err)
	}

	ready := time.NewTimer(b.opts.ConnectTimeout)
	defer ready.Stop()
	for {
		select {
		case n, ok := <-cli.Notifications():
			if !ok {
				_ = cli.Close()
				return nil, fmt.Errorf("hermes gateway: connection closed before gateway.ready")
			}
			var env eventEnvelope
			if json.Unmarshal(n.Params, &env) == nil && env.Type == "gateway.ready" {
				return cli, nil
			}
			// Unexpected pre-ready frame; keep waiting.
		case <-ready.C:
			_ = cli.Close()
			return nil, fmt.Errorf("hermes gateway: timed out waiting for gateway.ready")
		case <-ctx.Done():
			_ = cli.Close()
			return nil, ctx.Err()
		}
	}
}

// pump translates server notifications into protocol events until the
// client's notification channel closes. Interactive asks are declined
// inline (the respond RPC runs async; the visible notice is injected
// into the streaming text synchronously so ordering stays sane).
func (b *Backend) pump(cli *rpc.Client) {
	for n := range cli.Notifications() {
		b.mu.Lock()
		events, ask := b.tr.Translate(n)
		if ask != nil {
			events = append(events, b.tr.InjectSystemLine(ask.SessionID, declineNotice(ask)))
		}
		b.mu.Unlock()
		for _, ev := range events {
			b.emit(ev)
		}
		if ask != nil {
			go b.declineAsk(cli, ask)
		}
	}
}

// declineNotice is the visible explanation for an auto-declined ask.
func declineNotice(ask *Ask) string {
	kind := strings.TrimSuffix(ask.Type, ".request")
	return fmt.Sprintf("Hermes sent a %s request — auto-declined; interactive %s isn't supported on this connection yet. Configure the profile for autonomous approval.", kind, kind)
}

// declineAsk answers a blocking server→client ask with a deny/cancel
// so the turn can never hang the TUI silently. Phase 2 wires
// approval.request into the TUI's exec-approval prompt instead.
func (b *Backend) declineAsk(cli *rpc.Client, ask *Ask) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	params := map[string]any{"session_id": ask.SessionID, "request_id": ask.RequestID}
	var method string
	switch ask.Type {
	case "approval.request":
		method = "approval.respond"
		params["choice"] = "deny"
	case "clarify.request":
		method = "clarify.respond"
		params["answer"] = ""
	case "sudo.request":
		method = "sudo.respond"
		params["password"] = ""
	case "secret.request":
		method = "secret.respond"
		params["value"] = ""
	default:
		return
	}
	_ = cli.Call(ctx, method, params, nil)
}

// emit forwards an event, dropping it if the buffer is full (matching
// the policy every other backend uses) and tolerating a closed channel
// during teardown.
func (b *Backend) emit(ev protocol.Event) {
	defer func() { _ = recover() }()
	select {
	case b.events <- ev:
	default:
	}
}

// Close tears down the connection and the events channel.
func (b *Backend) Close() error {
	b.mu.Lock()
	cli := b.cli
	b.cli = nil
	closed := b.closed
	b.closed = true
	b.mu.Unlock()
	if cli != nil {
		_ = cli.Close()
	}
	if !closed {
		defer func() { _ = recover() }()
		close(b.events)
	}
	return nil
}

// Events returns the event channel.
func (b *Backend) Events() <-chan protocol.Event { return b.events }

// Supervise is a real reconnect loop: it watches the live client, and
// when the connection drops it redials with exponential backoff,
// resumes the active session before reporting healthy (the gateway
// detaches sessions on disconnect), and hands the fresh client to a
// new pump.
func (b *Backend) Supervise(ctx context.Context, notify func(client.ConnState)) {
	notify(client.ConnState{Status: client.StatusConnected})
	for {
		b.mu.Lock()
		cli := b.cli
		b.mu.Unlock()
		if cli == nil {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-cli.Done():
		}
		b.mu.Lock()
		wasClosed := b.closed
		b.mu.Unlock()
		if wasClosed {
			return
		}
		notify(client.ConnState{Status: client.StatusDisconnected})

		backoff := time.Second
		attempt := 0
		for {
			attempt++
			notify(client.ConnState{Status: client.StatusReconnecting, Attempt: attempt})
			next, err := b.dial(ctx)
			if err == nil {
				b.mu.Lock()
				b.cli = next
				b.mu.Unlock()
				go b.pump(next)
				b.resumeActive(ctx, next)
				notify(client.ConnState{Status: client.StatusConnected})
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	}
}

// resumeActive re-attaches the most recently used session after a
// reconnect. The old live handle died with the previous socket, so the
// stored id is the resume key; the fresh live handle replaces it.
func (b *Backend) resumeActive(ctx context.Context, cli *rpc.Client) {
	b.mu.Lock()
	key := b.active
	sess := b.sessions[key]
	b.mu.Unlock()
	if sess == nil || sess.stored == "" {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, b.opts.ConnectTimeout)
	defer cancel()
	var res sessionCreateResult
	if err := cli.Call(callCtx, "session.resume", map[string]string{"session_id": sess.stored}, &res); err != nil {
		return
	}
	b.mu.Lock()
	sess.live = res.SessionID
	if res.StoredSessionID != "" {
		sess.stored = res.StoredSessionID
	}
	b.mu.Unlock()
}

// client returns the live rpc client or a retryable error.
func (b *Backend) client() (*rpc.Client, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cli == nil {
		return nil, fmt.Errorf("hermes gateway not connected")
	}
	return b.cli, nil
}

// ListAgents returns a single synthetic entry — the connected Hermes
// profile is the agent. agents.list reports gateway-managed processes
// (usually none); the profile identity comes from session metadata.
func (b *Backend) ListAgents(ctx context.Context) (*protocol.AgentsListResult, error) {
	b.mu.Lock()
	model := b.model
	profile := b.profile
	b.mu.Unlock()
	name := "Hermes"
	if profile != "" && profile != "default" {
		name = "Hermes (" + profile + ")"
	} else if model != "" {
		name = model
	}
	summary := protocol.AgentSummary{ID: syntheticAgentID, Name: name}
	if model != "" {
		summary.Model = &protocol.AgentSummaryModel{Primary: model}
	}
	return &protocol.AgentsListResult{
		Agents:    []protocol.AgentSummary{summary},
		DefaultID: syntheticAgentID,
		MainKey:   syntheticAgentID,
	}, nil
}

// CreateAgent rejects: Hermes profiles are configured server-side and
// are not user-creatable from a chat client. AgentManagement=false
// hides the affordance; this is the belt-and-braces guard.
func (b *Backend) CreateAgent(ctx context.Context, params backend.CreateAgentParams) error {
	return fmt.Errorf("agents are not user-creatable on Hermes connections; configure profiles on the host")
}

// DeleteAgent rejects for the same reason as CreateAgent.
func (b *Backend) DeleteAgent(ctx context.Context, params backend.DeleteAgentParams) error {
	return fmt.Errorf("agents are not user-deletable on Hermes connections; manage profiles on the host")
}

// sessionCreateResult is the shared result shape of session.create and
// session.resume: the live handle, the persisted id, and the session
// metadata snapshot.
type sessionCreateResult struct {
	SessionID       string `json:"session_id"`
	StoredSessionID string `json:"stored_session_id"`
	Info            struct {
		Model       string `json:"model"`
		ProfileName string `json:"profile_name"`
	} `json:"info"`
}

// sessionListEntry is one row of session.list. The id is the stored
// session id; only sessions with at least one message are reported.
type sessionListEntry struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Preview      string  `json:"preview"`
	StartedAt    float64 `json:"started_at"`
	MessageCount int     `json:"message_count"`
}

// SessionsList maps session.list into the TUI's session-browser shape.
// Entries are keyed by stored session id — the resume key.
func (b *Backend) SessionsList(ctx context.Context, agentID string) (json.RawMessage, error) {
	if agentID != syntheticAgentID {
		return json.RawMessage(`{"sessions":[]}`), nil
	}
	cli, err := b.client()
	if err != nil {
		return nil, err
	}
	var res struct {
		Sessions []sessionListEntry `json:"sessions"`
	}
	if err := cli.Call(ctx, "session.list", nil, &res); err != nil {
		return nil, err
	}
	out := struct {
		Sessions []map[string]any `json:"sessions"`
	}{Sessions: []map[string]any{}}
	for _, s := range res.Sessions {
		title := s.Title
		if title == "" {
			title = s.ID
		}
		out.Sessions = append(out.Sessions, map[string]any{
			"key":         s.ID,
			"title":       title,
			"lastMessage": s.Preview,
			"updatedAt":   int64(s.StartedAt * 1000),
		})
	}
	return json.Marshal(out)
}

// CreateSession opens a fresh gateway session and returns its live
// handle as the TUI session key. The stored id is tracked alongside so
// the session survives a reconnect (and so /status can show it).
func (b *Backend) CreateSession(ctx context.Context, agentID, key string) (string, error) {
	if agentID != syntheticAgentID {
		return "", fmt.Errorf("agent not found: %s", agentID)
	}
	cli, err := b.client()
	if err != nil {
		return "", err
	}
	var res sessionCreateResult
	if err := cli.Call(ctx, "session.create", nil, &res); err != nil {
		return "", err
	}
	b.mu.Lock()
	b.sessions[res.SessionID] = &session{live: res.SessionID, stored: res.StoredSessionID}
	b.active = res.SessionID
	if res.Info.Model != "" {
		b.model = res.Info.Model
	}
	if res.Info.ProfileName != "" {
		b.profile = res.Info.ProfileName
	}
	b.mu.Unlock()
	return res.SessionID, nil
}

// resolve maps a TUI session key to its live gateway handle, resuming
// the session when the key is a stored id from the session browser
// (or a live handle orphaned by a reconnect).
func (b *Backend) resolve(ctx context.Context, key string) (*session, error) {
	b.mu.Lock()
	sess, ok := b.sessions[key]
	b.mu.Unlock()
	if ok && sess.live != "" {
		b.mu.Lock()
		b.active = key
		b.mu.Unlock()
		return sess, nil
	}
	cli, err := b.client()
	if err != nil {
		return nil, err
	}
	var res sessionCreateResult
	if err := cli.Call(ctx, "session.resume", map[string]string{"session_id": key}, &res); err != nil {
		return nil, fmt.Errorf("session not found: %s: %w", key, err)
	}
	b.mu.Lock()
	sess = &session{live: res.SessionID, stored: res.StoredSessionID}
	if sess.stored == "" {
		sess.stored = key
	}
	b.sessions[key] = sess
	b.active = key
	b.mu.Unlock()
	return sess, nil
}

// SessionDelete removes the session server-side. The gateway refuses
// to delete a session that is still attached (code 4023), so an open
// live handle is closed first (session.close), then the persisted row
// is deleted by its stored id — after close, the live handle is gone
// and only the stored id resolves.
func (b *Backend) SessionDelete(ctx context.Context, sessionKey string) error {
	cli, err := b.client()
	if err != nil {
		return err
	}
	id := sessionKey
	var live string
	b.mu.Lock()
	if sess, ok := b.sessions[sessionKey]; ok {
		live = sess.live
		if sess.stored != "" {
			id = sess.stored
		}
	}
	delete(b.sessions, sessionKey)
	if b.active == sessionKey {
		b.active = ""
	}
	b.mu.Unlock()
	if live != "" {
		// Best-effort detach; a session opened by another client or
		// already closed just falls through to the delete.
		_ = cli.Call(ctx, "session.close", map[string]string{"session_id": live}, nil)
	}
	return cli.Call(ctx, "session.delete", map[string]string{"session_id": id}, nil)
}

// ChatSend submits a turn via prompt.submit. The gateway answers
// {"status":"streaming"} and the content arrives as events; the
// Translator carries the run id onto them. On the session's first turn
// the local skills catalogue is prepended as a System: preamble, the
// same convention the OpenAI backend uses (and the history layer
// strips System:-prefixed lines on render).
func (b *Backend) ChatSend(ctx context.Context, sessionKey string, params backend.ChatSendParams) (*protocol.ChatSendResult, error) {
	sess, err := b.resolve(ctx, sessionKey)
	if err != nil {
		return nil, err
	}
	cli, err := b.client()
	if err != nil {
		return nil, err
	}

	runID := params.IdempotencyKey
	if runID == "" {
		runID = fmt.Sprintf("run-%d", time.Now().UnixNano())
	}

	text := params.Message
	b.mu.Lock()
	if !sess.preambled {
		if preamble := skillsPreamble(params.Skills); preamble != "" {
			text = preamble + "\n\n" + text
		}
		sess.preambled = true
	}
	b.tr.SetRun(sess.live, runID)
	b.mu.Unlock()

	var res struct {
		Status string `json:"status"`
	}
	if err := cli.Call(ctx, "prompt.submit", map[string]string{"session_id": sess.live, "text": text}, &res); err != nil {
		return nil, err
	}
	return &protocol.ChatSendResult{RunID: runID, Status: res.Status}, nil
}

// skillsPreamble formats the local skill catalogue for the turn-1
// system preamble. Empty catalogue → empty string (no preamble).
func skillsPreamble(skills []backend.SkillCatalogEntry) string {
	if len(skills) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("System: The user has these local skills available; mention one when it fits the request:")
	for _, s := range skills {
		sb.WriteString("\n- ")
		sb.WriteString(s.Name)
		if s.Description != "" {
			sb.WriteString(": ")
			sb.WriteString(s.Description)
		}
	}
	return sb.String()
}

// ChatAbort interrupts the turn server-side. The gateway acknowledges
// with {"status":"interrupted"} and terminates the turn with a
// message.complete whose status is "interrupted", which the Translator
// surfaces as the aborted event — nothing is synthesised locally.
func (b *Backend) ChatAbort(ctx context.Context, sessionKey, runID string) error {
	sess, err := b.resolve(ctx, sessionKey)
	if err != nil {
		return err
	}
	cli, err := b.client()
	if err != nil {
		return err
	}
	return cli.Call(ctx, "session.interrupt", map[string]string{"session_id": sess.live}, nil)
}

// historyMessage tolerates the gateway's transcript shapes: content as
// a plain string, as typed blocks, or a bare text field.
type historyMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Text    string          `json:"text"`
}

func (m historyMessage) text() string {
	if len(m.Content) > 0 {
		var s string
		if json.Unmarshal(m.Content, &s) == nil {
			return s
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(m.Content, &blocks) == nil {
			var sb strings.Builder
			for _, blk := range blocks {
				if blk.Text != "" {
					sb.WriteString(blk.Text)
				}
			}
			return sb.String()
		}
	}
	return m.Text
}

// ChatHistory returns the session's full transcript from
// session.history — both user and assistant turns, no client-side
// reconstruction.
func (b *Backend) ChatHistory(ctx context.Context, sessionKey string, limit int) (json.RawMessage, error) {
	sess, err := b.resolve(ctx, sessionKey)
	if err != nil {
		return nil, err
	}
	cli, err := b.client()
	if err != nil {
		return nil, err
	}
	var res struct {
		Count    int              `json:"count"`
		Messages []historyMessage `json:"messages"`
	}
	if err := cli.Call(ctx, "session.history", map[string]string{"session_id": sess.live}, &res); err != nil {
		return nil, err
	}
	type block struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type outMsg struct {
		Role    string  `json:"role"`
		Content []block `json:"content"`
	}
	out := struct {
		Messages []outMsg `json:"messages"`
	}{Messages: []outMsg{}}
	for _, m := range res.Messages {
		text := m.text()
		if text == "" || (m.Role != "user" && m.Role != "assistant") {
			continue
		}
		out.Messages = append(out.Messages, outMsg{
			Role:    m.Role,
			Content: []block{{Type: "text", Text: text}},
		})
	}
	return json.Marshal(out)
}

// ModelsList reports what the gateway exposes via model.options. The
// payload is provider-centric; the reliably present datum is the
// active model, so that is what the picker gets.
func (b *Backend) ModelsList(ctx context.Context) (*protocol.ModelsListResult, error) {
	cli, err := b.client()
	if err != nil {
		return nil, err
	}
	var res struct {
		Model     string `json:"model"`
		Providers []struct {
			Models []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := cli.Call(ctx, "model.options", nil, &res); err != nil {
		return nil, err
	}
	out := &protocol.ModelsListResult{}
	seen := map[string]bool{}
	if res.Model != "" {
		out.Models = append(out.Models, protocol.ModelChoice{ID: res.Model, Name: res.Model})
		seen[res.Model] = true
	}
	for _, p := range res.Providers {
		for _, m := range p.Models {
			if m.ID == "" || seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			name := m.Name
			if name == "" {
				name = m.ID
			}
			out.Models = append(out.Models, protocol.ModelChoice{ID: m.ID, Name: name})
		}
	}
	return out, nil
}

// SessionPatchModel stays unsupported in this phase; Phase 2 routes it
// via command.dispatch ("/model …").
func (b *Backend) SessionPatchModel(ctx context.Context, sessionKey, modelID string) error {
	return fmt.Errorf("model switching is not supported on Hermes connections yet; configure the profile on the host")
}

// Capabilities reports the phase-1 Hermes surface: status, usage, and
// compaction on; exec, thinking, and crons off (Phase 2+).
func (b *Backend) Capabilities() backend.Capabilities {
	return backend.Capabilities{
		GatewayStatus:   true,
		SessionUsage:    true,
		SessionCompact:  true,
		AuthRecovery:    backend.AuthRecoveryAPIKey,
		AgentManagement: false,
	}
}

// --- APIKeyAuth ---

// StoreAPIKey updates the gateway token used for subsequent dials.
// The live connection is not re-dialled here — the connecting view
// retries Connect after the modal stores the token.
func (b *Backend) StoreAPIKey(key string) error {
	b.mu.Lock()
	b.token = key
	b.mu.Unlock()
	return nil
}
