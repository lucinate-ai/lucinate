package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/lucinate-ai/lucinate/app"
	"github.com/lucinate-ai/lucinate/internal/backend"
	openaiBackend "github.com/lucinate-ai/lucinate/internal/backend/openai"
	openclawBackend "github.com/lucinate-ai/lucinate/internal/backend/openclaw"
	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
	"github.com/lucinate-ai/lucinate/internal/version"
)

// secretAwareBackend wraps an OpenAI backend and persists the API
// key to disk whenever the auth-modal resolution stores one. The
// concrete *openai.Backend's StoreAPIKey only updates the in-memory
// copy; this wrapper layers the disk write on top so the next launch
// reuses the key without re-prompting.
type secretAwareBackend struct {
	*openaiBackend.Backend
	connID string
}

// StoreAPIKey persists the key for the connection and updates the
// backend's in-memory copy.
func (s *secretAwareBackend) StoreAPIKey(key string) error {
	if err := config.SetAPIKey(s.connID, key); err != nil {
		return fmt.Errorf("persist api key: %w", err)
	}
	return s.Backend.StoreAPIKey(key)
}

// backendFactory builds an unconnected backend for a stored
// connection. Dispatches on Connection.Type so future backend types
// can plug in here. The TUI calls Connect itself so it can route
// auth errors into modal recovery flows.
func backendFactory(conn *config.Connection) (backend.Backend, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection is nil")
	}
	switch conn.Type {
	case config.ConnTypeOpenClaw:
		cfg, err := config.FromConnection(conn)
		if err != nil {
			return nil, err
		}
		c, err := client.New(cfg)
		if err != nil {
			return nil, err
		}
		// Apply the user-configured WebSocket handshake deadline to
		// every (re)dial so slow backends don't trip the SDK's default.
		prefs := config.LoadPreferences()
		c.SetConnectTimeout(time.Duration(prefs.ConnectTimeoutSeconds) * time.Second)
		return openclawBackend.New(c), nil
	case config.ConnTypeOpenAI:
		apiKey := config.GetAPIKey(conn.ID)
		if env := os.Getenv("LUCINATE_OPENAI_API_KEY"); env != "" && apiKey == "" {
			apiKey = env
		}
		prefs := config.LoadPreferences()
		b, err := openaiBackend.New(openaiBackend.Options{
			ConnectionID:   conn.ID,
			BaseURL:        conn.URL,
			APIKey:         apiKey,
			DefaultModel:   conn.DefaultModel,
			ConnectTimeout: time.Duration(prefs.ConnectTimeoutSeconds) * time.Second,
		})
		if err != nil {
			return nil, err
		}
		return &secretAwareBackend{Backend: b, connID: conn.ID}, nil
	default:
		return nil, fmt.Errorf("unsupported connection type: %q", conn.Type)
	}
}

func main() {
	fs := flag.NewFlagSet("lucinate", flag.ExitOnError)
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.BoolVar(showVersion, "v", false, "print version and exit")
	_ = fs.Parse(os.Args[1:])

	if *showVersion {
		fmt.Printf("lucinate %s\n", version.Version)
		return
	}

	entry := config.ResolveEntryConnection()

	if err := app.Run(context.Background(), app.RunOptions{
		Store:          &entry.Store,
		Initial:        entry.Connection,
		BackendFactory: backendFactory,
		OnConnectionsChanged: func(c config.Connections) {
			if err := config.SaveConnections(c); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to save connections: %v\n", err)
			}
		},
	}); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
