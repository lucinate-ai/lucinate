# Pre-Navigated Launch Specification

## Purpose

`lucinate chat` is a TUI entry point that accepts the same `--connection` / `--agent` / `--session` overrides as `lucinate send`, but instead of dispatching one turn and exiting it launches the regular interactive program with each override consumed at the transition that would otherwise have prompted the user. An optional positional message is auto-submitted as the first turn once the session's history loads. The user-facing CLI shape lives in the project README (the "skip the pickers" section); this spec covers the override plumbing and the seams the TUI uses to consume them safely.

## Requirements

### Requirement: Separate `chat` subcommand distinct from `send`

The system SHALL provide `lucinate chat` as a separate subcommand from `lucinate send` because the two are distinct lifecycles. `send` is a non-TUI lifecycle (connect → resolve → ChatSend → drain → exit). `chat` is the regular TUI lifecycle with the picker steps short-circuited. Both SHALL share connection / agent resolution helpers (`resolveSendConnection`, `resolveSendAgent` in `app/send.go`) but otherwise have nothing in common — `chat` does not call `ChatSend` itself, never owns a `replyCollector`, and never needs `--detach`. Auth-recovery modals, supervisor reconnect, `/connections`, and every other TUI affordance SHALL keep working unchanged.

#### Scenario: chat launches the interactive TUI, not a one-shot turn
- **WHEN** a user runs `lucinate chat` with `--connection` / `--agent` / `--session` overrides
- **THEN** the regular interactive TUI program launches with each override consumed at the transition that would otherwise have prompted the user
- **AND** the command does not call `ChatSend`, own a `replyCollector`, or require `--detach`

#### Scenario: TUI affordances keep working under chat
- **GIVEN** a session launched via `lucinate chat`
- **WHEN** the user encounters auth-recovery modals, supervisor reconnect, or invokes `/connections`
- **THEN** those affordances work unchanged, exactly as under a bare `lucinate` invocation

### Requirement: `app.Chat` pre-flight lifecycle

`app.Chat` (`app/chat.go`) SHALL be a thin pre-flight stage that performs three steps:

1. **Resolve the connection.** When `--connection` is set, `resolveSendConnection` matches by ID then case-insensitive Name; a miss is a clean error that distinguishes the empty-store first-run case from a typo against a populated store. When `--connection` is unset, `config.ResolveEntryConnection()` runs — the same env-var / default-id / single-entry decision tree the bare `lucinate` invocation uses, including the `ShowPicker` outcome that lands the user on the connections picker.
2. **Pack the overrides into `RunOptions`.** `InitialAgent`, `InitialSession`, `InitialMessage` go onto `RunOptions`; the existing `Initial *config.Connection` carries the resolved connection (or nil for the picker). Agent and session strings are trimmed; the message is taken verbatim so leading whitespace inside a quoted argument is preserved.
3. **Hand off to `Run`.** From here it is the regular TUI. The TUI consumes each override at a defined transition — the resolution logic that would otherwise have prompted runs, then the override is cleared.

`Chat` SHALL NOT connect, list agents, or create a session itself. It SHALL defer all three to the TUI so a typo in `--agent` lands on the existing picker (with an error banner) rather than failing before the user can recover.

#### Scenario: Connection resolved by explicit override
- **GIVEN** `--connection` is set
- **WHEN** `app.Chat` resolves it
- **THEN** `resolveSendConnection` matches by ID then case-insensitive Name
- **AND** a miss is a clean error that distinguishes the empty-store first-run case from a typo against a populated store

#### Scenario: Connection resolved when override is unset
- **GIVEN** `--connection` is unset
- **WHEN** `app.Chat` resolves the connection
- **THEN** `config.ResolveEntryConnection()` runs the same env-var / default-id / single-entry decision tree the bare `lucinate` invocation uses
- **AND** a `ShowPicker` outcome lands the user on the connections picker

#### Scenario: Overrides packed and handed to the TUI
- **WHEN** `app.Chat` packs overrides into `RunOptions`
- **THEN** `InitialAgent`, `InitialSession`, and `InitialMessage` are set, agent and session strings are trimmed, and the message is taken verbatim so leading whitespace inside a quoted argument is preserved
- **AND** `Initial *config.Connection` carries the resolved connection or nil for the picker
- **AND** control hands off to `Run` for the regular TUI

#### Scenario: Chat defers connect, list, and create to the TUI
- **GIVEN** a typo in `--agent`
- **WHEN** `app.Chat` runs
- **THEN** it does not connect, list agents, or create a session itself
- **AND** the typo lands on the existing picker with an error banner rather than failing before the user can recover

### Requirement: Override consumption at three transition points

