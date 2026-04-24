# Authentication

Repclaw authenticates to the OpenClaw gateway using device pairing. There are no usernames or passwords — each device generates a persistent Ed25519 keypair and an administrator approves it on the gateway.

## Configuration

The gateway URL is required and read from the environment:

```
OPENCLAW_GATEWAY_URL=https://your-gateway-host
```

This can be set in the shell or in a `.env` file in the working directory. Repclaw derives the WebSocket endpoint automatically by replacing `https://` with `wss://` (or `http://` with `ws://`) and appending `/ws`. Both values are held in `internal/config/config.go` as `Config.GatewayURL` and `Config.WSURL`.

## Device identity

On first run, `identity.Store.LoadOrGenerate()` creates an Ed25519 keypair in `~/.openclaw-go/identity/`. This directory is shared with other openclaw-go clients on the same machine. The keypair acts as a stable device identity across restarts.

## First-run pairing flow

1. Run `repclaw` — the client connects to the gateway with the device identity but no token. The gateway registers a pending pairing request.
2. On the gateway host, an administrator approves the device:
   ```
   openclaw device list --pending
   openclaw device approve <device-id>
   ```
3. Restart `repclaw`. The client reconnects; the gateway issues a device token, which is saved to `~/.openclaw-go/identity/` via `client.store.SaveDeviceToken()`.

The save is non-fatal — if it fails, a warning is logged but the session continues. On subsequent runs the saved token is loaded and presented on connect.

**Note:** Bootstrap tokens were removed in v0.10.2. Device pairing is the only setup path.

## Subsequent runs

1. Load `OPENCLAW_GATEWAY_URL` from environment.
2. Load the Ed25519 identity and stored device token from `~/.openclaw-go/identity/`.
3. Open a WebSocket connection to the gateway with the identity and token. The gateway SDK (`github.com/a3tai/openclaw-go`) attaches the token to all API calls.
4. On a successful `Hello` handshake, any refreshed token is saved back to disk.
5. Proceed to the agent picker.

If the token is expired or revoked the gateway rejects the connection and an error is shown in the agent picker. The user can press `r` to retry after the device has been re-approved.

## Scopes

The client connects with operator-level scopes: `ScopeOperatorRead`, `ScopeOperatorWrite`, `ScopeOperatorAdmin`, and `ScopeOperatorApprovals`. These are set in `internal/client/client.go` and are required for session management, exec approval, and agent administration.

## Stored files

| Path | Contents |
|---|---|
| `~/.openclaw-go/identity/` | Ed25519 keypair and device token (shared with other openclaw-go clients) |
| `~/.repclaw/config.json` | UI preferences — not authentication-related |
