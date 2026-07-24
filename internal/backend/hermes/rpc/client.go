// Package rpc is a small, Hermes-agnostic JSON-RPC 2.0 client over a
// WebSocket. It handles id-correlated request/response, fans server
// notifications out on a channel, and nothing else — the Hermes event
// vocabulary lives one layer up in the backend's translate step.
//
// The wire format is the tui_gateway one: each WebSocket text frame
// carries one JSON-RPC object (upstream describes it as newline-
// delimited, so frames are parsed tolerantly: a frame may carry several
// newline-separated objects). Requests carry an id; server pushes are
// notifications (method + params, no id).
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

// ErrClosed is returned by Call when the client has been closed or the
// read loop has terminated (connection dropped). Callers treat it as
// retryable: the supervisor rebuilds the client and re-issues the call.
var ErrClosed = errors.New("rpc: client closed")

// UpgradeError reports a WebSocket handshake rejected at the HTTP
// upgrade. The gateway auths at this layer — a bad or missing token is
// an HTTP 403 before the socket ever opens — so the backend inspects
// StatusCode to route auth recovery.
type UpgradeError struct {
	StatusCode int
}

func (e *UpgradeError) Error() string {
	return fmt.Sprintf("rpc: websocket upgrade rejected: HTTP %d", e.StatusCode)
}

// RPCError is a JSON-RPC error response. Codes observed from the
// gateway include -32601 (unknown method) and app-level 4xxx codes
// (4001 session not found, 4006 session_id required, …).
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("rpc: %s (code %d)", e.Message, e.Code)
}

// Notification is a server-initiated JSON-RPC notification: a frame
// with a method but no id. For the Hermes gateway the method is always
// "event" and Params carries {type, session_id, payload}; decoding
// that envelope is the backend's job, not this package's.
type Notification struct {
	Method string
	Params json.RawMessage
}

// frame is the superset of every JSON-RPC object we read: responses
// carry ID plus Result or Error; notifications carry Method + Params.
type frame struct {
	ID     *uint64         `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// Client is a JSON-RPC 2.0 client over one WebSocket connection. It is
// safe for concurrent use. A Client is single-shot: once closed (or
// once the connection drops) it stays closed, and the supervisor dials
// a fresh one.
type Client struct {
	conn *websocket.Conn

	writeMu sync.Mutex // gorilla allows one concurrent writer

	mu      sync.Mutex
	pending map[uint64]chan frame
	closed  bool
	err     error // terminal read-loop error, guarded by mu

	nextID atomic.Uint64

	// notifications is deliberately buffered: the read loop blocks when
	// it fills, which also stalls call responses. The consumer (the
	// backend's event pump) must drain it continuously.
	notifications chan Notification

	// closing is closed by Close() itself (not the read loop) so a
	// dispatch blocked on a full notifications channel still unblocks;
	// done closes only after the read loop has fully exited.
	closing   chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// Dial connects to wsURL and starts the read loop. The context governs
// the dial only, not the lifetime of the connection. A handshake
// rejected at the HTTP upgrade surfaces as *UpgradeError so the caller
// can distinguish auth rejection (403) from an endpoint that is not a
// WebSocket at all.
func Dial(ctx context.Context, wsURL string, header http.Header) (*Client, error) {
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, wsURL, header)
	if err != nil {
		if resp != nil {
			return nil, &UpgradeError{StatusCode: resp.StatusCode}
		}
		return nil, fmt.Errorf("rpc: dial %s: %w", wsURL, err)
	}
	c := &Client{
		conn:          conn,
		pending:       make(map[uint64]chan frame),
		notifications: make(chan Notification, 256),
		closing:       make(chan struct{}),
		done:          make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

// Call issues a JSON-RPC request and decodes the matching response's
// result into out (skipped when out is nil). The context bounds the
// full round-trip. A JSON-RPC error response is returned as *RPCError;
// a closed or dropped connection returns ErrClosed.
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	id := c.nextID.Add(1)
	ch := make(chan frame, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if params == nil {
		params = struct{}{}
	}
	data, err := json.Marshal(request{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("rpc: marshal %s: %w", method, err)
	}

	c.writeMu.Lock()
	err = c.conn.WriteMessage(websocket.TextMessage, data)
	c.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf("rpc: write %s: %w", method, err)
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrClosed
	case f := <-ch:
		if f.Error != nil {
			return f.Error
		}
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(f.Result, out); err != nil {
			return fmt.Errorf("rpc: decode %s result: %w", method, err)
		}
		return nil
	}
}

// Notifications returns the channel of server-initiated notifications.
// It is closed when the connection terminates, which is the signal the
// backend's event pump uses to stop.
func (c *Client) Notifications() <-chan Notification {
	return c.notifications
}

// Done is closed when the read loop exits — either from Close or a
// dropped connection. The supervisor watches it to trigger a redial.
func (c *Client) Done() <-chan struct{} {
	return c.done
}

// Err reports why the read loop terminated. It is nil until Done is
// closed, and nil after a clean Close.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close tears the connection down. Pending and future Calls fail with
// ErrClosed. Safe to call multiple times and concurrently with Calls.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		close(c.closing)   // unblocks a dispatch stuck on a full notifications channel
		_ = c.conn.Close() // unblocks the read loop, which finishes teardown
	})
	return nil
}

// readLoop is the sole reader. It dispatches responses to their pending
// call, pushes notifications, and on any read error marks the client
// closed, fails pending calls, and closes done + notifications.
func (c *Client) readLoop() {
	var loopErr error
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			loopErr = err
			break
		}
		// Tolerate newline-delimited batches inside one frame; in
		// practice the gateway sends one object per frame.
		for _, line := range bytes.Split(data, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var f frame
			if err := json.Unmarshal(line, &f); err != nil {
				continue // not ours to police; skip malformed frames
			}
			c.dispatch(f)
		}
	}

	c.mu.Lock()
	wasClosed := c.closed
	c.closed = true
	if !wasClosed {
		// Connection dropped underneath us — record why. After a
		// deliberate Close the read error is just the close racing
		// the loop, not a fault worth reporting.
		c.err = loopErr
	}
	c.mu.Unlock()

	_ = c.conn.Close()
	// Pending calls are woken by close(done) and return ErrClosed;
	// each Call unregisters its own pending entry on the way out.
	close(c.done)
	close(c.notifications)
}

func (c *Client) dispatch(f frame) {
	if f.ID != nil {
		c.mu.Lock()
		ch, ok := c.pending[*f.ID]
		c.mu.Unlock()
		if ok {
			ch <- f // buffered; the Call side owns unregistering
		}
		return
	}
	if f.Method == "" {
		return
	}
	select {
	case c.notifications <- Notification{Method: f.Method, Params: f.Params}:
	case <-c.closing:
	}
}
