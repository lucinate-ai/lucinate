package app

import (
	"context"
	"fmt"

	"github.com/a3tai/openclaw-go/identity"

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
