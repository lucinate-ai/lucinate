## MODIFIED Requirements

### Requirement: Backend structure and shared primitives

The Hermes backend (`internal/backend/hermes`) SHALL be a sibling of the OpenAI backend, not a subclass, and SHALL speak the Hermes `tui_gateway` JSON-RPC protocol over a WebSocket to the `hermes dashboard` gateway (endpoint `/api/ws`, default port 9119) rather than an HTTP/SSE transport. It SHALL NOT import `internal/backend/httpcommon`; the OpenAI backend keeps that shared HTTP/SSE plumbing but Hermes no longer shares it. The backend SHALL layer a thin translation over a generic newline-delimited JSON-RPC client in `internal/backend/hermes/rpc`: `backend.Backend` calls become JSON-RPC requests and server event notifications become `protocol.Event` values. Because Hermes is stateful server-side (each profile owns its own SOUL, sessions, memories, and runs the gateway on its own port), this backend SHALL stay thin and let the server be the source of truth. The cross-backend connection lifecycle this plugs into is covered by the `connections` spec.

#### Scenario: WebSocket JSON-RPC transport, independent of httpcommon
- **GIVEN** the Hermes and OpenAI backends both live under `internal/backend`
- **WHEN** the Hermes backend makes requests and emits events
- **THEN** it speaks JSON-RPC over a WebSocket to the `hermes dashboard` gateway (`/api/ws`) via the `internal/backend/hermes/rpc` client
- **AND** it does not import `internal/backend/httpcommon`
- **AND** its agent model and history strategy remain independent of the OpenAI backend rather than inherited from it

### Requirement: Capabilities reporting

`Backend.Capabilities()` SHALL report the following capability set for this phase:

- `AuthRecovery: AuthRecoveryAPIKey` — the API-key auth-recovery modal, relabelled for Hermes as the "gateway token" prompt. The stored secret slot holds the gateway session token (`HERMES_DASHBOARD_SESSION_TOKEN`).
- `AgentManagement: false` — Hermes profiles are configured server-side, so the TUI's "new agent" and "delete agent" affordances are both hidden. `CreateAgent` and `DeleteAgent` SHALL both return clear errors if ever called regardless.
- `GatewayStatus: true` — `/status` reports the gateway endpoint, auth mode, model, and active session.

The backend SHALL implement the `StatusBackend`, `UsageBackend`, and `CompactBackend` sub-interfaces, so `/status`, `/stats` (plus live header usage), and `/compact` all work. It SHALL NOT implement `ExecBackend`, `ThinkingBackend`, or `CronBackend` in this phase, so `!!`, `/think`, and `/crons` SHALL render a "not available on this connection" system message.

#### Scenario: Agent management is disabled
- **GIVEN** a connected Hermes backend
- **WHEN** the TUI reads `Backend.Capabilities()`
- **THEN** `AgentManagement` is `false` and the "new agent" and "delete agent" affordances are hidden
- **AND** `CreateAgent` and `DeleteAgent` return clear errors if called regardless

#### Scenario: Usage and compact are supported
- **WHEN** the user invokes `/stats` or `/compact` on a Hermes connection
- **THEN** the command succeeds because the backend implements `UsageBackend` and `CompactBackend`

#### Scenario: Out-of-phase commands are rejected with a message
- **WHEN** the user invokes `/think`, `/crons`, or `!!` on a Hermes connection
- **THEN** a "not available on this connection" system message is rendered

#### Scenario: Auth recovery uses the API-key modal for the gateway token
- **WHEN** capabilities are reported
- **THEN** `AuthRecovery` is `AuthRecoveryAPIKey`, driving the auth-recovery modal relabelled as the "gateway token" prompt

### Requirement: Status payload

`/status` on a Hermes connection SHALL return a `BackendStatus` carrying:

