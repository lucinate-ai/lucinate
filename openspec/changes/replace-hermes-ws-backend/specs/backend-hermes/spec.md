## MODIFIED Requirements

### Requirement: Backend structure and shared primitives

The Hermes backend (`internal/backend/hermes`) SHALL be a sibling of the OpenAI backend, not a subclass, and SHALL speak the Hermes `tui_gateway` JSON-RPC protocol over a WebSocket to `hermes serve` (endpoint `/api/ws`) rather than an HTTP/SSE transport. It SHALL NOT import `internal/backend/httpcommon`; the OpenAI backend keeps that shared HTTP/SSE plumbing but Hermes no longer shares it. The backend SHALL layer a thin translation over a generic newline-delimited JSON-RPC client in `internal/backend/hermes/rpc`: `backend.Backend` calls become JSON-RPC requests and server event notifications become `protocol.Event` values. Because Hermes is stateful server-side (each profile owns its own SOUL, sessions, memories, and runs the gateway on its own port), this backend SHALL stay thin and let the server be the source of truth. The cross-backend connection lifecycle this plugs into is covered by the `connections` spec.

#### Scenario: WebSocket JSON-RPC transport, independent of httpcommon
- **GIVEN** the Hermes and OpenAI backends both live under `internal/backend`
- **WHEN** the Hermes backend makes requests and emits events
- **THEN** it speaks JSON-RPC over a WebSocket to `hermes serve` (`/api/ws`) via the `internal/backend/hermes/rpc` client
- **AND** it does not import `internal/backend/httpcommon`
- **AND** its agent model and history strategy remain independent of the OpenAI backend rather than inherited from it

### Requirement: Capabilities reporting

`Backend.Capabilities()` SHALL report the following capability set for this phase:

- `AuthRecovery: AuthRecoveryAPIKey` — the API-key auth-recovery modal, relabelled for Hermes as the "gateway token" prompt. The stored secret slot holds the gateway session token.
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
- The model reported by the gateway for the active profile (`model.options` / `session.info`).
- The active session identifier when a session is open, so the user can see which server-side session the next turn continues.

The renderer SHALL omit the OpenClaw-specific gateway-health block entirely for Hermes — see the per-section gating in `internal/tui/commands.go` (`formatBackendStatus`).

#### Scenario: Status reports type, endpoint, auth, and model
- **WHEN** the user runs `/status` on a Hermes connection
- **THEN** the `BackendStatus` reports `Type: "hermes"`, the gateway base URL, and the auth mode
- **AND** the model is the one the gateway reports for the active profile

#### Scenario: Active session shown
- **GIVEN** a session is open on the connection
- **WHEN** `/status` renders
- **THEN** the active session identifier is shown so the user can see which server-side session the next turn continues
- **AND** the OpenClaw-specific gateway-health block is omitted via the per-section gating in `formatBackendStatus`

### Requirement: One profile, one agent

`ListAgents` SHALL return a single synthetic entry (ID `hermes`) representing the connected Hermes profile. The display name SHALL be the model the gateway reports for that profile. If the gateway returns no profile listing, the backend SHALL fall back to one synthetic profile agent. `CreateAgent` and `DeleteAgent` SHALL both be rejected with clear errors pointing the user at server-side profile configuration on the host. There SHALL be no multi-agent concept within a single Hermes connection — to talk to a different personality, the user configures a different Hermes profile (which runs on its own port) and adds it as a separate connection. Sessions within a connection are covered by the "Server-side sessions" requirement.

#### Scenario: Single synthetic agent listed
- **WHEN** `ListAgents` is called on a Hermes connection
- **THEN** it returns a single synthetic entry with ID `hermes`
- **AND** the display name is the model the gateway reports for the profile

#### Scenario: Agent creation and deletion rejected
- **WHEN** `CreateAgent` or `DeleteAgent` is called
- **THEN** it is rejected with a clear error pointing the user at server-side profile configuration on the host

### Requirement: Connect and API-key authentication

