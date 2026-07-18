# Authentication Specification

## Purpose

Authenticate lucinate to its backends. For OpenClaw connections this is device
pairing — no usernames or passwords: each device holds a persistent Ed25519 keypair
and an administrator approves it on the gateway. The OpenAI-compatible and Hermes
backends instead use a simpler bearer-token (API key) model (see the `connections` and
`backend-hermes` specs). This spec covers the OpenClaw device-pairing flow, identity and
token storage, interactive auth recovery, automatic reconnection, and operator scopes.

## Requirements

### Requirement: OpenClaw device-pairing authentication model

For OpenClaw connections the system SHALL authenticate by device pairing rather than
usernames or passwords. Each device generates a persistent Ed25519 keypair that acts as a
stable device identity across restarts, and an administrator approves that device on the
gateway before it can operate. Bootstrap tokens were removed in v0.10.2; device pairing is
the only setup path.

#### Scenario: No password prompt
- **WHEN** a user connects to an OpenClaw gateway
- **THEN** the system authenticates using the device's Ed25519 identity and (once issued) a device token
- **AND** never prompts for a username or password

### Requirement: Gateway URL configuration and WebSocket derivation

The system SHALL accept an OpenClaw gateway URL from the connections picker (the default UX)
or from the `OPENCLAW_GATEWAY_URL` environment variable (auto-added on first run), which may
be set in the shell or in a `.env` file in the working directory. The system SHALL derive the
WebSocket endpoint automatically by replacing `https://` with `wss://` (or `http://` with
`ws://`) and appending `/ws`. Both values are held in `internal/config/config.go` as
`Config.GatewayURL` and `Config.WSURL`. Resolution order is covered by the `connections` spec.

#### Scenario: WebSocket endpoint derived from an HTTPS gateway URL
- **GIVEN** `OPENCLAW_GATEWAY_URL=https://your-gateway-host`
- **WHEN** the client prepares to connect
- **THEN** `Config.WSURL` is `wss://your-gateway-host/ws`

#### Scenario: WebSocket endpoint derived from an HTTP gateway URL
- **GIVEN** a gateway URL beginning `http://`
- **WHEN** the client prepares to connect
- **THEN** the scheme becomes `ws://` and `/ws` is appended

### Requirement: Per-endpoint device identity generation and storage

On first run the system SHALL create an Ed25519 keypair via `identity.Store.LoadOrGenerate()`
under `~/.lucinate/identity/<endpoint>/`, where `<endpoint>` is derived from the gateway URL's
host and port (e.g. `gateway.example.com` or `localhost_8789`). Each gateway endpoint SHALL get
its own keypair and device token, so switching `OPENCLAW_GATEWAY_URL` does not overwrite
existing credentials. The keypair SHALL be reused across restarts as a stable device identity.

#### Scenario: Keypair generated on first run
- **GIVEN** no identity exists for the target endpoint
- **WHEN** the client starts
- **THEN** `LoadOrGenerate()` creates an Ed25519 keypair under `~/.lucinate/identity/<endpoint>/`

#### Scenario: Switching gateways preserves existing credentials
- **GIVEN** an existing keypair and device token for endpoint A
- **WHEN** `OPENCLAW_GATEWAY_URL` is changed to endpoint B
- **THEN** endpoint B gets its own keypair and token
- **AND** endpoint A's credentials are left intact

### Requirement: First-run pairing flow

On first connection with a device that has no token, the system SHALL guide the user through
gateway approval and obtain a device token without requiring a process restart. The flow is:

1. Running `lucinate` connects to the gateway with the device identity but no token; the
   gateway responds `NOT_PAIRED` and registers a pending pairing request.
2. The connecting view enters its `subStatePairingRequired` modal (`internal/tui/connecting.go`)
   with on-screen instructions. On the gateway host an administrator approves the device with
   `openclaw device list --pending` then `openclaw device approve <device-id>`.
3. The user presses Enter in the modal. `authResolvedMsg{}` triggers `retryConnect`
   (`internal/tui/app.go`), which re-invokes `Connect` on the same backend. The gateway accepts
   the connection and issues a fresh device token in `hello.Auth.DeviceToken`, which
   `client.dial` persists via `store.SaveDeviceToken`.
4. `dial` closes the bootstrap connection and re-dials with the freshly-issued token (see the
   re-dial requirement). The TUI advances to the agent picker with no process restart.

The token save SHALL be non-fatal — if it fails, a warning is logged but the session continues.

#### Scenario: Unpaired device on first connect
- **GIVEN** a device with no stored token
- **WHEN** it connects to the gateway
- **THEN** the gateway responds `NOT_PAIRED` and registers a pending pairing request
- **AND** the connecting view shows the `subStatePairingRequired` modal with approval instructions

#### Scenario: Retry after administrator approval
- **GIVEN** the pairing-required modal is showing and an administrator has run `openclaw device approve <device-id>`
- **WHEN** the user presses Enter
- **THEN** `authResolvedMsg{}` triggers `retryConnect`, the gateway issues a token in `hello.Auth.DeviceToken`, and it is persisted via `store.SaveDeviceToken`
- **AND** the TUI advances to the agent picker without a restart