The three overrides SHALL live on `AppModel` in `internal/tui/app.go` and be consumed at three different transition points, as follows:

| Override | Consumed at | Picker behaviour |
|---|---|---|
| `initialAgent` | `handleConnectResult` success branch — passed into `newSelectModel` as `autoPickName`. | The picker's `agentsLoadedMsg` handler (in `internal/tui/select.go`) runs ID-then-case-insensitive-name resolution **before** the existing single-agent auto-pick and post-create branches. A match sets `m.selected = true`; a miss sets `m.err = fmt.Errorf("agent %q not found", query)`. Either way the field is cleared so a second `agentsLoadedMsg` (e.g. after a user-driven agent create) doesn't re-fire. |
| `initialSession` | `viewSelect` block of `update` — used to override `createKey` before `CreateSession` is invoked. | Beats both `"main"` and the connection's default-agent `MainKey`. Cleared on consume. |
| `initialMessage` | `sessionCreatedMsg` handler — passed into `newChatModel` as a trailing arg, which appends it to `pendingMessages`. | The chat view's `historyLoadedMsg` handler returns `m.drainQueue()` when `pendingMessages` is non-empty and `!m.sending`, so the user turn lands *after* the history scrollback — matching what a human typing would see. |

#### Scenario: Initial agent consumed at connect success
- **GIVEN** `initialAgent` is set
- **WHEN** the `handleConnectResult` success branch runs
- **THEN** it is passed into `newSelectModel` as `autoPickName`, the `agentsLoadedMsg` handler runs ID-then-case-insensitive-name resolution before the single-agent auto-pick and post-create branches, a match sets `m.selected = true` and a miss sets `m.err = fmt.Errorf("agent %q not found", query)`
- **AND** the field is cleared so a second `agentsLoadedMsg` doesn't re-fire

#### Scenario: Initial session overrides the create key
- **GIVEN** `initialSession` is set
- **WHEN** the `viewSelect` block of `update` runs before `CreateSession` is invoked
- **THEN** it overrides `createKey`, beating both `"main"` and the connection's default-agent `MainKey`
- **AND** it is cleared on consume

#### Scenario: Initial message auto-submitted after history
- **GIVEN** `initialMessage` is set
- **WHEN** the `sessionCreatedMsg` handler passes it into `newChatModel` as a trailing arg, which appends it to `pendingMessages`
- **THEN** the chat view's `historyLoadedMsg` handler returns `m.drainQueue()` when `pendingMessages` is non-empty and `!m.sending`, so the user turn lands after the history scrollback matching what a human typing would see

### Requirement: Auto-pick miss beats single-agent auto-pick

The single-agent auto-pick at `select.go` SHALL run only when `autoPickName` was unset on entry. `--agent foo` against a single-agent connection where foo doesn't match MUST surface the error rather than silently picking the wrong agent. This precedence ordering is the design's load-bearing detail; `TestSelectModel_AutoPickName_MissBeatsSingleAgentAutoPick` (`internal/tui/select_test.go`) guards it.

#### Scenario: Non-matching agent on a single-agent connection surfaces an error
- **GIVEN** a single-agent connection and `--agent foo` where foo doesn't match
- **WHEN** the picker resolves the agent
- **THEN** the miss surfaces the error rather than silently picking the single available agent (the single-agent auto-pick only runs when `autoPickName` was unset on entry)

### Requirement: Stale-override clearing on scope change

Because agent IDs and session keys aren't portable across connections, the system SHALL clear all three overrides at the points where the user is signalling a scope change. If the user backs out of a connection the original invocation targeted, applying the original `--agent` against the new connection's agent list would either error spuriously or silently match the wrong agent. The `AppModel.update` method SHALL clear all three overrides at these points:

- **`authResolvedMsg{cancelled:true}`** — user aborted the auth modal, returns to the connections picker.
- **`handleConnectResult` unrecoverable-error branch** — connect failed in a way that bounces back to the connections picker with an error banner.
- **`showConnectionsMsg`** — user invoked `/connections` mid-chat to switch connection.

Successful `authResolvedMsg` retries (token resolved, `cancelled` false) SHALL leave the overrides set: the user just resolved the auth they originally intended to use, so the auto-pick should still fire. `handleConnectResult` success SHALL NOT clear the overrides — it consumes `initialAgent` (passing it into `newSelectModel`), but `initialSession` and `initialMessage` ride through to their later consumption points.

#### Scenario: Overrides cleared when the user signals a scope change
- **WHEN** `authResolvedMsg{cancelled:true}` fires, or the `handleConnectResult` unrecoverable-error branch runs, or `showConnectionsMsg` fires
- **THEN** `AppModel.update` clears all three overrides

