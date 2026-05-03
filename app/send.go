package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// SendOptions configures a one-shot Send invocation.
//
// Send dispatches a single user message through the same connection,
// backend, and session plumbing the TUI uses, then (when Detach is
// false) blocks until the assistant's first complete reply arrives. It
// is the programmatic surface behind the `lucinate send` subcommand —
// callers that want deeper control should drive a Program directly.
type SendOptions struct {
	// Connection identifies the saved connection to dispatch through,
	// matched first by ID and then case-insensitively by Name. Required.
	Connection string

	// Agent identifies the destination agent within the connection,
	// matched first by ID and then case-insensitively by Name.
	// Required.
	Agent string

	// Session is the optional session key. When empty Send routes the
	// message to the agent's main session, mirroring the TUI's pick:
	// the AgentsListResult.MainKey for the connection's default agent,
	// or the literal "main" for any other agent. The backend's
	// CreateSession is responsible for resolving the supplied key into
	// the wire-level session identifier (the gateway returns its own
	// canonical key for OpenClaw; OpenAI-compat returns the agent ID).
	Session string

	// Message is the user input dispatched as the chat turn. Equivalent
	// to typing the same text into the TUI textarea and pressing
	// Enter — including any leading slash, which the TUI would
	// normally route to a command handler. Send does not parse slash
	// commands; callers wanting that behaviour should use the TUI.
	// Required (must be non-empty after trimming surrounding
	// whitespace).
	Message string

	// Detach makes Send fire-and-forget: the user message is dispatched
	// and the call returns once the backend's chat-send RPC ack
	// arrives, without waiting for any streaming events. Useful for
	// scripted "kick off a turn and walk away" automation. When
	// false (the default) Send blocks until the run terminates with a
	// final, error, or aborted chat event.
	Detach bool

	// Out is where the assistant's final reply is written when Detach
	// is false. Defaults to os.Stdout. Send writes the reply text once
	// the run terminates and appends a single trailing newline if the
	// reply does not already end in one. Streaming deltas, tool
	// events, and connection chrome are intentionally suppressed so
	// the output is safe to capture into shell variables or pipe
	// into another tool.
	Out io.Writer

	// BackendFactory builds the backend for the resolved connection.
	// Defaults to DefaultBackendFactory so callers get the same auth
	// resolution and per-connection secrets store the CLI uses.
	// Embedders that want to substitute a fake (in tests, say) wire
	// their own factory in here.
	BackendFactory BackendFactory

	// ConnectionsStore, when non-nil, is the connection set Send
	// resolves Connection against. Defaults to LoadConnections() so
	// the CLI's standard on-disk store is used. Tests inject a
	// purpose-built store through this field.
	ConnectionsStore *Connections
}

