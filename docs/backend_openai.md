# OpenAI-compatible backend

The OpenAI-compat backend (`internal/backend/openai`) lets lucinate talk to any HTTP server that implements `/v1/chat/completions` and `/v1/models` — Ollama, vLLM, LM Studio, llamafile, OpenAI proper, and so on. Because those servers have no concept of agents, lucinate maintains agent state on disk locally.

See [connections.md](connections.md) for the cross-backend connection lifecycle this plugs into. The Ollama preset on the connections form is just an opinionated OpenAI preset and is documented there.

## Capabilities

`Backend.Capabilities()` reports `AuthRecovery: AuthRecoveryAPIKey` and `AgentWorkspace: false`. Everything else is off — `/status`, `/compact`, `/think`, `/stats`, and `!!` render a "not available on this connection" system message.

### `/think` is currently a no-op

`/think` reports `Thinking: false` so it's gated off, even though several OpenAI-compatible providers expose reasoning controls in their own request shapes (OpenAI's `reasoning.effort`, Ollama's `think` flag on reasoning models, DeepSeek's `<think>` tags, Anthropic-via-proxy's `thinking` block). Wiring `/think` to translate into the active provider's reasoning shape is a known gap — tracked in [#80](https://github.com/lucinate-ai/lucinate/issues/80).

## Connect and auth

`Connect(ctx)` issues `GET /v1/models`. A 401 or 403 is surfaced as the canonical `api key required` error so the connecting view routes it to the API-key modal. Any other ≥400 response surfaces as `connect: HTTP <code>: <body>`.

The API key is sent as `Authorization: Bearer <key>` when present and omitted otherwise (some endpoints — Ollama, vLLM without auth — accept anonymous requests). The auth modal calls `StoreAPIKey`, which updates the in-memory key on the live backend and (via the `secretAwareBackend` shim in `main.go`) writes through to `~/.lucinate/secrets/secrets.json` so the next launch reuses it without re-prompting.

## Agent storage

Each agent owns a directory under `~/.lucinate/agents/<connection-id>/<agent-id>/`:

| File            | Purpose                                                       |
|-----------------|---------------------------------------------------------------|
| `agent.json`    | Metadata: id, name, default model, created/updated timestamps |
| `IDENTITY.md`   | Who the agent is — markdown, user-editable                    |
| `SOUL.md`       | Tone / values / working style — markdown, user-editable       |
| `history.jsonl` | Append-only transcript, one JSON message per line             |

All files are mode `0600` and the agent directory is `0700`. `agent.json` is rewritten via tempfile + rename so a crash mid-write can't truncate the metadata.

The agent ID is derived from the user-supplied name via `slugify` (lowercase, alphanumerics and hyphens only) — the ID is also the session key, so it has to round-trip through gateway-protocol fields without escaping.

### IDENTITY.md and SOUL.md seeding

On create, the form seeds both files with placeholders the user can edit on disk later:

- `DefaultIdentity(name)` interpolates the agent's chosen name as the `Name:` header so the model addresses itself by the right label from turn one.
- `DefaultSoul` is a static template covering tone and working style.

Users can edit either file between sessions without going through the TUI — `SystemPrompt(agentID)` reloads from disk on every chat send.

### System prompt composition

`AgentStore.SystemPrompt(agentID)` reads IDENTITY.md and SOUL.md and concatenates them under `# Identity` / `# Soul` headers. All four cases are handled:

- Both present → `# Identity\n\n…\n\n# Soul\n\n…`
- Identity only → `# Identity\n\n…`
- Soul only → `# Soul\n\n…`
- Neither → empty string (model gets no preamble)

## Sessions and history

Agent ≡ session, 1:1 — there is no session browser sub-list because there is nothing for it to list. `CreateSession` returns the agent ID as the session key. `/reset` calls `SessionDelete`, which clears `history.jsonl` (the agent metadata stays).

`AppendMessage` writes one JSON-encoded `Message` line per call and touches `UpdatedAt` so `List()` orders recent agents first. `LoadHistory(limit)` returns up to `limit` most recent messages; `limit <= 0` returns the full transcript.

## Skill catalog injection

The chat layer passes the active skill catalog through `ChatSendParams.Skills`. The backend prepends a `System: Available agent skills (activate with /skill-name): …` block to the first turn of each session, then sets `catalogSent[sessionKey] = true` so subsequent turns omit it. The check-and-mark is mutex-guarded against concurrent sends.

## Streaming

`ChatSend` issues `POST /v1/chat/completions` with `stream: true` and parses the SSE response line-by-line, emitting `protocol.ChatEvent` for each `delta.content` chunk and a final event when `[DONE]` arrives. `ChatAbort` cancels the stored `context.CancelFunc` for the run, which terminates the in-flight HTTP request.