- `Type: "hermes"`, the gateway base URL, and the auth mode (`"gateway token"` or `"anonymous"`).
- The active model, taken from the `session.create` `info.model` field (or `model.options`' top-level `model`).
- The active session identifier when a session is open, so the user can see which server-side session the next turn continues.

The backend SHALL compose this from structured RPC results (`session.create` `info`, `model.options`, `session.usage`) rather than from `session.status`, whose result is a single pre-rendered text blob (`{"output": "…"}`), not structured fields. There is no `verification.status` RPC. The renderer SHALL omit the OpenClaw-specific gateway-health block entirely for Hermes — see the per-section gating in `internal/tui/commands.go` (`formatBackendStatus`).

#### Scenario: Status reports type, endpoint, auth, and model
- **WHEN** the user runs `/status` on a Hermes connection
- **THEN** the `BackendStatus` reports `Type: "hermes"`, the gateway base URL, and the auth mode
- **AND** the model is the one the gateway reports for the active profile via `session.create` `info.model` or `model.options`

#### Scenario: Active session shown
- **GIVEN** a session is open on the connection
- **WHEN** `/status` renders
- **THEN** the active session identifier is shown so the user can see which server-side session the next turn continues
- **AND** the OpenClaw-specific gateway-health block is omitted via the per-section gating in `formatBackendStatus`

### Requirement: One profile, one agent

`ListAgents` SHALL return a single synthetic entry (ID `hermes`) representing the connected Hermes profile. The `agents.list` RPC returns `{"processes": [...]}`; when it is empty (the common case), the backend SHALL fall back to one synthetic profile agent, taking the profile name from `session.create` `info.profile_name`. `CreateAgent` and `DeleteAgent` SHALL both be rejected with clear errors pointing the user at server-side profile configuration on the host. There SHALL be no multi-agent concept within a single Hermes connection — to talk to a different personality, the user configures a different Hermes profile (which runs on its own port) and adds it as a separate connection. Sessions within a connection are covered by the "Server-side sessions" requirement.

#### Scenario: Single synthetic agent listed
- **WHEN** `ListAgents` is called on a Hermes connection and `agents.list` returns no processes
- **THEN** it returns a single synthetic entry with ID `hermes`
- **AND** the display name reflects the profile the gateway reports

#### Scenario: Agent creation and deletion rejected
- **WHEN** `CreateAgent` or `DeleteAgent` is called
- **THEN** it is rejected with a clear error pointing the user at server-side profile configuration on the host

### Requirement: Connect and API-key authentication

`Connect(ctx)` SHALL dial the WebSocket at `/api/ws` (derived from the connection's HTTP base URL), await the server's `gateway.ready` handshake within a timeout, and issue `agents.list` to populate the agent snapshot. The gateway session token SHALL be supplied as the `?token=` query parameter for a loopback bind; it is read from the per-connection secret slot (formerly the `API_SERVER_KEY` bearer key, now the gateway token), with `StoreAPIKey` persisting it through the `secretAwareHermesBackend` shim in `app/factory.go` to `~/.lucinate/secrets/secrets.json`.

A bad or missing token is rejected at the **WebSocket upgrade with HTTP status 403** (the socket never opens; there is no post-open `4401`/`4403` close frame). Connect SHALL map a 403 upgrade rejection to the canonical `api key required` error so the connecting view routes it to the auth-recovery modal — the same flow as OpenAI.

#### Scenario: Connect dials the gateway and awaits handshake
- **WHEN** `Connect(ctx)` runs
- **THEN** it dials `ws(s)://…/api/ws?token=<gateway token>`, awaits `gateway.ready`, and issues `agents.list` to populate the agent snapshot

#### Scenario: Bad credential routes to the auth modal
- **GIVEN** the gateway rejects the WebSocket upgrade with HTTP 403
- **WHEN** `Connect` fails
- **THEN** it surfaces as the canonical `api key required` error and the connecting view routes it to the auth-recovery modal

#### Scenario: Gateway token stored and persisted
- **WHEN** the user submits a token in the auth modal
- **THEN** `StoreAPIKey` updates the in-memory token on the live backend and persists it to `~/.lucinate/secrets/secrets.json` via the `secretAwareHermesBackend` shim in `app/factory.go`

## ADDED Requirements

### Requirement: JSON-RPC over WebSocket transport

`internal/backend/hermes/rpc` SHALL provide a generic, Hermes-agnostic client speaking newline-delimited JSON-RPC 2.0 over a WebSocket, using the existing `github.com/gorilla/websocket` dependency (already required by the openclaw-go gateway SDK) rather than introducing a second WebSocket library. Its surface SHALL be small: `Call(ctx, method, params, &result)` for id-correlated request/response with per-call timeout, `Notifications() <-chan Notification` for server-pushed `method:"event"` frames, and `Close()`. The backend SHALL call the ~18 methods it needs with typed param/result structs local to the package; a generated client for the full method registry SHALL NOT be built. Server → client pushes SHALL be treated as `event` notifications carrying `{type, session_id, payload}`, where `session_id` scopes the event to a session.

#### Scenario: Requests are id-correlated calls
- **WHEN** the backend issues an RPC via `Call(ctx, method, params, &result)`
- **THEN** the client sends a JSON-RPC request with a unique id and resolves `result` from the matching response, honouring the call context's timeout and cancellation

#### Scenario: Server events fan out on the notification channel
- **GIVEN** the server pushes a `method:"event"` frame with `params:{type, session_id, payload}`
- **THEN** it is delivered on `Notifications()` rather than matched to a pending call

### Requirement: Streaming chat via prompt.submit

`ChatSend` SHALL submit a turn via the `prompt.submit` RPC with `{session_id, text}`, using a generated idempotency key as the run id; the RPC returns `{"status": "streaming"}` and the turn's output arrives as event notifications. On turn 1 of a session the skills catalogue SHALL be prepended as a system preamble, as the OpenAI backend does. Streamed `message.delta` events SHALL accumulate per session and surface as `protocol.ChatEvent` (state=delta). `message.complete` carries the turn's usage inline and a `status` field: `status:"complete"` SHALL produce a final event, and `status:"interrupted"` SHALL produce an aborted event. `ChatAbort` SHALL call `session.interrupt` (which returns `{"status":"interrupted"}`) for a real server-side interrupt, not merely dropping the stream; the interrupted turn terminates with a `message.complete` whose `status` is `"interrupted"`. The session SHALL remain usable afterwards.

#### Scenario: Streaming chat over the gateway
- **WHEN** `ChatSend` is invoked
- **THEN** it calls `prompt.submit` with `{session_id, text}`, and streamed `message.delta` events accumulate and surface as `protocol.ChatEvent` (state=delta)
- **AND** `message.complete` produces a final event

#### Scenario: Abort interrupts server-side
- **WHEN** `ChatAbort` is called mid-turn
- **THEN** it calls `session.interrupt`, the turn ends with a `message.complete` whose `status` is `"interrupted"` which surfaces as an aborted event, and the session remains usable for the next turn

### Requirement: Event translation to protocol events

Translation from Hermes event notifications to `protocol.Event` SHALL be a pure function so it can be table-tested exhaustively without a socket. Tool events SHALL map to the existing tool-card events: `tool.start` (`{tool_id, name, context}`) → `EventAgent` stream=`tool` phase=`start`, `tool.complete` (`{tool_id, name, args, duration_s, result}`) → phase=`result`. Hermes supplies a `tool_id` on both `tool.start` and `tool.complete`, so the backend SHALL pair start/result by that id directly rather than synthesising one. Success/error card state SHALL be derived from `result.error` (and `result.exit_code`), not from an `isError` field. `thinking.delta` and `reasoning.delta` SHALL map to `EventAgent` stream=`thinking`. `error` events (`{message}`) SHALL surface as `protocol.ChatEvent` state=`error`.

#### Scenario: Tool events render tool cards with the server-supplied id
- **GIVEN** a `tool.start` followed by a `tool.complete` sharing a `tool_id`
- **WHEN** the events are translated
- **THEN** they produce an `EventAgent` start + result pair keyed on that `tool_id`
- **AND** a `tool.complete` whose `result.error` is non-null (or `exit_code` non-zero) marks the card as an error result

#### Scenario: Translation is a pure, testable function
- **WHEN** the translation function is called with a notification
- **THEN** it returns the mapped `protocol.Event` values without requiring a live socket, so it can be table-tested against the Phase 0 golden fixtures

### Requirement: Server-side sessions

Sessions SHALL be backed by the gateway, not synthesised locally. `session.create` returns two identifiers: a short live `session_id` (the handle used by `prompt.submit`, `session.history`, `session.usage`, and `session.interrupt` during the connection) and a timestamped `stored_session_id` (the persisted id). `CreateSession` SHALL call `session.create` (or `session.resume`) and track both. `SessionsList` SHALL map `session.list` to `protocol.SessionsListResult`; its entries are keyed by `stored_session_id` and carry `{title, preview, started_at, message_count, source}`. Only sessions that have at least one message appear in `session.list` — a freshly created empty session is usable but not yet listed. `SessionDelete` SHALL detach an attached live handle first via `session.close` (the gateway refuses to delete an active session) and then call `session.delete` with the stored id. `ChatHistory` SHALL return the full transcript from `session.history` (`{count, messages}`) — both user and assistant turns — with no client-side walk-back or prompts log.

#### Scenario: A session with a turn appears in the list
- **GIVEN** `CreateSession` then a completed `ChatSend` round-trip
- **WHEN** `SessionsList` is called
- **THEN** the session appears keyed by its `stored_session_id` with a non-zero `message_count`
- **AND** `SessionDelete` calls `session.delete` and removes it

#### Scenario: History comes from the server
- **GIVEN** a session with one completed round-trip
- **WHEN** `ChatHistory` is called
- **THEN** it returns both the user and assistant turns from `session.history` with no client-side reconstruction

### Requirement: Usage and compact over the gateway

The backend SHALL implement `UsageBackend` from the `usage` object carried inline on each `message.complete` event (`{input, output, total, calls, context_used, context_max, context_percent, cost_usd, …}`), with `session.usage` (`{calls, input, output, total}`) as the on-demand refresh for `/stats`; there is no `session.context_breakdown` RPC. It SHALL implement `CompactBackend` via `session.compress`. After a compaction the session history SHALL remain coherent.

#### Scenario: Usage totals reflect the gateway
- **WHEN** a turn completes
- **THEN** `UsageBackend` exposes the totals from the `message.complete` `usage` payload, and `/stats` can refresh them via `session.usage`

#### Scenario: Compact succeeds and history stays coherent
- **WHEN** `/compact` is invoked
- **THEN** the gateway compresses the session via `session.compress` and subsequent `ChatHistory` remains coherent

### Requirement: Reconnect and supervision

`Supervise` SHALL be a real reconnect loop (modelled on `internal/client`'s supervisor): exponential-backoff dial, `gateway.ready` as the liveness signal, and connection-state transitions notified to the TUI banner. On reconnect the backend SHALL re-issue `session.resume` for the active session before reporting healthy, because the gateway detaches or reaps sessions on WebSocket disconnect. In-flight calls SHALL fail fast with a retryable error.

#### Scenario: Reconnect resumes the active session
- **GIVEN** an active session and a dropped WebSocket
- **WHEN** the supervisor reconnects
- **THEN** it dials with backoff, awaits `gateway.ready`, re-issues `session.resume` for the active session, and only then reports healthy
- **AND** the next `ChatSend` succeeds

### Requirement: Legacy endpoint migration error

A connection whose stored URL points at the legacy Hermes API server (port `8642` or a `/v1` path) SHALL fail `Connect` with a targeted, actionable error instructing the user to run `hermes dashboard`, repoint the URL to the gateway (default `http://localhost:9119`), and paste the gateway token. There SHALL be no silent auto-migration, because the user must change the server process they run (`gateway run` → `dashboard`).

#### Scenario: Legacy URL yields a migration hint
- **GIVEN** a stored Hermes connection URL on port `8642` or with a `/v1` path
- **WHEN** `Connect` runs and the gateway WebSocket is unavailable there
- **THEN** it fails with a targeted error telling the user to run `hermes dashboard`, repoint the URL to `http://localhost:9119`, and paste the gateway token

### Requirement: Interactive agent requests declined in this phase

Server → client blocking asks from the agent (`approval.request`, `clarify.request`, `sudo.request`, `secret.request`) each carry a `request_id` and are answered by a paired `<type>.respond` RPC (`clarify.respond`/`sudo.respond`/`secret.respond`/`approval.respond`). In this phase the backend SHALL auto-decline every such ask by calling the matching respond method with a deny/cancel value (for `approval.respond`, `{session_id, choice:"deny"}`) and render a visible system message, so a chat turn can never hang the TUI silently. The message SHALL tell the user the request is not supported yet and how to configure the profile for autonomous approval.

#### Scenario: Approval request is declined with a message
- **GIVEN** the agent emits an `approval.request` (or `clarify`/`sudo`/`secret` request) carrying a `request_id` during a turn
- **WHEN** the backend receives it
- **THEN** it calls the paired `<type>.respond` RPC with a deny/cancel value (e.g. `approval.respond {session_id, choice:"deny"}`) and renders a visible system message explaining the request is not supported yet, so the turn does not hang

## REMOVED Requirements

### Requirement: Chat over `/v1/responses` with response-ID chaining
**Reason**: The HTTP/SSE `/v1/responses` transport is replaced entirely by `prompt.submit` over the JSON-RPC WebSocket gateway (see the "Streaming chat via prompt.submit" requirement). Server-side session state replaces `previous_response_id` chaining.
**Migration**: Chat now flows over the `hermes dashboard` gateway (`/api/ws`); there is no `/v1/responses` path and no `previous_response_id`. Existing connections must repoint to the gateway URL (see "Legacy endpoint migration error").

### Requirement: Local per-connection state files
**Reason**: The gateway is the source of truth for session state and history, so the client-side `last_response_id` and `prompts.jsonl` files are no longer needed.
**Migration**: `~/.lucinate/hermes/<connection-id>/` is no longer written or read and can be deleted; history and continuity come from the gateway session.

### Requirement: History via walk-back
**Reason**: The gateway exposes `session.history` directly, so the 3-hop `previous_response_id` walk-back and its prompts-log pairing are obsolete.
**Migration**: `ChatHistory` now returns the full server transcript from `session.history` (see "Server-side sessions").