#### Scenario: Token save fails
- **WHEN** persisting the newly issued device token fails
- **THEN** a warning is logged
- **AND** the session continues

### Requirement: Token re-dial after first-time pairing

When the bootstrap connect for a freshly approved device presented an empty token (it
authenticated by Ed25519 device-key signature alone) AND the gateway returned a non-empty
`hello.Auth.DeviceToken`, the system SHALL persist that token, close the bootstrap
`gateway.Client`, and dial a second time so the surviving connection carries the issued token.
This is required because the OpenClaw protocol expects scoped operations to run on a connection
that authenticated *with* the token at connect time; reusing the bootstrap connection for scoped
RPCs leaves `sessions.create` silently stalled. On subsequent launches (token already on disk →
bootstrap presents it → gateway returns no new one) the second dial SHALL be skipped. Both
branches are pinned by tests in `internal/client/dial_test.go`. The TUI also wraps
`CreateSession` (`internal/tui/app.go`) in a per-call `context.WithTimeout` derived as `2×` the
configured connect timeout, so any future stall in this region surfaces as a UI error in the
agent picker rather than freezing the view.

#### Scenario: Second dial after first pairing
- **GIVEN** `dial` presented an empty token AND the gateway returned a non-empty `hello.Auth.DeviceToken`
- **WHEN** the handshake completes
- **THEN** the token is persisted, the bootstrap `gateway.Client` is closed, and a second dial is made carrying the issued token

#### Scenario: No re-dial on subsequent launches
- **GIVEN** a device token already on disk that the bootstrap presents at connect
- **WHEN** the gateway returns no new token
- **THEN** the second dial is skipped

#### Scenario: Session creation stall surfaces as a UI error
- **WHEN** `CreateSession` does not return within `2×` the configured connect timeout
- **THEN** the agent picker shows a UI error rather than freezing

### Requirement: Subsequent-run authentication

On runs after the first pairing, the system SHALL authenticate using the stored identity and
token without re-pairing:

1. Load `OPENCLAW_GATEWAY_URL` from the environment, or use a saved connection from
   `~/.lucinate/connections.json`.
2. Load the Ed25519 identity and stored device token from `~/.lucinate/identity/<endpoint>/`.
3. Open a WebSocket connection with the identity and token; the gateway SDK
   (`github.com/a3tai/openclaw-go`) attaches the token to all API calls.
4. On a successful `Hello` handshake, save any refreshed token back to disk; no second dial is
   needed because the connection already authenticated with a token.
5. Proceed to the agent picker.

If the token is expired or revoked, the gateway SHALL reject the connection and the connecting
view SHALL open the appropriate auth-recovery modal.

#### Scenario: Valid stored token
- **GIVEN** a stored Ed25519 identity and valid device token for the endpoint
- **WHEN** the client connects
- **THEN** the `Hello` handshake succeeds, any refreshed token is saved, and the TUI proceeds to the agent picker

#### Scenario: Expired or revoked token
- **GIVEN** a stored token that the gateway no longer accepts
- **WHEN** the client connects
- **THEN** the gateway rejects the connection and the connecting view opens the matching auth-recovery modal

### Requirement: Interactive auth recovery on connect

When `Connect` fails with a recognised auth error, `runConnect` (`internal/tui/app.go`) SHALL
classify it via the `isNotPairedErr` / `isTokenMismatchErr` / `isTokenMissingErr` / `isAPIKeyErr`
predicates and `handleConnectResult` SHALL open the matching modal sub-state in
`internal/tui/connecting.go`. All four flows happen inside the running TUI as modal text inputs,
not stdin; cancellation routes back to the connections picker and the active backend is closed
before the next pick. The four cases:

- **Not paired** (`NOT_PAIRED`) — pairing-required modal: instructions to run
  `openclaw device approve` on the gateway host, then Enter to retry.
- **Token mismatch** (`gateway token mismatch`) — the stored device token is no longer valid for
  this gateway (e.g. the device was removed and re-added). The modal offers three choices, each
  routed through the `DeviceTokenAuth` sub-interface on the live backend: (1) clear the stored
  token and retry pairing (`ClearToken`, default); (2) reset the device identity entirely (new
  keypair) and retry pairing (`ResetIdentity`); (3) cancel back to the connections picker.
- **Token missing** (`gateway token missing`) — text prompt for a pre-shared token; submission
  stores it via `DeviceTokenAuth.StoreToken` and the connect is retried.
- **API key required** (HTTP 401/403 from any `/v1` request on OpenAI-compat or Hermes
  connections) — text prompt for an API key; submission stores it via `APIKeyAuth.StoreAPIKey`.

#### Scenario: Token mismatch offers three choices
- **GIVEN** a connect fails with `gateway token mismatch`
- **WHEN** `handleConnectResult` opens the modal
- **THEN** the user is offered clear-token-and-retry (default), reset-identity-and-retry, and cancel — each routed through the `DeviceTokenAuth` sub-interface

