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
//   - Connection ↔ gateway. The gateway serves every profile on the
//     machine (Hermes multiplexing), so a profile is selected per
//     session rather than per connection: ListAgents reports the
//     gateway's profiles and CreateSession sends the chosen one.
//     CreateAgent stays rejected — profiles are configured server-side.
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
	"log/slog"
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

// syntheticAgentID is the fixed agent identifier used when the gateway does
// not serve a profile roster (an older gateway, or one that answers -32601 to
// profiles.list). The connection then behaves as the single launch profile.
const syntheticAgentID = "hermes"

// defaultConnectTimeout bounds the dial + gateway.ready handshake when
// the caller didn't configure one.
const defaultConnectTimeout = 10 * time.Second

// defaultAskTimeout bounds how long an interactive ask waits for the user
// before the backend denies it, so a turn can never hang unnoticed.
const defaultAskTimeout = 10 * time.Minute

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
	// AskTimeout bounds how long an interactive ask (approval/clarify)
	// waits for the user before it is denied. Zero means the default.
	AskTimeout time.Duration
}

// clarifyQuestion is one entry of a server→client clarify request's
// questions array.
type clarifyQuestion struct {
	QID         string   `json:"qid"`
	Question    string   `json:"question"`
	Choices     []string `json:"choices"`
	MultiSelect bool     `json:"multi_select"`
}

// serverAskParams is the params object of an inbound approval or clarify
// request. The gateway's live shape uses request_id (older casts used
// prompt_id); both are accepted. Everything except the id is optional so
// one missing key never fails the whole request.
type serverAskParams struct {
	SessionID   string            `json:"session_id"`
	PromptID    string            `json:"prompt_id"`
	RequestID   string            `json:"request_id"`
	Command     string            `json:"command"`
	Description string            `json:"description"`
	Question    string            `json:"question"`
	Choices     []string          `json:"choices"`
	Cwd         string            `json:"cwd"`
	Questions   []clarifyQuestion `json:"questions"`
}

// id returns the ask's stable payload id, falling back to the JSON-RPC
// request id when the payload carries neither prompt_id nor request_id.
func (p serverAskParams) id(rpcID json.RawMessage) string {
	if p.PromptID != "" {
		return p.PromptID
	}
	if p.RequestID != "" {
		return p.RequestID
	}
	return "rpc-" + strings.Trim(string(rpcID), `"`)
}

// pendingAsk is one server→client ask the backend has forwarded to the
// client and is waiting on. The answer is the JSON-RPC response for
// rpcID. done is closed when it is answered or withdrawn, stopping the
// timeout watcher.
type pendingAsk struct {
	// rpcID is the request id exactly as the gateway sent it (a JSON
	// string "srq-…" on the live gateway), so the answer can echo it.
	rpcID       json.RawMessage
	method      string // "approval" | "clarify"
	requestID   string
	sessionID   string
	command     string
	description string
	question    string
	choices     []string
	cwd         string
	questions   []clarifyQuestion
	timer       *time.Timer
	done        chan struct{}
}

