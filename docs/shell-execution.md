# Shell execution — lessons and rationale

The behavioural contract for shell execution lives in
[`openspec/specs/shell-execution/spec.md`](../openspec/specs/shell-execution/spec.md) — the
prefix convention, local execution, remote two-phase-approval execution, and message queueing
during execution are all captured there as requirements and scenarios. This file keeps the
hard-won lessons, pitfalls, and design rationale behind that flow.

## Two-phase approval for remote exec is not one call

Remote `!!` execution deliberately splits into submit then approve rather than a single request.
`client.ExecRequest` only lodges the request; the gateway may resolve it under its own exec
policy or leave it open for the client to decide. The client auto-approves an unresolved request
with `"allow-once"` — but the race here is the whole point of the design, and it bites two ways:

- If the gateway's own policy has *already* resolved the approval, the follow-up `ExecResolve`
  comes back with `"unknown or expired"`. That error is silently ignored on purpose — it means
  the request already went through, not that anything failed. Treating it as an error would
  reject perfectly good runs.
- If the gateway denies (`Decision == "deny"`), the error is shown immediately rather than
  waiting for the asynchronous `exec.finished` event — because no event is coming for a request
  that never ran.

## "running on gateway..." is a placeholder, not the result

Remote output arrives asynchronously via the `exec.finished` event, well after the submit call
returns. The "running on gateway..." system message is a placeholder that `handleEvent()` later
replaces with the real output or error. If you change how system messages are keyed, keep this
replacement working — otherwise you get a stuck "running on gateway..." line that never resolves.

## Local `!` has no gateway approval

Local execution runs straight through `sh -c` with no gateway involvement and no approval step.
This is intentional — `!` is the user's own shell on their own machine — but it means the
security model for `!` and `!!` is genuinely different: only remote exec passes through gateway
policy. Don't assume approval semantics carry over from one to the other.

## Message queueing overlaps with exec

Both local and remote execution can overlap with in-flight chat messages; new input while
`m.sending == true` is held in `m.pendingMessages` and drained afterwards. The rationale and
details live in the `sessions` spec (message queueing).
