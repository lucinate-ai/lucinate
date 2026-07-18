# One-Shot Mode Specification

## Purpose

`lucinate send` is a non-TUI entry point that dispatches a single chat turn through a stored connection / agent / session and (by default) prints the assistant's first complete reply to stdout. It is the scripting surface alongside the interactive TUI, and it deliberately reuses the TUI's connection store, backend factory, and event channel so retry / auth / capability behaviour stays consistent across both modes. This spec covers the `app.Send` lifecycle, the default-session rule, detach semantics, the `ask` alias, the embedder seam, reply-text extraction, and the deliberate non-goals. The user-facing CLI shape is documented in the README's one-shot-mode section.

## Requirements

### Requirement: Send lifecycle pipeline

`app.Send` (`app/send.go`) SHALL run an eight-step pipeline:

1. **Resolve the connection.** `LoadConnections()` from `internal/config`, then `resolveSendConnection` matches the user-supplied `--connection` first by ID and then case-insensitively by `Name`. A missing connection SHALL be a clean error — the resolver MUST NOT fall through to the entry-view decision tree (`config.ResolveEntryConnection`), which is TUI-only.
2. **Build the backend.** `DefaultBackendFactory` (`app/factory.go`) dispatches by `Connection.Type` — the same factory the TUI uses, with the same auth wiring (per-connection secrets store, env-var fallback for OpenAI, etc.).
3. **Connect.** `backend.Backend.Connect`. A failure SHALL tear the backend back down before returning. There is no auth-recovery modal in this mode; an auth error surfaces as a normal error and the process exits non-zero.
4. **Mark the connection as last-used.** Mirrors the TUI's "last used = default" pattern via `Connections.MarkUsed` + `config.SaveConnections` so the next launch (TUI or `send`) picks the same connection by default.
5. **List agents and resolve the agent.** `ListAgents`, then `resolveSendAgent` matches first by ID and then by case-insensitive `Name`.
6. **Default the session key.** `defaultSessionKey` mirrors the TUI's pick in `internal/tui/select.go`: `AgentsListResult.MainKey` when the chosen agent is the default agent (`agent.ID == list.DefaultID`), otherwise the literal string `"main"`. An explicit `--session` flag short-circuits this.
7. **Create or resume the session.** `CreateSession` round-trips through the backend; the canonical key it returns is what `ChatSend` uses. OpenClaw asks the gateway; OpenAI-compat returns `agentID` (agent ≡ session); Hermes returns the synthetic agent ID.
8. **Subscribe before send, then dispatch.** A `replyCollector` goroutine starts draining `backend.Events()` *before* `ChatSend` is called so the final event cannot race past the consumer. `ChatSend` returns its RPC ack containing the run ID; the collector is told the run ID after the ack lands so events from concurrent turns on the same session are filtered out.

#### Scenario: Missing connection is a clean error
- **WHEN** `--connection` names a connection that resolves against neither an ID nor a case-insensitive `Name`
- **THEN** `resolveSendConnection` returns a clean error
- **AND** it does NOT fall through to `config.ResolveEntryConnection`, which is TUI-only

#### Scenario: Full happy-path turn
- **GIVEN** a valid connection, agent, and message
- **WHEN** `app.Send` runs
- **THEN** it resolves the connection, builds the backend via `DefaultBackendFactory`, connects, marks the connection last-used via `Connections.MarkUsed` + `config.SaveConnections`, resolves the agent, defaults the session key, creates or resumes the session, and dispatches `ChatSend`
- **AND** the `replyCollector` goroutine begins draining `backend.Events()` before `ChatSend` is called so the final event cannot race past the consumer

#### Scenario: Connect failure tears down the backend
- **WHEN** `backend.Backend.Connect` fails
- **THEN** the backend is torn back down before returning
- **AND** the auth error surfaces as a normal error with a non-zero process exit, with no auth-recovery modal

#### Scenario: Run-ID filtering of concurrent turns
- **GIVEN** the `replyCollector` is draining events before dispatch
- **WHEN** `ChatSend` returns its RPC ack containing the run ID
- **THEN** the collector is told the run ID after the ack lands
- **AND** events from concurrent turns on the same session are filtered out

