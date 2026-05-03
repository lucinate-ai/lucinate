# Sessions

## Lifecycle

A session is created when the user selects an agent in the agent picker (see [agents.md](agents.md)). `client.CreateSession(agentID, key)` is called and the returned `sessionKey` is passed to `newChatModel()`. The session key is deterministic for non-default agents (based on agent ID) so the same session is restored on restart.

The same default-key rule is reused by the one-shot CLI mode: `app.Send` (`lucinate send`) calls `CreateSession` with `MainKey` for the default agent and the literal `"main"` for any other agent, so a scripted dispatch lands on the same conversation as "open the picker, pick the agent, hit enter". See [one-shot.md](one-shot.md) for the full lifecycle.

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

**`/compact`** calls `client.SessionCompact()`, which asks the gateway to summarise older messages in the session context to reduce token usage. The local message list is not modified; a full history refresh runs after the RPC completes.

**`/reset`** calls `client.SessionDelete()` to permanently remove the session, then immediately creates a replacement via `client.CreateSession()`. The new session key is returned as `sessionClearedMsg{newSessionKey}` and the chat model reinitialises with an empty history.

## Message queueing

While `m.sending == true` (a response is in flight), new user input is appended to `m.pendingMessages []string` rather than sent immediately. This prevents messages from being dropped when the user types quickly.

After each response (`drainQueue()` in `chat.go`):

1. If there are pending messages, the first one is dequeued and sent as if the user typed it fresh (including command detection, skill prepend, etc.).
2. History is refreshed once the queue is fully drained.

Local (`!`) and remote (`!!`) exec results also trigger queue draining. See [shell-execution.md](shell-execution.md) for the exec flow.
