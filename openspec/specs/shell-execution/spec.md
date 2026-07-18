# Shell Execution Specification

## Purpose

Lucinate supports running shell commands directly from the chat input using a prefix
convention: `!<command>` runs on the local machine (the user's shell), while `!!<command>`
runs on the gateway host (remote). Both prefixes are handled in `chat.go`'s `Update()` before
messages are sent to the gateway. This spec covers local execution, remote two-phase-approval
execution, and how command execution overlaps with in-flight chat messages.

## Requirements

### Requirement: Shell command prefix convention

The system SHALL let users run shell commands from the chat input using a prefix convention,
distinguishing local from remote execution by the prefix:

| Prefix | Where it runs |
|---|---|
| `!<command>` | Local machine (user's shell) |
| `!!<command>` | Gateway host (remote) |

Both prefixes SHALL be handled in `chat.go`'s `Update()` before messages are sent to the
gateway.

#### Scenario: Single-bang input runs locally

- **WHEN** the chat input starts with `!` but not `!!`
- **THEN** the command runs on the local machine in the user's shell

#### Scenario: Double-bang input runs remotely

- **WHEN** the chat input starts with `!!`
- **THEN** the command runs on the gateway host

### Requirement: Local execution (`!`)

Local execution SHALL be detected when the input starts with `!` but not `!!`.
`localExecCommand()` in `commands.go` SHALL spawn `exec.Command("sh", "-c", command)` and
capture combined stdout and stderr. The result SHALL be returned as
`localExecFinishedMsg{output, exitCode, err}` and displayed as a system message. Local
execution SHALL have no gateway involvement and SHALL require no approval.

#### Scenario: Local command runs in the user's shell

- **GIVEN** input that starts with `!` but not `!!`
- **WHEN** the command is executed
- **THEN** `localExecCommand()` spawns `exec.Command("sh", "-c", command)`, captures combined stdout and stderr, and returns `localExecFinishedMsg{output, exitCode, err}`
- **AND** the result is displayed as a system message
- **AND** there is no gateway involvement and no approval is required

### Requirement: Remote execution (`!!`) with two-phase approval

Remote execution SHALL be detected when the input starts with `!!`. The stripped command SHALL
be passed to `execCommand()` in `commands.go`, which implements a two-phase approval flow:

1. **Submit** — `client.ExecRequest(ctx, command, sessionKey)` is called. The gateway returns
   immediately with an `ExecApprovalRequestResult` containing `{ID, Status, Decision}`.
2. **Approve** — If `Decision` is empty (not yet resolved by gateway policy), the client
   auto-approves via `client.ExecResolve(ctx, id, "allow-once")`. If the approval was already
   resolved (the gateway's own exec policy accepted it), the `"unknown or expired"` error is
   silently ignored.
3. **Result** — Output arrives asynchronously via an `exec.finished` gateway event, processed
   in `handleEvent()` in `events.go`. The system message "running on gateway..." is replaced
   with the output or an error.

If the gateway denies the request (`Decision == "deny"`), an error SHALL be shown immediately
without waiting for the event.

#### Scenario: Submit and auto-approve an unresolved request

- **GIVEN** input that starts with `!!`
- **WHEN** the stripped command is passed to `execCommand()` and `client.ExecRequest(ctx, command, sessionKey)` returns an `ExecApprovalRequestResult` with an empty `Decision`
- **THEN** the client auto-approves via `client.ExecResolve(ctx, id, "allow-once")`

#### Scenario: Approval already resolved by gateway policy

- **GIVEN** the gateway's own exec policy has already resolved the approval
- **WHEN** the client attempts to auto-approve
- **THEN** the `"unknown or expired"` error is silently ignored

#### Scenario: Result arrives asynchronously

- **WHEN** the `exec.finished` gateway event arrives and is processed in `handleEvent()` in `events.go`
- **THEN** the system message "running on gateway..." is replaced with the output or an error

#### Scenario: Gateway denies the request

- **GIVEN** the gateway returns `Decision == "deny"`
- **WHEN** the request is handled
- **THEN** an error is shown immediately without waiting for the `exec.finished` event

### Requirement: Message queueing during execution

Both local and remote execution MAY overlap with in-flight chat messages. New user input while
`m.sending == true` SHALL be held in `m.pendingMessages` and drained after the current exchange
completes. See the `sessions` spec (message queueing) for details.

#### Scenario: Input held while a send is in flight

- **GIVEN** `m.sending == true`
- **WHEN** new user input arrives
- **THEN** it is held in `m.pendingMessages`
- **AND** drained after the current exchange completes