#### Scenario: API key prompt on 401/403
- **GIVEN** an OpenAI-compat or Hermes `/v1` request returns HTTP 401 or 403
- **WHEN** the connect result is handled
- **THEN** a modal text input prompts for an API key and stores it via `APIKeyAuth.StoreAPIKey` before retrying

#### Scenario: Cancelling recovery
- **WHEN** the user cancels any auth-recovery modal
- **THEN** the active backend is closed and control returns to the connections picker

### Requirement: Automatic reconnection after disconnection

Once the TUI is running, a supervisor in `internal/client/supervisor.go` SHALL watch the gateway
connection (`Client.Done()`) and reconnect automatically if it drops (e.g. when the gateway is
restarted). Reconnection SHALL follow the backoff schedule 1s, 2s, 4s, 8s, 15s, then 30s for
every subsequent attempt, with each attempt's connect timeout taken from
`prefs.ConnectTimeoutSeconds` (default 15s). The supervisor SHALL push `tui.ConnStateMsg` into the
bubbletea program so the chat header shows `⚠ disconnected`, `⟳ reconnecting (attempt N)`, or
`✖ auth failed`, and SHALL add a one-line system message to the chat scrollback on disconnect and
on recovery. If a reply was streaming when the connection dropped, the placeholder SHALL be
cleared so the input is usable again (the gateway has no resume protocol; the partial reply is
abandoned). If the gateway rejects the device token mid-session (`gateway token mismatch` /
`token missing`), the supervisor SHALL stop retrying and advise switching connections via
`/connections`, because the auth-recovery modal lives on the connect path, not the reconnect path.

#### Scenario: Transient drop reconnects with backoff
- **GIVEN** the gateway connection drops
- **WHEN** the supervisor detects `Client.Done()`
- **THEN** it retries on the schedule 1s, 2s, 4s, 8s, 15s, then 30s and surfaces `⟳ reconnecting (attempt N)` in the header

#### Scenario: Stream interrupted by a drop
- **GIVEN** a reply is streaming when the connection drops
- **WHEN** the disconnect is handled
- **THEN** the streaming placeholder is cleared and the input becomes usable again

#### Scenario: Auth failure mid-session stops retrying
- **GIVEN** the gateway rejects the device token mid-session with `gateway token mismatch` or `token missing`
- **WHEN** the supervisor observes it
- **THEN** it stops retrying, shows `✖ auth failed`, and advises switching connections via `/connections`

### Requirement: Operator scopes and override

The client SHALL connect requesting operator-level scopes `ScopeOperatorRead`,
`ScopeOperatorWrite`, `ScopeOperatorAdmin`, and `ScopeOperatorApprovals` (the default in
`internal/client/client.go`), which are required for session management, exec approval, and agent
administration. The gateway grants the intersection of the requested scopes and those the device
token actually carries, so the effective set can be narrower. The `OPENCLAW_OPERATOR_SCOPES`
environment variable (comma-separated, e.g. `operator.read,operator.write,operator.approvals`)
SHALL override the default requested set — useful when a pairing only carries a subset, since
requesting scopes beyond what the pairing grants is rejected as a scope mismatch. Because
`config.Load()` reads `.env` from the working directory, a stray `OPENCLAW_OPERATOR_SCOPES` in a
`.env` silently bounds scopes for every connection regardless of the selected gateway; admin-only
client operations SHALL fail fast with a message pointing here (e.g. `missing scope:
operator.admin`) rather than surfacing the raw gateway error.

#### Scenario: Default scope request
- **WHEN** the client connects without `OPENCLAW_OPERATOR_SCOPES` set
- **THEN** it requests operator read, write, admin, and approvals scopes and receives the intersection the token carries

#### Scenario: Bounding the requested scopes
- **GIVEN** a pairing that carries only a subset of the default scopes
- **WHEN** `OPENCLAW_OPERATOR_SCOPES` is set to that subset
- **THEN** the connection is accepted without the admin-only operations, instead of being rejected as a scope mismatch

#### Scenario: Stray scopes override diagnosed
- **GIVEN** a stray `OPENCLAW_OPERATOR_SCOPES` in a working-directory `.env`
- **WHEN** an admin-only operation such as creating an agent is attempted
- **THEN** it fails fast with `missing scope: operator.admin` and a message pointing to the scopes configuration

### Requirement: Credential and state file storage

The system SHALL store credentials and related state under `~/.lucinate/` at the following paths:

| Path | Contents |
|---|---|
| `~/.lucinate/identity/<endpoint>/` | Ed25519 keypair and device token (per gateway endpoint) |
| `~/.lucinate/secrets/secrets.json` | OpenAI-compat and Hermes API keys, keyed by connection ID (mode 0600) |
| `~/.lucinate/connections.json` | Saved connection records |
| `~/.lucinate/hermes/<conn-id>/` | Per-connection Hermes state: `last_response_id` pointer + capped prompts log |
| `~/.lucinate/config.json` | UI preferences — not authentication-related |

#### Scenario: API key secrets file permissions
- **WHEN** an OpenAI-compat or Hermes API key is stored
- **THEN** it is written to `~/.lucinate/secrets/secrets.json` keyed by connection ID with file mode 0600