// session tracks the two id-spaces the gateway exposes: the short live
// handle (used by prompt.submit / session.history / session.usage /
// session.interrupt during a connection) and the timestamped stored id
// (the one session.list reports, and the resume key across
// reconnects). Empty sessions aren't listed until they hold a message.
type session struct {
	live      string
	stored    string
	profile   string // the Hermes profile this session runs as; "" for the launch profile
	preambled bool   // skills catalogue already prepended on turn 1
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

	askTimeout     time.Duration
	pendingAsks    map[string]*pendingAsk // raw JSON-RPC request id → outstanding ask
	serverRequests []string               // what client.capabilities reported the gateway will send us
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
	if opts.AskTimeout <= 0 {
		opts.AskTimeout = defaultAskTimeout
	}
	return &Backend{
		opts:        opts,
		events:      make(chan protocol.Event, 64),
		tr:          newTranslator(),
		token:       opts.APIKey,
		sessions:    make(map[string]*session),
		askTimeout:  opts.AskTimeout,
		pendingAsks: make(map[string]*pendingAsk),
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

	callCtx, cancel := context.WithTimeout(ctx, b.opts.ConnectTimeout)
	defer cancel()
	b.advertiseServerRequests(callCtx, cli)

	// Best-effort metadata: the connection is usable without either.
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

// pump consumes both server notifications and server→client requests
// until the client's connection closes. Notifications become protocol
// events; approval/clarify requests become "ask" agent events the user
// answers, and sudo/secret are fast-failed with -32601.
func (b *Backend) pump(cli *rpc.Client) {
	for {
		select {
		case req, ok := <-cli.Requests():
			if !ok {
				return
			}
			b.handleServerRequest(cli, req)
		case n, ok := <-cli.Notifications():
			if !ok {
				return
			}
			b.handleNotification(n)
		}
	}
}

// handleNotification translates one server push and drops any withdrawn
// ask it reports.
func (b *Backend) handleNotification(n rpc.Notification) {
	b.mu.Lock()
	events, cancel := b.tr.Translate(n)
	if cancel != nil {
		b.cancelAskLocked(cancel.RequestID)
	}
	b.mu.Unlock()
	for _, ev := range events {
		b.emit(ev)
	}
}

// handleServerRequest answers one server→client JSON-RPC request. The
// gateway sends approval/clarify/sudo/secret here after the client
// advertised server_requests; an unknown method (and the two input kinds
// this client does not own) is answered -32601 so the gateway settles it
// at once instead of waiting for a callback that will never come.
func (b *Backend) handleServerRequest(cli *rpc.Client, req rpc.Request) {
	switch req.Method {
	case "approval":
		b.startApproval(cli, req)
	case "clarify":
		b.startClarify(cli, req)
	case "sudo", "secret":
		if err := cli.RespondError(req.ID, -32601, "method not found: "+req.Method+" input is not supported by this client"); err != nil {
			slog.Debug("respond -32601 failed", "method", req.Method, "err", err)
		}
	default:
		if err := cli.RespondError(req.ID, -32601, "method not found: "+req.Method); err != nil {
			slog.Debug("respond -32601 failed", "method", req.Method, "err", err)
		}
	}
}

// startApproval forwards one approval request to the client and arms its
// timeout. The user's answer is the JSON-RPC response for req.ID.
func (b *Backend) startApproval(cli *rpc.Client, req rpc.Request) {
	var p serverAskParams
	_ = json.Unmarshal(req.Params, &p)
	pa := &pendingAsk{
		rpcID:       req.ID,
		method:      "approval",
		requestID:   p.id(req.ID),
		sessionID:   p.SessionID,
		command:     p.Command,
		description: p.Description,
		question:    p.Question,
		choices:     p.Choices,
		cwd:         p.Cwd,
		timer:       time.NewTimer(b.askTimeout),
		done:        make(chan struct{}),
	}
	b.mu.Lock()
	b.pendingAsks[string(req.ID)] = pa
	b.mu.Unlock()
	go b.watchAskTimeout(cli, pa)
	b.emit(askAgentEvent(pa))
}

// startClarify forwards one clarify request. The gateway sends a
// questions array; this client renders a single-question panel (the first
// question) and answers the rest null.
func (b *Backend) startClarify(cli *rpc.Client, req rpc.Request) {
	var p serverAskParams
	_ = json.Unmarshal(req.Params, &p)
	questions := p.Questions
	if len(questions) == 0 && p.Question != "" {
		questions = []clarifyQuestion{{QID: "q0", Question: p.Question, Choices: p.Choices}}
	}
	pa := &pendingAsk{
		rpcID:     req.ID,
		method:    "clarify",
		requestID: p.id(req.ID),
		sessionID: p.SessionID,
		questions: questions,
		timer:     time.NewTimer(b.askTimeout),
		done:      make(chan struct{}),
	}
	if len(questions) > 0 {
		pa.question = questions[0].Question
		pa.choices = questions[0].Choices
	}
	b.mu.Lock()
	b.pendingAsks[string(req.ID)] = pa
	b.mu.Unlock()
	go b.watchAskTimeout(cli, pa)
	b.emit(askAgentEvent(pa))
}

// askAgentEvent builds the "ask" agent event for one pending request. The
// Data keys are the TUI's contract; empty optional fields are omitted.
func askAgentEvent(pa *pendingAsk) protocol.Event {
	data := map[string]any{
		"sessionKey": pa.sessionID,
		"requestId":  pa.requestID,
		"rpcId":      pa.rpcID,
		"kind":       pa.method,
	}
	if pa.command != "" {
		data["command"] = pa.command
	}
	if pa.description != "" {
		data["description"] = pa.description
	}
	if pa.cwd != "" {
		data["cwd"] = pa.cwd
	}
	if pa.question != "" {
		data["question"] = pa.question
	}
	if len(pa.choices) > 0 {
		data["choices"] = pa.choices
	}
	if len(pa.questions) > 0 {
		qs := make([]map[string]any, 0, len(pa.questions))
		for _, q := range pa.questions {
			qm := map[string]any{"qid": q.QID, "question": q.Question, "multiSelect": q.MultiSelect}
			if len(q.Choices) > 0 {
				qm["choices"] = q.Choices
			}
			qs = append(qs, qm)
		}
		data["questions"] = qs
	}
	return protocol.Event{
		EventName: protocol.EventAgent,
		Payload:   mustMarshal(protocol.AgentEvent{Stream: "ask", Data: data}),
	}
}

// watchAskTimeout denies/cancels an ask nobody answered within askTimeout,
// so a turn blocked on a question the user walked away from cannot hang.
// An answered or withdrawn ask closes done and the watcher returns.
func (b *Backend) watchAskTimeout(cli *rpc.Client, pa *pendingAsk) {
	select {
	case <-pa.timer.C:
	case <-pa.done:
		return
	}
	b.mu.Lock()
	key := string(pa.rpcID)
	if b.pendingAsks[key] != pa {
		b.mu.Unlock()
		return
	}
	delete(b.pendingAsks, key)
	notice := b.tr.InjectSystemLine(pa.sessionID, askTimeoutNotice(pa.method))
	b.mu.Unlock()

	b.emit(notice)
	if err := b.respondServerAsk(cli, pa, "deny", ""); err != nil {
		slog.Debug("ask timeout respond failed", "err", err, "request_id", pa.requestID)
	}
}

// askTimeoutNotice is the transcript line for an ask that expired
// unanswered.
func askTimeoutNotice(method string) string {
	return fmt.Sprintf("Hermes's %s request timed out — %sd.", method, map[string]string{
		"approval": "denied",
		"clarify":  "skipped",
	}[method])
}

// takeAskLocked removes an ask from the pending set and reports it. It
// matches on the JSON-RPC id first, then on the payload's request id (the
// only key a request.cancel notification carries).
func (b *Backend) takeAskLocked(rpcID, requestID string) *pendingAsk {
	if rpcID != "" {
		if pa, ok := b.pendingAsks[rpcID]; ok {
			delete(b.pendingAsks, rpcID)
			pa.timer.Stop()
			close(pa.done)
			return pa
		}
	}
	if requestID != "" {
		for id, pa := range b.pendingAsks {
			if pa.requestID == requestID {
				delete(b.pendingAsks, id)
				pa.timer.Stop()
				close(pa.done)
				return pa
			}
		}
	}
	return nil
}

// cancelAskLocked drops every pending ask matching requestID. request.cancel
// names only the payload id, so the panel's rpc id is not needed.
func (b *Backend) cancelAskLocked(requestID string) {
	for id, pa := range b.pendingAsks {
		if pa.requestID != requestID {
			continue
		}
		delete(b.pendingAsks, id)
		pa.timer.Stop()
		close(pa.done)
	}
}

// RespondAsk answers a pending ask. An answer for an ask that is no longer
// pending (expired, withdrawn, or answered twice) is logged and dropped
// rather than sent again.
func (b *Backend) RespondAsk(ctx context.Context, resp backend.AskResponse) error {
	b.mu.Lock()
	pa := b.takeAskLocked(resp.RPCID, resp.RequestID)
	cli := b.cli
	b.mu.Unlock()
	if pa == nil {
		slog.Debug("ask answer dropped: no pending ask", "request_id", resp.RequestID, "rpc_id", resp.RPCID)
		return nil
	}
	if cli == nil {
		return fmt.Errorf("hermes gateway not connected")
	}
	return b.respondServerAsk(cli, pa, resp.Choice, resp.Answer)
}

// respondServerAsk writes the JSON-RPC response for one pending ask.
// Approval returns {"choice": …} with a choice that must be among the
// offered ones (anything else becomes deny); clarify returns
// {"answers": {qid: text|null}, "outcome": submitted|cancelled}.
func (b *Backend) respondServerAsk(cli *rpc.Client, pa *pendingAsk, choice, answer string) error {
	switch pa.method {
	case "approval":
		return cli.Respond(pa.rpcID, map[string]any{"choice": validatedChoice(choice, pa.choices)})
	case "clarify":
		answers := make(map[string]any, len(pa.questions))
		outcome := "submitted"
		if answer == "" {
			outcome = "cancelled"
		}
		if len(pa.questions) == 0 {
			answers["q0"] = nil
			if answer != "" {
				answers["q0"] = answer
			}
		}
		for i, q := range pa.questions {
			if i == 0 && answer != "" {
				answers[q.QID] = answer
			} else {
				answers[q.QID] = nil
			}
		}
		return cli.Respond(pa.rpcID, map[string]any{"answers": answers, "outcome": outcome})
	default:
		return fmt.Errorf("hermes: unknown ask method %q", pa.method)
	}
}

// validatedChoice returns choice when the gateway offered it, otherwise
// deny. The gateway forces a deny for an unknown choice anyway, so make the
// contract explicit rather than sending a value it will discard.
func validatedChoice(choice string, choices []string) string {
	if len(choices) == 0 {
		if choice == "" {
			return "deny"
		}
		return choice
	}
	for _, c := range choices {
		if strings.EqualFold(c, choice) {
			return c
		}
	}
	return "deny"
}

// advertiseServerRequests tells the gateway this client can answer
// server→client requests. Without it the gateway withdraws every
// approval/clarify immediately and the agent is told nobody could answer.
// Called once per connection, right after the handshake.
func (b *Backend) advertiseServerRequests(ctx context.Context, cli *rpc.Client) {
	var res struct {
		ServerRequests []string `json:"server_requests"`
	}
	if err := cli.Call(ctx, "client.capabilities", map[string]any{"server_requests": true}, &res); err != nil {
		slog.Debug("client.capabilities failed", "err", err)
		return
	}
	b.mu.Lock()
	b.serverRequests = res.ServerRequests
	b.mu.Unlock()
	slog.Debug("hermes server_requests enabled", "methods", res.ServerRequests)
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
	for id, pa := range b.pendingAsks {
		pa.timer.Stop()
		close(pa.done)
		delete(b.pendingAsks, id)
	}
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
				capCtx, cancel := context.WithTimeout(ctx, b.opts.ConnectTimeout)
				b.advertiseServerRequests(capCtx, next)
				cancel()
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

// ListAgents returns the gateway's profile roster. Hermes is multiplexed: one
// gateway process serves every profile on the machine, and a session selects
// one. When the gateway does not answer profiles.list (an older gateway, or a
// failure), the single synthetic agent is reported — the connection then acts
// as its launch profile, exactly as before multiplexing.
func (b *Backend) ListAgents(ctx context.Context) (*protocol.AgentsListResult, error) {
	if cli, err := b.client(); err == nil {
		var res struct {
			Profiles []profileListEntry `json:"profiles"`
		}
		if err := cli.Call(ctx, "profiles.list", map[string]any{"include_sessions": false}, &res); err == nil && len(res.Profiles) > 0 {
			return agentsFromProfiles(res.Profiles), nil
		}
	}
	return b.syntheticAgent(), nil
}

// agentsFromProfiles maps the gateway's roster onto the TUI's agent shape.
// The profile name is the agent identifier because it is what session.create
// and session.list expect on the wire.
func agentsFromProfiles(profiles []profileListEntry) *protocol.AgentsListResult {
	out := &protocol.AgentsListResult{Agents: make([]protocol.AgentSummary, 0, len(profiles))}
	defaultID := ""
	for _, p := range profiles {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			continue
		}
		summary := protocol.AgentSummary{ID: name, Name: name}
		if p.Model != "" {
			summary.Model = &protocol.AgentSummaryModel{Primary: p.Model}
		}
		out.Agents = append(out.Agents, summary)
		if p.IsDefault || defaultID == "" {
			defaultID = name
		}
	}
	out.DefaultID = defaultID
	out.MainKey = defaultID
	return out
}

// syntheticAgent is the pre-multiplexing shape: one agent, the launch profile.
func (b *Backend) syntheticAgent() *protocol.AgentsListResult {
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
	}
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
	Source       string  `json:"source"`
}

// SessionsList maps session.list into the TUI's session-browser shape.
// Entries are keyed by profile-qualified stored session id — the resume key.
func (b *Backend) SessionsList(ctx context.Context, agentID string) (json.RawMessage, error) {
	cli, err := b.client()
	if err != nil {
		return nil, err
	}
	profile := profileFor(agentID)
	if profile == "" && strings.TrimSpace(agentID) == "" {
		b.mu.Lock()
		profile = b.profile
		b.mu.Unlock()
	}
	params := map[string]any{}
	if profile != "" {
		params["profile"] = profile
	}
	var res struct {
		Sessions []sessionListEntry `json:"sessions"`
	}
	if err := cli.Call(ctx, "session.list", params, &res); err != nil {
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
			"key":                sessionKeyFor(profile, s.ID),
			"derivedTitle":       title,
			"lastMessagePreview": s.Preview,
			"updatedAt":          int64(s.StartedAt * 1000),
			"source":             s.Source,
		})
	}
	return json.Marshal(out)
}

