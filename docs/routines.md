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

`validName` rejects empty, `.`, `..`, leading-dot, and any name containing `/`, `\\`, or NUL — the same conservative shape used elsewhere for filesystem-derived identifiers.

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
| `maybeAdvanceRoutine()` | Called from the chat `final` event handler. Returns `nil` unless the routine is active, mode is auto, not paused, and a step remains. Otherwise returns `sendNextRoutineStep`. When all steps have been sent, calls `endRoutine("completed")` and returns nil. |
| `applyDirectives(reply)` | Scans the assistant reply for `/routine:` directives and applies them in order |
| `endRoutine(reason)` | Closes the logger, clears `activeRoutine`, posts a "Routine X <reason>" notification |
| `cycleRoutineMode()` | Bound to `Alt+M` — flips between auto and manual; entering auto unsets `paused` |

Step indexing is strictly monotonic: only `sendNextRoutineStep` increments `ar.sent`, and it does so once per call. Auto-advance is gated solely on `ar.sent < len(Steps)` and the directive/pause flags — there is no path that decrements or skips.

## Auto-advance hook

Auto-advance lives in the `final` case of `handleEvent` (`internal/tui/events.go`). The order matters:

1. Mark the streaming assistant message as finalised (existing behaviour).
2. If `m.activeRoutine != nil`: log the assistant content (when a logger is configured) and call `applyDirectives` so a `/routine:stop` or `/routine:pause` is honoured before any auto-advance fires.
3. Drain `m.pendingMessages` — user-typed queue jumps ahead of the routine.
4. If the queue is empty and `m.sending` is now false, call `maybeAdvanceRoutine()`. If it returns a cmd, dispatch it (sending the next step) and return.
5. Otherwise fall through to the standard `refreshHistory` + `loadStats` batch.

`error` and `aborted` set `paused = true` instead of advancing, so a transient gateway error doesn't loop the next step. The user can press Enter (empty input) to retry the next step or Esc to end the routine.

## Stale-event filtering

The OpenClaw gateway has been observed emitting a duplicate `delta` event with the full content right *after* the matching `final`, on the same `runID`. Without filtering, that delta lands on the next routine step's freshly-appended placeholder, flipping `awaitingDelta` and letting a subsequent empty-content `final` falsely finalise an empty turn — which spuriously auto-advances the routine.

`chatModel.prevFinalisedRunID` tracks the last finalised run; the top of the chat-event branch in `handleEvent` drops any event whose `RunID` matches it:

```go
if chatEv.RunID != "" && chatEv.RunID == m.prevFinalisedRunID {
    logEvent("  STALE event for finalised run %s — ignored", chatEv.RunID)
    return nil
}
```

`prevFinalisedRunID` is set inside the `final`, `error`, and `aborted` paths, but only when the corresponding state mutation actually happened (gated on the same `finalised` flag). `TestHandleEvent_StaleDeltaAfterFinalIgnored` covers the bug end-to-end.

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
| `routinesSubForm` | Create / edit form |
| `routinesSubConfirmDelete` | y/n prompt before `routines.Delete` fires |

The form has three textinputs (name, mode, log) plus a slice of `textarea.Model` for the steps — one per step. The focus index is a single int: `0..2` are the header fields, `3+i` is step `i`. Key bindings inside the form:

| Key | Action |
|---|---|
| Tab / Shift+Tab | Cycle focus |
| Alt+S (or Ctrl+S) | Save — Alt+S is the primary because Ctrl+S is XOFF on most terminals |
| Alt+Up | Insert blank step above the focused step |
| Alt+Down | Insert blank step below the focused step |
| Alt+Delete (or Alt+Backspace) | Remove the focused step (y/n confirm) |
| Esc | Cancel without saving |
| Alt+Enter | Newline within a step textarea |

`insertStep(idx, value)` uses an overlap-safe `copy` (`memmove`) so shift-right insertion never duplicates content. `deleteStep(idx)` re-inserts a blank textarea when the slice would otherwise empty out, so the form always has at least one step to type into.

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

When `m.activeRoutine != nil`, the chat View renders a single styled row immediately above the input box:

```
routine: demo — AUTO — sent: 5/10 — next: <40-char preview>
```

`AUTO`/`MANUAL` reflects the mode; `(paused)` is appended when `paused` is set; `next:` previews the next step body, single-lined and ellipsised. The renderer is `routineStatusLine` + `routineStatusStyle` in `routines_chat.go`. `applyLayout()` (`completion.go`) subtracts one row from the viewport height when a routine is active so the status row doesn't push the input off-screen.

## Key bindings

| Key | Behaviour |
|---|---|
| Alt+M | Cycle mode (auto ↔ manual). No-op when no routine is active. Alt is used because every Ctrl+letter has a readline binding (Ctrl+R reverse-search, Ctrl+S XOFF, etc.). |
| Esc | When a routine is active: end the routine and (if streaming) cancel the in-flight turn. Otherwise behaves as before (`/cancel`-equivalent or transcript back). |
| Enter (empty input) | When a routine is active and idle (manual or paused): send the next step. Otherwise no-op. |

## Verification

Unit tests:

- `internal/routines/parse_test.go` — frontmatter parsing, default mode, blank-line preservation, format round-trip.
- `internal/routines/directives_test.go` — own-line matching, inline-mention rejection, all five directive kinds.
- `internal/routines/store_test.go` — disk round-trip, invalid-name rejection.
- `internal/routines/log_test.go` — header + per-message timestamp shape, multi-line bodies, append across reopens, nil-receiver safety.
- `internal/tui/events_test.go::TestHandleEvent_StaleDeltaAfterFinalIgnored` — pins the duplicate-delta-after-final filter.
- `internal/tui/notifications_test.go` — notify/clear and history-refresh persistence.

Manual smoke:

1. Drop a routine at `~/.lucinate/routines/demo/STEPS.md` with `mode: manual`. `/routine demo` dispatches step 0; status row reads `MANUAL — sent: 1/N — next: …`.
2. Press Enter on an empty input — step 1 dispatches.
3. Alt+M flips status to `AUTO`. Subsequent step finals auto-advance.
4. Have step N's reply emit `/routine:stop` on its own line — routine ends; "Routine 'demo' stopped by assistant." notification appears above the input and survives the post-turn `refreshHistory`.
5. Repeat with `log: ./routine.log` set — verify a run header and ISO-timestamped `user:` / `assistant:` lines.
6. Activate a routine, run `/agents` — confirm the gate prompt; `n` keeps the routine running, `y` ends it cleanly and returns to the picker.
