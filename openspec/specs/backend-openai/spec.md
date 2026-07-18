# OpenAI-Compatible Backend Specification

## Purpose

The OpenAI-compat backend (`internal/backend/openai`) lets lucinate talk to any HTTP server that implements `/v1/chat/completions` and `/v1/models` — Ollama, vLLM, LM Studio, llamafile, OpenAI proper, and so on. Because those servers have no concept of agents, lucinate maintains agent state on disk locally. This spec covers the backend's declared capabilities, status payload, connect and auth behaviour, local agent storage, session and history handling, skill-catalogue injection, streaming, and local compaction. The cross-backend connection lifecycle this plugs into is covered by the `connections` spec, where the Ollama preset (an opinionated OpenAI preset) is also documented.

## Requirements

### Requirement: Declared backend capabilities

`Backend.Capabilities()` SHALL report `AuthRecovery: AuthRecoveryAPIKey`, `AgentManagement: true`, `SessionCompact: true` (the local summarisation pass — see the compaction requirement), and `GatewayStatus: true` (`/status` — see the status-payload requirement). Everything else SHALL be off — `/think`, `/stats`, and `!!` SHALL render a "not available on this connection" system message.

#### Scenario: Unsupported command on this connection

- **GIVEN** a connection served by the OpenAI-compat backend
- **WHEN** the user issues `/think`, `/stats`, or `!!`
- **THEN** the backend renders a "not available on this connection" system message

#### Scenario: Capabilities that are enabled

- **WHEN** `Backend.Capabilities()` is queried
- **THEN** it reports `AuthRecovery: AuthRecoveryAPIKey`, `AgentManagement: true`, `SessionCompact: true`, and `GatewayStatus: true`

### Requirement: Status payload

`/status` SHALL return a `BackendStatus` carrying:

- `Type: "openai"`, the configured `BaseURL`, the auth mode (`"API key"` or `"anonymous"`), and the configured `DefaultModel`.
- `AgentCount` — the number of agent directories under the connection's store root.
- `History` — the active agent's `history.jsonl` size and newline-counted message count. Files larger than `historyCountMaxBytes` (1 MiB) SHALL skip the count and the renderer SHALL fall back to size-only so an interactive command never blocks on a huge transcript.

The `BackendStatus` SHALL carry no `Gateway` block — that section is OpenClaw-specific and stays nil here.

#### Scenario: Status for a connection with an API key

- **WHEN** the user issues `/status` on an OpenAI-compat connection configured with an API key
- **THEN** the returned `BackendStatus` reports `Type: "openai"`, the configured `BaseURL`, auth mode `"API key"`, the configured `DefaultModel`, the agent count under the store root, and the active agent's history size and message count
- **AND** the `Gateway` block is nil

#### Scenario: Huge transcript skips the message count

- **GIVEN** the active agent's `history.jsonl` is larger than `historyCountMaxBytes` (1 MiB)
- **WHEN** `/status` builds the `History` section
- **THEN** it skips the newline count and the renderer falls back to size-only so the command never blocks

### Requirement: `/think` is currently a no-op

`/think` SHALL report `Thinking: false` so it is gated off, even though several OpenAI-compatible providers expose reasoning controls in their own request shapes (OpenAI's `reasoning.effort`, Ollama's `think` flag on reasoning models, DeepSeek's `<think>` tags, Anthropic-via-proxy's `thinking` block). Wiring `/think` to translate into the active provider's reasoning shape is a known gap — tracked in [#80](https://github.com/lucinate-ai/lucinate/issues/80).

#### Scenario: Thinking reported as disabled

- **WHEN** capabilities are reported for the OpenAI-compat backend
- **THEN** `/think` reports `Thinking: false` and is gated off

### Requirement: Connect and auth

`Connect(ctx)` SHALL issue `GET /v1/models`. A 401 or 403 SHALL be surfaced as the canonical `api key required` error so the connecting view routes it to the API-key modal. Any other ≥400 response SHALL surface as `connect: HTTP <code>: <body>`.

The API key SHALL be sent as `Authorization: Bearer <key>` when present and omitted otherwise (some endpoints — Ollama, vLLM without auth — accept anonymous requests). The auth modal SHALL call `StoreAPIKey`, which updates the in-memory key on the live backend and (via the `secretAwareOpenAIBackend` shim in `app/factory.go`) writes through to `~/.lucinate/secrets/secrets.json` so the next launch reuses it without re-prompting.

#### Scenario: Successful connect probes the models endpoint

- **WHEN** `Connect(ctx)` runs
- **THEN** it issues `GET /v1/models`

#### Scenario: 401 or 403 routes to the API-key modal