// Send is the programmatic entry point for the one-shot CLI mode.
// See SendOptions for argument semantics.
//
// Send's lifecycle on a non-detached call:
//
//  1. Resolve the connection and build a fresh backend.
//  2. Connect, defer Close.
//  3. Resolve the agent against ListAgents, picking the main session if
//     the caller did not name one.
//  4. CreateSession to get the canonical session key.
//  5. Subscribe to backend events before issuing ChatSend so the final
//     event cannot race past the consumer.
//  6. ChatSend, then drain events until a chat event with state
//     "final", "error", or "aborted" matching the run lands. Write the
//     reply text to opts.Out and return.
//
// In detached mode Send stops at step 5: it drains the ChatSend RPC
// ack so any synchronous error (auth, validation, no-such-agent)
// surfaces, and returns nil after the gateway has accepted the turn.
// The chat run continues server-side and any subsequent reply is
// rendered the next time the user opens the TUI on the same session.
func Send(ctx context.Context, opts SendOptions) error {
	if strings.TrimSpace(opts.Connection) == "" {
		return errors.New("send: --connection is required")
	}
	if strings.TrimSpace(opts.Agent) == "" {
		return errors.New("send: --agent is required")
	}
	if strings.TrimSpace(opts.Message) == "" {
		return errors.New("send: message is required")
	}

	factory := opts.BackendFactory
	if factory == nil {
		factory = DefaultBackendFactory
	}
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}

	var store Connections
	if opts.ConnectionsStore != nil {
		store = *opts.ConnectionsStore
	} else {
		store = LoadConnections()
	}
	conn := resolveSendConnection(&store, opts.Connection)
	if conn == nil {
		return fmt.Errorf("send: connection %q not found", opts.Connection)
	}

	b, err := factory(conn)
	if err != nil {
		return fmt.Errorf("send: build backend: %w", err)
	}
	if err := b.Connect(ctx); err != nil {
		_ = b.Close()
		return fmt.Errorf("send: connect: %w", err)
	}
	defer func() { _ = b.Close() }()

	// Mark the connection as last-used so the next CLI launch picks
	// it by default — same behaviour as a successful TUI connect.
	store.MarkUsed(conn.ID)
	if opts.ConnectionsStore == nil {
		// Best-effort persist; matches CLI's OnConnectionsChanged
		// callback. A failed write is non-fatal — the message still
		// goes through.
		_ = config.SaveConnections(store)
	}

	agentList, err := b.ListAgents(ctx)
	if err != nil {
		return fmt.Errorf("send: list agents: %w", err)
	}
	agent := resolveSendAgent(agentList, opts.Agent)
	if agent == nil {
		return fmt.Errorf("send: agent %q not found", opts.Agent)
	}

	createKey := opts.Session
	if createKey == "" {
		createKey = defaultSessionKey(agentList, agent)
	}
	sessionKey, err := b.CreateSession(ctx, agent.ID, createKey)
	if err != nil {
		return fmt.Errorf("send: create session: %w", err)
	}

	// Subscribe before ChatSend: backends with a single shared events
	// channel (every concrete backend in this tree) buffer events but
	// we still want a consumer ready before the run starts so the
	// final event cannot race past us between RPC ack and the loop.
	var (
		collector *replyCollector
		waitCh    chan error
		waitCtx   context.Context
		waitStop  context.CancelFunc
	)
	if !opts.Detach {
		waitCtx, waitStop = context.WithCancel(ctx)
		defer waitStop()
		collector = newReplyCollector(sessionKey)
		waitCh = make(chan error, 1)
		go collector.run(waitCtx, b.Events(), waitCh)
	}

	idemKey := fmt.Sprintf("lucinate-send-%d", time.Now().UnixNano())
	res, err := b.ChatSend(ctx, sessionKey, backend.ChatSendParams{
		Message:        opts.Message,
		IdempotencyKey: idemKey,
	})
	if err != nil {
		return fmt.Errorf("send: chat: %w", err)
	}
	if opts.Detach {
		return nil
	}
	collector.setRunID(res.RunID)

	select {
	case err := <-waitCh:
		if err != nil {
			return err
		}
		text := collector.text()
		if _, err := io.WriteString(out, text); err != nil {
			return fmt.Errorf("send: write reply: %w", err)
		}
		if !strings.HasSuffix(text, "\n") {
			if _, err := io.WriteString(out, "\n"); err != nil {
				return fmt.Errorf("send: write reply: %w", err)
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// resolveSendConnection picks the connection a one-shot Send invocation
// should use. The user-typed identifier is matched first by ID (so
// scripts that captured a generated connection ID keep working) and
// then case-insensitively by Name (the friendly form humans actually
// type).
func resolveSendConnection(store *Connections, query string) *Connection {
	if conn := store.Find(query); conn != nil {
		return conn
	}
	lc := strings.ToLower(query)
	for i := range store.Connections {
		if strings.ToLower(store.Connections[i].Name) == lc {
			return &store.Connections[i]
		}
	}
	return nil
}

// resolveSendAgent picks the AgentSummary matching the user-typed
// identifier — same ID-then-name precedence as the connection lookup.
// Returns nil if no agent matches; the caller surfaces a clear error.
func resolveSendAgent(list *protocol.AgentsListResult, query string) *protocol.AgentSummary {
	if list == nil {
		return nil
	}
	for i := range list.Agents {
		if list.Agents[i].ID == query {
			return &list.Agents[i]
		}
	}
	lc := strings.ToLower(query)
	for i := range list.Agents {
		if strings.ToLower(list.Agents[i].Name) == lc {
			return &list.Agents[i]
		}
	}
	return nil
}

// defaultSessionKey mirrors the TUI's "main session" pick from
// internal/tui/select.go: the AgentsListResult.MainKey when the
// chosen agent is the connection's default agent, otherwise the
// literal "main" so backends create a fresh per-agent main session.
func defaultSessionKey(list *protocol.AgentsListResult, agent *protocol.AgentSummary) string {
	if list != nil && agent != nil && agent.ID == list.DefaultID && list.MainKey != "" {
		return list.MainKey
	}
	return "main"
}

// replyCollector consumes backend chat events for a one-shot Send and
// resolves once the assistant's first turn lands. The latest delta
// text is buffered so the caller has a usable reply even when the
// final event's structured Content array is empty (some backends emit
// the whole body in deltas and only an ack-shaped final).
type replyCollector struct {
	sessionKey string

	mu    sync.Mutex
	runID string
	body  string
}

func newReplyCollector(sessionKey string) *replyCollector {
	return &replyCollector{sessionKey: sessionKey}
}

// setRunID is called after ChatSend returns its ack so the collector
// can ignore events from any concurrently-running turn on the same
// session. Until the ID is known the collector accepts every event
// matching the session key — the ChatSend ack and the first delta
// can race in either order.
func (c *replyCollector) setRunID(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.runID = id
}

func (c *replyCollector) currentRunID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runID
}

func (c *replyCollector) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.body
}

func (c *replyCollector) setText(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.body = s
}

// run drains events until the first matching final / error / aborted
// chat event lands, then signals via done. ctx cancellation and a
// closed events channel are surfaced as errors so a network drop
// during the wait turns into a clean Send failure instead of a hang.
func (c *replyCollector) run(ctx context.Context, events <-chan protocol.Event, done chan<- error) {
	for {
		select {
		case <-ctx.Done():
			done <- ctx.Err()
			return
		case ev, ok := <-events:
			if !ok {
				done <- errors.New("send: backend event channel closed before reply")
				return
			}
			if ev.EventName != protocol.EventChat {
				continue
			}
			var chatEv protocol.ChatEvent
			if err := json.Unmarshal(ev.Payload, &chatEv); err != nil {
				continue
			}
			if chatEv.SessionKey != "" && chatEv.SessionKey != c.sessionKey {
				continue
			}
			// Once we know the run ID, filter strictly to it so a
			// concurrent turn on the same session can't bleed into
			// the captured reply.
			if rid := c.currentRunID(); rid != "" && chatEv.RunID != "" && chatEv.RunID != rid {
				continue
			}
			switch chatEv.State {
			case "delta":
				if t := backend.ExtractChatText(chatEv.Message); t != "" {
					c.setText(t)
				}
			case "final":
				if t := backend.ExtractChatText(chatEv.Message); t != "" {
					c.setText(t)
				}
				done <- nil
				return
			case "error":
				msg := chatEv.ErrorMessage
				if msg == "" {
					msg = "chat error"
				}
				done <- fmt.Errorf("send: chat: %s", msg)
				return
			case "aborted":
				done <- errors.New("send: chat aborted")
				return
			}
		}
	}
}