// CreateSession opens a fresh gateway session for the selected profile and
// returns its live handle as the TUI session key. The stored id is tracked
// alongside so the session survives a reconnect (and so /status can show it).
func (b *Backend) CreateSession(ctx context.Context, agentID, key string) (string, error) {
	cli, err := b.client()
	if err != nil {
		return "", err
	}
	profile := profileFor(agentID)
	params := map[string]any{}
	if profile != "" {
		params["profile"] = profile
	}
	var res sessionCreateResult
	if err := cli.Call(ctx, "session.create", params, &res); err != nil {
		return "", err
	}
	// The gateway reports the profile the session actually runs as, which is
	// authoritative (a routed profile may be renamed upstream).
	if res.Info.ProfileName != "" {
		profile = res.Info.ProfileName
	}
	b.mu.Lock()
	b.sessions[res.SessionID] = &session{
		live:    res.SessionID,
		stored:  res.StoredSessionID,
		profile: profile,
	}
	b.active = res.SessionID
	if res.Info.Model != "" {
		b.model = res.Info.Model
	}
	if profile != "" {
		b.profile = profile
	}
	b.mu.Unlock()
	return res.SessionID, nil
}

// resolve maps a TUI session key to its live gateway handle, resuming the
// session when the key is a profile-qualified stored id from the session
// browser (or a live handle orphaned by a reconnect).
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
	profile, stored := splitSessionKey(key)
	params := map[string]any{"session_id": stored}
	if profile != "" {
		params["profile"] = profile
	}
	var res sessionCreateResult
	if err := cli.Call(ctx, "session.resume", params, &res); err != nil {
		return nil, fmt.Errorf("session not found: %s: %w", key, err)
	}
	if res.Info.ProfileName != "" {
		profile = res.Info.ProfileName
	}
	b.mu.Lock()
	sess = &session{live: res.SessionID, stored: res.StoredSessionID, profile: profile}
	if sess.stored == "" {
		sess.stored = stored
	}
	b.sessions[key] = sess
	b.active = key
	if profile != "" {
		b.profile = profile
	}
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
	var live, profile string
	b.mu.Lock()
	if sess, ok := b.sessions[sessionKey]; ok {
		live = sess.live
		profile = sess.profile
		if sess.stored != "" {
			id = sess.stored
		}
	}
	delete(b.sessions, sessionKey)
	if b.active == sessionKey {
		b.active = ""
	}
	b.mu.Unlock()
	if profile == "" {
		profile, id = splitSessionKey(id)
	}
	if live != "" {
		// Best-effort detach; a session opened by another client or
		// already closed just falls through to the delete.
		_ = cli.Call(ctx, "session.close", map[string]string{"session_id": live}, nil)
	}
	params := map[string]any{"session_id": id}
	if profile != "" {
		params["profile"] = profile
	}
	return cli.Call(ctx, "session.delete", params, nil)
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

