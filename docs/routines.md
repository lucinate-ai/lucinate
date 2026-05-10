# Routines

Routines are ordered prompt sequences stored on disk and replayed against the active session via `/routine <name>`. Each step is a complete user message; the controller dispatches them one at a time, optionally auto-advancing after each assistant reply. Routines are a chat-only concept — there is no gateway counterpart, and every backend works with them.

The user-facing surface is `/routine <name>` (activate) and `/routines` (manage). Routine state lives entirely in `chatModel`; on disk, each routine is a directory under `~/.lucinate/routines/` containing a single `STEPS.md` file. The implementation splits across `internal/routines/` (file format + storage) and `internal/tui/` (controller + manager view).

## STEPS.md format

Plain markdown. Optional YAML frontmatter delimited by `---` lines carries routine metadata. The body is split into steps on lines containing exactly `---`.

```markdown
---
name: demo
mode: auto              # auto | manual; default = manual
log: ./demo.log         # absolute or relative-to-cwd; omit to disable logging
---

generate two integers between 1 and 10

---

if the sum is greater than 10 say /routine:stop, otherwise /routine:continue

---

say "the sum is less than or equal to 10"
```

Parser rules (`internal/routines/parse.go`):

- If the first non-blank line is `---`, consume YAML frontmatter until the next `---` line. Anything else is treated as body with no frontmatter.
- The body is split on lines whose `strings.TrimRight(line, " \t\r")` equals `---`.
- Each chunk is `strings.TrimSpace`'d. Empty chunks are dropped, so consecutive `---` lines collapse harmlessly.
- The on-disk directory name is the routine's identity (`Routine.Name`); `frontmatter.name` is informational.

`Format(r Routine)` is the round-trip — frontmatter is emitted only when at least one field is non-empty, and steps are joined with `\n---\n\n`. `TestFormatRoundTrip` pins the parse→format→parse invariant.

## Disk layout

Resolved through `config.DataDir()`:

```
~/.lucinate/routines/
  <name>/
    STEPS.md
```

`internal/routines/store.go` exposes:

| Function | Behaviour |
|---|---|
| `Dir()` | `<data-dir>/routines` (not auto-created) |
| `List()` | Scan + parse every subdirectory; entries that fail to parse are silently skipped so one bad file doesn't sink the listing |
| `Load(name)` | Returns `Routine{}` + `ErrNotFound` if the directory is missing |
| `Save(r)` | `MkdirAll(0o700)` + atomic `WriteFile`/`Rename` of `STEPS.md` |
| `Delete(name)` | `os.RemoveAll(<dir>/<name>)` |

Two name predicates live in `internal/routines/`:

- `validName` (path safety) rejects empty, `.`, `..`, leading-dot, and any name containing `/`, `\\`, or NUL. `Load` and `Delete` use this predicate so existing on-disk directories — including any non-kebab names left over from earlier versions — keep working.
- `IsValidKebab` (form rule) tightens to lowercase ASCII letters, digits, and hyphens; no leading or trailing hyphen, no consecutive hyphens. `Save` enforces it, so any new save or rename lands on disk with a typeable, lowercase identifier. The companion `ToKebab(s string) string` produces a best-effort kebab version of arbitrary input — used by the form's submit error to suggest a fix when the user typed `My Routine!` or similar.

## Controller

The active routine lives on `chatModel.activeRoutine` (`internal/tui/routines_chat.go`):

```go
type activeRoutine struct {
    routine routines.Routine
    mode    routines.Mode
    sent    int                 // count of steps already dispatched
    paused  bool
    logger  *routines.Logger    // nil if no `log:` configured
}
```

Lifecycle entry points on `*chatModel`:

