package config

import (
	"fmt"
	"net/url"
	"os"

	"github.com/joho/godotenv"
)

// Config holds the OpenClaw gateway connection settings.
type Config struct {
	GatewayURL string
	WSURL      string
}

// Load reads configuration from environment variables (and .env file if present).
func Load() (*Config, error) {
	_ = godotenv.Load() // silently ignore missing .env

	gatewayURL := os.Getenv("OPENCLAW_GATEWAY_URL")
	if gatewayURL == "" {
		return nil, fmt.Errorf("OPENCLAW_GATEWAY_URL is required")
	}

	return New(gatewayURL)
}

// FromConnection builds a Config from a stored Connection, deriving
// the matching WebSocket endpoint. Used by the TUI now that the
// connection lifecycle (selection, persistence, mid-session switch)
// happens above the gateway-client layer.
func FromConnection(conn *Connection) (*Config, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection is required")
	}
	return New(conn.URL)
}

// New builds a Config from a gateway URL, deriving the matching WebSocket
// endpoint. Useful for embedders that obtain the gateway URL from somewhere
// other than the OPENCLAW_GATEWAY_URL environment variable.
func New(gatewayURL string) (*Config, error) {
	wsURL, err := deriveWSURL(gatewayURL)
	if err != nil {
		return nil, fmt.Errorf("invalid gateway URL: %w", err)
	}
	return &Config{
		GatewayURL: gatewayURL,
		WSURL:      wsURL,
	}, nil
}

func deriveWSURL(gatewayURL string) (string, error) {
	u, err := url.Parse(gatewayURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "wss", "ws":
		// already correct
	default:
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	u.Path = "/ws"
	return u.String(), nil
}
