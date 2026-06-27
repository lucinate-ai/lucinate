# Subagents

The `/subagents` command exposes management of subagent (child)
sessions: list live children, view metadata, spawn new ones, kill or
steer them. Lucinate-native syntax that loosely aligns with OpenClaw's
reference subagent UX.

## Surfaces

- `/subagents` (bare) — opens the dedicated browser view (live status,
  spawn / kill / steer).
- `/subagents list` — prints a one-shot inline table of the children
  spawned from the current session.
- `/subagents info <#|key>` — prints metadata for a single child
  (status, parent key, agent, model, depth, last activity).
- `/subagents spawn <task>` — opens the browser and dispatches a new
  child for the given task. Defaults (model, label, target agent) come
  from `~/.lucinate/subagents.json`.
- `/subagents kill <#|key|all>` — aborts an in-flight subagent. `all`
  iterates every tracked child.

The browser view (`viewSubagents`, `internal/tui/subagents.go`) lists
every child the gateway reports for the active session. Selecting a row
exposes `n` (spawn), `s` (steer), `x` / `K` (kill), `r` (refresh).

## Capability gating

`/subagents` is only available when the active backend implements
`backend.SubagentBackend`:

| Backend | Capability | Notes |
| --- | --- | --- |
| OpenClaw | enabled | `sessions.list` (with `spawnedBy` filter), `sessions.create` (with `parentSessionKey`), `sessions.abort`, `sessions.steer` |
| Hermes | not available | `delegate_task` is server-side & blocking within a turn; nothing to manage client-side. The TUI does promote delegation tool calls in the transcript so the user can see the activity. |
| OpenAI-compat / Ollama | not available | The backend has no tool/function-calling loop today (see deferred design below). |

Backends without the capability print
`/subagents is not available on this connection` rather than failing
the type assertion.

## OpenClaw mechanics

A subagent is a session linked to its parent via `spawnedBy` and
`spawnDepth` metadata (`sessions.list` exposes them). The gateway
spawns children automatically when the model invokes `sessions_spawn` /
`sessions_yield`; the `/subagents` command exposes the same surface to
the user.

Spawn:

1. `sessions.create` with `parentSessionKey`, `task`, optional
   `model` / `label`.
2. Gateway issues the child session key and routes the task as the
   first user message in the child.

Lifecycle visibility:

- The chat view's existing tool-event handler recognises subagent /
  delegation tool names (`sessions_spawn`, `sessions_yield`,
  `subagents`, `delegate_task`, …) and routes them through the
  `subagentTracker` so the browser view and `/subagents list` reflect
  live state without an extra RPC.
- Final state arrives via the standard `chat` / `agent` event stream
  and is reconciled by the next `SubagentsList` call.

Kill:

- `sessions.abort` on the child session key. The gateway cascades the
  abort to any nested grandchildren.

## Hermes rendering

Hermes returns no client-driven subagent surface — `delegate_task`
runs synchronously inside the parent's turn and only the final summary
re-enters the parent context. The TUI tags delegation tool calls and
renders them as distinct subagent tool cards so the user can see the
delegation activity and outcome inline.

## Configuration

Client-side defaults live in `~/.lucinate/subagents.json`
(`internal/config/subagents.go`):

```json
{
  "model": "claude-sonnet-4-6",
  "label": "scout",
  "agentId": "secondary",
  "timeoutSeconds": 0
}
```

All fields are optional. Real safety limits (max depth, max
concurrent, idle timeout) live server-side on the gateway / Hermes
profile.

## Deferred: OpenAI-compat / Ollama orchestrator

OpenAI-compatible backends (Ollama, vLLM, llamafile, OpenAI proper)
don't yet have a tool/function-calling loop in lucinate — the model
just streams text. True subagent support there is a separate, larger
feature with two prerequisites in order:

1. **Tool/function-calling loop in `internal/backend/openai/backend.go`.**
   Parse `tool_calls` out of the `/v1/chat/completions` stream,
   dispatch each call, feed the result back as a tool message, loop.
   Until this exists no client-side delegation has anywhere to hook.

2. **Client-side delegation controller.** Modelled on
   `internal/tui/routines_chat.go` (`activeRoutine`): expose a
   `spawn_subagent` tool to the model; on call, run a child chat loop
   against the same backend with an **isolated** message history
   (children start with a fresh context, not a fork of the parent),
   honouring the `subagents.json` model override and a bounded
   concurrency knob; inject only the final summary as the tool result;
   continue the parent turn. Blocking semantics match Hermes — the
   parent turn pauses until children finish.

The design above keeps the `SubagentBackend` interface in
`internal/backend/backend.go` backend-agnostic, so once steps 1 + 2 are
in place the OpenAI-compat backend can implement the same interface
and `/subagents` + the browser view light up automatically.
