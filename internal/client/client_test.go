package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lucinate-ai/lucinate/internal/config"
)

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