### Requirement: No supervision for one-shot turns

`Send` SHALL NOT run `Backend.Supervise`. A one-shot turn is short-lived enough that auto-reconnect is dead weight; a dropped socket SHALL surface as a clean failure ("backend event channel closed before reply") rather than triggering exponential backoff. The TUI's `app.Program` keeps Supervise — the lifetimes are different.

#### Scenario: Dropped socket surfaces as a clean failure
- **GIVEN** a one-shot turn is in flight
- **WHEN** the backend socket drops
- **THEN** `Send` returns the clean failure "backend event channel closed before reply"
- **AND** no exponential backoff or auto-reconnect is triggered

### Requirement: Default session selection

The default-key rule SHALL be shared with the TUI agent picker so `lucinate send` and "select agent → main" land on the same gateway-side session:

| Agent | `--session` unset → key passed to `CreateSession` |
|-------|---------------------------------------------------|
| Default agent (`agent.ID == list.DefaultID`) | `list.MainKey` |
| Any other agent                              | `"main"` (literal)                                |

If the same key already exists on the gateway, `CreateSession` SHALL resume it; if not, the gateway SHALL provision one. From the script's point of view, `lucinate send --connection X --agent Y "hello"` repeats into the same conversation forever unless `--session` is supplied.

The literal-`"main"` fallback for non-default agents matches what the TUI passes when the user picks a non-default agent and accepts the picker's "main" session. Backends that don't keep server-side session state (OpenAI, Hermes) SHALL ignore the key shape and route by `agentID` regardless.

#### Scenario: Default agent uses the main-session key
- **GIVEN** the chosen agent is the default agent (`agent.ID == list.DefaultID`) and `--session` is unset
- **WHEN** the session key is defaulted
- **THEN** `list.MainKey` is passed to `CreateSession`

#### Scenario: Non-default agent falls back to literal "main"
- **GIVEN** the chosen agent is not the default agent and `--session` is unset
- **WHEN** the session key is defaulted
- **THEN** the literal string `"main"` is passed to `CreateSession`, matching what the TUI passes when the user accepts the picker's "main" session

#### Scenario: Repeated sends land in the same conversation
- **GIVEN** `--session` is not supplied
- **WHEN** `lucinate send --connection X --agent Y "hello"` is run repeatedly
- **THEN** `CreateSession` resumes the existing key if present or the gateway provisions one, so the turns repeat into the same conversation

#### Scenario: Stateless backends route by agent
- **GIVEN** an OpenAI or Hermes backend that keeps no server-side session state
- **WHEN** a session key is passed
- **THEN** the backend ignores the key shape and routes by `agentID`

### Requirement: Detach mode

`Send` in detach mode (`--detach`) SHALL skip step 8's wait: after `ChatSend` returns, `Send` SHALL return nil regardless of subsequent events. `--detach` returns as soon as `ChatSend` resolves its RPC ack.

This guarantees:

- Validation errors surface synchronously: missing flag, no such agent, no such connection, wire-level rejection (auth, idempotency conflict, malformed input).
- The gateway has accepted the turn and assigned a run ID.

It does **not** guarantee:

- That the assistant will reply.
- That a reply, if it arrives, ever reaches stdout — the run continues server-side, and the streaming events are consumed nowhere in detach mode. The reply is rendered the next time the user opens the TUI on that session, just as if a previous TUI window had been closed mid-turn.

Detach is intended for cron-style automation ("nudge the agent at 09:00 to draft the morning digest, render the result on my next browse") and for fire-and-forget shell-pipeline steps that don't care about the response text.

#### Scenario: Detach returns on the ack
- **GIVEN** `--detach` is set
- **WHEN** `ChatSend` resolves its RPC ack
- **THEN** `Send` returns nil immediately, skipping the reply wait, regardless of subsequent events

#### Scenario: Validation errors surface synchronously in detach
- **GIVEN** `--detach` is set
- **WHEN** there is a missing flag, no such agent, no such connection, or a wire-level rejection (auth, idempotency conflict, malformed input)
- **THEN** the error surfaces synchronously

