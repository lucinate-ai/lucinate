package client

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ConnStatus describes the current state of the gateway WebSocket connection.
type ConnStatus int

const (
	// StatusConnected: the WebSocket is up and the SDK has completed handshake.
	StatusConnected ConnStatus = iota
	// StatusDisconnected: the previous connection has dropped; reconnect not yet attempted.
	StatusDisconnected
	// StatusReconnecting: a reconnect attempt is in progress (or about to retry).
	StatusReconnecting
	// StatusAuthFailed: reconnect was rejected for auth reasons; supervisor has stopped.
	// The user must restart the client so the interactive auth recovery flow can run.
	StatusAuthFailed
)

// ConnState is the value emitted by the supervisor each time the connection
// state changes.
type ConnState struct {
	Status  ConnStatus
	Attempt int   // 1-based attempt count for Reconnecting; 0 for other statuses
	Err     error // non-nil for Disconnected/Reconnecting/AuthFailed
}

// reconnectFn is the connect-attempt function used by the supervisor. The
// real one is Client.Reconnect; tests inject a fake.
type reconnectFn func(ctx context.Context) error

// doneFn returns a channel that closes when the active connection drops.
// The real one is Client.Done.
type doneFn func() <-chan struct{}

// supervisorConfig is exposed for tests; production callers use Supervise.
type supervisorConfig struct {
	reconnect      reconnectFn
	done           doneFn
	notify         func(ConnState)
	attemptTimeout time.Duration
	backoff        []time.Duration
	isAuthFatal    func(error) bool
}

// defaultBackoff is the schedule of waits between reconnect attempts. The
// final entry caps every subsequent retry.
var defaultBackoff = []time.Duration{
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	15 * time.Second,
	30 * time.Second,
}

// defaultAttemptTimeout is the per-attempt connect deadline. Matches the
// startup connect timeout in main.go.
const defaultAttemptTimeout = 15 * time.Second

// Supervise watches the client's connection and reconnects with exponential
// backoff if it drops. It blocks until ctx is cancelled or the connection
// hits a fatal auth error (in which case the supervisor returns and the
// last emitted state is StatusAuthFailed).
//
// notify is called from the supervisor goroutine and must not block.
func (c *Client) Supervise(ctx context.Context, notify func(ConnState)) {
	cfg := supervisorConfig{
		reconnect:      c.Reconnect,
		done:           c.Done,
		notify:         notify,
		attemptTimeout: defaultAttemptTimeout,
		backoff:        defaultBackoff,
		isAuthFatal:    isAuthFatal,
	}
	runSupervisor(ctx, cfg)
}

// runSupervisor is the testable inner loop.
func runSupervisor(ctx context.Context, cfg supervisorConfig) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-cfg.done():
		}

		// Connection dropped. If the parent ctx was the cause, exit.
		if ctx.Err() != nil {
			return
		}

		emit(cfg.notify, ConnState{Status: StatusDisconnected})

		if !attemptReconnect(ctx, cfg) {
			return
		}
	}
}

// attemptReconnect retries with backoff until success, fatal error, or ctx
// cancellation. Returns true if reconnected, false if the supervisor should
// stop (auth-fatal or ctx done).
func attemptReconnect(ctx context.Context, cfg supervisorConfig) bool {
	for attempt := 1; ; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, cfg.attemptTimeout)
		err := cfg.reconnect(attemptCtx)
		cancel()

		if err == nil {
			emit(cfg.notify, ConnState{Status: StatusConnected})
			return true
		}

		if cfg.isAuthFatal(err) {
			emit(cfg.notify, ConnState{Status: StatusAuthFailed, Attempt: attempt, Err: err})
			return false
		}

		emit(cfg.notify, ConnState{Status: StatusReconnecting, Attempt: attempt, Err: err})

		wait := backoffFor(cfg.backoff, attempt)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(wait):
		}
	}
}

// backoffFor returns the wait duration for the given 1-based attempt number,
// clamping to the last entry once the schedule is exhausted.
func backoffFor(schedule []time.Duration, attempt int) time.Duration {
	if len(schedule) == 0 {
		return time.Second
	}
	if attempt-1 >= len(schedule) {
		return schedule[len(schedule)-1]
	}
	return schedule[attempt-1]
}

func emit(notify func(ConnState), s ConnState) {
	if notify != nil {
		notify(s)
	}
}

// isAuthFatal reports whether the gateway error indicates the device token is
// missing or wrong. These errors will not resolve themselves; the user must
// restart so the interactive auth recovery flow can prompt for a new token.
func isAuthFatal(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "gateway token mismatch") ||
		strings.Contains(msg, "gateway token missing")
}

// IsAuthFatal exposes the auth-error predicate so main.go's startup auth
// recovery can share the same classification.
func IsAuthFatal(err error) bool { return isAuthFatal(err) }

// ErrSupervisorStopped is returned from helpers that observe a stopped
// supervisor; reserved for future use by callers that want a sentinel.
var ErrSupervisorStopped = errors.New("connection supervisor stopped")
