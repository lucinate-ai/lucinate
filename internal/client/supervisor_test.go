package client

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// stateRecorder captures the sequence of ConnState values the supervisor emits.
type stateRecorder struct {
	mu     sync.Mutex
	states []ConnState
}

func (r *stateRecorder) notify(s ConnState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, s)
}

func (r *stateRecorder) snapshot() []ConnState {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ConnState, len(r.states))
	copy(out, r.states)
	return out
}

// fakeConn drives the supervisor: it owns a "done" channel and a queue of
// reconnect outcomes. Each reconnect attempt pops the next outcome and (if
// successful) replaces the done channel with a new open one.
type fakeConn struct {
	mu       sync.Mutex
	done     chan struct{}
	outcomes []error
	calls    int
}

func newFakeConn() *fakeConn {
	return &fakeConn{done: make(chan struct{})}
}

func (f *fakeConn) doneCh() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.done
}

func (f *fakeConn) reconnect(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if len(f.outcomes) == 0 {
		// No more outcomes scripted: behave as a permanent failure so the
		// supervisor doesn't loop forever in a misconfigured test.
		return errors.New("test: no more outcomes")
	}
	err := f.outcomes[0]
	f.outcomes = f.outcomes[1:]
	if err == nil {
		// Reconnected: install a fresh "alive" done channel.
		f.done = make(chan struct{})
	}
	return err
}

// trip closes the current done channel to simulate a connection drop.
func (f *fakeConn) trip() {
	f.mu.Lock()
	defer f.mu.Unlock()
	close(f.done)
}

// instantBackoff is used in tests so the supervisor doesn't actually sleep.
var instantBackoff = []time.Duration{0, 0, 0, 0, 0, 0}

func TestSupervisor_ReconnectsAfterDrop(t *testing.T) {
	fc := newFakeConn()
	fc.outcomes = []error{nil} // one successful reconnect

	rec := &stateRecorder{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		runSupervisor(ctx, supervisorConfig{
			reconnect:      fc.reconnect,
			done:           fc.doneCh,
			notify:         rec.notify,
			attemptTimeout: time.Second,
			backoff:        instantBackoff,
			isAuthFatal:    isAuthFatal,
		})
		close(done)
	}()

	fc.trip()

	// Wait for the supervisor to emit Connected then block on the new done.
	waitFor(t, func() bool {
		for _, s := range rec.snapshot() {
			if s.Status == StatusConnected {
				return true
			}
		}
		return false
	}, time.Second)

	cancel()
	<-done

	got := rec.snapshot()
	if len(got) < 2 {
		t.Fatalf("expected at least 2 states (disconnected, connected), got %d: %+v", len(got), got)
	}
	if got[0].Status != StatusDisconnected {
		t.Errorf("first emit: want Disconnected, got %v", got[0].Status)
	}
	if got[1].Status != StatusConnected {
		t.Errorf("second emit: want Connected, got %v", got[1].Status)
	}
}

func TestSupervisor_BackoffSequenceOnFailure(t *testing.T) {
	fc := newFakeConn()
	// Three transient failures then success.
	fc.outcomes = []error{errors.New("dial: ECONNREFUSED"), errors.New("dial: ECONNREFUSED"), errors.New("dial: ECONNREFUSED"), nil}

	rec := &stateRecorder{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		runSupervisor(ctx, supervisorConfig{
			reconnect:      fc.reconnect,
			done:           fc.doneCh,
			notify:         rec.notify,
			attemptTimeout: time.Second,
			backoff:        instantBackoff,
			isAuthFatal:    isAuthFatal,
		})
		close(done)
	}()

	fc.trip()

	waitFor(t, func() bool {
		for _, s := range rec.snapshot() {
			if s.Status == StatusConnected {
				return true
			}
		}
		return false
	}, time.Second)

	cancel()
	<-done

	got := rec.snapshot()

	// Expect: Disconnected, Reconnecting(1), Reconnecting(2), Reconnecting(3), Connected
	var reconnectingAttempts []int
	var sawConnected bool
	for _, s := range got {
		if s.Status == StatusReconnecting {
			reconnectingAttempts = append(reconnectingAttempts, s.Attempt)
		}
		if s.Status == StatusConnected {
			sawConnected = true
		}
	}
	if !sawConnected {
		t.Fatalf("never saw Connected: %+v", got)
	}
	wantAttempts := []int{1, 2, 3}
	if len(reconnectingAttempts) != len(wantAttempts) {
		t.Fatalf("reconnecting attempts: got %v want %v (full: %+v)", reconnectingAttempts, wantAttempts, got)
	}
	for i, a := range reconnectingAttempts {
		if a != wantAttempts[i] {
			t.Errorf("attempt %d: got %d want %d", i, a, wantAttempts[i])
		}
	}
}

func TestSupervisor_HaltsOnAuthFailure(t *testing.T) {
	fc := newFakeConn()
	fc.outcomes = []error{errors.New("connect: gateway token mismatch")}

	rec := &stateRecorder{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		runSupervisor(ctx, supervisorConfig{
			reconnect:      fc.reconnect,
			done:           fc.doneCh,
			notify:         rec.notify,
			attemptTimeout: time.Second,
			backoff:        instantBackoff,
			isAuthFatal:    isAuthFatal,
		})
		close(done)
	}()

	fc.trip()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("supervisor did not halt on auth-fatal error within 1s")
	}

	got := rec.snapshot()
	if len(got) == 0 || got[len(got)-1].Status != StatusAuthFailed {
		t.Fatalf("last state: want StatusAuthFailed, got %+v", got)
	}
	// And the supervisor must NOT have retried after the auth failure.
	if fc.calls != 1 {
		t.Errorf("reconnect calls after auth-fatal: want 1, got %d", fc.calls)
	}
}

func TestSupervisor_StopsOnContextCancel(t *testing.T) {
	fc := newFakeConn()
	// Never close fc.done; supervisor should sit on the initial wait.

	rec := &stateRecorder{}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		runSupervisor(ctx, supervisorConfig{
			reconnect:      fc.reconnect,
			done:           fc.doneCh,
			notify:         rec.notify,
			attemptTimeout: time.Second,
			backoff:        instantBackoff,
			isAuthFatal:    isAuthFatal,
		})
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("supervisor did not exit within 1s of ctx cancel")
	}

	if fc.calls != 0 {
		t.Errorf("reconnect should not have been called: got %d calls", fc.calls)
	}
}

func TestBackoffFor_ClampsToLastEntry(t *testing.T) {
	schedule := []time.Duration{1, 2, 3}
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 1}, {2, 2}, {3, 3}, {4, 3}, {99, 3},
	}
	for _, tc := range cases {
		got := backoffFor(schedule, tc.attempt)
		if got != tc.want {
			t.Errorf("backoffFor(attempt=%d): got %v want %v", tc.attempt, got, tc.want)
		}
	}
}

func TestIsAuthFatal(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("connect: dial tcp: connection refused"), false},
		{errors.New("connect: gateway token mismatch"), true},
		{errors.New("connect: gateway token missing"), true},
		{errors.New("websocket: bad handshake"), false},
	}
	for _, tc := range cases {
		got := isAuthFatal(tc.err)
		if got != tc.want {
			t.Errorf("isAuthFatal(%v): got %v want %v", tc.err, got, tc.want)
		}
	}
}

// waitFor polls until cond returns true or the deadline expires.
func waitFor(t *testing.T, cond func() bool, max time.Duration) {
	t.Helper()
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