- **GIVEN** the server returns HTTP 401 or 403 from `GET /v1/models`
- **WHEN** the connect result is handled
- **THEN** it is surfaced as the canonical `api key required` error and the connecting view opens the API-key modal

#### Scenario: Other error responses surface with code and body

- **GIVEN** the server returns a ≥400 response other than 401/403
- **WHEN** the connect result is handled
- **THEN** it surfaces as `connect: HTTP <code>: <body>`

#### Scenario: API key sent as a bearer token when present

- **GIVEN** an API key is configured
- **WHEN** the backend makes a request
- **THEN** it sends `Authorization: Bearer <key>`
- **AND** when no key is configured, the header is omitted so anonymous endpoints (Ollama, vLLM without auth) still work

#### Scenario: Stored key persists across launches

- **WHEN** the auth modal calls `StoreAPIKey`
- **THEN** the in-memory key on the live backend is updated
- **AND** via the `secretAwareOpenAIBackend` shim in `app/factory.go` it is written through to `~/.lucinate/secrets/secrets.json` so the next launch reuses it without re-prompting

### Requirement: Agent storage layout

Each agent SHALL own a directory under `~/.lucinate/agents/<connection-id>/<agent-id>/` containing:

| File            | Purpose                                                       |
|-----------------|---------------------------------------------------------------|
| `agent.json`    | Metadata: id, name, default model, created/updated timestamps |
| `IDENTITY.md`   | Who the agent is — markdown, user-editable                    |
| `SOUL.md`       | Tone / values / working style — markdown, user-editable       |
| `history.jsonl` | Append-only transcript, one JSON message per line             |

All files SHALL be mode `0600` and the agent directory SHALL be `0700`. `agent.json` SHALL be rewritten via tempfile + rename so a crash mid-write can't truncate the metadata.

The agent ID SHALL be derived from the user-supplied name via `slugify` (lowercase, alphanumerics and hyphens only) — the ID is also the session key, so it has to round-trip through gateway-protocol fields without escaping.

A sibling `~/.lucinate/agents/<connection-id>/.archive/` directory SHALL hold agents the user deleted with the "Keep files" option set (see the delete-vs-archive requirement).

#### Scenario: Agent directory created with restrictive permissions

- **WHEN** an agent is created
- **THEN** its directory `~/.lucinate/agents/<connection-id>/<agent-id>/` is mode `0700` and contains `agent.json`, `IDENTITY.md`, `SOUL.md`, and `history.jsonl`, each mode `0600`

#### Scenario: Metadata write is crash-safe

- **WHEN** `agent.json` is written
- **THEN** it is rewritten via tempfile + rename so a crash mid-write cannot truncate the metadata

#### Scenario: Agent ID slugified from name

- **GIVEN** a user-supplied agent name
- **WHEN** the agent ID is derived
- **THEN** `slugify` produces a lowercase, alphanumeric-and-hyphen-only ID that also serves as the session key and round-trips through gateway-protocol fields without escaping

### Requirement: IDENTITY.md and SOUL.md seeding

On create, the form SHALL seed both files with placeholders the user can edit on disk later:

- `DefaultIdentity(name)` SHALL interpolate the agent's chosen name as the `Name:` header so the model addresses itself by the right label from turn one.
- `DefaultSoul` SHALL be a static template covering tone and working style.

Users SHALL be able to edit either file between sessions without going through the TUI — `SystemPrompt(agentID)` reloads from disk on every chat send.

#### Scenario: Files seeded on create

- **WHEN** an agent is created
- **THEN** `IDENTITY.md` is seeded via `DefaultIdentity(name)` with the agent's chosen name as the `Name:` header
- **AND** `SOUL.md` is seeded from the static `DefaultSoul` template

#### Scenario: Edits picked up on next send

- **GIVEN** a user has edited `IDENTITY.md` or `SOUL.md` on disk between sessions
- **WHEN** the next chat send occurs
- **THEN** `SystemPrompt(agentID)` reloads the files from disk

### Requirement: System prompt composition

`AgentStore.SystemPrompt(agentID)` SHALL read IDENTITY.md and SOUL.md and concatenate them under `# Identity` / `# Soul` headers. All four cases SHALL be handled:

- Both present → `# Identity\n\n…\n\n# Soul\n\n…`
- Identity only → `# Identity\n\n…`
- Soul only → `# Soul\n\n…`
- Neither → empty string (model gets no preamble)

#### Scenario: Both files present

- **GIVEN** both IDENTITY.md and SOUL.md have content
- **WHEN** `SystemPrompt(agentID)` runs
- **THEN** it returns `# Identity\n\n…\n\n# Soul\n\n…`

#### Scenario: Only one file present

