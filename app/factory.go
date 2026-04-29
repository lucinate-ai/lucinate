package app

import (
	"fmt"
	"os"
	"time"

	openaiBackend "github.com/lucinate-ai/lucinate/internal/backend/openai"
	openclawBackend "github.com/lucinate-ai/lucinate/internal/backend/openclaw"
	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// secretAwareOpenAIBackend layers persistence onto the OpenAI backend's
// auth-modal StoreAPIKey so the next launch reuses the key without re-
// prompting. The concrete *openai.Backend's StoreAPIKey only updates
// the in-memory copy.
type secretAwareOpenAIBackend struct {
	*openaiBackend.Backend
	connID string
}

// StoreAPIKey persists the key under the connection ID in the secrets
// store and updates the backend's in-memory copy.
func (s *secretAwareOpenAIBackend) StoreAPIKey(key string) error {
	if err := config.SetAPIKey(s.connID, key); err != nil {
		return fmt.Errorf("persist api key: %w", err)
	}
	return s.Backend.StoreAPIKey(key)
}

// DefaultBackendFactory builds an unconnected backend for a stored
// connection by dispatching on Connection.Type. Auth resolution mirrors
// the CLI: API keys are read from the per-connection secrets store and
// fall back to LUCINATE_OPENAI_API_KEY when the store is empty. The TUI
// calls Connect on the returned backend itself so it can route auth
// errors into the modal recovery flows.
//
// Embedders pass this directly via RunOptions.BackendFactory unless
// they need to register additional ConnectionTypes or substitute a
// different secrets store, in which case they wrap or replace it.
func DefaultBackendFactory(conn *Connection) (Backend, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection is nil")
	}
	// User-configured WebSocket / HTTP handshake deadline applied to
	// every (re)dial so slow backends don't trip the SDK's default.
	prefs := config.LoadPreferences()
	connectTimeout := time.Duration(prefs.ConnectTimeoutSeconds) * time.Second
	switch conn.Type {
	case ConnTypeOpenClaw:
		cfg, err := config.FromConnection(conn)
		if err != nil {
			return nil, err
		}
		c, err := client.New(cfg)
		if err != nil {
			return nil, err
		}
		c.SetConnectTimeout(connectTimeout)
		return openclawBackend.New(c), nil
	case ConnTypeOpenAI:
		apiKey := config.GetAPIKey(conn.ID)
		if env := os.Getenv("LUCINATE_OPENAI_API_KEY"); env != "" && apiKey == "" {
			apiKey = env
		}
		b, err := openaiBackend.New(openaiBackend.Options{
			ConnectionID:   conn.ID,
			BaseURL:        conn.URL,
			APIKey:         apiKey,
			DefaultModel:   conn.DefaultModel,
			ConnectTimeout: connectTimeout,
		})
		if err != nil {
			return nil, err
		}
		return &secretAwareOpenAIBackend{Backend: b, connID: conn.ID}, nil
	default:
		return nil, fmt.Errorf("unsupported connection type: %q", conn.Type)
	}
}
