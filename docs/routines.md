# Routines — lessons and rationale

The behavioural contract for routines lives in
[`openspec/specs/routines/spec.md`](../openspec/specs/routines/spec.md) — the STEPS.md file
format, disk storage, the controller lifecycle, the auto-advance hook, stale-event filtering,
directives, logging, the manager view, slash-command gating, notifications, the status row, and
key bindings are all captured there as requirements and scenarios. This file keeps the hard-won
lessons, pitfalls, and design rationale behind that flow: the "why it works this odd way" that
the spec's requirements don't dwell on.

## Auto-advance hook ordering in the final event

Auto-advance lives in the `final` case of `handleEvent` (`internal/tui/events.go`), and the
order of operations is load-bearing. The sequence is: finalise the streaming message, capture
the merge boundary via `bumpGen()`, then — while a routine is active — log the assistant content
and run `applyDirectives` *before* any auto-advance fires, so a `/routine:stop` or
`/routine:pause` is honoured before the next step could be dispatched. Only after the
unconditional `refreshHistoryAt(boundary)` and queue drain does `maybeAdvanceRoutine()` get its
chance.

The reason the boundary is captured before the routine work: the just-finalised turn is now on
the history-side of any refresh issued from here on, and subsequent appends (the next step's
placeholder, system rows) get the new gen and survive the merge. The merge in the
`historyRefreshMsg` handler is non-destructive precisely because those live-tail rows carry a
higher gen than the boundary.

The queue is drained via `drainQueueSkipRefresh` rather than plain `drainQueue`: user-typed
messages jump ahead of the routine, and the `SkipRefresh` variant avoids duplicating the resync
we already queued above — `drainQueue`'s built-in empty-queue refresh would otherwise fire it
twice.

`error` and `aborted` also `bumpGen()` so the boundary stays monotonic, and set `paused = true`
instead of advancing — a transient gateway error must not loop the next step at the user. The
user can press Enter (empty input) to retry or Esc to end. Those cases don't issue their own
refresh; the next successful turn's refresh covers the canonical reconciliation.

## The Pre-Layer-3 drift, and why every final now reconciles

The unconditional refresh is the heart of the resync architecture. Pre-Layer-3, the refresh was
deferred to "queue empty AND no routine to advance", which meant a 10-step auto-mode routine
accumulated drift across all 10 steps before the first server-canonical reconciliation. With
drift large enough, stale-event filtering became the only line of defence against spurious step
submission. Now every `final` reconciles, every step — the drift never gets a chance to build.

## Stale-event filtering by finalised run: the back-to-back race

The OpenClaw gateway has been observed emitting a duplicate `delta` event with the full content
right *after* the matching `final`, on the same `runID`. Without filtering, that delta lands on
the next routine step's freshly-appended placeholder, flipping `awaitingDelta` and letting a
subsequent empty-content `final` falsely finalise an empty turn — which spuriously auto-advances
the routine.

The guard is `chatModel.finalisedRuns`, a bounded LRU set of run IDs already finalised; the top
of the chat-event branch in `handleEvent` drops any event whose `RunID` is a member:

```go
if m.finalisedRuns.contains(chatEv.RunID) {
    slog.Debug("stale event ignored for finalised run", "run_id", chatEv.RunID)
    return nil
}
```

The set's depth is the subtle part. A single-deep filter is enough for the immediate
duplicate-after-final case, but back-to-back routine steps open a wider window: a stale event
for run N-2 can arrive while run N is streaming, and with only the most-recent run remembered
that earlier id slips past and corrupts the live placeholder. Hence the cap of 32
(`finalisedRunsCap`) with FIFO eviction — deep enough to cover overlapping runs, bounded so a
long-lived chat can't grow the filter unboundedly. The set is added to only when the
corresponding state mutation actually happened (gated on the same `finalised` flag), so we never
remember a run we didn't really finalise.

## Directive regex: anchored, per-line, deliberately strict

Directives are matched with `internal/routines/directives.go`:

```go
^\s*/routine:(stop|pause|continue|mode\s+(auto|manual))\s*$
```

Anchored with `^...$` and applied per line, so inline mentions (`as in /routine:stop`,
backtick-wrapped tokens) deliberately do *not* match — the assistant can talk *about* a directive
without accidentally firing it. Directives are kept verbatim in the rendered transcript;
`applyDirectives` doesn't rewrite the assistant message. And only assistant replies are scanned —
user-typed `/routine:*` lines in the chat input are never parsed, so a user can't drive the
controller by pasting a directive into the input.