#### Scenario: Detach does not guarantee a reply reaches stdout
- **GIVEN** `--detach` is set and the gateway has accepted the turn with a run ID
- **WHEN** the assistant later replies
- **THEN** the reply is not written to stdout — the run continues server-side and streaming events are consumed nowhere
- **AND** the reply is rendered the next time the user opens the TUI on that session

### Requirement: The `ask` alias

`lucinate ask` (`internal/cli/ask.go`) SHALL be a thin wrapper over the same `app.Send` pipeline — there is no second code path. The only difference from `send` is where the flags' default values come from:

- `runAsk` loads `config.LoadPreferences().Ask` (a `config.AskDefaults` block in `config.json`) and seeds the `ask` flag set's defaults with it. An omitted `-c`/`-a`/`-s`/`-d` SHALL fall back to the saved value; an explicit flag SHALL still override it, because `flag` parsing runs after the defaults are set.
- After parsing, `runAsk` SHALL guard on a blank connection or agent and return an `ask:`-prefixed error pointing the user at `/settings ▸ Ask command defaults`, rather than letting `app.Send`'s `send:`-prefixed "required" error surface for what is, in `ask`'s world, a configuration gap.
- Everything else — message join, `--`/dash handling, the `app.Send` dispatch — SHALL be identical to `runSend`.

`config.AskDefaults` has one field per `send` flag (`Connection`, `Agent`, `Session`, `Detach`). The three files carry mutual `KEEP IN SYNC` comments (`internal/cli/send.go`, `internal/cli/ask.go`, `internal/config/preferences.go`): a new field added to `send` should gain an `AskDefaults` field and a row in the TUI sub-screen so `ask` stays a complete alias. The defaults are edited in the TUI under `/settings ▸ Ask command defaults` (`internal/tui/askconfigview.go`); see the `commands` spec for `/settings` dispatch.

#### Scenario: Omitted flag falls back to saved default
- **GIVEN** a `config.AskDefaults` block in `config.json`
- **WHEN** `-c`/`-a`/`-s`/`-d` is omitted from an `ask` invocation
- **THEN** the value falls back to the saved default, because `runAsk` seeds the flag set's defaults before `flag` parsing

#### Scenario: Explicit flag overrides the saved default
- **GIVEN** a saved `AskDefaults` value for a flag
- **WHEN** the flag is passed explicitly
- **THEN** the explicit value overrides the saved default, because `flag` parsing runs after the defaults are set

#### Scenario: Blank connection or agent yields an ask-prefixed error
- **GIVEN** an `ask` invocation whose resolved connection or agent is blank
- **WHEN** `runAsk` guards after parsing
- **THEN** it returns an `ask:`-prefixed error pointing at `/settings ▸ Ask command defaults`, rather than `app.Send`'s `send:`-prefixed "required" error

#### Scenario: Everything else matches runSend
- **WHEN** an `ask` turn is dispatched
- **THEN** message join, `--`/dash handling, and the `app.Send` dispatch are identical to `runSend`

### Requirement: Embedding `app.Send` from Go

`SendOptions` SHALL be the embedder's seam set:

```go
err := app.Send(ctx, app.SendOptions{
    Connection:       "my-con",
    Agent:            "main",
    Session:          "",       // empty → main-session default
    Message:          "hello",
    Detach:           false,
    Out:              os.Stdout,
    BackendFactory:   nil,      // nil → DefaultBackendFactory
    ConnectionsStore: nil,      // nil → LoadConnections()
})
```

- `Out` is where the reply is written when not detached.
- `BackendFactory` lets tests substitute a fake backend (see `app/send_test.go` for the pattern) without going through `DefaultBackendFactory`'s auth / secrets wiring.
- `ConnectionsStore`, when non-nil, SHALL suppress the implicit `SaveConnections` after `MarkUsed` so test runs don't leave a dirty `connections.json` on disk; production callers leave it nil.

The function is single-shot — there is no streaming surface and no incremental callback. Embedders that need to observe deltas, tool events, or thinking blocks SHALL drive `app.Program` directly.