#### Scenario: Successful auth retry keeps overrides
- **GIVEN** a successful `authResolvedMsg` retry with token resolved and `cancelled` false
- **WHEN** it is handled
- **THEN** the overrides are left set so the auto-pick still fires

#### Scenario: Connect success rides session and message overrides through
- **WHEN** `handleConnectResult` succeeds
- **THEN** it consumes `initialAgent` by passing it into `newSelectModel` but does not clear the overrides
- **AND** `initialSession` and `initialMessage` ride through to their later consumption points

### Requirement: Embedding `app.Chat` from Go via `ChatOptions`

The system SHALL expose `ChatOptions` as the embedder seam for calling `app.Chat` from Go:

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

`resolveChatRunOptions` (the unexported core that builds `RunOptions` without spinning up Bubble Tea) SHALL be what the unit tests in `app/chat_test.go` exercise. Embedders that want to layer additional `RunOptions` fields (`HideInputArea`, `OnActionsChanged`, …) SHALL call `Chat`'s peer logic themselves rather than relying on internals — those callbacks are TUI-host concerns that don't belong in `ChatOptions`. The function SHALL be a single-shot launcher: there is no resume surface and no incremental progress callback — once the TUI is running, embedders interact with it through the `app.Program` API the bare invocation uses.

#### Scenario: Embedder defaults resolved from empty fields
- **GIVEN** an embedder calls `app.Chat` with an empty `Connection`, empty `Session`, nil `BackendFactory`, and nil `ConnectionsStore`
- **THEN** `Connection` falls back to `ResolveEntryConnection`, `Session` falls back to the main-session default, `BackendFactory` falls back to `DefaultBackendFactory`, and `ConnectionsStore` falls back to `LoadConnections()`

#### Scenario: RunOptions built without Bubble Tea for tests
- **WHEN** the unit tests in `app/chat_test.go` run
- **THEN** they exercise `resolveChatRunOptions`, the unexported core that builds `RunOptions` without spinning up Bubble Tea

#### Scenario: Single-shot launcher with no resume surface
- **WHEN** an embedder calls `app.Chat`
- **THEN** it is a single-shot launcher with no resume surface and no incremental progress callback
- **AND** once the TUI is running, embedders interact with it through the `app.Program` API the bare invocation uses

### Requirement: No pre-flight agent or session validation

The system SHALL NOT perform pre-flight agent / session validation; the TUI SHALL be the validator. A typo in `--agent` lands on the picker with an error banner; a typo in `--session` reaches the backend's `CreateSession`, which decides whether to create-or-resume. Surfacing those errors before the TUI starts would mean a Connect + ListAgents round-trip from `Chat` itself, duplicating the picker's existing error path.

#### Scenario: Agent typo validated by the TUI
- **GIVEN** a typo in `--agent`
- **WHEN** the TUI runs
- **THEN** the invocation lands on the picker with an error banner rather than being validated before the TUI starts

#### Scenario: Session typo reaches CreateSession
- **GIVEN** a typo in `--session`
- **WHEN** the TUI runs
- **THEN** it reaches the backend's `CreateSession`, which decides whether to create-or-resume

### Requirement: Single first-turn message, not multi-turn pre-seeding

The system SHALL treat `--message` as a single first-turn override, not a script. Chains of turns belong on the TUI side (the user types follow-ups) or the `send` side (one turn per invocation, optionally `--detach`'d).

#### Scenario: Message is a single first turn
- **WHEN** `--message` is provided
- **THEN** it is auto-submitted as a single first turn, not a script of multiple turns
- **AND** multi-turn chains belong on the TUI side (user follow-ups) or the `send` side (one turn per invocation, optionally `--detach`'d)

### Requirement: No slash-command parsing on auto-submit

The auto-submitted message SHALL drain through `drainQueue`, not the textarea's Enter handler. `drainQueue` recognises `!` and `!!` exec prefixes and does skill-reference expansion, but it SHALL NOT dispatch slash commands. A message of `/sessions` is sent to the agent as the literal string `/sessions`, not routed to the sessions browser. Scripted workflows that want slash-command effects SHALL call the corresponding RPC directly — the same advice as `send`.

#### Scenario: Slash command sent as a literal string
- **GIVEN** an auto-submitted message of `/sessions`
- **WHEN** it drains through `drainQueue`
- **THEN** it is sent to the agent as the literal string `/sessions`, not routed to the sessions browser
- **AND** `drainQueue` still recognises `!` and `!!` exec prefixes and does skill-reference expansion

#### Scenario: Scripted slash-command effects use RPC directly
- **GIVEN** a scripted workflow that wants slash-command effects
- **WHEN** it needs those effects on auto-submit
- **THEN** it calls the corresponding RPC directly, the same advice as `send`