| Method | Purpose |
|---|---|
| `startRoutine(name)` | Load + parse from disk, open the log if configured, append the "Routine started" notification, return the `tea.Cmd` for step 0's send |
| `sendNextRoutineStep()` | Read `Steps[ar.sent]`, increment `ar.sent`, append the user message + assistant placeholder to `m.messages`, set `m.sending=true`, log the user line, return `sendMessage(...)` |
| `maybeAdvanceRoutine()` | Called from the chat `final` event handler. Completion fires first: when the answered step was the last, calls `endRoutine("completed")` regardless of mode and returns nil so the user always gets the same notification + cleared `activeRoutine`. Otherwise returns `sendNextRoutineStep` only when mode is auto and not paused; in manual or paused mid-routine it returns nil and the user drives the next step. |
| `applyDirectives(reply)` | Scans the assistant reply for `/routine:` directives and applies them in order |
| `endRoutine(reason)` | Closes the logger, clears `activeRoutine`, posts a "Routine X <reason>" notification |
| `cycleRoutineMode()` | Bound to `Shift+Tab` (mirrors Claude Code's mode-cycle gesture) — flips between auto and manual; entering auto unsets `paused`. Yields to slash-menu cycling when the completion menu is active. |

Step indexing is strictly monotonic: only `sendNextRoutineStep` increments `ar.sent`, and it does so once per call. Auto-advance is gated solely on `ar.sent < len(Steps)` and the directive/pause flags — there is no path that decrements or skips.

## Auto-advance hook

Auto-advance lives in the `final` case of `handleEvent` (`internal/tui/events.go`). The order matters:

1. Mark the streaming assistant message as finalised (existing behaviour).
2. Capture the merge boundary via `bumpGen()` — the just-finalised turn is now on the history-side of any refresh issued from here on; subsequent appends get the new gen and survive the merge.
3. If `m.activeRoutine != nil`: log the assistant content (when a logger is configured) and call `applyDirectives` so a `/routine:stop` or `/routine:pause` is honoured before any auto-advance fires.
4. Always queue `refreshHistoryAt(boundary)` and `loadStats()`. The merge in the `historyRefreshMsg` handler is non-destructive — the live tail of the next routine step (placeholder, in-flight tool card, system rows) survives because those rows carry a higher gen than the boundary.
5. Drain `m.pendingMessages` via `drainQueueSkipRefresh` — user-typed queue jumps ahead of the routine. The `SkipRefresh` variant is used because we already queued the resync above; `drainQueue`'s built-in empty-queue refresh would otherwise duplicate it.
6. If the queue was empty and `m.sending` is now false, call `maybeAdvanceRoutine()`. If it returns a cmd, append it to the batch (sending the next step).

The unconditional refresh is the heart of the resync architecture. Pre-Layer-3, the refresh was deferred to "queue empty AND no routine to advance", which meant a 10-step auto-mode routine accumulated drift across all 10 steps before the first server-canonical reconciliation. With drift large enough, stale-event filtering became the only line of defence against spurious step submission. Now every `final` reconciles, every step.

`error` and `aborted` also `bumpGen()` so the boundary stays monotonic, and set `paused = true` instead of advancing — so a transient gateway error doesn't loop the next step. The user can press Enter (empty input) to retry the next step or Esc to end the routine. They do not currently issue their own refresh; the next successful turn's refresh covers the canonical reconciliation.

## Stale-event filtering

The OpenClaw gateway has been observed emitting a duplicate `delta` event with the full content right *after* the matching `final`, on the same `runID`. Without filtering, that delta lands on the next routine step's freshly-appended placeholder, flipping `awaitingDelta` and letting a subsequent empty-content `final` falsely finalise an empty turn — which spuriously auto-advances the routine.

`chatModel.finalisedRuns` is a bounded LRU set (cap `finalisedRunsCap = 32`) of run IDs we have already finalised; the top of the chat-event branch in `handleEvent` drops any event whose `RunID` is a member:

```go
if m.finalisedRuns.contains(chatEv.RunID) {
    logEvent("  STALE event for finalised run %s — ignored", chatEv.RunID)
    return nil
}
```

The set is added to inside the `final`, `error`, and `aborted` paths, but only when the corresponding state mutation actually happened (gated on the same `finalised` flag). FIFO eviction keeps a long-lived chat from growing the filter unboundedly.

The set's depth matters: a single-deep filter is enough for the immediate duplicate-after-final case, but back-to-back routine steps open a wider window. A stale event for run N-2 can arrive while run N is streaming; with only the most-recent run remembered, that earlier id slips past and corrupts the live placeholder. `TestHandleEvent_StaleDeltaAfterFinalIgnored` pins the duplicate case; `TestHandleEvent_StaleDeltaFromOlderRunIgnored` pins the back-to-back race; `TestFinalisedRunSet_EvictsOldestPastCap` pins the FIFO bound.

## Directives

The assistant can steer the routine by emitting one of these on its own line (leading whitespace allowed):

| Directive | Effect |
|---|---|
| `/routine:stop` | End the active routine immediately |
| `/routine:pause` | Pause without ending — Enter sends the next step, Esc ends |
| `/routine:continue` | Explicit no-op; also unsets `paused` so it can resume an auto-mode routine |
| `/routine:mode auto` | Switch to auto mode; unsets `paused` |
| `/routine:mode manual` | Switch to manual mode |

Matching (`internal/routines/directives.go`):

```go
^\s*/routine:(stop|pause|continue|mode\s+(auto|manual))\s*$
```

Anchored with `^...$` and applied per line. Inline mentions (`as in /routine:stop`, backtick-wrapped tokens) deliberately do not match. Directives are kept verbatim in the rendered transcript — `applyDirectives` doesn't rewrite the assistant message.

User-typed `/routine:*` lines in the chat input are not parsed. Only assistant replies are scanned.

## Logging

When the frontmatter sets `log: <path>`, `routines.Logger` opens the file (`O_APPEND | O_CREATE | O_WRONLY`, mode `0o600`) at routine start and closes it on `endRoutine`. Relative paths resolve against the lucinate working directory at start time, captured via `os.Getwd()`.

Format:

```
--- routine: demo started 2026-05-09T22:30:00Z ---
[2026-05-09T22:30:00Z] user: <step 1 text>
[2026-05-09T22:30:05Z] assistant: <reply line 1>
<reply line 2 — no per-line prefix>
```

Only the *first* line of a multi-line message gets the `[ts] role:` prefix; subsequent lines are written verbatim so log diffs read like the chat. The logger is best-effort — write errors are silently swallowed so a logging hiccup never breaks the running routine. `Open` returns `nil, nil` for an empty path so callers can invoke it unconditionally.

## Manager view

`/routines` opens `routinesModel` (`internal/tui/routines.go`), modelled on the cron browser. Four substates:

| Substate | Purpose |
|---|---|
| `routinesSubList` | List of routines (name + step count + mode chips) |
| `routinesSubDetail` | Read-only view of a single routine — frontmatter, file path, every step rendered in order |
| `routinesSubForm` | Create / edit / duplicate form |
| `routinesSubConfirmDelete` | y/n prompt before `routines.Delete` fires |

Key bindings follow the project-wide conventions documented in [key-conventions.md](key-conventions.md). The list view exposes `n` (new) and `d` (duplicate, gated on a non-empty list); the detail view exposes `e` (edit) and `x` (delete) — `x` rather than `d` so duplicate and delete stay distinct in the user's vocabulary, matching the cron browser.

The form has three textinputs (name, mode, log) plus a slice of `textarea.Model` for the steps — one per step. The focus index is a single int: `0..2` are the header fields, `3+i` is step `i`. Key bindings inside the form:

| Key | Action |
|---|---|
| Tab / Shift+Tab | Cycle focus |
| Ctrl+S (or Alt+S) | Save — Ctrl+S is the surfaced binding; Alt+S is kept as a fallback for terminals that intercept Ctrl+S as XOFF |
| Alt+Up | Insert blank step above the focused step |
| Alt+Down | Insert blank step below the focused step |
| Alt+Delete (or Alt+Backspace) | Remove the focused step (y/n confirm) |
| Esc | Cancel without saving |
| Alt+Enter | Newline within a step textarea |

`insertStep(idx, value)` uses an overlap-safe `copy` (`memmove`) so shift-right insertion never duplicates content. `deleteStep(idx)` re-inserts a blank textarea when the slice would otherwise empty out, so the form always has at least one step to type into.

### Duplicate

Pressing `d` on the list view opens the form pre-populated from the highlighted routine, in **create mode** — `editingID` stays empty so submission goes through the plain `routines.Save` path with no rename, no overwrite, no delete-of-original. The cloned name is built by `duplicateRoutineName(name, existing)`:

- `"" → ""` (passes through so the form-level "name is required" check fires).
- Otherwise: `"Copy of " + name`, walking `(2)`, `(3)`, … to find the first slot that doesn't collide with an existing routine. Routines are name-keyed (the directory under `~/.lucinate/routines/<name>/` is the identity), so collision avoidance is required — unlike cron jobs which have a separate ID.

`Frontmatter.Name` is set to the duplicated name so the `STEPS.md` metadata stays in sync with the directory identity. `Frontmatter.Log` is copied verbatim — if it's a relative path the user can change it in the form before saving so the duplicate doesn't share the original's log file.

### Form layout

The form's middle section (header inputs + step textareas) renders inside a scrollable `viewport.Model`; the title and the footer (error line + help line) stay pinned. `viewForm` records the line index where each focusable field starts as it builds the body content (`fieldLineStarts`), and `ensureFocusVisible` adjusts the viewport's `YOffset` so the focused field is fully on-screen — scrolling down when the user tabs past the bottom of the visible window, scrolling up when they shift-tab back. Without this, a routine with many steps used to push the help line off the terminal entirely.

Step textareas are sized at a fixed 4 lines each rather than auto-fitting the available height: the previous "divide remaining space across all steps" heuristic squeezed every textarea down to a 1-line stub once step count grew, and the user couldn't see the content of any step. The viewport handles overflow now, so a fixed per-step size is friendlier to type into.

### Submission

Submission iterates `form.steps` in order, dropping blank ones, and goes through `routines.Save`. Editing with a renamed `name` writes the new directory and `Delete`s the old one. After save (or delete), the model emits `routinesChangedMsg` so the chat view refreshes its `m.routineNames` cache for `/routine <TAB>` completion.

## Slash commands and gating

Two entries in `slashCommands`:

| Command | Behaviour |
|---|---|
| `/routine <name>` | Activate the named routine. Bare `/routine` is an error pointing at `/routines`. Tab completion uses `m.routineNames` populated by `loadRoutineNames()` at chat init and after any manager-view CRUD. |
| `/routines` | Open the manager via `showRoutinesMsg{}` |

Only one routine can run at a time per session. Both commands route through `gateNavigation()` (`routines_chat.go`) when a routine is already active:

```
Routine "demo" is active. Starting routine "other" will cancel it. Continue? (y/n)
```

The same gate covers the navigations that strand or replace the chat model — `/agents`, `/agent <name>`, `/sessions`, `/crons`, `/crons all`, `/connections` — for the same reason: the active routine controller can't survive a chat-view reset, and silently dropping it would leak the open log file. On `y`, the gate cancels any in-flight turn (`cancelTurn`) and ends the routine (`endRoutine`) before dispatching the navigation. On `n` or Esc the prompt clears and the routine continues.

`startRoutine` itself still has a defensive `if m.activeRoutine != nil` guard, but in normal flow the gate runs first.

## Notifications

Routine state changes (started, paused, ended) and routine errors are surfaced as ephemeral notifications, not chat rows — see [chat-ux.md → Notifications](chat-ux.md#notifications). They live outside `m.messages` so a `historyRefreshMsg` doesn't wipe them, and they clear when the user submits any non-empty input. The routine controller calls `m.notify` / `m.notifyError` directly; the legacy `appendSystemError` helper still exists but routes to `notifyError` under the hood for older callers.

## Status row

When `m.activeRoutine != nil`, the chat View renders a single styled row immediately above the input box. While auto-advancing or mid-turn the trailing segment is a passive preview:

```
routine: demo — AUTO — sent: 5/10 — next: <preview>
```

When the routine is awaiting user input — manual mode, or auto with `paused` set, with no turn in flight and steps remaining — the trailing segment switches to a call-to-action so the user sees both what the next message is and that the routine is parked on them:

```
routine: demo — MANUAL — sent: 5/10 — ▶ Press Enter to send: <preview>
```

`AUTO`/`MANUAL` reflects the mode; `(paused)` is appended when `paused` is set. The row is coloured by mode — amber (`execClr`) for auto, cyan (`userClr`) for manual — so the driver is legible at a glance without the row competing with the purple chat header. The renderer is `routineStatusLine` + `routineStatusStyle` in `routines_chat.go`; the preview length is computed from `m.width` so it grows with the terminal (floor 20 chars, fallback 40 when width is not yet known). `applyLayout()` (`completion.go`) subtracts one row from the viewport height when a routine is active so the status row doesn't push the input off-screen.

## Key bindings

| Key | Behaviour |
|---|---|
| Shift+Tab | Cycle mode (auto ↔ manual). No-op when no routine is active. Mirrors Claude Code's mode-cycle gesture. Yields to slash-menu cycling when the completion menu is active. |
| Esc | When a routine is active: end the routine and (if streaming) cancel the in-flight turn. Otherwise behaves as before (`/cancel`-equivalent or transcript back). |
| Enter (empty input) | When a routine is active and idle (manual or paused): send the next step. Otherwise no-op. |

## Verification

Unit tests:

- `internal/routines/parse_test.go` — frontmatter parsing, default mode, blank-line preservation, format round-trip.
- `internal/routines/directives_test.go` — own-line matching, inline-mention rejection, all five directive kinds.
- `internal/routines/store_test.go` — disk round-trip, invalid-name rejection.
- `internal/routines/log_test.go` — header + per-message timestamp shape, multi-line bodies, append across reopens, nil-receiver safety.
- `internal/tui/events_test.go::TestHandleEvent_StaleDeltaAfterFinalIgnored` / `TestHandleEvent_StaleDeltaFromOlderRunIgnored` / `TestFinalisedRunSet_*` — pin the bounded stale-run filter.
- `internal/tui/events_test.go::TestHandleEvent_FinalRefreshesEvenWithQueuedMessages` / `TestHandleEvent_FinalRefreshesDuringRoutineAutoAdvance` — pin that the resync fires on every successful `final`, not just at queue/routine end.
- `internal/tui/events_test.go::TestHandleEvent_FinalBumpsGen` / `TestHandleEvent_FinalEmptyAckDoesNotBumpGen` — pin the gen-bump semantics that anchor the merge boundary.
- `internal/tui/events_test.go::TestMergeHistoryRefresh_PreservesLiveTail` / `TestMergeHistoryRefresh_NoLiveTail` — pin the merge contract the unconditional refresh depends on.
- `internal/tui/notifications_test.go` — notify/clear and history-refresh persistence.
- `internal/tui/routines_test.go::TestRoutinesDuplicate_*` / `TestDuplicateRoutineName_*` / `TestRoutinesDetailKey_X_TriggersDelete` / `TestRoutinesDetailKey_D_NoLongerDeletes` — pin the duplicate flow, the collision-suffix algorithm, and the `d` → `x` delete remap.
- `internal/tui/routines_chat_test.go::TestMaybeAdvanceRoutine_ManualCompletion` / `TestMaybeAdvanceRoutine_ManualMidStepIsNoOp` — pin that manual routines complete on their final reply and stay quiet between steps.

Manual smoke:

1. Drop a routine at `~/.lucinate/routines/demo/STEPS.md` with `mode: manual`. `/routine demo` dispatches step 0; status row reads `MANUAL — sent: 1/N — ▶ Press Enter to send: …`.
2. Press Enter on an empty input — step 1 dispatches.
3. Step through to the end. After the final step's reply, a `Routine "demo" completed.` notification appears, the status row disappears, and the input returns to plain chat.
4. Shift+Tab flips status to `AUTO`. Subsequent step finals auto-advance.
5. Have step N's reply emit `/routine:stop` on its own line — routine ends; "Routine 'demo' stopped by assistant." notification appears above the input and survives the post-turn `refreshHistory`.
6. Repeat with `log: ./routine.log` set — verify a run header and ISO-timestamped `user:` / `assistant:` lines.
7. Activate a routine, run `/agents` — confirm the gate prompt; `n` keeps the routine running, `y` ends it cleanly and returns to the picker.
