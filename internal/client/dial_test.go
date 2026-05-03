package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a3tai/openclaw-go/identity"
	"github.com/a3tai/openclaw-go/protocol"
	"github.com/gorilla/websocket"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// newRealStoreInTempDir returns an identity.Store rooted at a
// per-test temp directory, optionally pre-seeded with a device token.
// Real signing is required because the gateway connect path runs the
// device key through ed25519 before the test handler ever sees it.
func newRealStoreInTempDir(t *testing.T, presetToken string) *identity.Store {
	t.Helper()
	store, err := identity.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("identity.NewStore: %v", err)
	}
	if _, err := store.LoadOrGenerate(); err != nil {
		t.Fatalf("LoadOrGenerate: %v", err)
	}
	if presetToken != "" {
		if err := store.SaveDeviceToken(presetToken); err != nil {
			t.Fatalf("SaveDeviceToken: %v", err)
		}
	}
	return store
}

// TestDial_RedialsAfterFirstTimePairing pins the first-time-pairing
// fix: when the bootstrap connect presents an empty device token and
// the gateway issues a fresh one in hello-ok, the client must close
// the bootstrap connection and re-dial so subsequent RPCs run on a
// connection that authenticated with the issued token. Skipping the
// re-dial leaves scoped operations (sessions.create most visibly)
// silently stalling on the bootstrap connection.
func TestDial_RedialsAfterFirstTimePairing(t *testing.T) {
	const issuedToken = "issued-from-hello"

	var mu sync.Mutex
	var presentedTokens []string

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		challenge := protocol.ConnectChallenge{Nonce: "n", Ts: time.Now().UnixMilli()}
		evData, _ := protocol.MarshalEvent("connect.challenge", challenge)
		_ = conn.WriteMessage(websocket.TextMessage, evData)

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var req protocol.Request
		if err := json.Unmarshal(msg, &req); err != nil {
			return
		}
		var params protocol.ConnectParams
		_ = json.Unmarshal(req.Params, &params)

		mu.Lock()
		presentedTokens = append(presentedTokens, params.Auth.Token)
		dialIndex := len(presentedTokens)
		mu.Unlock()

		hello := protocol.HelloOK{
			Type:     "hello-ok",
			Protocol: protocol.ProtocolVersion,
			Policy:   protocol.HelloPolicy{TickIntervalMs: 15000},
		}
		// Only the first dial — the bootstrap one — gets a fresh
		// device token. A real gateway behaves the same: subsequent
		// connects authenticate via the token they presented and get
		// no new one in reply.
		if dialIndex == 1 {
			hello.Auth = &protocol.HelloAuth{DeviceToken: issuedToken}
		}
		respData, _ := protocol.MarshalResponse(req.ID, hello)
		_ = conn.WriteMessage(websocket.TextMessage, respData)

		// Hold the connection open briefly so the client's read loop
		// has a moment to attach; avoids a flake where the test ends
		// before the SDK observes the response.
		_, _, _ = conn.ReadMessage()
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
	store := newRealStoreInTempDir(t, "")
	c := NewWithIdentityStore(&config.Config{GatewayURL: srv.URL, WSURL: wsURL}, store)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.dial(ctx); err != nil {
		t.Fatalf("dial: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(presentedTokens) != 2 {
		t.Fatalf("expected 2 dials (bootstrap + post-pair re-dial), got %d: %v", len(presentedTokens), presentedTokens)
	}
	if presentedTokens[0] != "" {
		t.Errorf("bootstrap dial should present no token, got %q", presentedTokens[0])
	}
	if presentedTokens[1] != issuedToken {
		t.Errorf("re-dial should present issued token %q, got %q", issuedToken, presentedTokens[1])
	}
	if got := store.LoadDeviceToken(); got != issuedToken {
		t.Errorf("issued token should be persisted, got %q", got)
	}
}

// TestDial_NoRedialWhenAlreadyPaired pins the symmetric case: a
// subsequent launch (we already have a stored token, gateway returns
// no new one) must not perform a wasteful second dial.
func TestDial_NoRedialWhenAlreadyPaired(t *testing.T) {
	var mu sync.Mutex
	dials := 0

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		mu.Lock()
		dials++
		mu.Unlock()

		challenge := protocol.ConnectChallenge{Nonce: "n", Ts: time.Now().UnixMilli()}
		evData, _ := protocol.MarshalEvent("connect.challenge", challenge)
		_ = conn.WriteMessage(websocket.TextMessage, evData)

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var req protocol.Request
		_ = json.Unmarshal(msg, &req)

		hello := protocol.HelloOK{
			Type:     "hello-ok",
			Protocol: protocol.ProtocolVersion,
			Policy:   protocol.HelloPolicy{TickIntervalMs: 15000},
		}
		respData, _ := protocol.MarshalResponse(req.ID, hello)
		_ = conn.WriteMessage(websocket.TextMessage, respData)
		_, _, _ = conn.ReadMessage()
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
	store := newRealStoreInTempDir(t, "pre-existing")
	c := NewWithIdentityStore(&config.Config{GatewayURL: srv.URL, WSURL: wsURL}, store)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.dial(ctx); err != nil {
		t.Fatalf("dial: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if dials != 1 {
		t.Fatalf("expected exactly 1 dial when already paired, got %d", dials)
	}
}
