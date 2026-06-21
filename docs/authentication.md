# Authentication

This document covers OpenClaw device pairing — the auth flow used by the OpenClaw backend. The OpenAI-compatible and Hermes backends use a simpler bearer-token model documented in [connections.md](connections.md#auth-recovery-modals) and [connections.md](connections.md#secrets-storage).

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

1. Run `lucinate` — the client connects to the gateway with the device identity but no token. The gateway responds `NOT_PAIRED` and registers a pending pairing request.
2. The connecting view enters its `subStatePairingRequired` modal (`internal/tui/connecting.go`) with on-screen instructions. On the gateway host, an administrator approves the device:
   ```
   openclaw device list --pending
   openclaw device approve <device-id>
   ```
3. The user presses Enter in the modal. `authResolvedMsg{}` triggers `retryConnect` (`internal/tui/app.go`), which re-invokes `Connect` on the same backend. The gateway accepts the connection and issues a fresh device token in `hello.Auth.DeviceToken`, which `client.dial` persists via `store.SaveDeviceToken`.
4. `dial` then closes the bootstrap connection and re-dials with the freshly-issued token (see [Re-dial after first-time pairing](#re-dial-after-first-time-pairing)). The TUI advances to the agent picker — no process restart required.

The token save is non-fatal — if it fails, a warning is logged but the session continues. On subsequent runs the saved token is loaded and presented on connect.

**Note:** Bootstrap tokens were removed in v0.10.2. Device pairing is the only setup path.

## Re-dial after first-time pairing

The bootstrap connect for a freshly approved device authenticates by Ed25519 device-key signature alone — the token field on `connect` is empty because the client doesn't have one yet. The gateway issues the first device token in `hello-ok`. Reusing that bootstrap connection for scoped RPCs leaves `sessions.create` silently stalled; the OpenClaw protocol expects scoped operations to run on a connection that authenticated *with* the token at connect time.

`internal/client/client.go` handles this inline: when `dial` observes that it presented an empty token AND the gateway returned a non-empty `hello.Auth.DeviceToken`, it persists the token, closes the bootstrap `gateway.Client`, and dials a second time so the surviving connection carries the issued token. Subsequent launches (token already on disk → bootstrap presents it → gateway returns no new one) skip the second dial. Tests pin both branches in `internal/client/dial_test.go`.

The TUI also wraps `CreateSession` (`internal/tui/app.go`) in a per-call `context.WithTimeout` derived as `2 ×` the configured connect timeout, so any future stall in this region surfaces as a UI error in the agent picker rather than freezing the view.

## Subsequent runs

1. Load `OPENCLAW_GATEWAY_URL` from environment, or use a saved connection from `~/.lucinate/connections.json`.
2. Load the Ed25519 identity and stored device token from `~/.lucinate/identity/<endpoint>/`.
3. Open a WebSocket connection to the gateway with the identity and token. The gateway SDK (`github.com/a3tai/openclaw-go`) attaches the token to all API calls.
4. On a successful `Hello` handshake, any refreshed token is saved back to disk; no second dial is needed because the connection already authenticated with a token.
5. Proceed to the agent picker.

If the token is expired or revoked the gateway rejects the connection and the connecting view opens the appropriate auth-recovery modal (see below).

## Interactive auth recovery on connect

When `Connect` fails with a recognised auth error, `runConnect` in `internal/tui/app.go` classifies it via the `isNotPairedErr` / `isTokenMismatchErr` / `isTokenMissingErr` / `isAPIKeyErr` predicates and `handleConnectResult` opens the matching modal sub-state in `internal/tui/connecting.go`:

- **Not paired** (`NOT_PAIRED`) — pairing-required modal: instructions to run `openclaw device approve` on the gateway host, then Enter to retry. See [First-run pairing flow](#first-run-pairing-flow).
- **Token mismatch** (`gateway token mismatch`) — the stored device token is no longer valid for this gateway (for example, the device was removed and re-added). The modal offers three choices, each routed through the `DeviceTokenAuth` sub-interface on the live backend:
  1. Clear the stored token and retry pairing (`ClearToken`, default).
  2. Reset the device identity entirely (new keypair) and retry pairing (`ResetIdentity`).
  3. Cancel back to the connections picker.
- **Token missing** (`gateway token missing`) — text prompt for a pre-shared token; submission stores via `DeviceTokenAuth.StoreToken` and the connect is retried.
- **API key required** (HTTP 401/403 from any `/v1` request on OpenAI-compat or Hermes connections) — text prompt for an API key; submission stores via `APIKeyAuth.StoreAPIKey`.

All four flows happen inside the running TUI — modal text inputs, not stdin. Cancellation routes back to the connections picker; the active backend is closed before the next pick.

## Reconnect after disconnection

Once the TUI is running, a supervisor in `internal/client/supervisor.go` watches the gateway connection (`Client.Done()`) and reconnects automatically if it drops — for example, when the gateway is restarted.

- **Backoff schedule:** 1s, 2s, 4s, 8s, 15s, then 30s for every subsequent attempt. Each attempt's connect timeout comes from `prefs.ConnectTimeoutSeconds` (default 15s), the same knob that floors the initial connect.
- **State surface:** the supervisor pushes `tui.ConnStateMsg` into the bubbletea program. The chat header shows `⚠ disconnected`, `⟳ reconnecting (attempt N)`, or `✖ auth failed`. A one-line system message is added to the chat scrollback on disconnect and on recovery.
- **In-flight streams:** if a reply was streaming when the connection dropped, the placeholder is cleared so the input is usable again. The gateway has no resume protocol for an interrupted run; the partial reply is abandoned.
- **Auth failures:** if the gateway rejects the device token mid-session (`gateway token mismatch` / `token missing`), the supervisor stops retrying. The chat banner advises switching connections via `/connections` so the connecting view's auth-recovery modal can run on the chosen connection. The supervisor cannot drive the modal itself — that flow lives on the connect path, not the reconnect path.

## Scopes

The client connects with operator-level scopes: `ScopeOperatorRead`, `ScopeOperatorWrite`, `ScopeOperatorAdmin`, and `ScopeOperatorApprovals`. These are the default requested in `internal/client/client.go` and are required for session management, exec approval, and agent administration. The gateway grants the intersection of the requested scopes and those the device token actually carries, so the effective set can be narrower.

`OPENCLAW_OPERATOR_SCOPES` (comma-separated, e.g. `operator.read,operator.write,operator.approvals`) overrides the default requested set. This is needed when the device token is bounded to a subset — such as a setup-code bootstrap credential that cannot carry `operator.admin` — because requesting scopes beyond what the token grants is rejected as a scope mismatch.

> **Pitfall:** because `config.Load()` reads `.env` from the working directory, an `OPENCLAW_OPERATOR_SCOPES` left in a `.env` silently bounds scopes for *every* connection, regardless of which gateway is selected. The integration-test setup scripts write such a file (to `test/integration/integration.env`, not the repo root, and remove it on teardown). If admin-only operations such as creating an agent fail with `missing scope: operator.admin` despite a token that should carry admin, check for a stray `OPENCLAW_OPERATOR_SCOPES`. Admin-only client operations fail fast with a message pointing here rather than surfacing the raw gateway error.

## Stored files

| Path | Contents |
|---|---|
| `~/.lucinate/identity/<endpoint>/` | Ed25519 keypair and device token (per gateway endpoint) |
| `~/.lucinate/secrets/secrets.json` | OpenAI-compat and Hermes API keys, keyed by connection ID (mode 0600) — see [connections.md](connections.md#secrets-storage) |
| `~/.lucinate/connections.json` | Saved connection records — see [connections.md](connections.md) |
| `~/.lucinate/hermes/<conn-id>/` | Per-connection Hermes state: `last_response_id` pointer + capped prompts log — see [backend_hermes.md](backend_hermes.md#local-state) |
| `~/.lucinate/config.json` | UI preferences — not authentication-related |