#### Scenario: Nil options resolve to defaults
- **GIVEN** `SendOptions` with `BackendFactory: nil` and `ConnectionsStore: nil`
- **WHEN** `app.Send` runs
- **THEN** `BackendFactory` resolves to `DefaultBackendFactory` and `ConnectionsStore` resolves to `LoadConnections()`

#### Scenario: Non-nil ConnectionsStore suppresses the disk write
- **GIVEN** a non-nil `ConnectionsStore`
- **WHEN** the connection is marked used
- **THEN** the implicit `SaveConnections` after `MarkUsed` is suppressed so no dirty `connections.json` is left on disk

#### Scenario: Fake backend for tests
- **GIVEN** a `BackendFactory` supplied by a test
- **WHEN** `app.Send` builds the backend
- **THEN** the fake backend is used without going through `DefaultBackendFactory`'s auth / secrets wiring

#### Scenario: Streaming observers must drive the program
- **GIVEN** an embedder that needs to observe deltas, tool events, or thinking blocks
- **WHEN** it wants incremental output
- **THEN** it drives `app.Program` directly, because `app.Send` is single-shot with no streaming surface or incremental callback

### Requirement: Reply text extraction

The shared wire-format parser SHALL live at `internal/backend/chatevent.go` as the single seam for reply-text extraction:

- `backend.ExtractChatText(raw)` SHALL handle the two shapes a chat event's `Message` can take — a plain JSON string (delta) or a `{ content: [{type, text}, ...] }` object (final). It is used by the TUI's chat view (`internal/tui/events.go`) and by `app/send.go`'s `replyCollector` so both paths agree on what the visible body is.
- `backend.ExtractChatThinking(raw)` SHALL return the concatenated `type:"thinking"` blocks from a final event. Currently only the TUI consumes this; `Send` ignores thinking blocks because the contract for `--connection X --agent Y "msg"` is "the assistant's reply on stdout", not the deliberation that led to it.

If a backend ever changes the wire shape, both consumers update together — the parser is the single seam.

#### Scenario: Extract handles delta and final shapes
- **WHEN** `backend.ExtractChatText(raw)` receives a plain JSON string (delta) or a `{ content: [{type, text}, ...] }` object (final)
- **THEN** it returns the visible body for both shapes, so the TUI chat view and `send`'s `replyCollector` agree

#### Scenario: Send ignores thinking blocks
- **GIVEN** a final event containing `type:"thinking"` blocks
- **WHEN** `Send` extracts the reply
- **THEN** it ignores the thinking blocks, because the contract is the assistant's reply on stdout, not the deliberation
- **AND** only the TUI consumes `backend.ExtractChatThinking(raw)`

### Requirement: One-shot non-goals

`Send` SHALL deliberately omit the following TUI-only behaviours:

- **Slash-command parsing.** A message body of `/help` SHALL be sent verbatim to the agent; the TUI's command dispatcher is intentionally not on this path. Scripts that want a command's effect should call the corresponding RPC directly rather than typing it as chat input.
- **Skill catalog injection.** The TUI sends `Skills` in `ChatSendParams` so the gateway can advertise local skills to the model; `Send` SHALL omit it. Skills are a TUI-discovery concept (slash-command activation, mid-message expansion); revisit if scripted workflows ever want `lucinate send … "/review the diff"` to expand the same way.
- **Auth recovery modals.** The TUI routes `token-mismatch` / `401` into modal flows; `Send` SHALL let them bubble. Scripts that need to bootstrap auth should run `lucinate` once interactively and let the TUI's auth flow seed the secrets store.

#### Scenario: Slash command sent verbatim
- **WHEN** a message body of `/help` is sent through `Send`
- **THEN** it is delivered verbatim to the agent, with no command dispatch

#### Scenario: Skills omitted from ChatSendParams
- **WHEN** `Send` builds `ChatSendParams`
- **THEN** it omits `Skills`, unlike the TUI

#### Scenario: Auth errors bubble instead of opening a modal
- **WHEN** a `token-mismatch` or `401` auth error occurs during `Send`
- **THEN** it bubbles up as a normal error rather than opening an auth-recovery modal