- **GIVEN** only IDENTITY.md (or only SOUL.md) has content
- **WHEN** `SystemPrompt(agentID)` runs
- **THEN** it returns just `# Identity\n\n…` (or just `# Soul\n\n…`)

#### Scenario: Neither file present

- **GIVEN** neither IDENTITY.md nor SOUL.md has content
- **WHEN** `SystemPrompt(agentID)` runs
- **THEN** it returns an empty string and the model gets no preamble

### Requirement: Delete vs archive

`Backend.DeleteAgent(ctx, params)` SHALL dispatch on `params.DeleteFiles` (the user's keep-vs-delete-files toggle on the picker — see the deleting-an-agent behaviour in the `agents` spec):

- `DeleteFiles=true` → `AgentStore.Delete` (`os.RemoveAll(AgentDir(id))`). The on-disk content is gone.
- `DeleteFiles=false` → `AgentStore.Archive` renames the agent dir to `<root>/.archive/<id>-<unixts>/`. IDENTITY.md, SOUL.md, history.jsonl, and agent.json all survive verbatim so the user can recover them by hand.

`AgentStore.List` SHALL filter by parsable `agent.json` at the top of each direct child of the picker root, so the `.archive` directory is naturally skipped (it has no `agent.json` of its own and `LoadMeta` returns an error). No special-case is needed.

`DeleteAgent` SHALL call `LoadMeta` first to surface a "not found" error rather than silently succeeding on a stale agent ID — important because the UI presence-toggles its `confirm-delete` action on `nameMatches()`, which can theoretically pass while the underlying agent has already vanished.

#### Scenario: Delete with files removed

- **GIVEN** `params.DeleteFiles=true`
- **WHEN** `DeleteAgent` runs
- **THEN** `AgentStore.Delete` runs `os.RemoveAll(AgentDir(id))` and the on-disk content is gone

#### Scenario: Delete keeping files archives the directory

- **GIVEN** `params.DeleteFiles=false`
- **WHEN** `DeleteAgent` runs
- **THEN** `AgentStore.Archive` renames the agent dir to `<root>/.archive/<id>-<unixts>/` with IDENTITY.md, SOUL.md, history.jsonl, and agent.json surviving verbatim for manual recovery

#### Scenario: Archive directory skipped by the picker

- **WHEN** `AgentStore.List` enumerates the picker root
- **THEN** it filters by parsable `agent.json` at the top of each direct child, so `.archive` (which has no `agent.json` and makes `LoadMeta` error) is naturally skipped without a special case

#### Scenario: Deleting a stale agent ID fails loudly

- **GIVEN** an agent ID that no longer exists on disk but passes the UI's `nameMatches()` presence toggle for `confirm-delete`
- **WHEN** `DeleteAgent` runs
- **THEN** its up-front `LoadMeta` call surfaces a "not found" error rather than silently succeeding

### Requirement: Sessions and history

Agent ≡ session, 1:1 — there SHALL be no session browser sub-list because there is nothing for it to list. `CreateSession` SHALL return the agent ID as the session key. `/reset` SHALL call `SessionDelete`, which clears `history.jsonl` (the agent metadata stays).

`AppendMessage` SHALL write one JSON-encoded `Message` line per call and touch `UpdatedAt` so `List()` orders recent agents first. `LoadHistory(limit)` SHALL return up to `limit` most recent messages; `limit <= 0` returns the full transcript.

#### Scenario: Session key is the agent ID

- **WHEN** `CreateSession` runs
- **THEN** it returns the agent ID as the session key
- **AND** there is no session browser sub-list

#### Scenario: Reset clears history but keeps metadata

- **WHEN** the user issues `/reset`
- **THEN** `SessionDelete` clears `history.jsonl` while the agent metadata stays

#### Scenario: Appending a message reorders the agent list

- **WHEN** `AppendMessage` is called
- **THEN** it writes one JSON-encoded `Message` line and touches `UpdatedAt` so `List()` orders recent agents first

#### Scenario: History limit honoured

- **WHEN** `LoadHistory(limit)` is called
- **THEN** it returns up to `limit` most recent messages, or the full transcript when `limit <= 0`

### Requirement: Skill catalog injection

The chat layer SHALL pass the active skill catalog through `ChatSendParams.Skills`. The backend SHALL prepend a `System: Available agent skills (activate with /skill-name): …` block to the first turn of each session, then set `catalogSent[sessionKey] = true` so subsequent turns omit it. The check-and-mark SHALL be mutex-guarded against concurrent sends.

#### Scenario: Catalogue prepended on the first turn only

- **GIVEN** a new session whose `catalogSent[sessionKey]` is unset
- **WHEN** the first turn is sent with `ChatSendParams.Skills` populated
- **THEN** the backend prepends a `System: Available agent skills (activate with /skill-name): …` block and sets `catalogSent[sessionKey] = true`
- **AND** subsequent turns omit the block

#### Scenario: Concurrent sends are serialised

- **WHEN** two sends race on the same session
- **THEN** the check-and-mark of `catalogSent` is mutex-guarded so the catalogue block is injected once

### Requirement: Streaming chat send and abort

`ChatSend` SHALL issue `POST /v1/chat/completions` with `stream: true` and parse the SSE response line-by-line, emitting `protocol.ChatEvent` for each `delta.content` chunk and a final event when `[DONE]` arrives. `ChatAbort` SHALL cancel the stored `context.CancelFunc` for the run, which terminates the in-flight HTTP request.

#### Scenario: Streamed deltas emitted as chat events

- **WHEN** `ChatSend` runs
- **THEN** it issues `POST /v1/chat/completions` with `stream: true`, emits a `protocol.ChatEvent` for each `delta.content` chunk, and emits a final event when `[DONE]` arrives

#### Scenario: Abort terminates the in-flight request

- **WHEN** `ChatAbort` is called for a run
- **THEN** it cancels the stored `context.CancelFunc`, terminating the in-flight HTTP request

### Requirement: Local compaction

`/compact` SHALL run locally — there is no gateway-side compactor, so `SessionCompact` SHALL issue a streaming `POST /v1/chat/completions` against the agent's configured model with a summarisation prompt and the older portion of the transcript as context. Deltas SHALL be accumulated into a string without touching the events channel, so the compaction is invisible to the chat view. The accumulated text SHALL be written back to `history.jsonl` as a single `role: "system"` message with `Summary: true`, followed by the most recent `compactKeepTail` messages preserved verbatim. `compactMinHistory` SHALL gate the no-op-when-too-small case (returns success without a network round-trip).

Streaming (rather than non-streaming) is intentional: some Ollama setups, particularly with reasoning-capable models, return an empty `message.content` on the non-streaming path while the streamed `delta.content` deltas produce the actual answer. Reusing the streaming code path means /compact works across the same compatibility matrix the regular chat send already covers.

The `Summary` flag is what distinguishes a compact-produced digest from the legacy "skip stored system messages" defence in `runStream`: messages with `Summary: true` SHALL be forwarded on every turn after compaction, while any other `role: "system"` line in `history.jsonl` is still ignored. `ChatHistory` SHALL mirror the same rule so the digest renders in the chat view rather than vanishing on history refresh.

A previously-compacted session that gets compacted again SHALL fold the existing summary into the new one — `renderTranscriptForCompact` includes prior summaries under a `summary:` label so multiple compactions don't lose detail accumulated across earlier passes.

The transcript SHALL be dumped as labelled text inside a single `role: user` message, not forwarded as the literal user/assistant message sequence. Forwarding raw turns ends the request on `role: assistant` — and OpenAI-compatible servers (Ollama, vLLM, llama.cpp) interpret that as "the conversation is complete" and respond with empty content, defeating the summarisation. Wrapping the transcript in a user turn lets the model treat it as input data and produce the summary as a normal reply.

#### Scenario: Compaction runs locally and stores a summary

- **WHEN** the user issues `/compact` on a session with enough history
- **THEN** `SessionCompact` issues a streaming `POST /v1/chat/completions` against the agent's configured model with a summarisation prompt and the older transcript, accumulates the deltas into a string without touching the events channel, and writes the result back to `history.jsonl` as a single `role: "system"` message with `Summary: true`, followed by the most recent `compactKeepTail` messages preserved verbatim

#### Scenario: Too-small history is a no-op

- **GIVEN** a transcript below `compactMinHistory`
- **WHEN** `/compact` runs
- **THEN** it returns success without a network round-trip

#### Scenario: Summary digest survives history refresh

- **GIVEN** a stored `role: "system"` message with `Summary: true`
- **WHEN** turns are sent and history is refreshed
- **THEN** `runStream` and `ChatHistory` forward the `Summary: true` message on every turn while still ignoring any other `role: "system"` line, so the digest renders in the chat view rather than vanishing

#### Scenario: Re-compacting folds in prior summaries

- **GIVEN** a session that was already compacted
- **WHEN** it is compacted again
- **THEN** `renderTranscriptForCompact` includes prior summaries under a `summary:` label so detail accumulated across earlier passes is not lost

#### Scenario: Transcript wrapped in a user turn

- **WHEN** the transcript is sent for summarisation
- **THEN** it is dumped as labelled text inside a single `role: user` message rather than the literal user/assistant sequence, so OpenAI-compatible servers (Ollama, vLLM, llama.cpp) do not see the request end on `role: assistant` and respond with empty content
