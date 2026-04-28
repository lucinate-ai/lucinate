# Authentication

This document covers OpenClaw device pairing — the auth flow used by the OpenClaw backend. The OpenAI-compatible backend uses a simpler bearer-token model documented in [connections.md](connections.md#auth-recovery-modals) and [connections.md](connections.md#secrets-storage).

For OpenClaw connections, lucinate authenticates using device pairing — no usernames or passwords. Each device generates a persistent Ed25519 keypair and an administrator approves it on the gateway.

## Configuration

OpenClaw URLs can be added via the connections picker (the default UX) or via `OPENCLAW_GATEWAY_URL` (auto-added on first run). See [connections.md](connections.md#startup-decision-tree) for the resolution order.

```
OPENCLAW_GATEWAY_URL=https://your-gateway-host
```

This can be set in the shell or in a `.env` file in the working directory. Lucinate derives the WebSocket endpoint automatically by replacing `https://` with `wss://` (or `http://` with `ws://`) and appending `/ws`. Both values are held in `internal/config/config.go` as `Config.GatewayURL` and `Config.WSURL`.

## Device identity

On first run, `identity.Store.LoadOrGenerate()` creates an Ed25519 keypair under `~/.lucinate/identity/<endpoint>/`, where `<endpoint>` is derived from the gateway URL's host and port (e.g. `gateway.example.com` or `localhost_8789`). Each gateway endpoint gets its own keypair and device token, so switching `OPENCLAW_GATEWAY_URL` does not overwrite existing credentials. The keypair acts as a stable device identity across restarts.

## First-run pairing flow

1. Run `lucinate` — the client connects to the gateway with the device identity but no token. The gateway registers a pending pairing request.
2. On the gateway host, an administrator approves the device:
   ```
   openclaw device list --pending
   openclaw device approve <device-id>
   ```
3. Restart `lucinate`. The client reconnects; the gateway issues a device token, which is saved to the endpoint's identity directory via `client.store.SaveDeviceToken()`.

The save is non-fatal — if it fails, a warning is logged but the session continues. On subsequent runs the saved token is loaded and presented on connect.

**Note:** Bootstrap tokens were removed in v0.10.2. Device pairing is the only setup path.

## Subsequent runs

1. Load `OPENCLAW_GATEWAY_URL` from environment.
2. Load the Ed25519 identity and stored device token from `~/.lucinate/identity/<endpoint>/`.
3. Open a WebSocket connection to the gateway with the identity and token. The gateway SDK (`github.com/a3tai/openclaw-go`) attaches the token to all API calls.
4. On a successful `Hello` handshake, any refreshed token is saved back to disk.
5. Proceed to the agent picker.

If the token is expired or revoked the gateway rejects the connection and an error is shown in the agent picker. The user can press `r` to retry after the device has been re-approved.

## Interactive auth recovery on connect

When the initial connect fails with an auth error, `connectWithAuth` in `main.go` handles it interactively before exiting:

- **Token mismatch** (`gateway token mismatch`) — the stored device token is no longer valid for this gateway (for example, the device was removed and re-added). The user is offered three choices:
  1. Clear the stored token and retry pairing (default).
  2. Reset the device identity entirely (new keypair) and retry pairing.
  3. Quit.
- **Token missing** (`gateway token missing`) — the gateway requires a pre-shared auth token that the client doesn't have. This can occur on a first-time connect, or as a follow-up after a clear/reset retry. The user is prompted to paste the gateway auth token, which is saved via `client.StoreToken()` and the connect is retried.

Both flows read from `os.Stdin`, so they only trigger in an interactive terminal. Non-interactive callers will see the original error and exit.

## Reconnect after disconnection

Once the TUI is running, a supervisor in `internal/client/supervisor.go` watches the gateway connection (`Client.Done()`) and reconnects automatically if it drops — for example, when the gateway is restarted.

- **Backoff schedule:** 1s, 2s, 4s, 8s, 15s, then 30s for every subsequent attempt. Each attempt has a 15s connect timeout (matching `connectTimeout` for the initial connect).
- **State surface:** the supervisor pushes `tui.ConnStateMsg` into the bubbletea program. The chat header shows `⚠ disconnected`, `⟳ reconnecting (attempt N)`, or `✖ auth failed — restart`. A one-line system message is added to the chat scrollback on disconnect and on recovery.
- **In-flight streams:** if a reply was streaming when the connection dropped, the placeholder is cleared so the input is usable again. The gateway has no resume protocol for an interrupted run; the partial reply is abandoned.
- **Auth failures:** if the gateway rejects the device token after restart (`gateway token mismatch` / `token missing`), the supervisor stops retrying. The chat banner instructs the user to quit (Ctrl+C) and restart so the interactive `connectWithAuth` flow can prompt for a fix — that flow needs `os.Stdin`, which bubbletea owns once the TUI is up.

## Scopes

The client connects with operator-level scopes: `ScopeOperatorRead`, `ScopeOperatorWrite`, `ScopeOperatorAdmin`, and `ScopeOperatorApprovals`. These are set in `internal/client/client.go` and are required for session management, exec approval, and agent administration.

## Stored files

| Path | Contents |
|---|---|
| `~/.lucinate/identity/<endpoint>/` | Ed25519 keypair and device token (per gateway endpoint) |
| `~/.lucinate/secrets/secrets.json` | OpenAI-compat API keys, keyed by connection ID (mode 0600) — see [connections.md](connections.md#secrets-storage) |
| `~/.lucinate/connections.json` | Saved connection records — see [connections.md](connections.md) |
| `~/.lucinate/config.json` | UI preferences — not authentication-related |
