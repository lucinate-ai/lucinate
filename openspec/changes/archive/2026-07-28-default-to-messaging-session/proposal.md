## Why

When an agent is reached through an external messaging channel (e.g. Telegram), the real conversation lives in a channel-tied session whose key the gateway mints itself — for example `agent:main:telegram:direct:123456789`. Opening that agent in lucinate today lands the user on a blank `main`/`MainKey` session instead of the conversation they actually hold with the agent, so the history and context they came to see are not there.

## What Changes

- When opening an agent, lucinate SHALL default to the external-messaging conversation the user talks to that agent on, instead of the literal `"main"`/`MainKey`. This applies at all three "open this agent" sites: the TUI agent picker, the in-chat `/agent <name>` switch, and the one-shot `lucinate send`.
- The messaging session SHALL be found by reading the structured `channel` / `kind` / `origin` / `deliveryContext` fields the gateway already returns from `sessions.list` — never by parsing the opaque session key. A session qualifies when it is a one-to-one (`direct`) conversation that carries both a messaging channel and a peer identity.
- When several messaging conversations exist for one agent, the most-recently-updated one SHALL win (nothing tells the client which human is "the user").
- When the agent has no messaging conversation, behaviour SHALL fall back to today's rule: `MainKey` for the connection's default agent, the literal `"main"` otherwise. A `sessions.list` error SHALL be treated as "no messaging session" so opening an agent is never blocked.
- An explicit `--session <key>` override SHALL continue to beat every default, now including the messaging-session default.

## Capabilities

### New Capabilities

<!-- none: this refines existing session-selection behaviour -->

### Modified Capabilities

- `sessions`: the session-creation default-key rule gains a messaging-conversation preference ahead of the `main`/`MainKey` fallback; the chat-launcher `--session` override now also beats that preference. (The `chat-launch` spec's override plumbing is unchanged — the override still wins, and that spec cross-references `sessions` for the rule.)
- `one-shot`: `lucinate send` defaults to the same messaging conversation before falling back to `defaultSessionKey`.

## Impact

- New helper `backend.PickMessagingSessionKey` in `internal/backend/session_pick.go` (reads `sessions.list` structured fields; pure selection split out for testing).
- Wired into `internal/tui/app.go` (`viewSelect` open path, now performing the `sessions.list` lookup inside the async create command), `internal/tui/commands.go` (`/agent` switch), and `app/send.go` (one-shot default).
- No SDK, protocol, or gateway changes: uses the existing `SessionsList` / `CreateSession` surface. Backends without messaging sessions (OpenAI-compatible, Hermes) return no qualifying session and keep their current behaviour.