// profileListEntry is one row of profiles.list. Only the fields this
// backend needs are decoded; the gateway sends far more.
type profileListEntry struct {
	Name      string `json:"name"`
	Model     string `json:"model"`
	IsDefault bool   `json:"is_default"`
}

// profileFor maps an agent identifier onto the profile a session should run
// as. The synthetic agent (and an empty identifier) means "whatever the
// gateway's launch profile is", which is what older gateways serve.
func profileFor(agentID string) string {
	id := strings.TrimSpace(agentID)
	if id == "" || id == syntheticAgentID {
		return ""
	}
	return id
}

// sessionKeyFor qualifies a stored session id with its profile. A stored id is
// only unique inside one profile's state.db, and resuming needs both halves;
// the key is opaque to the TUI (it renders the title, not the key).
func sessionKeyFor(profile, stored string) string {
	if profile == "" {
		return stored
	}
	return profile + "::" + stored
}

// splitSessionKey reverses sessionKeyFor. Keys that are not qualified — a live
// handle, or a stored id from before this change — pass through unchanged.
func splitSessionKey(key string) (profile, stored string) {
	if i := strings.LastIndex(key, "::"); i > 0 && i+2 < len(key) {
		candidate := key[:i]
		if !strings.ContainsAny(candidate, `/\`) && !strings.Contains(candidate, "::") {
			return candidate, key[i+2:]
		}
	}
	return "", key
}

// ModelsList reports what the gateway exposes via model.options. The
// payload is provider-centric: a provider's models arrive either as plain
// "provider/model" strings (the live Hermes gateway) or as {id,name}
// objects (older gateways). Both shapes are accepted so the picker works
// against either. The active model is also taken from the top-level
// "model" field.
func (b *Backend) ModelsList(ctx context.Context) (*protocol.ModelsListResult, error) {
	cli, err := b.client()
	if err != nil {
		return nil, err
	}
	var res struct {
		Model     string `json:"model"`
		Providers []struct {
			Models []json.RawMessage `json:"models"`
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
		for _, raw := range p.Models {
			id, name := decodeModelOption(raw)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			if name == "" {
				name = id
			}
			out.Models = append(out.Models, protocol.ModelChoice{ID: id, Name: name})
		}
	}
	return out, nil
}

// decodeModelOption reads one provider model entry. The gateway sends a
// bare string; older payloads send {"id","name"}. An unrecognised entry
// yields empty strings and is skipped by the caller.
func decodeModelOption(raw json.RawMessage) (id, name string) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, s
	}
	var obj struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", ""
	}
	return obj.ID, obj.Name
}

// SessionPatchModel switches the session model through the gateway's own
// slash-command dispatcher: the tui_gateway has no model-patch RPC, and
// `/model <ref>` is what the dashboard's chat uses. The reference is the bare
// model id (`provider/model`) — model.options carries no per-model provider
// for Hermes, so the TUI sends it unqualified. The switch is session-scoped
// unless the command says otherwise, and the gateway's own answer decides
// success: a refusal is returned as an error rather than reported as a switch
// that never happened.
func (b *Backend) SessionPatchModel(ctx context.Context, sessionKey, modelID string) error {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return fmt.Errorf("model reference is required")
	}
	sess, err := b.resolve(ctx, sessionKey)
	if err != nil {
		return err
	}
	cli, err := b.client()
	if err != nil {
		return err
	}
	var res struct {
		Output string `json:"output"`
	}
	if err := cli.Call(ctx, "slash.exec", map[string]string{
		"session_id": sess.live,
		"command":    "/model " + modelID,
	}, &res); err != nil {
		return err
	}
	out := strings.TrimSpace(res.Output)
	if out != "" && !strings.Contains(out, "Model switched") {
		return fmt.Errorf("%s", firstLine(out))
	}
	b.mu.Lock()
	b.model = modelID
	b.mu.Unlock()
	return nil
}

// firstLine returns the first non-empty line of a gateway answer, so a refusal
// reads as a sentence instead of a wall of provider details.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return s
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
		Rooms:           true,
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
