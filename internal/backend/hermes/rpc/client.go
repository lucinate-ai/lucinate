// Package rpc is a small, Hermes-agnostic JSON-RPC 2.0 client over a
// WebSocket. It handles id-correlated request/response, fans server
// notifications out on a channel, and nothing else — the Hermes event
// vocabulary lives one layer up in the backend's translate step.
//
// The wire format is the tui_gateway one: each WebSocket text frame
// carries one JSON-RPC object (upstream describes it as newline-
// delimited, so frames are parsed tolerantly: a frame may carry several
// newline-separated objects). Our requests carry an id; server pushes
// are notifications (method + params, no id), and the gateway also sends
// its own requests (id + method) that the caller answers with Respond.
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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

// Request is a server→client JSON-RPC request: a frame carrying both an
// id and a method. The client owns one id-space, the server another, so
// an inbound id must never be mistaken for a response to our call.
//
// ID is kept as raw JSON because the gateway's server-side ids are
// strings ("srq-1a2b3c") while our own call ids are numbers. Respond
// must echo the id byte-for-byte, so the raw form travels with the
// request.
type Request struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

// frame is the superset of every JSON-RPC object we read: responses
// carry ID plus Result or Error; notifications carry Method + Params.
// ID is raw so an id of either JSON type decodes; a *uint64 would make
// the whole frame fail to unmarshal on a string id and the request would
// be dropped silently.
type frame struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

// hasID reports whether the frame carried a usable id (present and not
// null).
func (f frame) hasID() bool {
	return len(f.ID) > 0 && string(f.ID) != "null"
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

	// notifications and requests are deliberately buffered: the read
	// loop blocks when either fills, which also stalls call responses.
	// The consumer (the backend's pump) must drain both continuously.
	notifications chan Notification
	requests      chan Request

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
		requests:      make(chan Request, 64),
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

// Requests returns the channel of server→client requests (frames with
// both an id and a method). The consumer must answer each one with
// Respond or RespondError; the channel is closed when the connection
// terminates.
func (c *Client) Requests() <-chan Request {
	return c.requests
}

// responseFrame is the answer we write for a server→client request. The
// id is raw so a string id goes back as a string and a numeric one as a
// number — the gateway matches callbacks by exact id.
type responseFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *responseError  `json:"error,omitempty"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Respond writes the JSON-RPC response frame for a server-initiated
// request. It is the counterpart of Requests and must use the raw id that
// arrived with the request, unchanged.
func (c *Client) Respond(id json.RawMessage, result any) error {
	return c.writeFrame(responseFrame{JSONRPC: "2.0", ID: id, Result: result})
}

// RespondError answers a server-initiated request with a JSON-RPC error
// frame. The gateway settles the pending callback as soon as this lands.
func (c *Client) RespondError(id json.RawMessage, code int, msg string) error {
	return c.writeFrame(responseFrame{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &responseError{Code: code, Message: msg},
	})
}

func (c *Client) writeFrame(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("rpc: marshal response: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("rpc: write response: %w", err)
	}
	return nil
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
	close(c.requests)
}

func (c *Client) dispatch(f frame) {
	// A frame with both an id and a method is a server→client request,
	// not a response to one of our calls. The two id-spaces are
	// independent, so routing it by id would either drop it (no pending
	// call) or, worse, settle an unrelated call.
	if f.hasID() && f.Method != "" {
		select {
		case c.requests <- Request{ID: f.ID, Method: f.Method, Params: f.Params}:
		case <-c.closing:
		}
		return
	}
	if f.hasID() {
		var id uint64
		if err := json.Unmarshal(f.ID, &id); err != nil {
			slog.Debug("rpc: ignoring response with non-numeric id", "id", string(f.ID))
			return
		}
		c.mu.Lock()
		ch, ok := c.pending[id]
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