`Connect(ctx)` SHALL dial the WebSocket at `/api/ws` (derived from the connection's HTTP base URL), await the server's `gateway.ready` handshake within a timeout, and issue `agents.list` to populate the agent snapshot. The gateway session token SHALL be supplied as the `?token=` query parameter for a loopback bind; it is read from the per-connection secret slot (formerly the `API_SERVER_KEY` bearer key, now the gateway token), with `StoreAPIKey` persisting it through the `secretAwareHermesBackend` shim in `app/factory.go` to `~/.lucinate/secrets/secrets.json`.

A WebSocket close with code `4401` (bad or missing credential) SHALL surface as the canonical `api key required` error so the connecting view routes it to the auth-recovery modal — the same flow as OpenAI. A close with code `4403` (origin/host gate) SHALL be surfaced verbatim with a hint about the server's host gate.

#### Scenario: Connect dials the gateway and awaits handshake
- **WHEN** `Connect(ctx)` runs
- **THEN** it dials `ws(s)://…/api/ws?token=<gateway token>`, awaits `gateway.ready`, and issues `agents.list` to populate the agent snapshot

#### Scenario: Bad credential routes to the auth modal
- **GIVEN** the gateway rejects the connection with WebSocket close code `4401`
- **WHEN** `Connect` fails
- **THEN** it surfaces as the canonical `api key required` error and the connecting view routes it to the auth-recovery modal

#### Scenario: Gateway token stored and persisted
- **WHEN** the user submits a token in the auth modal
- **THEN** `StoreAPIKey` updates the in-memory token on the live backend and persists it to `~/.lucinate/secrets/secrets.json` via the `secretAwareHermesBackend` shim in `app/factory.go`

## ADDED Requirements

### Requirement: JSON-RPC over WebSocket transport

`internal/backend/hermes/rpc` SHALL provide a generic, Hermes-agnostic client speaking newline-delimited JSON-RPC 2.0 over a WebSocket, using `nhooyr.io/websocket` (`github.com/coder/websocket`). Its surface SHALL be small: `Call(ctx, method, params, &result)` for id-correlated request/response with per-call timeout, `Notifications() <-chan Notification` for server-pushed `method:"event"` frames, and `Close()`. The backend SHALL call the ~20 methods it needs with typed param/result structs local to the package; a generated client for the full method registry SHALL NOT be built. Server → client pushes SHALL be treated as `event` notifications carrying `{type, payload, sid}`, where `sid` scopes the event to a session.

#### Scenario: Requests are id-correlated calls
- **WHEN** the backend issues an RPC via `Call(ctx, method, params, &result)`
- **THEN** the client sends a JSON-RPC request with a unique id and resolves `result` from the matching response, honouring the call context's timeout and cancellation

#### Scenario: Server events fan out on the notification channel
- **GIVEN** the server pushes a `method:"event"` frame with `params:{type, payload, sid}`
- **THEN** it is delivered on `Notifications()` rather than matched to a pending call

### Requirement: Streaming chat via prompt.submit

`ChatSend` SHALL submit a turn via the `prompt.submit` RPC with the open session id and the user text, using a generated idempotency key as the run id. On turn 1 of a session the skills catalogue SHALL be prepended as a system preamble, as the OpenAI backend does. Streamed `message.delta` events SHALL accumulate per session and surface as `protocol.ChatEvent` (state=delta); `message.complete` SHALL produce a final event. `ChatAbort` SHALL call `session.interrupt` for a real server-side interrupt (not merely dropping the stream) and surface an aborted event; the session SHALL remain usable afterwards.

#### Scenario: Streaming chat over the gateway
- **WHEN** `ChatSend` is invoked
- **THEN** it calls `prompt.submit` with the session id and text, and streamed `message.delta` events accumulate and surface as `protocol.ChatEvent` (state=delta)
- **AND** `message.complete` produces a final event

#### Scenario: Abort interrupts server-side
- **WHEN** `ChatAbort` is called mid-turn
- **THEN** it calls `session.interrupt`, an aborted event is surfaced, and the session remains usable for the next turn

### Requirement: Event translation to protocol events

Translation from Hermes event notifications to `protocol.Event` SHALL be a pure function so it can be table-tested exhaustively without a socket. Tool events SHALL map to the existing tool-card events: `tool.start` → `EventAgent` stream=`tool` phase=`start`, `tool.complete` → phase=`result` (carrying success/error state). Because Hermes does not send OpenClaw-style `toolCallId`s, the backend SHALL assign a stable id per `(sid, tool-invocation)` as it pairs `tool.start` with `tool.complete`, matching the shape `internal/tui/events.go` expects. `thinking.delta` / `reasoning.delta` SHALL map to `EventAgent` stream=`thinking`. `error` events SHALL surface as `protocol.ChatEvent` state=`error`.

#### Scenario: Tool events render tool cards with paired ids
- **GIVEN** a `tool.start` followed by a `tool.complete` for the same invocation
- **WHEN** the events are translated
- **THEN** they produce an `EventAgent` start + result pair sharing a stable id the tool card renderer can match
- **AND** a `tool.complete` carrying an error marks the card as an error result

#### Scenario: Translation is a pure, testable function
- **WHEN** the translation function is called with a notification
- **THEN** it returns the mapped `protocol.Event` values without requiring a live socket, so it can be table-tested

### Requirement: Server-side sessions

Sessions SHALL be backed by the gateway, not synthesised locally. `SessionsList` SHALL map `session.list` to `protocol.SessionsListResult`. `CreateSession` SHALL call `session.create` (or `session.resume` for an existing id) and return the gateway session id as the session key. `SessionDelete` SHALL call `session.delete`, replacing the old local-pointer reset. `ChatHistory` SHALL return the full transcript from `session.history` — both user and assistant turns — with no client-side walk-back or prompts log.

#### Scenario: Sessions round-trip through the gateway
- **WHEN** `CreateSession` runs then `SessionsList` is called
- **THEN** the created session appears in the list keyed by its gateway session id
- **AND** `SessionDelete` calls `session.delete` and removes it

#### Scenario: History comes from the server
- **GIVEN** a session with one completed round-trip
- **WHEN** `ChatHistory` is called
- **THEN** it returns both the user and assistant turns from `session.history` with no client-side reconstruction

### Requirement: Usage and compact over the gateway

The backend SHALL implement `UsageBackend` by mapping `session.usage` (and context breakdown) into the fields the chat header and `/stats` read, and `CompactBackend` by calling the gateway's session-compress RPC. After a compaction the session history SHALL remain coherent.

#### Scenario: Usage totals reflect the gateway
- **WHEN** `/stats` is invoked after a turn
- **THEN** `UsageBackend` returns parseable totals sourced from `session.usage`

#### Scenario: Compact succeeds and history stays coherent
- **WHEN** `/compact` is invoked
- **THEN** the gateway compresses the session and subsequent `ChatHistory` remains coherent

### Requirement: Reconnect and supervision

`Supervise` SHALL be a real reconnect loop (modelled on `internal/client`'s supervisor): exponential-backoff dial, `gateway.ready` as the liveness signal, and connection-state transitions notified to the TUI banner. On reconnect the backend SHALL re-issue `session.resume` for the active session before reporting healthy, because the gateway detaches or reaps sessions on WebSocket disconnect. In-flight calls SHALL fail fast with a retryable error.

#### Scenario: Reconnect resumes the active session
- **GIVEN** an active session and a dropped WebSocket
- **WHEN** the supervisor reconnects
- **THEN** it dials with backoff, awaits `gateway.ready`, re-issues `session.resume` for the active session, and only then reports healthy
- **AND** the next `ChatSend` succeeds

### Requirement: Legacy endpoint migration error

A connection whose stored URL points at the legacy Hermes API server (port `8642` or a `/v1` path) SHALL fail `Connect` with a targeted, actionable error instructing the user to run `hermes serve`, repoint the URL to the gateway (default `http://localhost:9119`), and paste the gateway token. There SHALL be no silent auto-migration, because the user must change the server process they run (`gateway run` → `serve`).

#### Scenario: Legacy URL yields a migration hint
- **GIVEN** a stored Hermes connection URL on port `8642` or with a `/v1` path
- **WHEN** `Connect` runs and the gateway upgrade is unavailable
- **THEN** it fails with a targeted error telling the user to run `hermes serve`, repoint the URL to `http://localhost:9119`, and paste the gateway token

### Requirement: Interactive agent requests declined in this phase

Server → client blocking asks from the agent (`approval.request`, `clarify.request`, `sudo.request`, `secret.request`) SHALL be auto-responded with a deny/cancel and a visible system message, so a chat turn can never hang the TUI silently. The message SHALL tell the user the request is not supported yet and how to configure the profile for autonomous approval.

#### Scenario: Approval request is declined with a message
- **GIVEN** the agent emits an `approval.request` during a turn
- **WHEN** the backend receives it
- **THEN** it auto-responds deny/cancel and renders a visible system message explaining the request is not supported yet, so the turn does not hang

## REMOVED Requirements

### Requirement: Chat over `/v1/responses` with response-ID chaining
**Reason**: The HTTP/SSE `/v1/responses` transport is replaced entirely by `prompt.submit` over the JSON-RPC WebSocket gateway (see the "Streaming chat via prompt.submit" requirement). Server-side session state replaces `previous_response_id` chaining.
**Migration**: Chat now flows over `hermes serve` (`/api/ws`); there is no `/v1/responses` path and no `previous_response_id`. Existing connections must repoint to the gateway URL (see "Legacy endpoint migration error").

### Requirement: Local per-connection state files
**Reason**: The gateway is the source of truth for session state and history, so the client-side `last_response_id` and `prompts.jsonl` files are no longer needed.
**Migration**: `~/.lucinate/hermes/<connection-id>/` is no longer written or read and can be deleted; history and continuity come from the gateway session.

### Requirement: History via walk-back
**Reason**: The gateway exposes `session.history` directly, so the 3-hop `previous_response_id` walk-back and its prompts-log pairing are obsolete.
**Migration**: `ChatHistory` now returns the full server transcript from `session.history` (see "Server-side sessions").
