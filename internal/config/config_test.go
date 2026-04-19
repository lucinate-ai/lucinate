package config

import (
	"os"
	"testing"
)

func TestDeriveWSURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "https to wss",
			input: "https://example.com",
			want:  "wss://example.com/ws",
		},
		{
			name:  "http to ws",
			input: "http://localhost:18789",
			want:  "ws://localhost:18789/ws",
		},
		{
			name:  "wss passthrough",
			input: "wss://example.com",
			want:  "wss://example.com/ws",
		},
		{
			name:  "ws passthrough",
			input: "ws://localhost:18789",
			want:  "ws://localhost:18789/ws",
		},
		{
			name:  "replaces existing path",
			input: "https://example.com/api/v1",
			want:  "wss://example.com/ws",
		},
		{
			name:  "preserves port",
			input: "https://myhost.example.com:8443",
			want:  "wss://myhost.example.com:8443/ws",
		},
		{
			name:    "unsupported scheme",
			input:   "ftp://example.com",
			wantErr: true,
		},
		{
			name:    "empty scheme",
			input:   "://example.com",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := deriveWSURL(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("deriveWSURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLoad_MissingGatewayURL(t *testing.T) {
	t.Setenv("OPENCLAW_GATEWAY_URL", "")

	// Ensure no .env file interferes.
	origDir, _ := os.Getwd()
	dir := t.TempDir()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing OPENCLAW_GATEWAY_URL")
	}
	if got := err.Error(); got != "OPENCLAW_GATEWAY_URL is required" {
		t.Errorf("unexpected error message: %s", got)
	}
}

func TestLoad_Success(t *testing.T) {
	t.Setenv("OPENCLAW_GATEWAY_URL", "https://mygateway.example.com")

	origDir, _ := os.Getwd()
	dir := t.TempDir()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.GatewayURL != "https://mygateway.example.com" {
		t.Errorf("GatewayURL = %q", cfg.GatewayURL)
	}
	if cfg.WSURL != "wss://mygateway.example.com/ws" {
		t.Errorf("WSURL = %q", cfg.WSURL)
	}
}

func TestLoad_InvalidURL(t *testing.T) {
	t.Setenv("OPENCLAW_GATEWAY_URL", "ftp://bad-scheme.example.com")

	origDir, _ := os.Getwd()
	dir := t.TempDir()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}
