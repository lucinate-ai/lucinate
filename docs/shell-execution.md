# Shell execution

Repclaw supports running shell commands directly from the chat input using a prefix convention:

| Prefix | Where it runs |
|---|---|
| `!<command>` | Local machine (user's shell) |
| `!!<command>` | Gateway host (remote) |

Both are handled in `chat.go`'s `Update()` before messages are sent to the gateway.

## Local execution (`!`)

Detected when the input starts with `!` but not `!!`. `localExecCommand()` in `commands.go` spawns `exec.Command("sh", "-c", command)` and captures combined stdout and stderr. The result is returned as `localExecFinishedMsg{output, exitCode, err}` and displayed as a system message. No gateway involvement; no approval required.

## Remote execution (`!!`)

Detected when the input starts with `!!`. The stripped command is passed to `execCommand()` in `commands.go`, which implements a two-phase approval flow:

1. **Submit** — `client.ExecRequest(ctx, command, sessionKey)` is called. The gateway returns immediately with an `ExecApprovalRequestResult` containing `{ID, Status, Decision}`.
2. **Approve** — If `Decision` is empty (not yet resolved by gateway policy), the client auto-approves via `client.ExecResolve(ctx, id, "allow-once")`. If the approval was already resolved (the gateway's own exec policy accepted it), the `"unknown or expired"` error is silently ignored.
3. **Result** — Output arrives asynchronously via an `exec.finished` gateway event, processed in `handleEvent()` in `events.go`. The system message "running on gateway..." is replaced with the output or an error.

If the gateway denies the request (`Decision == "deny"`), an error is shown immediately without waiting for the event.

## Message queueing during execution

Both local and remote execution can overlap with in-flight chat messages. New user input while `m.sending == true` is held in `m.pendingMessages` and drained after the current exchange completes. See [sessions.md](sessions.md#message-queueing) for details.
