# Sessions

## Lifecycle

A session is created when the user selects an agent in the agent picker (see [agents.md](agents.md)). `client.CreateSession(agentID, key)` is called and the returned `sessionKey` is passed to `newChatModel()`. The session key is deterministic for non-default agents (based on agent ID) so the same session is restored on restart.

The same default-key rule is reused by the one-shot CLI mode: `app.Send` (`lucinate send`) calls `CreateSession` with `MainKey` for the default agent and the literal `"main"` for any other agent, so a scripted dispatch lands on the same conversation as "open the picker, pick the agent, hit enter". See [one-shot.md](one-shot.md) for the full lifecycle.

`lucinate chat --session <key>` overrides this default at the picker's `CreateSession` site: `AppModel.initialSession` is consumed in the `viewSelect` block of `update`, beating both the literal `"main"` and `MainKey`. The override is one-shot — cleared once consumed so a follow-up agent pick on the same picker doesn't keep landing on the original key. See [chat-launch.md](chat-launch.md).

On `chatModel.Init()`, two async commands run in parallel:

- `loadHistory()` — fetches the last N messages from the gateway (`client.SessionHistory()`), strips `System:` lines (see [message-rendering.md](message-rendering.md#history-cleanup)), and populates the viewport.
- `loadStats()` — fetches token usage and cost via `client.SessionUsage()` for the header bar.

History depth (N) is configurable; see [chat-ux.md](chat-ux.md#history-depth).

## Session browser

`/sessions` emits `showSessionsMsg{}`, which navigates to the sessions view (`sessionsModel` in `internal/tui/sessions.go`). The model calls `client.SessionsList(agentID)` and parses the response into `sessionItem` values grouped into two lists:

- **Conversations** — regular sessions.
- **Scheduled** — sessions whose key contains `:cron:`, used by scheduled/automated agents.

Both lists are sorted by `updatedAt` descending. Selecting a session returns `sessionSelectedMsg` and a new `chatModel` is constructed with the chosen key, loading its history.

## Compact and reset

Both commands use the [confirmation pattern](commands.md#confirmation-pattern) before taking action.

**`/compact`** calls `backend.SessionCompact()`, which summarises older messages in the session context to reduce token usage. On OpenClaw the gateway runs the pass server-side; on the OpenAI-compatible backend the pass runs locally (a non-streaming `POST /v1/chat/completions` against the agent's configured model — see [backend_openai.md](backend_openai.md#compaction)). The chat view shows a `Session compacted.` system line on success; the smaller context is picked up on the next chat send (the in-memory message list is not redrawn, so the user's transcript stays where it was).

**`/reset`** calls `client.SessionDelete()` to permanently remove the session, then immediately creates a replacement via `client.CreateSession()`. The new session key is returned as `sessionClearedMsg{newSessionKey}` and the chat model reinitialises with an empty history.

## Message queueing

While `m.sending == true` (a response is in flight), new user input is appended to `m.pendingMessages []string` rather than sent immediately. This prevents messages from being dropped when the user types quickly.

After each response (`drainQueue()` in `chat.go`):

1. If there are pending messages, the first one is dequeued and sent as if the user typed it fresh (including command detection, skill prepend, etc.).
2. History is refreshed once the queue is fully drained.

Local (`!`) and remote (`!!`) exec results also trigger queue draining. See [shell-execution.md](shell-execution.md) for the exec flow.

`lucinate chat <message>` pre-seeds the same queue: `newChatModel` appends the supplied text to `pendingMessages` at construction time, and the `historyLoadedMsg` handler returns `m.drainQueue()` once the scrollback has rendered — so the user turn appears *after* the loaded history, matching what a human typing the same message would see. See [chat-launch.md](chat-launch.md).
