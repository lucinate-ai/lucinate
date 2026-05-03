# One-shot mode

`lucinate send` is a non-TUI entry point that dispatches a single chat turn through a stored connection / agent / session and (by default) prints the assistant's first complete reply to stdout. It is the scripting surface alongside the interactive TUI, and it deliberately reuses the TUI's connection store, backend factory, and event channel so retry / auth / capability behaviour stays consistent across both modes.

The user-facing CLI shape is documented in [README.md](../README.md#one-shot-mode). This doc covers the lifecycle, the wire-format edges, and the seams that exist for embedders.

## Lifecycle

`app.Send` (`app/send.go`) runs an eight-step pipeline:

1. **Resolve the connection.** `LoadConnections()` from `internal/config`, then `resolveSendConnection` matches the user-supplied `--connection` first by ID and then case-insensitively by `Name`. A missing connection is a clean error — the resolver does not fall through to the entry-view decision tree (`config.ResolveEntryConnection`), which is TUI-only.
2. **Build the backend.** `DefaultBackendFactory` (`app/factory.go`) dispatches by `Connection.Type` — same factory the TUI uses, same auth wiring (per-connection secrets store, env-var fallback for OpenAI, etc.).
3. **Connect.** `backend.Backend.Connect`. A failure tears the backend back down before returning. There is no auth-recovery modal in this mode; an auth error surfaces as a normal error and the process exits non-zero.
4. **Mark the connection as last-used.** Mirrors the TUI's "last used = default" pattern via `Connections.MarkUsed` + `config.SaveConnections` so the next launch (TUI or `send`) picks the same connection by default.
5. **List agents and resolve the agent.** `ListAgents`, then `resolveSendAgent` matches first by ID and then by case-insensitive `Name`.
6. **Default the session key.** `defaultSessionKey` mirrors the TUI's pick in `internal/tui/select.go`: `AgentsListResult.MainKey` when the chosen agent is the default agent (`agent.ID == list.DefaultID`), otherwise the literal string `"main"`. An explicit `--session` flag short-circuits this.
7. **Create or resume the session.** `CreateSession` round-trips through the backend; the canonical key it returns is what `ChatSend` uses. OpenClaw asks the gateway; OpenAI-compat returns `agentID` (agent ≡ session); Hermes returns the synthetic agent ID.
8. **Subscribe before send, then dispatch.** A `replyCollector` goroutine starts draining `backend.Events()` *before* `ChatSend` is called so the final event cannot race past the consumer. `ChatSend` returns its RPC ack containing the run ID; the collector is told the run ID after the ack lands so events from concurrent turns on the same session are filtered out.

Detach mode skips step 8's wait: after `ChatSend` returns, `Send` returns nil regardless of subsequent events.

`Send` does not run `Backend.Supervise`. A one-shot turn is short-lived enough that auto-reconnect is dead weight; a dropped socket surfaces as a clean failure ("backend event channel closed before reply") rather than triggering exponential backoff. The TUI's `app.Program` keeps Supervise — the lifetimes are different.

## Default session selection

The default-key rule is shared with the TUI agent picker so `lucinate send` and "select agent → main" land on the same gateway-side session:

| Agent | `--session` unset → key passed to `CreateSession` |
|-------|---------------------------------------------------|
| Default agent (`agent.ID == list.DefaultID`) | `list.MainKey` |
| Any other agent                              | `"main"` (literal)                                |

If the same key already exists on the gateway, `CreateSession` resumes it; if not, the gateway provisions one. From the script's point of view, `lucinate send --connection X --agent Y "hello"` repeats into the same conversation forever unless `--session` is supplied.

The literal-`"main"` fallback for non-default agents matches what the TUI passes when the user picks a non-default agent and accepts the picker's "main" session. Backends that don't keep server-side session state (OpenAI, Hermes) ignore the key shape and route by `agentID` regardless.

## Detach semantics

`--detach` returns as soon as `ChatSend` resolves its RPC ack. That guarantees:

- Validation errors surface synchronously: missing flag, no such agent, no such connection, wire-level rejection (auth, idempotency conflict, malformed input).
- The gateway has accepted the turn and assigned a run ID.

It does **not** guarantee:

- That the assistant will reply.
- That a reply, if it arrives, ever reaches stdout — the run continues server-side, and the streaming events are consumed nowhere in detach mode. The reply is rendered the next time the user opens the TUI on that session, just as if a previous TUI window had been closed mid-turn.

Detach is intended for cron-style automation ("nudge the agent at 09:00 to draft the morning digest, render the result on my next browse") and for fire-and-forget shell-pipeline steps that don't care about the response text.

## Embedding `app.Send` from Go

`SendOptions` is the embedder's seam set:

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

`Out` is where the reply is written when not detached. `BackendFactory` lets tests substitute a fake backend (see `app/send_test.go` for the pattern) without going through `DefaultBackendFactory`'s auth / secrets wiring. `ConnectionsStore`, when non-nil, suppresses the implicit `SaveConnections` after `MarkUsed` so test runs don't leave a dirty `connections.json` on disk; production callers leave it nil.

The function is single-shot — there is no streaming surface and no incremental callback. Embedders that need to observe deltas, tool events, or thinking blocks should drive `app.Program` directly.

## Reply text extraction

The shared wire-format parser lives at `internal/backend/chatevent.go`:

- `backend.ExtractChatText(raw)` handles the two shapes a chat event's `Message` can take — a plain JSON string (delta) or a `{ content: [{type, text}, ...] }` object (final). Used by the TUI's chat view (`internal/tui/events.go`) and by `app/send.go`'s `replyCollector` so both paths agree on what the visible body is.
- `backend.ExtractChatThinking(raw)` returns the concatenated `type:"thinking"` blocks from a final event. Currently only the TUI consumes this; `Send` ignores thinking blocks because the contract for `--connection X --agent Y "msg"` is "the assistant's reply on stdout", not the deliberation that led to it.

If a backend ever changes the wire shape, both consumers update together — the parser is the single seam.

## Non-goals

- **Slash-command parsing.** A message body of `/help` is sent verbatim to the agent; the TUI's command dispatcher is intentionally not on this path. Scripts that want a command's effect should call the corresponding RPC directly rather than typing it as chat input.
- **Skill catalog injection.** The TUI sends `Skills` in `ChatSendParams` so the gateway can advertise local skills to the model; `Send` deliberately omits it. Skills are a TUI-discovery concept (slash-command activation, mid-message expansion); revisit if scripted workflows ever want `lucinate send … "/review the diff"` to expand the same way.
- **Auth recovery modals.** The TUI routes `token-mismatch` / `401` into modal flows; `Send` lets them bubble. Scripts that need to bootstrap auth should run `lucinate` once interactively and let the TUI's auth flow seed the secrets store.
