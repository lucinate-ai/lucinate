# Hermes backend

The Hermes backend (`internal/backend/hermes`) talks to a [Nous Research Hermes Agent](https://github.com/nousresearch/hermes-agent) profile via its OpenAI-compatible HTTP server (`/v1/...`). Unlike the OpenAI-compat backend (which treats the remote as a stateless `/v1/chat/completions` sink and keeps client-side identity / history), Hermes is **stateful server-side** — each profile owns its own SOUL, sessions, memories, and runs an API server on its own port — so this backend stays thin and lets the server be the source of truth.

The backend is a sibling of the OpenAI backend, not a subclass. Both use the shared HTTP / SSE / event-emission primitives in `internal/backend/httpcommon` (request builder with bearer auth, SSE scanner, `protocol.Event` emitter). Beyond that they are independent: the wire shape, agent model, and history strategy all differ.

See [connections.md](connections.md) for the cross-backend connection lifecycle this plugs into.

## Capabilities

`Backend.Capabilities()` reports:

- `AuthRecovery: AuthRecoveryAPIKey` — bearer-token auth-recovery modal, same as OpenAI.
- `AgentManagement: false` — Hermes profiles are configured server-side (`hermes profile create` / `hermes profile delete` on the host), so the TUI's "new agent" and "delete agent" affordances are both hidden. `CreateAgent` and `DeleteAgent` both return clear errors if ever called regardless.
- Everything else is off — `/status`, `/compact`, `/think`, `/stats`, `/crons`, `!!` render a "not available on this connection" system message.

## One profile, one agent

`ListAgents` returns a single synthetic entry (ID `hermes`) representing the connected Hermes profile. The display name is the model surfaced by `GET /v1/models` — Hermes advertises the profile's pinned upstream model there. `CreateAgent` and `DeleteAgent` are both rejected with clear errors pointing the user at `hermes profile create` / `hermes profile delete` on the host.

`SessionsList` returns one session keyed off the same synthetic ID. `CreateSession` is a no-op that round-trips the agent ID. There is no concept of multi-agent or multi-session within a single Hermes connection — to talk to a different personality, configure a different Hermes profile (which runs on its own port) and add it as a separate connection.

## Chat over `/v1/responses`

`ChatSend` posts to `/v1/responses` with `stream: true` and chains via `previous_response_id` rather than a named conversation. Two reasons:

- Hermes maintains conversation continuity server-side from the chained ID; we don't resend history on every turn.
- Pinning a named conversation per connection meant `/reset` wiped the local last-response pointer but left Hermes' server-side thread alive, so the next chat continued the old conversation. Chaining via `previous_response_id` makes `SessionDelete` actually start a fresh chain. (Regression test: `TestSessionDelete_NextChatStartsFreshChain` in `backend_test.go`.)

The streaming SSE emits typed events — `response.created`, `response.output_text.delta`, `response.completed` — which the backend dispatches on the `type` field in each `data:` payload. Deltas accumulate into a string and surface as `protocol.ChatEvent` (state=delta) just like the OpenAI backend; `response.completed` produces a final event and persists the new response ID.

`ChatAbort` cancels the streaming request context. The Runs API (`POST /v1/runs/{id}/stop`) exists for tool-heavy turns but isn't used in V1 — closing the SSE connection is enough for plain chat.

## Local state

Two small files per connection live under `~/.lucinate/hermes/<connection-id>/`:

| File                | Purpose                                                                            |
|---------------------|------------------------------------------------------------------------------------|
| `last_response_id`  | The most recent response ID, used to chain `previous_response_id` on the next turn |
| `prompts.jsonl`     | Append-only log of `{response_id, prompt, time}` entries, capped at 100            |

Both files are mode `0600` and the directory is `0700`. They're cleared by `SessionDelete` (`/reset`).

The prompts log exists because `GET /v1/responses/{id}` on Hermes returns only the assistant output — the user input field is omitted. Without a client-side mirror, the history-walk reconstruction would be assistant-only. The 100-entry cap matches Hermes' server-side LRU cap on stored responses, so the two stay in rough sync.

## History via walk-back

Hermes has no list-by-conversation endpoint (`GET /v1/conversations/<name>/responses` 404s). To populate the chat scrollback, `ChatHistory` walks the chain backwards from the stored last-response ID via repeated `GET /v1/responses/{id}`, following `previous_response_id`. The walk is capped at **3 hops** in `historyWalkLimit` because each hop is a separate round-trip and first-load latency adds up; the chat view doesn't need a deep transcript on connect.

Each hop yields the assistant's output text from the server, paired with the user prompt looked up from the local prompts log. Both are emitted in chronological order (user → assistant per turn) so the chat view renders a normal transcript.

## Connect and auth

`Connect(ctx)` issues `GET /v1/models`, caching the discovered profile name as the synthetic agent's display model. A 401/403 surfaces as the canonical `api key required` error so the connecting view routes it to the API-key modal — same flow as OpenAI.

The Hermes API server requires `API_SERVER_KEY` to be set on the server side; the client sends `Authorization: Bearer <key>`. The auth modal calls `StoreAPIKey` which updates the in-memory key on the live backend and (via the `secretAwareHermesBackend` shim in `app/factory.go`) persists it to `~/.lucinate/secrets/secrets.json`.
