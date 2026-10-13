package rooms

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/lucinate-ai/lucinate/internal/backend/hermes/rpc"
)

func TestBackoff_GrowsExponentiallyAndCaps(t *testing.T) {
	b := NewBackoff()
	b.Base = 100 * time.Millisecond
	b.Max = 3 * time.Second

	want := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
		1600 * time.Millisecond,
		3 * time.Second,
		3 * time.Second,
	}
	for i, w := range want {
		got := b.Next()
		if got != w {
			t.Fatalf("Next() #%d = %v, want %v", i+1, got, w)
		}
	}
	if b.Attempt() != len(want) {
		t.Errorf("Attempt() = %d, want %d", b.Attempt(), len(want))
	}
}

func TestBackoff_ResetStartsTheScheduleOver(t *testing.T) {
	b := NewBackoff()
	b.Base = 50 * time.Millisecond
	for i := 0; i < 4; i++ {
		b.Next()
	}
	b.Reset()
	if b.Attempt() != 0 {
		t.Errorf("Attempt() after Reset = %d, want 0", b.Attempt())
	}
	if got := b.Next(); got != 50*time.Millisecond {
		t.Errorf("Next() after Reset = %v, want the base delay", got)
	}
}

func TestBackoff_PeekDoesNotAdvanceTheSchedule(t *testing.T) {
	b := NewBackoff()
	b.Base = 250 * time.Millisecond
	if got, want := b.Peek(), 250*time.Millisecond; got != want {
		t.Fatalf("Peek() = %v, want %v", got, want)
	}
	if b.Attempt() != 0 {
		t.Errorf("Peek advanced the attempt counter to %d", b.Attempt())
	}
	if got := b.Next(); got != 250*time.Millisecond {
		t.Errorf("Next() = %v, want the peeked delay", got)
	}
}

func TestBackoff_ZeroValueIsUsable(t *testing.T) {
	var b Backoff
	first := b.Next()
	if first <= 0 {
		t.Fatalf("zero-value Backoff.Next() = %v, want a positive delay", first)
	}
	if second := b.Next(); second <= first {
		t.Errorf("second delay %v should exceed the first %v", second, first)
	}
	if b.Peek() <= 0 {
		t.Errorf("Peek() = %v, want a positive delay", b.Peek())
	}
	if b.Describe() == "" {
		t.Error("Describe() should not be empty — it is what the status line shows")
	}
}

func TestIsDisconnect_ClassifiesTransportFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "eof", err: io.EOF, want: true},
		{name: "unexpected eof", err: io.ErrUnexpectedEOF, want: true},
		{name: "closed connection", err: net.ErrClosed, want: true},
		{name: "connection reset", err: errors.New("read tcp 127.0.0.1:5000: connection reset by peer"), want: true},
		{name: "broken pipe", err: errors.New("write tcp: broken pipe"), want: true},
		{name: "refused", err: errors.New("dial tcp 127.0.0.1:9119: connect: connection refused"), want: true},
		{name: "websocket close", err: errors.New("websocket: close 1006 (abnormal closure)"), want: true},
		{name: "502 upgrade", err: &rpc.UpgradeError{StatusCode: 502}, want: true},
		{name: "wrapped eof", err: errors.New("call groups.log: " + io.EOF.Error()), want: true},
		// A method error means the socket is fine — reconnecting would not help.
		{name: "rpc method error", err: &rpc.RPCError{Code: -32601, Message: "method not found"}, want: false},
		// A clean local close is not a dropped socket: the caller closed it.
		{name: "clean close", err: rpc.ErrClosed, want: false},
		{name: "plain problem", err: errors.New("room id is required"), want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsDisconnect(tc.err); got != tc.want {
				t.Errorf("IsDisconnect(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
