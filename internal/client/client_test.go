package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/a3tai/openclaw-go/gateway"
	"github.com/a3tai/openclaw-go/identity"

	"github.com/lucinate-ai/lucinate/internal/config"
)

var _ IdentityStore = (*identity.Store)(nil)

type fakeIdentityStore struct {
	token string
}

func (f *fakeIdentityStore) LoadOrGenerate() (*identity.Identity, error) { return &identity.Identity{}, nil }
func (f *fakeIdentityStore) LoadDeviceToken() string                     { return f.token }
func (f *fakeIdentityStore) SaveDeviceToken(t string) error              { f.token = t; return nil }
func (f *fakeIdentityStore) ClearDeviceToken() error                     { f.token = ""; return nil }
func (f *fakeIdentityStore) Reset() error                                { f.token = ""; return nil }

func TestNewWithIdentityStore(t *testing.T) {
	c := NewWithIdentityStore(&config.Config{}, &fakeIdentityStore{})
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.store == nil {
		t.Fatal("expected store to be set")
	}
}

func TestSanitiseHost(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"localhost:8789", "localhost_8789"},
		{"gateway.example.com", "gateway.example.com"},
		{"gateway.example.com:443", "gateway.example.com_443"},
		{"my-host", "my-host"},
		{"host/with/slashes", "hostwithslashes"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitiseHost(tt.input)
			if got != tt.want {
				t.Errorf("sanitiseHost(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIdentityDirForEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		gatewayURL string
		wantSuffix string
		wantErr    bool
	}{
		{
			name:       "https with default port",
			gatewayURL: "https://gateway.example.com",
			wantSuffix: filepath.Join(".lucinate", "identity", "gateway.example.com"),
		},
		{
			name:       "http with explicit port",
			gatewayURL: "http://localhost:8789",
			wantSuffix: filepath.Join(".lucinate", "identity", "localhost_8789"),
		},
		{
			name:       "different endpoints produce different dirs",
			gatewayURL: "https://other.example.com",
			wantSuffix: filepath.Join(".lucinate", "identity", "other.example.com"),
		},
		{
			name:       "no host",
			gatewayURL: "file:///tmp/foo",
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := identityDirForEndpoint(tt.gatewayURL)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !filepath.IsAbs(got) {
				t.Errorf("expected absolute path, got %q", got)
			}
			suffix := filepath.Join(".lucinate", "identity")
			if got[len(got)-len(tt.wantSuffix):] != tt.wantSuffix {
				t.Errorf("got %q, want suffix %q", got, tt.wantSuffix)
			}
			_ = suffix
		})
	}
}

// newTestClient creates a Client backed by a temporary home directory.
// The config uses GatewayURL "http://example.com", so the identity directory
// will be <home>/.lucinate/identity/example.com/.
func newTestClient(t *testing.T) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	c, err := New(&config.Config{GatewayURL: "http://example.com", WSURL: "ws://example.com/ws"})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return c, dir
}

// testIdentityDir returns the identity directory for the test client's gateway URL.
func testIdentityDir(home string) string {
	return filepath.Join(home, ".lucinate", "identity", "example.com")
}

func TestClearToken_RemovesStoredToken(t *testing.T) {
	c, home := newTestClient(t)

	tokenPath := filepath.Join(testIdentityDir(home), "device-token")
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := c.ClearToken(); err != nil {
		t.Fatalf("ClearToken: %v", err)
	}
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Error("expected token file to be removed after ClearToken")
	}
}

func TestClearToken_NoopWhenAbsent(t *testing.T) {
	c, _ := newTestClient(t)
	if err := c.ClearToken(); err != nil {
		t.Errorf("ClearToken with no token should not error, got: %v", err)
	}
}

func TestResetIdentity_RemovesAllData(t *testing.T) {
	c, home := newTestClient(t)

	idDir := testIdentityDir(home)
	if err := os.MkdirAll(idDir, 0700); err != nil {
		t.Fatal(err)
	}
	keypairPath := filepath.Join(idDir, "keypair.json")
	tokenPath := filepath.Join(idDir, "device-token")
	if err := os.WriteFile(keypairPath, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := c.ResetIdentity(); err != nil {
		t.Fatalf("ResetIdentity: %v", err)
	}
	for _, path := range []string{keypairPath, tokenPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed after ResetIdentity", filepath.Base(path))
		}
	}
}

func TestResetIdentity_NoopWhenAbsent(t *testing.T) {
	c, _ := newTestClient(t)
	if err := c.ResetIdentity(); err != nil {
		t.Errorf("ResetIdentity with no files should not error, got: %v", err)
	}
}

// TestDone_PreClosedWhenNotConnected pins the contract Run's driver
// relies on: before the first successful dial, Done() must return a
// channel that is already closed so a select sitting on
// <-client.Done() doesn't hang waiting for a connection that hasn't
// been initiated yet.
func TestDone_PreClosedWhenNotConnected(t *testing.T) {
	c := NewWithIdentityStore(&config.Config{}, &fakeIdentityStore{})
	ch := c.Done()
	select {
	case <-ch:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Done() channel was not pre-closed when no gateway is attached")
	}
}

