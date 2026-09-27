package rooms

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/lucinate-ai/lucinate/internal/backend/hermes/rpc"
)

// DefaultTimeout bounds one gateway round-trip. Room calls are metadata
// operations (create/list/send/log) — the agent turns they trigger run
// server-side and are observed through the log, never awaited here — so
// a short bound is right.
const DefaultTimeout = 20 * time.Second

// DefaultLogLimit is the page size used when a caller does not ask for
// a specific one. The gateway caps pages at Capabilities.MaxLogLimit.
const DefaultLogLimit = 200

// Client speaks the gateway's `groups.*` hosted-room surface over one
// WebSocket. It is safe for concurrent use.
type Client struct {
	rpc   *rpc.Client
	wsURL string

	mu      sync.Mutex
	timeout time.Duration
}

// Dial connects to a gateway WebSocket URL. Use GatewayWSURL to derive
// one from a Hermes connection's HTTP base URL. A rejected upgrade
// surfaces as *rpc.UpgradeError (HTTP 403 is the gateway's auth
// rejection).
func Dial(ctx context.Context, wsURL string) (*Client, error) {
	cli, err := rpc.Dial(ctx, wsURL, http.Header{})
	if err != nil {
		return nil, err
	}
	return &Client{rpc: cli, wsURL: wsURL, timeout: DefaultTimeout}, nil
}

// DialBaseURL is Dial for an HTTP(S) base URL plus optional gateway
// token — the shape a stored Hermes connection holds.
func DialBaseURL(ctx context.Context, baseURL, token string) (*Client, error) {
	wsURL, err := GatewayWSURL(baseURL, token)
	if err != nil {
		return nil, err
	}
	return Dial(ctx, wsURL)
}

// WSURL reports the endpoint this client is bound to.
func (c *Client) WSURL() string { return c.wsURL }

// SetTimeout replaces the per-call bound. Zero means no bound beyond the
// caller's own context.
func (c *Client) SetTimeout(d time.Duration) {
	c.mu.Lock()
	c.timeout = d
	c.mu.Unlock()
}

// Close releases the underlying RPC connection. It is nil-safe so a
// zero-value client (as tests build) cannot panic a caller that is only
// trying to release a cache slot.
func (c *Client) Close() error {
	if c == nil || c.rpc == nil {
		return nil
	}
	return c.rpc.Close()
}

// Done is closed when the connection drops, so a view can stop polling.
func (c *Client) Done() <-chan struct{} { return c.rpc.Done() }

// Err reports why the connection died, or nil while it is healthy and after
// a deliberate Close. Callers use it to tell a dropped socket (redial) from
// their own teardown (do not).
func (c *Client) Err() error {
	if c == nil || c.rpc == nil {
		return nil
	}
	return c.rpc.Err()
}

func (c *Client) call(ctx context.Context, method string, params, out any) error {
	c.mu.Lock()
	timeout := c.timeout
	c.mu.Unlock()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return c.rpc.Call(ctx, method, params, out)
}

// ── params ───────────────────────────────────────────────────────────────────

type listParams struct {
	IncludeDisbanded bool `json:"include_disbanded"`
	Limit            int  `json:"limit,omitempty"`
	Offset           int  `json:"offset,omitempty"`
}

type createParams struct {
	RoomID  string   `json:"room_id"`
	Name    string   `json:"name"`
	Members []Member `json:"members"`
}

type roomParams struct {
	RoomID string `json:"room_id"`
}

type sendParams struct {
	RoomID  string         `json:"room_id"`
	EventID string         `json:"event_id,omitempty"`
	Payload map[string]any `json:"payload"`
}

type logParams struct {
	RoomID   string `json:"room_id"`
	SinceSeq int    `json:"since_seq"`
	Limit    int    `json:"limit,omitempty"`
}

type renameParams struct {
	RoomID  string `json:"room_id"`
	EventID string `json:"event_id"`
	Name    string `json:"name"`
}

// ── methods ──────────────────────────────────────────────────────────────────

// Capabilities describes the hosted-room protocol this gateway speaks.
func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var out Capabilities
	if err := c.call(ctx, "groups.capabilities", struct{}{}, &out); err != nil {
		return Capabilities{}, err
	}
	return out, nil
}

// List returns the rooms hosted by this gateway, most recently changed
// first.
func (c *Client) List(ctx context.Context, includeDisbanded bool) ([]Room, error) {
	var out struct {
		Rooms      []Room `json:"rooms"`
		NextOffset *int   `json:"next_offset"`
	}
	params := listParams{IncludeDisbanded: includeDisbanded}
	if err := c.call(ctx, "groups.list", params, &out); err != nil {
		return nil, err
	}
	return out.Rooms, nil
}

