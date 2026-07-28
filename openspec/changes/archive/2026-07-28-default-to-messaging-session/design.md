## Context

Opening an agent creates or resumes a session at three sites — the TUI agent picker (`viewSelect` in `internal/tui/app.go`), the in-chat `/agent` switch (`internal/tui/commands.go`), and one-shot `lucinate send` (`app/send.go`). All three default the session key to `MainKey` for the connection's default agent, or the literal `"main"` otherwise.

When the user actually converses with the agent over an external messaging channel, the conversation lives in a channel-tied session the gateway minted, e.g. `agent:main:telegram:direct:123456789`. That key is opaque to lucinate. The goal is to open that conversation by default rather than a blank `main` bucket. The constraint: do it without new gateway/SDK surface and without brittle string-parsing of a gateway-owned key format.

## Goals / Non-Goals

**Goals:**
- Default to the messaging conversation the user talks to an agent on, at all three open-agent sites.
- Reuse existing RPCs (`sessions.list`, `CreateSession`) — no protocol changes.
- Recognise the messaging session from structured data, not by decoding the opaque key.
- Fail safe: any error or "no messaging session" falls back to today's behaviour; never block opening an agent.

**Non-Goals:**
- Knowing definitively *which* human is "the user" — the client has no such signal, so a heuristic (most-recent) is accepted.
- Filtering by live channel connectivity (`channels.status`) — see Decisions.
- Changing session keys, the session browser, or how history loads.

## Decisions

**Anchor the lookup on `sessions.list` structured fields, not `channels.status` or `sessions.resolve`.** Each `sessions.list` row already carries `channel`, `kind` (`direct`/`group`/`global`), and an `origin`/`deliveryContext` block with the provider (`telegram`), the peer id (`from` / `nativeDirectUserId` / `to`), and the account. A row qualifies as a messaging conversation when it is `direct` and has both a channel and a peer; among qualifiers the most-recently-updated `key` is used verbatim. This mirrors the gateway's own conversation-extraction precedence.

- *Alternative — `channels.status`:* rejected. It is a global snapshot with no agent linkage and no sender identity, so it cannot yield the `123456789` needed to identify the conversation. It can only report that *a* channel account is connected. (It was the initially-preferred option until investigation showed it lacks the key data.)
- *Alternative — `sessions.resolve`:* rejected. It only normalises a key/sessionId/label you already hold into a canonical key; it cannot take channel + sender and return a key.
- *Alternative — parse the opaque key (`:telegram:`, `:direct:`):* rejected as brittle; the format is gateway-owned and undocumented on the client side. The structured fields carry the same information without that coupling.

**Perform the lookup inside the existing async create command.** The picker's create step is already a `tea.Cmd` closure, so the extra `sessions.list` round-trip runs there before `CreateSession`, under the same request timeout. `CreateSession` already "creates or resumes", so passing the existing messaging key simply resumes that conversation — no new open-existing path needed.

**One shared helper.** `backend.PickMessagingSessionKey(ctx, b, agentID)` lives in `internal/backend` (imported by both `internal/tui` and `app`, no import cycle) with the pure selection split into `pickMessagingKeyFromRaw` for table-driven tests.

## Risks / Trade-offs

- [Wrong DM chosen when an agent has several messaging conversations] → most-recently-updated wins; acceptable because the client cannot identify the operator's own peer id, and the user accepted this trade-off.
- [Opens a session on a channel that is no longer connected] → accepted; the conversation and its history still exist and are useful. Live-connectivity filtering via `channels.status` was explicitly deferred.
- [Extra `sessions.list` round-trip on every open] → one fast call the session browser already makes; runs under the existing timeout and falls back to the old default on error, so latency and failure are bounded.
- [Gateway changes the `origin`/`deliveryContext` field shape] → the helper degrades to "no messaging session" (fields absent → no qualifier) and falls back, rather than misbehaving.

## Open Questions

None outstanding. The messaging-vs-main disambiguation (most-recent) and the decision to skip `channels.status` connectivity filtering were both settled during proposal review.