// TestRPCsReturnErrNotConnectedWhenGWNil pins the contract that RPC
// methods surface a clean ErrNotConnected when the gateway client is
// not attached, instead of dereferencing nil. The original bug: a
// dropped tailscale tunnel left the supervisor mid-reconnect with
// c.gw == nil; ChatSend then panicked when the user hit Enter.
func TestRPCsReturnErrNotConnectedWhenGWNil(t *testing.T) {
	c := NewWithIdentityStore(&config.Config{}, &fakeIdentityStore{})

	if _, err := c.ChatSend(context.Background(), "sess", "hi", "idem"); !errors.Is(err, ErrNotConnected) {
		t.Errorf("ChatSend: got %v, want ErrNotConnected", err)
	}
	if err := c.ChatAbort(context.Background(), "sess", "run"); !errors.Is(err, ErrNotConnected) {
		t.Errorf("ChatAbort: got %v, want ErrNotConnected", err)
	}
	if _, err := c.ListAgents(context.Background()); !errors.Is(err, ErrNotConnected) {
		t.Errorf("ListAgents: got %v, want ErrNotConnected", err)
	}
	if _, err := c.CreateSession(context.Background(), "agent", "main"); !errors.Is(err, ErrNotConnected) {
		t.Errorf("CreateSession: got %v, want ErrNotConnected", err)
	}
}

// TestRPC_BoundsContextWhenNoDeadline pins the anti-hang contract: an RPC
// whose caller passed a deadline-less context (every TUI background cmd
// uses context.Background()) is given defaultRPCTimeout, so a silently
// dropped transport cannot park the calling goroutine forever waiting on
// a reply that never comes.
func TestRPC_BoundsContextWhenNoDeadline(t *testing.T) {
	c := NewWithIdentityStore(&config.Config{}, &fakeIdentityStore{})
	// A non-nil gateway is required to reach the context-bounding path;
	// rpc makes no network call, so an unconnected client is fine here.
	c.gw = gateway.NewClient()

	_, ctx, cancel, err := c.rpc(context.Background())
	if err != nil {
		t.Fatalf("rpc: unexpected error %v", err)
	}
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("rpc did not bound a deadline-less context")
	}
	if d := time.Until(deadline); d <= 0 || d > defaultRPCTimeout+time.Second {
		t.Errorf("deadline %v out of expected ~%v window", d, defaultRPCTimeout)
	}
}

// TestRPC_RespectsCallerDeadline pins that rpc does not override a
// deadline the caller already set (so a caller wanting a tighter or
// looser bound keeps it).
func TestRPC_RespectsCallerDeadline(t *testing.T) {
	c := NewWithIdentityStore(&config.Config{}, &fakeIdentityStore{})
	c.gw = gateway.NewClient()

	caller, callerCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callerCancel()

	_, ctx, cancel, err := c.rpc(caller)
	if err != nil {
		t.Fatalf("rpc: unexpected error %v", err)
	}
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("rpc dropped the caller's deadline")
	}
	if d := time.Until(deadline); d > 3*time.Second {
		t.Errorf("rpc widened the caller's 2s deadline to %v", d)
	}
}

// TestRPC_ReturnsErrNotConnected pins that rpc surfaces ErrNotConnected
// (and a no-op cancel is unnecessary because cancel is nil) when no
// gateway is attached.
func TestRPC_ReturnsErrNotConnected(t *testing.T) {
	c := NewWithIdentityStore(&config.Config{}, &fakeIdentityStore{})
	if _, _, _, err := c.rpc(context.Background()); !errors.Is(err, ErrNotConnected) {
		t.Errorf("rpc with no gateway: got %v, want ErrNotConnected", err)
	}
}

// TestIsAuthFatal_Exported pins the exported predicate the startup
// recovery path uses so the auth-modal classification stays in lockstep
// with the supervisor's internal predicate.
func TestIsAuthFatal_Exported(t *testing.T) {
	if IsAuthFatal(nil) {
		t.Error("IsAuthFatal(nil) should be false")
	}
	if !IsAuthFatal(errStr("connect: gateway token mismatch")) {
		t.Error("IsAuthFatal should classify a token-mismatch error as fatal")
	}
	if IsAuthFatal(errStr("dial tcp: connection refused")) {
		t.Error("IsAuthFatal should treat transient errors as non-fatal")
	}
}

type errStr string

func (e errStr) Error() string { return string(e) }

// TestSetConnectTimeout_ClampsNonPositiveToZero pins the small but
// load-bearing rule: a non-positive duration disables the override and
// lets the SDK fall back to its own default. Without this clamp, a
// preferences file set to "0 seconds" would forward a zero deadline
// through gateway.WithConnectTimeout and trip the SDK's "must be > 0"
// guard on the next dial.
func TestSetConnectTimeout_ClampsNonPositiveToZero(t *testing.T) {
	c := NewWithIdentityStore(&config.Config{}, &fakeIdentityStore{})

	c.SetConnectTimeout(5 * time.Second)
	if got := c.connectTimeout; got != 5*time.Second {
		t.Errorf("after positive set: got %v want 5s", got)
	}

	c.SetConnectTimeout(0)
	if got := c.connectTimeout; got != 0 {
		t.Errorf("after zero set: got %v want 0", got)
	}

	c.SetConnectTimeout(3 * time.Second)
	c.SetConnectTimeout(-time.Second)
	if got := c.connectTimeout; got != 0 {
		t.Errorf("after negative set: got %v want 0", got)
	}
}

func TestStoreToken_PersistsToken(t *testing.T) {
	c, home := newTestClient(t)

	if err := c.StoreToken("my-gateway-token"); err != nil {
		t.Fatalf("StoreToken: %v", err)
	}
	tokenPath := filepath.Join(testIdentityDir(home), "device-token")
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("reading token file: %v", err)
	}
	if got := string(data); got != "my-gateway-token" {
		t.Errorf("stored token = %q, want %q", got, "my-gateway-token")
	}
}