## Fixed 4-line step textareas

Step textareas in the form are sized at a fixed 4 lines each rather than auto-fitting the
available height. The previous "divide remaining space across all steps" heuristic squeezed every
textarea down to a 1-line stub once step count grew, and the user couldn't see the content of any
step. The scrollable viewport handles overflow now, so a fixed per-step size is friendlier to
type into. The related `ensureFocusVisible`/`fieldLineStarts` scrolling exists for the same
reason: without it, a routine with many steps used to push the help line off the terminal
entirely.

## Collision avoidance on duplicate, and the name predicates

Routines are name-keyed — the directory under `~/.lucinate/routines/<name>/` *is* the identity,
unlike cron jobs which carry a separate ID. So the duplicate flow can't just reuse a name: it
builds `"Copy of " + name`, walking `(2)`, `(3)`, … to find the first slot that doesn't collide
with an existing routine. Duplicate opens in create mode (`editingID` stays empty) so submission
goes through the plain `routines.Save` path — no rename, no overwrite, no delete-of-original.

The name validation is deliberately split into two predicates. `validName` (path safety only —
rejects empty, `.`, `..`, leading-dot, and anything with `/`, `\`, or NUL) is what `Load` and
`Delete` use, so existing on-disk directories — including any non-kebab names left over from
earlier versions — keep working. `IsValidKebab` is the *form* rule, tighter (lowercase ASCII,
digits, hyphens; no leading/trailing/consecutive hyphens), and only `Save` enforces it — so
anything newly saved or renamed lands on disk with a typeable identifier, while legacy directories
aren't retroactively locked out. `ToKebab` produces the best-effort suggestion the form offers
when the user types something like `My Routine!`.

## Why the controller lifecycle is shaped as it is

Step indexing is strictly monotonic: only `sendNextRoutineStep` increments `ar.sent`, once per
call, and auto-advance is gated solely on `ar.sent < len(Steps)` plus the directive/pause flags.
There is deliberately no path that decrements or skips — the invariant is what makes "spurious
auto-advance" a filtering problem (see stale-event filtering above) rather than an indexing one.

Completion is checked *before* mode: when the answered step was the last,
`maybeAdvanceRoutine()` calls `endRoutine("completed")` regardless of mode and returns nil, so
manual and auto routines both end with the same notification and cleared `activeRoutine` on their
final reply. Mode only decides what happens *mid*-routine.

## Navigation gating: don't strand or leak the controller

Only one routine runs at a time per session, and starting a second — or navigating away — routes
through `gateNavigation()` when a routine is already active. The gate covers more than the obvious
`/routine`/`/routines` case: `/agents`, `/agent <name>`, `/sessions`, `/crons`, `/crons all`, and
`/connections` all strand or replace the chat model, and the active routine controller can't
survive a chat-view reset. Silently dropping it would leak the open log file — which is the real
reason the gate exists rather than just a courtesy prompt. On `y` the gate cancels any in-flight
turn (`cancelTurn`) and ends the routine (`endRoutine`) before dispatching. `startRoutine` keeps a
defensive `if m.activeRoutine != nil` guard, but in normal flow the gate runs first.

## Notifications live outside the message list, on purpose

Routine state changes and errors are ephemeral notifications, not chat rows (see the `chat-ux`
spec, Notifications). They live outside `m.messages` specifically so a `historyRefreshMsg` — which
the routine machinery fires on every `final` — doesn't wipe them. They clear when the user submits
any non-empty input. The controller calls `m.notify` / `m.notifyError` directly; the legacy
`appendSystemError` helper still exists but routes to `notifyError` under the hood for older
callers.

## Small deliberate deviations

- `x` deletes on the detail view, not `d` — `d` is duplicate on the list, and keeping the two
  verbs distinct in the user's vocabulary (matching the cron browser) is worth the minor
  departure from a "d for delete" habit.
- `Alt+S` is kept as a save fallback alongside the surfaced `Ctrl+S`, because some terminals
  intercept Ctrl+S as XOFF and would otherwise swallow the save.
- `deleteStep` re-inserts a blank textarea when the slice would empty out, so the form always has
  at least one step to type into rather than presenting an empty, un-focusable body.
- `Frontmatter.Log` is copied verbatim on duplicate rather than cleared: a shared relative log
  path is a hazard, but blanking it silently would be surprising, so we leave it visible in the
  form for the user to change before saving.
