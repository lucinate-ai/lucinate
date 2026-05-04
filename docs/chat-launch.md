# Pre-navigated launch (`lucinate chat`)

`lucinate chat` is a TUI entry point that accepts the same `--connection` / `--agent` / `--session` overrides as `lucinate send`, but instead of dispatching one turn and exiting it launches the regular interactive program with each override consumed at the transition that would otherwise have prompted the user. An optional positional message is auto-submitted as the first turn once the session's history loads.

The user-facing CLI shape lives in [README.md](../README.md#skip-the-pickers). This doc covers the override plumbing and the seams the TUI uses to consume them safely.

## Why a separate subcommand

`send` is a non-TUI lifecycle (connect → resolve → ChatSend → drain → exit). `chat` is the regular TUI lifecycle with the picker steps short-circuited. Both share connection / agent resolution helpers (`resolveSendConnection`, `resolveSendAgent` in `app/send.go`) but otherwise have nothing in common — `chat` does not call `ChatSend` itself, never owns a `replyCollector`, and never needs `--detach`. Auth-recovery modals, supervisor reconnect, `/connections`, and every other TUI affordance keep working unchanged.

## Lifecycle

`app.Chat` (`app/chat.go`) is a thin pre-flight stage:

1. **Resolve the connection.** When `--connection` is set, `resolveSendConnection` matches by ID then case-insensitive Name; a miss is a clean error that distinguishes the empty-store first-run case from a typo against a populated store. When `--connection` is unset, `config.ResolveEntryConnection()` runs — same env-var / default-id / single-entry decision tree the bare `lucinate` invocation uses, including the `ShowPicker` outcome that lands the user on the connections picker.
2. **Pack the overrides into `RunOptions`.** `InitialAgent`, `InitialSession`, `InitialMessage` go onto `RunOptions`; the existing `Initial *config.Connection` carries the resolved connection (or nil for the picker). Agent and session strings are trimmed; the message is taken verbatim so leading whitespace inside a quoted argument is preserved.
3. **Hand off to `Run`.** From here it's the regular TUI. The TUI consumes each override at a defined transition — the resolution logic that would otherwise have prompted runs, then the override is cleared.

`Chat` does not connect, list agents, or create a session itself. It defers all three to the TUI so a typo in `--agent` lands on the existing picker (with an error banner) rather than failing before the user can recover.

## Override consumption

The three overrides live on `AppModel` in `internal/tui/app.go` and are consumed at three different transition points:

| Override | Consumed at | Picker behaviour |
|---|---|---|
| `initialAgent` | `handleConnectResult` success branch — passed into `newSelectModel` as `autoPickName`. | The picker's `agentsLoadedMsg` handler (in `internal/tui/select.go`) runs ID-then-case-insensitive-name resolution **before** the existing single-agent auto-pick and post-create branches. A match sets `m.selected = true`; a miss sets `m.err = fmt.Errorf("agent %q not found", query)`. Either way the field is cleared so a second `agentsLoadedMsg` (e.g. after a user-driven agent create) doesn't re-fire. |
| `initialSession` | `viewSelect` block of `update` — used to override `createKey` before `CreateSession` is invoked. | Beats both `"main"` and the connection's default-agent `MainKey`. Cleared on consume. |
| `initialMessage` | `sessionCreatedMsg` handler — passed into `newChatModel` as a trailing arg, which appends it to `pendingMessages`. | The chat view's `historyLoadedMsg` handler returns `m.drainQueue()` when `pendingMessages` is non-empty and `!m.sending`, so the user turn lands *after* the history scrollback — matching what a human typing would see. |

The single-agent auto-pick at `select.go` runs only when `autoPickName` was unset on entry — `--agent foo` against a single-agent connection where foo doesn't match must surface the error rather than silently picking the wrong agent. This precedence ordering is the design's load-bearing detail; `TestSelectModel_AutoPickName_MissBeatsSingleAgentAutoPick` (`internal/tui/select_test.go`) guards it.

## Stale-override clearing

Agent IDs and session keys aren't portable across connections. If the user backs out of a connection the original invocation targeted, applying the original `--agent` against the new connection's agent list would either error spuriously or silently match the wrong agent. The `AppModel.update` method clears all three overrides at the points where the user is signalling a scope change:

- **`authResolvedMsg{cancelled:true}`** — user aborted the auth modal, returns to the connections picker.
- **`handleConnectResult` unrecoverable-error branch** — connect failed in a way that bounces back to the connections picker with an error banner.
- **`showConnectionsMsg`** — user invoked `/connections` mid-chat to switch connection.

Successful `authResolvedMsg` retries (token resolved, `cancelled` false) leave the overrides set: the user just resolved the auth they originally intended to use, so the auto-pick should still fire.

`handleConnectResult` success does **not** clear the overrides — it consumes `initialAgent` (passing it into `newSelectModel`), but `initialSession` and `initialMessage` ride through to their later consumption points.

## Embedding `app.Chat` from Go

`ChatOptions` is the embedder seam:

```go
err := app.Chat(ctx, app.ChatOptions{
    Connection:       "my-con",   // empty → ResolveEntryConnection
    Agent:            "main",
    Session:          "",          // empty → main-session default
    Message:          "hello",
    BackendFactory:   nil,         // nil → DefaultBackendFactory
    ConnectionsStore: nil,         // nil → LoadConnections()
})
```

`resolveChatRunOptions` (the unexported core that builds `RunOptions` without spinning up Bubble Tea) is what the unit tests in `app/chat_test.go` exercise; embedders that want to layer additional `RunOptions` fields (`HideInputArea`, `OnActionsChanged`, …) should call `Chat`'s peer logic themselves rather than relying on internals — those callbacks are TUI-host concerns that don't belong in `ChatOptions`.

The function is a single-shot launcher. There is no resume surface and no incremental progress callback — once the TUI is running, embedders interact with it through the `app.Program` API the bare invocation uses.

## Non-goals

- **Pre-flight agent / session validation.** The TUI is the validator. A typo in `--agent` lands on the picker with an error banner; a typo in `--session` reaches the backend's `CreateSession`, which decides whether to create-or-resume. Surfacing those errors before the TUI starts would mean a Connect + ListAgents round-trip from `Chat` itself, duplicating the picker's existing error path.
- **Multi-turn pre-seeding.** `--message` is a single first-turn override, not a script. Chains of turns belong on the TUI side (the user types follow-ups) or the `send` side (one turn per invocation, optionally `--detach`'d).
- **Slash-command parsing.** The auto-submit drains through `drainQueue`, not the textarea's Enter handler — `drainQueue` recognises `!` and `!!` exec prefixes and does skill-reference expansion, but it does **not** dispatch slash commands. A message of `/sessions` is sent to the agent as the literal string `/sessions`, not routed to the sessions browser. Scripted workflows that want slash-command effects should call the corresponding RPC directly, same advice as `send`.
