package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// newTestClient creates a Client backed by a temporary home directory.
func newTestClient(t *testing.T) (*client.Client, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	c, err := client.New(&config.Config{GatewayURL: "http://example.com", WSURL: "ws://example.com/ws"})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return c, dir
}

// testIdentityDir returns the identity directory for the test client's gateway
// URL (http://example.com → ~/.lucinate/identity/example.com/).
func testIdentityDir(home string) string {
	return filepath.Join(home, ".lucinate", "identity", "example.com")
}

func seedToken(t *testing.T, home, token string) {
	t.Helper()
	idDir := testIdentityDir(home)
	if err := os.MkdirAll(idDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, "device-token"), []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
}

func readToken(home string) string {
	data, err := os.ReadFile(filepath.Join(testIdentityDir(home), "device-token"))
	if err != nil {
		return ""
	}
	return string(data)
}

func TestPromptAuthFix(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantResult bool
		wantToken  string // expected stored token ("" = absent)
	}{
		{
			name:       "choice 1 clears token and returns true",
			input:      "1\n",
			wantResult: true,
			wantToken:  "",
		},
		{
			name:       "empty input defaults to clear token",
			input:      "\n",
			wantResult: true,
			wantToken:  "",
		},
		{
			name:       "choice 2 resets identity and returns true",
			input:      "2\n",
			wantResult: true,
			wantToken:  "",
		},
		{
			name:       "choice 3 quits without modification",
			input:      "3\n",
			wantResult: false,
			wantToken:  "old-token",
		},
		{
			name:       "unknown choice quits without modification",
			input:      "x\n",
			wantResult: false,
			wantToken:  "old-token",
		},
		{
			name:       "EOF returns false",
			input:      "",
			wantResult: false,
			wantToken:  "old-token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, home := newTestClient(t)
			seedToken(t, home, "old-token")

			got := promptAuthFix(c, strings.NewReader(tt.input))

			if got != tt.wantResult {
				t.Errorf("promptAuthFix returned %v, want %v", got, tt.wantResult)
			}
			if gotToken := readToken(home); gotToken != tt.wantToken {
				t.Errorf("stored token = %q, want %q", gotToken, tt.wantToken)
			}
		})
	}
}

func TestPromptForToken(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantResult bool
		wantToken  string
	}{
		{
			name:       "stores provided token and returns true",
			input:      "my-gateway-token\n",
			wantResult: true,
			wantToken:  "my-gateway-token",
		},
		{
			name:       "empty input returns false",
			input:      "\n",
			wantResult: false,
			wantToken:  "",
		},
		{
			name:       "EOF returns false",
			input:      "",
			wantResult: false,
			wantToken:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, home := newTestClient(t)

			got := promptForToken(c, strings.NewReader(tt.input))

			if got != tt.wantResult {
				t.Errorf("promptForToken returned %v, want %v", got, tt.wantResult)
			}
			if gotToken := readToken(home); gotToken != tt.wantToken {
				t.Errorf("stored token = %q, want %q", gotToken, tt.wantToken)
			}
		})
	}
}
