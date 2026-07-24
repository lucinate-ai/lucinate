package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeServer is an in-process WS endpoint whose handler receives the
// upgraded connection. Send frames with conn.WriteJSON; read requests
// with conn.ReadMessage.
func fakeServer(t *testing.T, handler func(conn *websocket.Conn)) (wsURL string) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		handler(conn)
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

// readReq decodes one incoming JSON-RPC request from the fake server's
// side of the connection.
func readReq(t *testing.T, conn *websocket.Conn) (id uint64, method string, params json.RawMessage) {
	t.Helper()
	var req struct {
		ID     uint64          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("server read: %v", err)
	}
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("server decode: %v", err)
	}
	return req.ID, req.Method, req.Params
}

func respond(conn *websocket.Conn, id uint64, result any) error {
	return conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func notify(conn *websocket.Conn, method string, params any) error {
	return conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func dialT(t *testing.T, wsURL string) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// Two concurrent calls answered out of order must each receive their
// own result — the whole point of id correlation.
func TestCall_IDCorrelationOutOfOrder(t *testing.T) {
	release := make(chan struct{})
	wsURL := fakeServer(t, func(conn *websocket.Conn) {
		// Concurrent calls race to the wire, so key responses by the
		// method actually read, then answer in reverse arrival order.
		byMethod := map[string]uint64{}
		for range 2 {
			id, method, _ := readReq(t, conn)
			byMethod[method] = id
		}
		<-release
		_ = respond(conn, byMethod["second.method"], map[string]string{"who": "second"})
		_ = respond(conn, byMethod["first.method"], map[string]string{"who": "first"})
		// Hold the connection open until the test finishes so client
		// teardown doesn't race the asserts.
		_, _, _ = conn.ReadMessage()
	})
	c := dialT(t, wsURL)

	type result struct {
		Who string `json:"who"`
	}
	var wg sync.WaitGroup
	results := make([]result, 2)
	errs := make([]error, 2)
	for i, method := range []string{"first.method", "second.method"} {
		wg.Add(1)
		go func(i int, method string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			errs[i] = c.Call(ctx, method, nil, &results[i])
		}(i, method)
	}
	// Give both calls time to hit the wire before the server answers
	// in reverse order.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if results[0].Who != "first" || results[1].Who != "second" {
		t.Fatalf("responses crossed: got %+v", results)
	}
}

// A call whose context expires returns ctx.Err() and leaves the client
// usable for subsequent calls.
func TestCall_TimeoutLeavesClientUsable(t *testing.T) {
	wsURL := fakeServer(t, func(conn *websocket.Conn) {
		_, _, _ = readReq(t, conn) // swallow the first call, never answer
		id, _, _ := readReq(t, conn)
		_ = respond(conn, id, map[string]string{"ok": "yes"})
		_, _, _ = conn.ReadMessage()
	})
	c := dialT(t, wsURL)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := c.Call(ctx, "never.answered", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	var out struct {
		OK string `json:"ok"`
	}
	if err := c.Call(ctx2, "answered", nil, &out); err != nil {
		t.Fatalf("second call after timeout: %v", err)
	}
	if out.OK != "yes" {
		t.Fatalf("unexpected result: %+v", out)
	}
}

// Server pushes (method, no id) arrive on Notifications, interleaved
// with call responses, and don't disturb id correlation.
func TestNotifications_RoutedAroundCalls(t *testing.T) {
	wsURL := fakeServer(t, func(conn *websocket.Conn) {
		_ = notify(conn, "event", map[string]any{"type": "gateway.ready"})
		id, _, _ := readReq(t, conn)
		_ = notify(conn, "event", map[string]any{"type": "message.delta", "payload": map[string]string{"text": "hi"}})
		_ = respond(conn, id, map[string]string{"status": "streaming"})
		_, _, _ = conn.ReadMessage()
	})
	c := dialT(t, wsURL)

	waitNotification := func(wantType string) {
		t.Helper()
		select {
		case n := <-c.Notifications():
			if n.Method != "event" {
				t.Fatalf("method = %q, want event", n.Method)
			}
			var p struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(n.Params, &p); err != nil {
				t.Fatalf("params decode: %v", err)
			}
			if p.Type != wantType {
				t.Fatalf("type = %q, want %q", p.Type, wantType)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %s", wantType)
		}
	}

	waitNotification("gateway.ready")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out struct {
		Status string `json:"status"`
	}
	if err := c.Call(ctx, "prompt.submit", map[string]string{"text": "x"}, &out); err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.Status != "streaming" {
		t.Fatalf("status = %q", out.Status)
	}

	waitNotification("message.delta")
}

// A JSON-RPC error response surfaces as *RPCError with code + message.
func TestCall_RPCErrorSurfaced(t *testing.T) {
	wsURL := fakeServer(t, func(conn *websocket.Conn) {
		id, _, _ := readReq(t, conn)
		_ = conn.WriteJSON(map[string]any{
			"jsonrpc": "2.0", "id": id,
			"error": map[string]any{"code": 4001, "message": "session not found"},
		})
		_, _, _ = conn.ReadMessage()
	})
	c := dialT(t, wsURL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := c.Call(ctx, "session.history", map[string]string{"session_id": "nope"}, nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("want *RPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != 4001 || rpcErr.Message != "session not found" {
		t.Fatalf("unexpected error payload: %+v", rpcErr)
	}
}

// Close fails a pending call fast with ErrClosed, closes Done, and
// closes the Notifications channel.
func TestClose_FailsPendingAndSignalsDone(t *testing.T) {
	wsURL := fakeServer(t, func(conn *websocket.Conn) {
		_, _, _ = readReq(t, conn) // never answered
		_, _, _ = conn.ReadMessage()
	})
	c := dialT(t, wsURL)

	callErr := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		callErr <- c.Call(ctx, "hangs.forever", nil, nil)
	}()
	time.Sleep(50 * time.Millisecond) // let the call register + write
	_ = c.Close()

	select {
	case err := <-callErr:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("want ErrClosed, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending call not released by Close")
	}

	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done not closed")
	}
	if _, open := <-c.Notifications(); open {
		t.Fatal("notifications channel still open after Close")
	}
	if err := c.Call(context.Background(), "after.close", nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("call after close: want ErrClosed, got %v", err)
	}
	if err := c.Err(); err != nil {
		t.Fatalf("clean Close should leave Err nil, got %v", err)
	}
}

// A server that drops the connection wakes pending calls, closes Done,
// and records a non-nil Err — the supervisor's redial signal.
func TestServerDrop_SignalsDoneWithError(t *testing.T) {
	wsURL := fakeServer(t, func(conn *websocket.Conn) {
		_, _, _ = readReq(t, conn)
		_ = conn.Close() // hang up without answering
	})
	c := dialT(t, wsURL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Call(ctx, "dropped.mid.call", nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("want ErrClosed, got %v", err)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done not closed after server drop")
	}
	if c.Err() == nil {
		t.Fatal("Err should be non-nil after an unexpected drop")
	}
}

// An endpoint that rejects the upgrade (the gateway's auth layer does
// this with 403) surfaces as *UpgradeError carrying the status code.
func TestDial_UpgradeRejectionCarriesStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	var ue *UpgradeError
	if !errors.As(err, &ue) {
		t.Fatalf("want *UpgradeError, got %T: %v", err, err)
	}
	if ue.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", ue.StatusCode)
	}
}

// Frames batched newline-delimited inside a single WS message are all
// dispatched — upstream documents the wire as NDJSON.
func TestReadLoop_NewlineBatchedFrames(t *testing.T) {
	wsURL := fakeServer(t, func(conn *websocket.Conn) {
		batch := `{"jsonrpc":"2.0","method":"event","params":{"type":"a"}}` + "\n" +
			`{"jsonrpc":"2.0","method":"event","params":{"type":"b"}}`
		_ = conn.WriteMessage(websocket.TextMessage, []byte(batch))
		_, _, _ = conn.ReadMessage()
	})
	c := dialT(t, wsURL)

	for _, want := range []string{"a", "b"} {
		select {
		case n := <-c.Notifications():
			var p struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(n.Params, &p)
			if p.Type != want {
				t.Fatalf("type = %q, want %q", p.Type, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}
}
