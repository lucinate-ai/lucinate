package client

import (
	"path/filepath"
	"testing"
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