// Create creates (or idempotently re-adopts) a room. The roster is
// validated locally first so a bad one never reaches the wire.
func (c *Client) Create(ctx context.Context, roomID, name string, members []Member) (Room, error) {
	if err := ValidateRoster(members); err != nil {
		return Room{}, err
	}
	if roomID == "" {
		return Room{}, fmt.Errorf("room id is required")
	}
	if name == "" {
		return Room{}, fmt.Errorf("room name is required")
	}
	var out struct {
		Room Room `json:"room"`
	}
	if err := c.call(ctx, "groups.create", createParams{RoomID: roomID, Name: name, Members: members}, &out); err != nil {
		return Room{}, err
	}
	return out.Room, nil
}

// State returns one room plus its live driver status.
func (c *Client) State(ctx context.Context, roomID string) (StateResult, error) {
	var out StateResult
	if err := c.call(ctx, "groups.state", roomParams{RoomID: roomID}, &out); err != nil {
		return StateResult{}, err
	}
	return out, nil
}

// Send appends one user message to the room and starts the member turns.
// It returns as soon as the gateway accepts the event — the members'
// replies arrive later on the log. Retry with the same event id to be
// idempotent; Send generates one.
func (c *Client) Send(ctx context.Context, roomID, text, threadID string) (Event, error) {
	return c.SendEvent(ctx, roomID, NewEventID(), text, threadID)
}

// SendEvent is Send with a caller-supplied event id.
func (c *Client) SendEvent(ctx context.Context, roomID, eventID, text, threadID string) (Event, error) {
	if roomID == "" {
		return Event{}, fmt.Errorf("room id is required")
	}
	if text == "" {
		return Event{}, fmt.Errorf("message text is required")
	}
	if threadID == "" {
		threadID = DefaultThreadID
	}
	var out struct {
		Event         Event `json:"event"`
		Accepted      bool  `json:"accepted"`
		DriverStarted bool  `json:"driver_started"`
	}
	params := sendParams{
		RoomID:  roomID,
		EventID: eventID,
		Payload: map[string]any{"text": text, "thread_id": threadID},
	}
	if err := c.call(ctx, "groups.send", params, &out); err != nil {
		return Event{}, err
	}
	return out.Event, nil
}

// DefaultThreadID is the thread a room's conversation runs on when the
// caller does not pick one.
const DefaultThreadID = "main"

// SendOpening posts the room's first message with the project context
// prepended, and every later message unchanged.
//
// The context is one-shot on purpose: the gateway's payload has no system
// channel, so repeating the header on every turn would crowd both the
// transcript and every member's context window. A room with no prior user
// message is a fresh conversation, which is exactly when the members need
// to be told where they are.
func (c *Client) SendOpening(ctx context.Context, roomID, text, threadID string, orient Orientation) (Event, error) {
	if orient.Empty() {
		return c.Send(ctx, roomID, text, threadID)
	}
	page, err := c.Log(ctx, roomID, 0, 50)
	if err != nil {
		return Event{}, err
	}
	for _, ev := range page.Events {
		if ev.Kind == "message.user" {
			return c.Send(ctx, roomID, text, threadID)
		}
	}
	return c.Send(ctx, roomID, orient.Block()+"\n"+text, threadID)
}

// Log returns the room transcript delta after sinceSeq. Pass 0 to read
// from the start; pass the previous page's Cursor to advance.
func (c *Client) Log(ctx context.Context, roomID string, sinceSeq, limit int) (LogPage, error) {
	if limit <= 0 {
		limit = DefaultLogLimit
	}
	var out LogPage
	params := logParams{RoomID: roomID, SinceSeq: sinceSeq, Limit: limit}
	if err := c.call(ctx, "groups.log", params, &out); err != nil {
		return LogPage{}, err
	}
	return out, nil
}

// Rename renames a room atomically with its replay event.
func (c *Client) Rename(ctx context.Context, roomID, name string) (Room, error) {
	var out struct {
		Room Room `json:"room"`
	}
	params := renameParams{RoomID: roomID, EventID: NewEventID(), Name: name}
	if err := c.call(ctx, "groups.rename", params, &out); err != nil {
		return Room{}, err
	}
	return out.Room, nil
}

// Stop durably cancels queued or running work for one room.
func (c *Client) Stop(ctx context.Context, roomID string) (int, error) {
	var out struct {
		Cancelled int `json:"cancelled"`
	}
	if err := c.call(ctx, "groups.stop", roomParams{RoomID: roomID}, &out); err != nil {
		return 0, err
	}
	return out.Cancelled, nil
}

// Disband permanently tombstones a room after stopping its work.
func (c *Client) Disband(ctx context.Context, roomID string) error {
	var out struct {
		Tombstone map[string]any `json:"tombstone"`
	}
	return c.call(ctx, "groups.disband", roomParams{RoomID: roomID}, &out)
}

// NewEventID mints an idempotency key for a room event.
func NewEventID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("ev-%d", time.Now().UnixNano())
	}
	return "ev-" + hex.EncodeToString(b[:])
}
