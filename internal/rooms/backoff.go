package rooms

import (
	"errors"
	"io"
	"math"
	"net"
	"strings"
	"time"

	"github.com/lucinate-ai/lucinate/internal/backend/hermes/rpc"
)

// Default reconnect schedule for a dropped gateway socket: half a second,
// doubling to a 30s ceiling. The first attempt is deliberately quick (a
// gateway restart is over in a second) and the ceiling is deliberately
// patient (a laptop resuming from sleep can be offline for minutes, and
// hammering a dead port shows up as a wall of failed connects).
const (
	DefaultBackoffBase   = 500 * time.Millisecond
	DefaultBackoffFactor = 2.0
	DefaultBackoffMax    = 30 * time.Second
)

// Backoff is the reconnect schedule. The zero value is usable and behaves
// like NewBackoff, so a model that gained the field later needs no
// constructor.
type Backoff struct {
	Base   time.Duration
	Factor float64
	Max    time.Duration

	attempt int
}

// NewBackoff returns the default schedule.
func NewBackoff() Backoff {
	return Backoff{Base: DefaultBackoffBase, Factor: DefaultBackoffFactor, Max: DefaultBackoffMax}
}

func (b *Backoff) normalise() {
	if b.Base <= 0 {
		b.Base = DefaultBackoffBase
	}
	if b.Factor <= 0 {
		b.Factor = DefaultBackoffFactor
	}
	if b.Max <= 0 {
		b.Max = DefaultBackoffMax
	}
}

// Next returns the delay for the next attempt and advances the schedule.
func (b *Backoff) Next() time.Duration {
	b.normalise()
	b.attempt++
	return b.delay(b.attempt)
}

// Peek returns the delay the next call to Next would produce, without
// advancing: the status line shows it before the wait starts.
func (b *Backoff) Peek() time.Duration {
	b.normalise()
	return b.delay(b.attempt + 1)
}

// Attempt is how many delays have been handed out since the last Reset.
func (b *Backoff) Attempt() int { return b.attempt }

// Reset restarts the schedule. Called after a successful reconnect, so the
// next drop starts from the base delay again instead of the ceiling.
func (b *Backoff) Reset() { b.attempt = 0 }

// Describe renders the schedule for a status line.
func (b *Backoff) Describe() string {
	if b.attempt == 0 {
		return "reconnecting"
	}
	return "reconnect attempt " + itoaSmall(b.attempt) + ", next in " + b.Peek().String()
}

func (b *Backoff) delay(attempt int) time.Duration {
	if attempt <= 1 {
		return b.Base
	}
	// float64 keeps the growth honest for large attempt counts without
	// overflowing a duration before the cap applies.
	d := float64(b.Base) * math.Pow(b.Factor, float64(attempt-1))
	if d > float64(b.Max) || math.IsInf(d, 0) {
		return b.Max
	}
	return time.Duration(d)
}

// disconnectText are the transport-level failures worth a redial. Matching on
// text is the least-bad option here: the WebSocket library, net and the OS
// each wrap their own error types, and a reconnection decision that misses
// one of them leaves the room silently dead.
var disconnectText = []string{
	"connection reset",
	"connection refused",
	"broken pipe",
	"closed network connection",
	"websocket: close",
	"no route to host",
	"network is unreachable",
	"unexpected eof",
	"eof",
}

// IsDisconnect reports whether err means the gateway socket is gone and a
// redial is the right response.
//
// Two kinds of error are deliberately excluded:
//   - a JSON-RPC error (the socket is fine; retrying cannot help), and
//   - rpc.ErrClosed from a *deliberate* Close. Callers distinguish a dropped
//     socket from their own teardown by checking Client.Err(), which is only
//     set when the read loop died on its own.
func IsDisconnect(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	// A rejected upgrade means the endpoint answered and refused — the room
	// is unreachable, but the same code can come back (gateway restarting,
	// token refreshed), so it is worth a retry.
	var upgrade *rpc.UpgradeError
	if errors.As(err, &upgrade) {
		return true
	}
	if errors.Is(err, rpc.ErrClosed) {
		return false
	}
	var rpcErr *rpc.RPCError
	if errors.As(err, &rpcErr) {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, s := range disconnectText {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
