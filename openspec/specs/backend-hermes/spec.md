# Hermes Backend Specification

## Purpose

The Hermes backend (`internal/backend/hermes`) talks to a [Nous Research Hermes Agent](https://github.com/nousresearch/hermes-agent) profile via its OpenAI-compatible HTTP server (`/v1/...`). Unlike the OpenAI-compat backend (which treats the remote as a stateless `/v1/chat/completions` sink and keeps client-side identity and history), Hermes is **stateful server-side** — each profile owns its own SOUL, sessions, memories, and runs an API server on its own port — so this backend stays thin and lets the server be the source of truth. It is a sibling of the OpenAI backend, not a subclass; it plugs into the cross-backend connection lifecycle described by the `connections` spec.

## Requirements

### Requirement: Backend structure and shared primitives

The Hermes backend SHALL be a sibling of the OpenAI backend, not a subclass. Both SHALL use the shared HTTP / SSE / event-emission primitives in `internal/backend/httpcommon` (request builder with bearer auth, SSE scanner, `protocol.Event` emitter). Beyond those shared primitives the two backends SHALL remain independent: the wire shape, agent model, and history strategy all differ. Because Hermes is stateful server-side (each profile owns its own SOUL, sessions, memories, and runs an API server on its own port), this backend SHALL stay thin and let the server be the source of truth, in contrast to the OpenAI-compat backend which treats the remote as a stateless `/v1/chat/completions` sink and keeps client-side identity and history. The cross-backend connection lifecycle this plugs into is covered by the `connections` spec.

#### Scenario: Shared HTTP primitives reused, independent wire shape
- **GIVEN** the Hermes and OpenAI backends both live under `internal/backend`
- **WHEN** the Hermes backend makes requests and emits events
- **THEN** it uses the shared `internal/backend/httpcommon` request builder with bearer auth, SSE scanner, and `protocol.Event` emitter
- **AND** its wire shape, agent model, and history strategy remain independent of the OpenAI backend rather than inherited from it

### Requirement: Capabilities reporting

`Backend.Capabilities()` SHALL report the following capability set:

- `AuthRecovery: AuthRecoveryAPIKey` — bearer-token auth-recovery modal, same as OpenAI.
- `AgentManagement: false` — Hermes profiles are configured server-side (`hermes profile create` / `hermes profile delete` on the host), so the TUI's "new agent" and "delete agent" affordances are both hidden. `CreateAgent` and `DeleteAgent` SHALL both return clear errors if ever called regardless.
- `GatewayStatus: true` — `/status` reports endpoint, auth, model, and (when present) the active `lastResponseID` thread.
- Everything else SHALL be off — `/compact`, `/think`, `/stats`, `/crons`, and `!!` SHALL render a "not available on this connection" system message.

#### Scenario: Agent management is disabled
- **GIVEN** a connected Hermes backend
- **WHEN** the TUI reads `Backend.Capabilities()`
- **THEN** `AgentManagement` is `false` and the "new agent" and "delete agent" affordances are hidden
- **AND** `CreateAgent` and `DeleteAgent` return clear errors if called regardless

#### Scenario: Unsupported commands are rejected with a message
- **WHEN** the user invokes `/compact`, `/think`, `/stats`, `/crons`, or `!!` on a Hermes connection
- **THEN** a "not available on this connection" system message is rendered

#### Scenario: Auth recovery uses the API-key modal
- **WHEN** capabilities are reported
- **THEN** `AuthRecovery` is `AuthRecoveryAPIKey`, driving the bearer-token auth-recovery modal, same as OpenAI

### Requirement: Status payload

`/status` on a Hermes connection SHALL return a `BackendStatus` carrying:

- `Type: "hermes"`, the API base URL, and the auth mode (`"API key"` or `"anonymous"`).
- The discovered or configured default model — `opts.DefaultModel` SHALL win over the model cached during connect via `profileModel`.
- `Thread` populated when `lastResponseID` is set, so the user can see whether the next turn will chain onto an existing server-side conversation or start fresh.

The renderer SHALL omit the OpenClaw-specific gateway block entirely for Hermes — see the per-section gating in `internal/tui/commands.go` (`formatBackendStatus`).

#### Scenario: Status reports type, endpoint, auth, and model
- **WHEN** the user runs `/status` on a Hermes connection
- **THEN** the `BackendStatus` reports `Type: "hermes"`, the API base URL, and the auth mode (`"API key"` or `"anonymous"`)
- **AND** the model is the discovered or configured default, with `opts.DefaultModel` taking precedence over the `profileModel` cached during connect

#### Scenario: Thread shown when a chain is active
- **GIVEN** `lastResponseID` is set for the connection
- **WHEN** `/status` renders
- **THEN** `Thread` is populated so the user can see the next turn will chain onto an existing server-side conversation
- **AND** the OpenClaw-specific gateway block is omitted from the rendering via the per-section gating in `formatBackendStatus`

### Requirement: One profile, one agent

`ListAgents` SHALL return a single synthetic entry (ID `hermes`) representing the connected Hermes profile. The display name SHALL be the model surfaced by `GET /v1/models` — Hermes advertises the profile's pinned upstream model there. `CreateAgent` and `DeleteAgent` SHALL both be rejected with clear errors pointing the user at `hermes profile create` / `hermes profile delete` on the host.

`SessionsList` SHALL return one session keyed off the same synthetic ID. `CreateSession` SHALL be a no-op that round-trips the agent ID. There SHALL be no concept of multi-agent or multi-session within a single Hermes connection — to talk to a different personality, the user configures a different Hermes profile (which runs on its own port) and adds it as a separate connection.

#### Scenario: Single synthetic agent listed
- **WHEN** `ListAgents` is called on a Hermes connection
- **THEN** it returns a single synthetic entry with ID `hermes`
- **AND** the display name is the model surfaced by `GET /v1/models`

#### Scenario: Agent creation and deletion rejected
- **WHEN** `CreateAgent` or `DeleteAgent` is called
- **THEN** it is rejected with a clear error pointing the user at `hermes profile create` / `hermes profile delete` on the host

#### Scenario: Single session, no-op create
- **WHEN** `SessionsList` is called
- **THEN** it returns one session keyed off the synthetic `hermes` ID
- **AND** `CreateSession` is a no-op that round-trips the agent ID

### Requirement: Chat over `/v1/responses` with response-ID chaining

`ChatSend` SHALL post to `/v1/responses` with `stream: true` and SHALL chain via `previous_response_id` rather than a named conversation. Two reasons:

- Hermes maintains conversation continuity server-side from the chained ID, so history is not resent on every turn.
- Pinning a named conversation per connection meant `/reset` wiped the local last-response pointer but left Hermes' server-side thread alive, so the next chat continued the old conversation. Chaining via `previous_response_id` makes `SessionDelete` actually start a fresh chain. (Regression test: `TestSessionDelete_NextChatStartsFreshChain` in `backend_test.go`.)

The streaming SSE SHALL emit typed events — `response.created`, `response.output_text.delta`, `response.completed` — which the backend dispatches on the `type` field in each `data:` payload. Deltas SHALL accumulate into a string and surface as `protocol.ChatEvent` (state=delta) just like the OpenAI backend; `response.completed` SHALL produce a final event and persist the new response ID.

`ChatAbort` SHALL cancel the streaming request context. The Runs API (`POST /v1/runs/{id}/stop`) exists for tool-heavy turns but SHALL NOT be used in V1 — closing the SSE connection is enough for plain chat.

#### Scenario: Streaming chat chains on the previous response
- **WHEN** `ChatSend` is invoked
- **THEN** it posts to `/v1/responses` with `stream: true` and chains via `previous_response_id` rather than a named conversation
- **AND** history is not resent on each turn because Hermes maintains continuity server-side from the chained ID

#### Scenario: Typed SSE events dispatched
- **GIVEN** a streaming `/v1/responses` request
- **WHEN** `data:` payloads arrive
- **THEN** the backend dispatches on the `type` field for `response.created`, `response.output_text.delta`, and `response.completed`
- **AND** deltas accumulate into a string and surface as `protocol.ChatEvent` (state=delta), while `response.completed` produces a final event and persists the new response ID

#### Scenario: Reset starts a fresh chain
- **GIVEN** a connection with an active response chain
- **WHEN** `SessionDelete` (`/reset`) runs
- **THEN** the next chat starts a fresh chain because continuity is keyed on `previous_response_id`, not a named conversation
- **AND** this is pinned by `TestSessionDelete_NextChatStartsFreshChain` in `backend_test.go`

#### Scenario: Abort cancels the stream
- **WHEN** `ChatAbort` is called during a plain chat turn
- **THEN** the streaming request context is cancelled and closing the SSE connection is sufficient, without using the `POST /v1/runs/{id}/stop` Runs API in V1

### Requirement: Local per-connection state files

Two small files per connection SHALL live under `~/.lucinate/hermes/<connection-id>/`:

| File                | Purpose                                                                            |
|---------------------|------------------------------------------------------------------------------------|
| `last_response_id`  | The most recent response ID, used to chain `previous_response_id` on the next turn |
| `prompts.jsonl`     | Append-only log of `{response_id, prompt, time}` entries, capped at 100            |

Both files SHALL be mode `0600` and the directory SHALL be `0700`. They SHALL be cleared by `SessionDelete` (`/reset`).

The prompts log SHALL exist because `GET /v1/responses/{id}` on Hermes returns only the assistant output — the user input field is omitted. Without a client-side mirror, the history-walk reconstruction would be assistant-only. The 100-entry cap SHALL match Hermes' server-side LRU cap on stored responses, so the two stay in rough sync.

#### Scenario: State files created with restrictive permissions
- **WHEN** the Hermes backend persists per-connection state
- **THEN** `last_response_id` and `prompts.jsonl` are written under `~/.lucinate/hermes/<connection-id>/` with file mode `0600` and directory mode `0700`

#### Scenario: Reset clears local state
- **WHEN** `SessionDelete` (`/reset`) runs
- **THEN** both `last_response_id` and `prompts.jsonl` are cleared

#### Scenario: Prompts log mirrors user input
- **GIVEN** `GET /v1/responses/{id}` returns only assistant output with the user input field omitted
- **WHEN** a turn completes
- **THEN** the `{response_id, prompt, time}` entry is appended to `prompts.jsonl`, capped at 100 entries to match Hermes' server-side LRU cap on stored responses

### Requirement: History via walk-back

Hermes has no list-by-conversation endpoint (`GET /v1/conversations/<name>/responses` 404s). To populate the chat scrollback, `ChatHistory` SHALL walk the chain backwards from the stored last-response ID via repeated `GET /v1/responses/{id}`, following `previous_response_id`. The walk SHALL be capped at **3 hops** in `historyWalkLimit` because each hop is a separate round-trip and first-load latency adds up; the chat view does not need a deep transcript on connect.

Each hop SHALL yield the assistant's output text from the server, paired with the user prompt looked up from the local prompts log. Both SHALL be emitted in chronological order (user → assistant per turn) so the chat view renders a normal transcript.

#### Scenario: Scrollback reconstructed by walking the chain
- **GIVEN** no list-by-conversation endpoint exists (`GET /v1/conversations/<name>/responses` 404s)
- **WHEN** `ChatHistory` populates the chat scrollback
- **THEN** it walks the chain backwards from the stored last-response ID via repeated `GET /v1/responses/{id}`, following `previous_response_id`, capped at 3 hops in `historyWalkLimit`

#### Scenario: Each hop pairs assistant output with the stored prompt
- **WHEN** a history hop is processed
- **THEN** the assistant's output text from the server is paired with the user prompt looked up from the local prompts log
- **AND** both are emitted in chronological order (user → assistant per turn) so the chat view renders a normal transcript

### Requirement: Connect and API-key authentication

`Connect(ctx)` SHALL issue `GET /v1/models`, caching the discovered profile name as the synthetic agent's display model. A 401/403 SHALL surface as the canonical `api key required` error so the connecting view routes it to the API-key modal — the same flow as OpenAI.

The Hermes API server requires `API_SERVER_KEY` to be set on the server side; the client SHALL send `Authorization: Bearer <key>`. The auth modal SHALL call `StoreAPIKey`, which updates the in-memory key on the live backend and (via the `secretAwareHermesBackend` shim in `app/factory.go`) persists it to `~/.lucinate/secrets/secrets.json`.

#### Scenario: Connect discovers the profile model
- **WHEN** `Connect(ctx)` runs
- **THEN** it issues `GET /v1/models` and caches the discovered profile name as the synthetic agent's display model

#### Scenario: Unauthorised connect routes to the API-key modal
- **GIVEN** the Hermes API server requires `API_SERVER_KEY` and the client sends `Authorization: Bearer <key>`
- **WHEN** a request returns HTTP 401 or 403
- **THEN** it surfaces as the canonical `api key required` error and the connecting view routes it to the API-key modal, the same flow as OpenAI

#### Scenario: API key stored and persisted
- **WHEN** the user submits a key in the auth modal
- **THEN** `StoreAPIKey` updates the in-memory key on the live backend and persists it to `~/.lucinate/secrets/secrets.json` via the `secretAwareHermesBackend` shim in `app/factory.go`
