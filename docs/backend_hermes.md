# Hermes backend — lessons and rationale

The behavioural contract for the Hermes backend lives in
[`openspec/specs/backend-hermes/spec.md`](../openspec/specs/backend-hermes/spec.md) — the
capabilities, status payload, one-profile-one-agent model, chat over `/v1/responses`, local
state files, history walk-back, and connect/auth flow are all captured there as requirements and
scenarios. This file keeps the hard-won lessons, pitfalls, and design rationale behind that
contract: the "why it works this odd way" that the spec's requirements don't dwell on.

## Why the backend stays thin

Unlike the OpenAI-compat backend (which treats the remote as a stateless
`/v1/chat/completions` sink and keeps client-side identity and history), Hermes is **stateful
server-side** — each profile owns its own SOUL, sessions, memories, and runs an API server on
its own port. So this backend stays thin and lets the server be the source of truth. It is a
sibling of the OpenAI backend, not a subclass: beyond the shared HTTP / SSE / event-emission
primitives in `internal/backend/httpcommon`, the wire shape, agent model, and history strategy
all differ. The cross-backend connection lifecycle this plugs into is described by the
`connections` spec.

## Why chaining on `previous_response_id`, not a named conversation

`ChatSend` chains via `previous_response_id` rather than pinning a named conversation. Two
reasons:

- Hermes maintains conversation continuity server-side from the chained ID; we don't resend
  history on every turn.
- Pinning a named conversation per connection meant `/reset` wiped the local last-response
  pointer but left Hermes' server-side thread alive, so the next chat continued the old
  conversation. Chaining via `previous_response_id` makes `SessionDelete` actually start a fresh
  chain. (Regression test: `TestSessionDelete_NextChatStartsFreshChain` in `backend_test.go`.)

The Runs API (`POST /v1/runs/{id}/stop`) exists for tool-heavy turns but isn't used in V1 —
closing the SSE connection is enough for plain chat.

## Why the prompts log exists

The prompts log (`prompts.jsonl`) exists because `GET /v1/responses/{id}` on Hermes returns
only the assistant output — the user input field is omitted. Without a client-side mirror, the
history-walk reconstruction would be assistant-only. The 100-entry cap matches Hermes'
server-side LRU cap on stored responses, so the two stay in rough sync.

## Why the history walk is capped

Hermes has no list-by-conversation endpoint (`GET /v1/conversations/<name>/responses` 404s), so
`ChatHistory` walks the chain backwards from the stored last-response ID via repeated
`GET /v1/responses/{id}`, following `previous_response_id`. The walk is capped at **3 hops** in
`historyWalkLimit` because each hop is a separate round-trip and first-load latency adds up; the
chat view doesn't need a deep transcript on connect.

## One profile, one agent — configure a second connection instead

There is no concept of multi-agent or multi-session within a single Hermes connection. Profiles
are configured server-side (`hermes profile create` / `hermes profile delete` on the host), so
the TUI's "new agent" and "delete agent" affordances are hidden and `CreateAgent` / `DeleteAgent`
return clear errors pointing there. To talk to a different personality, configure a different
Hermes profile (which runs on its own port) and add it as a separate connection.
