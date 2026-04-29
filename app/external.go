package app

import (
	"context"
	"fmt"

	"github.com/a3tai/openclaw-go/identity"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
	"github.com/lucinate-ai/lucinate/internal/tui"
)

// IdentityStore is the persistence interface for the device keypair and
// device token. Type-aliased here so embedders do not need to import
// internal packages.
type IdentityStore = client.IdentityStore

// Action mirrors tui.Action so embedders can read the slice published
// through RunOptions.OnActionsChanged without importing the internal
// tui package.
type Action = tui.Action

// Identity is the loaded device identity. Re-exported so embedders that
// implement IdentityStore can return values of this type.
type Identity = identity.Identity

// Client is an opaque, connected handle to the OpenClaw gateway. Embedders
// obtain one from Connect and pass it to RunOptions.Client. The only method
// they need to call directly is Close.
type Client = client.Client

// Backend is the chat-service abstraction the TUI consumes. Values of
// this type are produced by a BackendFactory and passed to
// RunOptions.Backend (single-connection mode) or returned from the
// factory the TUI calls itself (managed mode).
type Backend = backend.Backend

// Connection is a saved chat-service target. The picker in managed
// mode adds, edits, and deletes Connections in the surrounding store.
type Connection = config.Connection

// Connections is the on-disk persistence shape for the connection
// store: a list of Connection plus the ID to auto-pick at startup.
type Connections = config.Connections

// ConnectionType identifies the protocol/backend a Connection points
// at. Use the ConnTypeOpenClaw / ConnTypeOpenAI constants.
type ConnectionType = config.ConnectionType

// ConnectionFields is the input shape Connections.Add and Update take.
// Only the fields relevant to the chosen Type need to be populated.
type ConnectionFields = config.ConnectionFields

// EntryConnection is the resolved startup decision returned from
// ResolveEntryConnection: either an explicit Connection to use, or a
// directive to drop the user into the picker.
type EntryConnection = config.EntryConnection

// Connection-type constants re-exported so embedders can build
// Connection values without importing internal/config.
const (
	ConnTypeOpenClaw = config.ConnTypeOpenClaw
	ConnTypeOpenAI   = config.ConnTypeOpenAI
)

// DataDirEnvVar is the environment variable that overrides the
// default lucinate state directory (where connections, secrets,
// identity, and agents live). Embedders whose host platform doesn't
// expose a writable user home directory — typically native-platform
// hosts whose process inherits a read-only bundle path from
// os.UserHomeDir() — set this before invoking app.New / app.Run;
// pointing at a writable sandboxed location keeps every persistence
// path inside the host's data container.
const DataDirEnvVar = config.DataDirEnvVar

// DataDir returns the resolved root directory used for all on-disk
// lucinate state. Resolution: LUCINATE_DATA_DIR if set, else
// <UserHomeDir>/.lucinate. Embedders typically don't need to call
// this — every persistence helper goes through it transparently —
// but it is exposed so embedders can surface the location to the
// user (e.g. "your data lives at …").
func DataDir() (string, error) { return config.DataDir() }

// LoadConnections reads the connection store from the lucinate
// data dir's connections.json, returning an empty store on first
// run or when the file is missing or unreadable.
func LoadConnections() Connections { return config.LoadConnections() }

// SaveConnections writes the connection store to disk atomically. The
// CLI calls this from the OnConnectionsChanged callback so a successful
// connect persists; embedders that own persistence elsewhere can skip
// it.
func SaveConnections(c Connections) error { return config.SaveConnections(c) }

// ResolveEntryConnection runs the startup decision tree (env vars,
// stored DefaultID, single-entry auto-pick, picker fallback) against
// the on-disk store. Embedders that don't honour env vars and want
// pure on-disk behaviour can call LoadConnections directly.
func ResolveEntryConnection() EntryConnection { return config.ResolveEntryConnection() }

// Connect builds a gateway client for the given URL, hooks it up to the
// supplied IdentityStore, and performs the connect handshake. The returned
// *Client must be Closed after Run completes.
func Connect(ctx context.Context, gatewayURL string, store IdentityStore) (*Client, error) {
	cfg, err := config.New(gatewayURL)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	c := client.NewWithIdentityStore(cfg, store)
	if err := c.Connect(ctx); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("connect: %w", err)
	}
	return c, nil
}
