# Pre-navigated launch — lessons and rationale

The behavioural contract for pre-navigated launch lives in
[`openspec/specs/chat-launch/spec.md`](../openspec/specs/chat-launch/spec.md) — the `chat`
subcommand, the `app.Chat` pre-flight lifecycle, override consumption, stale-override clearing,
and the `ChatOptions` embedder seam are all captured there as requirements and scenarios. This
file keeps the hard-won lessons, pitfalls, and design rationale behind that flow: the "why it
works this odd way" that the spec's requirements don't dwell on.

## Why a separate subcommand rather than a `send` flag

`send` is a non-TUI lifecycle (connect → resolve → ChatSend → drain → exit). `chat` is the
regular TUI lifecycle with the picker steps short-circuited. Both share connection / agent
resolution helpers (`resolveSendConnection`, `resolveSendAgent` in `app/send.go`) but otherwise
have nothing in common — `chat` does not call `ChatSend` itself, never owns a `replyCollector`,
and never needs `--detach`. Bolting this onto `send` as a flag would have meant one command
straddling two incompatible lifecycles.

`Chat` deliberately does not connect, list agents, or create a session itself. It defers all
three to the TUI so a typo in `--agent` lands on the existing picker (with an error banner)
rather than failing before the user can recover.

## The load-bearing precedence: auto-pick miss beats single-agent auto-pick

The single-agent auto-pick in `select.go` runs only when `autoPickName` was unset on entry.
`--agent foo` against a single-agent connection where `foo` doesn't match must surface the error
rather than silently picking the wrong agent — the one agent available is not necessarily the one
the user asked for. This precedence ordering is the design's load-bearing detail;
`TestSelectModel_AutoPickName_MissBeatsSingleAgentAutoPick` (`internal/tui/select_test.go`)
guards it. Treat that test as a regression fence.

## Why stale overrides are cleared on scope change

Agent IDs and session keys aren't portable across connections. If the user backs out of a
connection the original invocation targeted, applying the original `--agent` against the new
connection's agent list would either error spuriously or silently match the wrong agent. So the
overrides are cleared at exactly the points where the user is signalling a scope change: aborting
the auth modal, an unrecoverable connect error that bounces back to the connections picker, and
`/connections` mid-chat.

The two cases that deliberately do *not* clear are the subtle part. A successful
`authResolvedMsg` retry (token resolved, `cancelled` false) leaves the overrides set — the user
just resolved the auth they originally intended to use, so the auto-pick should still fire. And
`handleConnectResult` success consumes `initialAgent` but leaves `initialSession` and
`initialMessage` to ride through to their later consumption points; clearing them there would
strand the session and message overrides before they'd ever been used.

## Why no pre-flight validation

The TUI is the validator, on purpose. A typo in `--agent` lands on the picker with an error
banner; a typo in `--session` reaches the backend's `CreateSession`, which decides whether to
create-or-resume. Surfacing those errors before the TUI starts would mean a Connect + ListAgents
round-trip from `Chat` itself, duplicating the picker's existing error path for no gain.

## Why `--message` isn't a script, and slash commands stay literal

`--message` is a single first-turn override, not a script — chains of turns belong on the TUI
side (the user types follow-ups) or the `send` side (one turn per invocation, optionally
`--detach`'d).

The auto-submit is easy to misread as "type this into the box and press Enter", but it drains
through `drainQueue`, not the textarea's Enter handler. `drainQueue` recognises `!` and `!!` exec
prefixes and does skill-reference expansion, but it does **not** dispatch slash commands. A
message of `/sessions` is sent to the agent as the literal string `/sessions`, not routed to the
sessions browser. Scripted workflows that want slash-command effects should call the
corresponding RPC directly — the same advice as `send`.

## Embedder seam: what deliberately isn't in `ChatOptions`

`resolveChatRunOptions` (the unexported core that builds `RunOptions` without spinning up Bubble
Tea) is what the unit tests in `app/chat_test.go` exercise — that split exists so the resolution
logic is testable without a running TUI. Embedders that want to layer additional `RunOptions`
fields (`HideInputArea`, `OnActionsChanged`, …) should call `Chat`'s peer logic themselves rather
than relying on internals — those callbacks are TUI-host concerns that don't belong in
`ChatOptions`. `Chat` is a single-shot launcher with no resume surface and no incremental
progress callback by design; once the TUI is running, embedders interact with it through the same
`app.Program` API the bare invocation uses.
