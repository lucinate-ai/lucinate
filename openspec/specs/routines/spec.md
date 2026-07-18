# Routines Specification

## Purpose

Routines are ordered prompt sequences stored on disk and replayed against the active session via `/routine <name>`. Each step is a complete user message; the controller dispatches them one at a time, optionally auto-advancing after each assistant reply. Routines are a chat-only concept — there is no gateway counterpart, and every backend works with them. The user-facing surface is `/routine <name>` (activate) and `/routines` (manage). This spec covers the STEPS.md file format, disk storage, the routine controller lifecycle, the auto-advance hook, stale-event filtering, assistant directives, logging, the manager view, slash-command gating, notifications, the status row, and key bindings.

## Requirements

### Requirement: STEPS.md file format

Each routine SHALL be stored as a single `STEPS.md` file in plain markdown. Optional YAML frontmatter delimited by `---` lines carries routine metadata; the body is split into steps on lines containing exactly `---`. The recognised frontmatter fields are `name` (informational), `mode` (`auto` | `manual`; default `manual`), and `log` (absolute or relative-to-cwd path; omit to disable logging). A representative file:

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

The parser (`internal/routines/parse.go`) SHALL apply these rules:

- If the first non-blank line is `---`, consume YAML frontmatter until the next `---` line. Anything else is treated as body with no frontmatter.
- The body is split on lines whose `strings.TrimRight(line, " \t\r")` equals `---`.
- Each chunk is `strings.TrimSpace`'d. Empty chunks are dropped, so consecutive `---` lines collapse harmlessly.
- The on-disk directory name is the routine's identity (`Routine.Name`); `frontmatter.name` is informational.

`Format(r Routine)` is the round-trip: frontmatter is emitted only when at least one field is non-empty, and steps are joined with `\n---\n\n`. `TestFormatRoundTrip` pins the parse→format→parse invariant.

#### Scenario: File with frontmatter and multiple steps parses to steps

- **GIVEN** a `STEPS.md` whose first non-blank line is `---`
- **WHEN** the parser reads it
- **THEN** it consumes YAML frontmatter until the next `---`, then splits the remaining body into steps on lines equal to `---` after trailing whitespace is trimmed
- **AND** trims each chunk and drops empty chunks so consecutive `---` lines collapse harmlessly

#### Scenario: File with no frontmatter

- **GIVEN** a `STEPS.md` whose first non-blank line is not `---`
- **WHEN** the parser reads it
- **THEN** the whole content is treated as body with no frontmatter

#### Scenario: Default mode

- **GIVEN** a routine whose frontmatter omits `mode`
- **WHEN** it is parsed
- **THEN** the mode defaults to `manual`

#### Scenario: Round-trip is stable

- **WHEN** a routine is formatted with `Format(r Routine)` and re-parsed
- **THEN** the parse→format→parse invariant holds (pinned by `TestFormatRoundTrip`)
- **AND** frontmatter is emitted only when at least one field is non-empty and steps are joined with `\n---\n\n`

### Requirement: Disk layout and storage operations

Routines SHALL be stored on disk under the data directory resolved through `config.DataDir()`, one directory per routine named by the routine's identity, each containing a single `STEPS.md`:

```
~/.lucinate/routines/
  <name>/
    STEPS.md
```

`internal/routines/store.go` SHALL expose these operations:

| Function | Behaviour |
|---|---|
| `Dir()` | `<data-dir>/routines` (not auto-created) |
| `List()` | Scan + parse every subdirectory; entries that fail to parse are silently skipped so one bad file doesn't sink the listing |
| `Load(name)` | Returns `Routine{}` + `ErrNotFound` if the directory is missing |
| `Save(r)` | `MkdirAll(0o700)` + atomic `WriteFile`/`Rename` of `STEPS.md` |
| `Delete(name)` | `os.RemoveAll(<dir>/<name>)` |

#### Scenario: Listing tolerates a bad file

- **WHEN** `List()` scans the routines directory and one subdirectory fails to parse
- **THEN** that entry is silently skipped and the remaining routines are still listed

#### Scenario: Load of a missing routine

- **GIVEN** no directory exists for the requested name
- **WHEN** `Load(name)` is called
- **THEN** it returns `Routine{}` and `ErrNotFound`

#### Scenario: Save writes atomically

- **WHEN** `Save(r)` is called
- **THEN** it creates the directory with `MkdirAll(0o700)` and writes `STEPS.md` atomically via `WriteFile`/`Rename`

### Requirement: Name validation predicates

Two name predicates SHALL live in `internal/routines/`:

- `validName` (path safety) rejects empty, `.`, `..`, leading-dot, and any name containing `/`, `\`, or NUL. `Load` and `Delete` use this predicate so existing on-disk directories — including any non-kebab names left over from earlier versions — keep working.
- `IsValidKebab` (form rule) tightens to lowercase ASCII letters, digits, and hyphens; no leading or trailing hyphen, no consecutive hyphens. `Save` enforces it, so any new save or rename lands on disk with a typeable, lowercase identifier. The companion `ToKebab(s string) string` produces a best-effort kebab version of arbitrary input — used by the form's submit error to suggest a fix when the user typed `My Routine!` or similar.

#### Scenario: Path-unsafe names rejected on load and delete

- **GIVEN** a name that is empty, `.`, `..`, leading-dot, or contains `/`, `\`, or NUL
- **WHEN** `Load` or `Delete` is called with it
- **THEN** `validName` rejects it

#### Scenario: Save enforces kebab identifiers

- **WHEN** `Save` is called
- **THEN** `IsValidKebab` requires lowercase ASCII letters, digits, and hyphens with no leading or trailing hyphen and no consecutive hyphens
- **AND** existing non-kebab on-disk directories still load and delete because those paths use `validName`, not `IsValidKebab`

#### Scenario: Kebab suggestion for invalid form input

- **GIVEN** the user typed a name such as `My Routine!`
- **WHEN** the form submit error is raised
- **THEN** `ToKebab` produces a best-effort kebab suggestion to fix it

### Requirement: Routine controller state and lifecycle

The active routine SHALL live on `chatModel.activeRoutine` (`internal/tui/routines_chat.go`), holding the routine, its mode, the count of dispatched steps, the paused flag, and an optional logger:

```go
type activeRoutine struct {
    routine routines.Routine
    mode    routines.Mode
    sent    int                 // count of steps already dispatched
    paused  bool
    logger  *routines.Logger    // nil if no `log:` configured
}
```

The lifecycle entry points on `*chatModel` SHALL be:

| Method | Purpose |
|---|---|
| `startRoutine(name)` | Load + parse from disk, open the log if configured, append the "Routine started" notification, return the `tea.Cmd` for step 0's send |
| `sendNextRoutineStep()` | Read `Steps[ar.sent]`, increment `ar.sent`, append the user message + assistant placeholder to `m.messages`, set `m.sending=true`, log the user line, return `sendMessage(...)` |
| `maybeAdvanceRoutine()` | Called from the chat `final` event handler. Completion fires first: when the answered step was the last, calls `endRoutine("completed")` regardless of mode and returns nil so the user always gets the same notification + cleared `activeRoutine`. Otherwise returns `sendNextRoutineStep` only when mode is auto and not paused; in manual or paused mid-routine it returns nil and the user drives the next step. |
| `applyDirectives(reply)` | Scans the assistant reply for `/routine:` directives and applies them in order |
| `endRoutine(reason)` | Closes the logger, clears `activeRoutine`, posts a "Routine X <reason>" notification |
| `cycleRoutineMode()` | Bound to `Shift+Tab` (mirrors Claude Code's mode-cycle gesture) — flips between auto and manual; entering auto unsets `paused`. Yields to slash-menu cycling when the completion menu is active. |

Step indexing SHALL be strictly monotonic: only `sendNextRoutineStep` increments `ar.sent`, and it does so once per call. Auto-advance SHALL be gated solely on `ar.sent < len(Steps)` and the directive/pause flags — there is no path that decrements or skips.

#### Scenario: Starting a routine dispatches step 0

- **WHEN** `startRoutine(name)` runs
- **THEN** it loads and parses the routine from disk, opens the log if configured, appends the "Routine started" notification, and returns the `tea.Cmd` for step 0's send

#### Scenario: Manual routine completes on its final reply

- **GIVEN** a routine in manual mode whose last step has just been answered
- **WHEN** `maybeAdvanceRoutine()` runs from the `final` handler
- **THEN** completion fires first: `endRoutine("completed")` is called regardless of mode, the notification is posted, `activeRoutine` is cleared, and it returns nil

#### Scenario: Auto routine advances mid-routine

- **GIVEN** a routine in auto mode, not paused, with steps remaining
- **WHEN** `maybeAdvanceRoutine()` runs
- **THEN** it returns `sendNextRoutineStep` to dispatch the next step

#### Scenario: Manual or paused routine stays quiet between steps

- **GIVEN** a routine in manual mode, or auto with `paused` set, with steps remaining
- **WHEN** `maybeAdvanceRoutine()` runs mid-routine
- **THEN** it returns nil and the user drives the next step

#### Scenario: Step index only ever advances

- **WHEN** any step is dispatched
- **THEN** only `sendNextRoutineStep` increments `ar.sent`, once per call, with no path that decrements or skips

### Requirement: Auto-advance hook ordering in the final event

Auto-advance SHALL live in the `final` case of `handleEvent` (`internal/tui/events.go`) and SHALL run in this order:

1. Mark the streaming assistant message as finalised (existing behaviour).
2. Capture the merge boundary via `bumpGen()` — the just-finalised turn is now on the history-side of any refresh issued from here on; subsequent appends get the new gen and survive the merge.
3. If `m.activeRoutine != nil`: log the assistant content (when a logger is configured) and call `applyDirectives` so a `/routine:stop` or `/routine:pause` is honoured before any auto-advance fires.
4. Always queue `refreshHistoryAt(boundary)` and `loadStats()`. The merge in the `historyRefreshMsg` handler is non-destructive — the live tail of the next routine step (placeholder, system rows) survives because those rows carry a higher gen than the boundary.
5. Drain `m.pendingMessages` via `drainQueueSkipRefresh` — user-typed queue jumps ahead of the routine. The `SkipRefresh` variant is used because we already queued the resync above; `drainQueue`'s built-in empty-queue refresh would otherwise duplicate it.
6. If the queue was empty and `m.sending` is now false, call `maybeAdvanceRoutine()`. If it returns a cmd, append it to the batch (sending the next step).

The unconditional refresh SHALL be the heart of the resync architecture. Pre-Layer-3, the refresh was deferred to "queue empty AND no routine to advance", which meant a 10-step auto-mode routine accumulated drift across all 10 steps before the first server-canonical reconciliation; with drift large enough, stale-event filtering became the only line of defence against spurious step submission. Now every `final` reconciles, every step.

The `error` and `aborted` cases SHALL also `bumpGen()` so the boundary stays monotonic, and SHALL set `paused = true` instead of advancing — so a transient gateway error doesn't loop the next step. The user MAY press Enter (empty input) to retry the next step or Esc to end the routine. These cases do not currently issue their own refresh; the next successful turn's refresh covers the canonical reconciliation.

#### Scenario: Directives are honoured before auto-advance

- **GIVEN** `m.activeRoutine != nil` and the assistant reply contains `/routine:stop` or `/routine:pause`
- **WHEN** the `final` case runs
- **THEN** the assistant content is logged (when a logger is configured) and `applyDirectives` is applied before any auto-advance fires

#### Scenario: Every final reconciles with the server

- **WHEN** any successful `final` is handled during a routine
- **THEN** `refreshHistoryAt(boundary)` and `loadStats()` are queued unconditionally, and the merge preserves the live tail because those rows carry a higher gen than the boundary

#### Scenario: Queued user messages jump ahead of the routine

- **GIVEN** pending user-typed messages when a `final` arrives
- **WHEN** the queue is drained via `drainQueueSkipRefresh`
- **THEN** the queued messages are sent before any routine auto-advance, and the built-in empty-queue refresh is skipped because the resync was already queued

#### Scenario: Transient error pauses rather than advancing

- **GIVEN** an `error` or `aborted` event during a routine
- **WHEN** it is handled
- **THEN** `bumpGen()` keeps the boundary monotonic, `paused` is set to true, and the next step is not looped
- **AND** the user can press Enter to retry the next step or Esc to end the routine

### Requirement: Stale-event filtering by finalised run

The OpenClaw gateway has been observed emitting a duplicate `delta` event with the full content right after the matching `final`, on the same `runID`. Without filtering, that delta lands on the next routine step's freshly-appended placeholder, flipping `awaitingDelta` and letting a subsequent empty-content `final` falsely finalise an empty turn — which spuriously auto-advances the routine.

The system SHALL keep `chatModel.finalisedRuns`, a bounded LRU set (cap `finalisedRunsCap = 32`) of run IDs already finalised. The top of the chat-event branch in `handleEvent` SHALL drop any event whose `RunID` is a member:

```go
if m.finalisedRuns.contains(chatEv.RunID) {
    slog.Debug("stale event ignored for finalised run", "run_id", chatEv.RunID)
    return nil
}
```

The set SHALL be added to inside the `final`, `error`, and `aborted` paths, but only when the corresponding state mutation actually happened (gated on the same `finalised` flag). FIFO eviction SHALL keep a long-lived chat from growing the filter unboundedly.

The set's depth matters: a single-deep filter is enough for the immediate duplicate-after-final case, but back-to-back routine steps open a wider window. A stale event for run N-2 can arrive while run N is streaming; with only the most-recent run remembered, that earlier id slips past and corrupts the live placeholder. `TestHandleEvent_StaleDeltaAfterFinalIgnored` pins the duplicate case; `TestHandleEvent_StaleDeltaFromOlderRunIgnored` pins the back-to-back race; `TestFinalisedRunSet_EvictsOldestPastCap` pins the FIFO bound.

#### Scenario: Duplicate delta after final is dropped

- **GIVEN** a `delta` event arrives on a `runID` already in `finalisedRuns`
- **WHEN** the chat-event branch of `handleEvent` runs
- **THEN** the event is dropped (a debug line is logged) so it cannot flip `awaitingDelta` on the next step's placeholder

#### Scenario: Stale event from an older run is dropped

- **GIVEN** a stale event for run N-2 arrives while run N is streaming
- **WHEN** the filter checks it against `finalisedRuns` (cap 32)
- **THEN** the older run id is still remembered and the event is dropped, not left to corrupt the live placeholder

#### Scenario: Filter is bounded

- **WHEN** more than `finalisedRunsCap = 32` runs have been finalised
- **THEN** FIFO eviction drops the oldest ids so the set never grows unboundedly

### Requirement: Assistant directives

The assistant SHALL be able to steer the routine by emitting one of these directives on its own line (leading whitespace allowed):

| Directive | Effect |
|---|---|
| `/routine:stop` | End the active routine immediately |
| `/routine:pause` | Pause without ending — Enter sends the next step, Esc ends |
| `/routine:continue` | Explicit no-op; also unsets `paused` so it can resume an auto-mode routine |
| `/routine:mode auto` | Switch to auto mode; unsets `paused` |
| `/routine:mode manual` | Switch to manual mode |

Matching (`internal/routines/directives.go`) SHALL use the anchored, per-line pattern:

```go
^\s*/routine:(stop|pause|continue|mode\s+(auto|manual))\s*$
```

Because it is anchored with `^...$` and applied per line, inline mentions (`as in /routine:stop`, backtick-wrapped tokens) SHALL deliberately not match. Directives SHALL be kept verbatim in the rendered transcript — `applyDirectives` does not rewrite the assistant message. User-typed `/routine:*` lines in the chat input SHALL NOT be parsed; only assistant replies are scanned.

#### Scenario: Own-line directive is applied

- **GIVEN** an assistant reply with `/routine:stop` on its own line (leading whitespace allowed)
- **WHEN** `applyDirectives` scans it
- **THEN** the directive matches the anchored per-line pattern and the routine ends immediately

#### Scenario: Inline mention does not match

- **GIVEN** an assistant reply mentioning `/routine:stop` inline or inside backticks
- **WHEN** `applyDirectives` scans it
- **THEN** it deliberately does not match, and the directive text stays verbatim in the transcript

#### Scenario: User-typed directives are ignored

- **GIVEN** a user types a `/routine:*` line in the chat input
- **WHEN** the input is processed
- **THEN** it is not parsed as a directive; only assistant replies are scanned

#### Scenario: Continue unsets paused

- **GIVEN** a paused auto-mode routine
- **WHEN** the assistant emits `/routine:continue`
- **THEN** it acts as a no-op that also unsets `paused`, allowing the routine to resume

### Requirement: Routine logging

When the frontmatter sets `log: <path>`, `routines.Logger` SHALL open the file (`O_APPEND | O_CREATE | O_WRONLY`, mode `0o600`) at routine start and close it on `endRoutine`. Relative paths SHALL resolve against the lucinate working directory at start time, captured via `os.Getwd()`. The log format SHALL be:

```
--- routine: demo started 2026-05-09T22:30:00Z ---
[2026-05-09T22:30:00Z] user: <step 1 text>
[2026-05-09T22:30:05Z] assistant: <reply line 1>
<reply line 2 — no per-line prefix>
```

Only the first line of a multi-line message SHALL get the `[ts] role:` prefix; subsequent lines are written verbatim so log diffs read like the chat. The logger SHALL be best-effort — write errors are silently swallowed so a logging hiccup never breaks the running routine. `Open` SHALL return `nil, nil` for an empty path so callers can invoke it unconditionally.

#### Scenario: Log opened at start and closed at end

- **GIVEN** frontmatter with `log: ./demo.log`
- **WHEN** the routine starts and later ends
- **THEN** the file is opened at start with `O_APPEND | O_CREATE | O_WRONLY` mode `0o600`, a run header is written, and the file is closed on `endRoutine`
- **AND** a relative path resolves against the working directory captured via `os.Getwd()` at start time

#### Scenario: Only the first line of a message is prefixed

- **WHEN** a multi-line user or assistant message is logged
- **THEN** only the first line gets the `[ts] role:` prefix and subsequent lines are written verbatim

#### Scenario: Logging never breaks the routine

- **WHEN** a log write fails
- **THEN** the error is silently swallowed and the routine continues

#### Scenario: Empty log path is a no-op

- **GIVEN** no `log:` path is configured
- **WHEN** `Open` is called
- **THEN** it returns `nil, nil` so callers can invoke it unconditionally

### Requirement: Manager view substates

`/routines` SHALL open `routinesModel` (`internal/tui/routines.go`), modelled on the cron browser, with four substates:

| Substate | Purpose |
|---|---|
| `routinesSubList` | List of routines (name + step count + mode chips) |
| `routinesSubDetail` | Read-only view of a single routine — frontmatter, file path, every step rendered in order |
| `routinesSubForm` | Create / edit / duplicate form |
| `routinesSubConfirmDelete` | y/n prompt before `routines.Delete` fires |

Key bindings SHALL follow the project-wide conventions (captured in project config). The list view SHALL expose `n` (new) and `d` (duplicate, gated on a non-empty list); the detail view SHALL expose `e` (edit) and `x` (delete) — `x` rather than `d` so duplicate and delete stay distinct in the user's vocabulary, matching the cron browser.

#### Scenario: Manager opens on the list

- **WHEN** the user runs `/routines`
- **THEN** `routinesModel` opens on `routinesSubList` showing each routine's name, step count, and mode chips

#### Scenario: List and detail key bindings distinguish duplicate from delete

- **GIVEN** the manager view
- **WHEN** the user is on the list with a non-empty list
- **THEN** `n` creates a new routine and `d` duplicates the highlighted one
- **AND** on the detail view `e` edits and `x` (not `d`) deletes, keeping duplicate and delete distinct

### Requirement: Form editing and step manipulation

The form SHALL have three textinputs (name, mode, log) plus a slice of `textarea.Model` for the steps — one per step. The focus index SHALL be a single int: `0..2` are the header fields, `3+i` is step `i`. Key bindings inside the form SHALL be:

| Key | Action |
|---|---|
| Tab / Shift+Tab | Cycle focus |
| Ctrl+S (or Alt+S) | Save — Ctrl+S is the surfaced binding; Alt+S is kept as a fallback for terminals that intercept Ctrl+S as XOFF |
| Alt+Up | Insert blank step above the focused step |
| Alt+Down | Insert blank step below the focused step |
| Alt+Delete (or Alt+Backspace) | Remove the focused step (y/n confirm) |
| Esc | Cancel without saving |
| Alt+Enter | Newline within a step textarea |

`insertStep(idx, value)` SHALL use an overlap-safe `copy` (`memmove`) so shift-right insertion never duplicates content. `deleteStep(idx)` SHALL re-insert a blank textarea when the slice would otherwise empty out, so the form always has at least one step to type into.

#### Scenario: Save has a fallback binding

- **WHEN** the user saves the form
- **THEN** Ctrl+S saves, and Alt+S is accepted as a fallback for terminals that intercept Ctrl+S as XOFF

#### Scenario: Insert and delete steps

- **WHEN** the user presses Alt+Up or Alt+Down on a focused step
- **THEN** a blank step is inserted above or below it via `insertStep`, using an overlap-safe copy so no content is duplicated

#### Scenario: Form always keeps at least one step

- **WHEN** the user removes the only remaining step (after y/n confirm)
- **THEN** `deleteStep` re-inserts a blank textarea so the form always has at least one step to type into

### Requirement: Duplicate routine flow

Pressing `d` on the list view SHALL open the form pre-populated from the highlighted routine, in create mode — `editingID` stays empty so submission goes through the plain `routines.Save` path with no rename, no overwrite, no delete-of-original. The cloned name SHALL be built by `duplicateRoutineName(name, existing)`:

- `"" → ""` (passes through so the form-level "name is required" check fires).
- Otherwise: `"Copy of " + name`, walking `(2)`, `(3)`, … to find the first slot that doesn't collide with an existing routine. Routines are name-keyed (the directory under `~/.lucinate/routines/<name>/` is the identity), so collision avoidance is required — unlike cron jobs which have a separate ID.

`Frontmatter.Name` SHALL be set to the duplicated name so the `STEPS.md` metadata stays in sync with the directory identity. `Frontmatter.Log` SHALL be copied verbatim — if it's a relative path the user can change it in the form before saving so the duplicate doesn't share the original's log file.

#### Scenario: Duplicate opens in create mode

- **GIVEN** a highlighted routine on the list view
- **WHEN** the user presses `d`
- **THEN** the form opens pre-populated in create mode with `editingID` empty, so submission uses plain `routines.Save` with no rename, overwrite, or delete-of-original

#### Scenario: Clone name avoids collisions

- **GIVEN** an existing routine named `demo`
- **WHEN** `duplicateRoutineName` builds the clone name
- **THEN** it yields `Copy of demo`, walking `(2)`, `(3)`, … to find the first non-colliding slot, because routines are name-keyed by directory

#### Scenario: Frontmatter stays in sync

- **WHEN** a routine is duplicated
- **THEN** `Frontmatter.Name` is set to the duplicated name and `Frontmatter.Log` is copied verbatim so the user can edit a relative log path before saving to avoid sharing the original's log file

### Requirement: Form scrolling and step sizing

The form's middle section (header inputs + step textareas) SHALL render inside a scrollable `viewport.Model`; the title and the footer (error line + help line) SHALL stay pinned. `viewForm` SHALL record the line index where each focusable field starts as it builds the body content (`fieldLineStarts`), and `ensureFocusVisible` SHALL adjust the viewport's `YOffset` so the focused field is fully on-screen — scrolling down when the user tabs past the bottom of the visible window, scrolling up when they shift-tab back. Without this, a routine with many steps used to push the help line off the terminal entirely.

Step textareas SHALL be sized at a fixed 4 lines each rather than auto-fitting the available height: the previous "divide remaining space across all steps" heuristic squeezed every textarea down to a 1-line stub once step count grew, and the user couldn't see the content of any step. The viewport handles overflow now, so a fixed per-step size is friendlier to type into.

#### Scenario: Focused field stays visible while scrolling

- **GIVEN** a routine with many steps in the form
- **WHEN** the user tabs past the bottom or shift-tabs back above the visible window
- **THEN** `ensureFocusVisible` adjusts the viewport's `YOffset` so the focused field is fully on-screen while the title and footer stay pinned

#### Scenario: Fixed step height

- **WHEN** the form renders step textareas
- **THEN** each is a fixed 4 lines with viewport overflow, rather than divided down to 1-line stubs as step count grows

### Requirement: Form submission and change notification

Submission SHALL iterate `form.steps` in order, dropping blank ones, and go through `routines.Save`. Editing with a renamed `name` SHALL write the new directory and `Delete` the old one. After save (or delete), the model SHALL emit `routinesChangedMsg` so the chat view refreshes its `m.routineNames` cache for `/routine <TAB>` completion.

#### Scenario: Blank steps dropped on save

- **WHEN** the form is submitted
- **THEN** it iterates `form.steps` in order, drops blank steps, and saves through `routines.Save`

#### Scenario: Rename writes new and deletes old

- **GIVEN** an edit that changes the `name`
- **WHEN** it is saved
- **THEN** the new directory is written and the old one is deleted

#### Scenario: Completion cache refreshes after CRUD

- **WHEN** a save or delete completes
- **THEN** the model emits `routinesChangedMsg` and the chat view refreshes its `m.routineNames` cache for `/routine <TAB>` completion

### Requirement: Slash commands and navigation gating

The system SHALL register two entries in `slashCommands`:

| Command | Behaviour |
|---|---|
| `/routine <name>` | Activate the named routine. Bare `/routine` is an error pointing at `/routines`. Tab completion uses `m.routineNames` populated by `loadRoutineNames()` at chat init and after any manager-view CRUD. |
| `/routines` | Open the manager via `showRoutinesMsg{}` |

Only one routine SHALL run at a time per session. Both commands SHALL route through `gateNavigation()` (`routines_chat.go`) when a routine is already active, showing:

```
Routine "demo" is active. Starting routine "other" will cancel it. Continue? (y/n)
```

The same gate SHALL cover the navigations that strand or replace the chat model — `/agents`, `/agent <name>`, `/sessions`, `/crons`, `/crons all`, `/connections` — for the same reason: the active routine controller can't survive a chat-view reset, and silently dropping it would leak the open log file. On `y`, the gate SHALL cancel any in-flight turn (`cancelTurn`) and end the routine (`endRoutine`) before dispatching the navigation. On `n` or Esc the prompt SHALL clear and the routine SHALL continue. `startRoutine` itself still has a defensive `if m.activeRoutine != nil` guard, but in normal flow the gate runs first.

#### Scenario: Bare /routine is an error

- **WHEN** the user submits `/routine` with no name
- **THEN** it is an error pointing at `/routines`

#### Scenario: Starting a second routine prompts to cancel the first

- **GIVEN** routine `demo` is active
- **WHEN** the user runs `/routine other` (or a stranding navigation such as `/agents`, `/agent <name>`, `/sessions`, `/crons`, `/crons all`, `/connections`)
- **THEN** `gateNavigation()` shows the "Continue? (y/n)" prompt
- **AND** on `y` it cancels the in-flight turn via `cancelTurn` and ends the routine via `endRoutine` before dispatching the navigation
- **AND** on `n` or Esc the prompt clears and the routine continues

#### Scenario: Tab completion of routine names

- **WHEN** the user types `/routine ` and presses Tab
- **THEN** completion draws from `m.routineNames`, populated by `loadRoutineNames()` at chat init and after any manager-view CRUD

### Requirement: Routine notifications

Routine state changes (started, paused, ended) and routine errors SHALL be surfaced as ephemeral notifications, not chat rows (see the `chat-ux` spec, Notifications). They SHALL live outside `m.messages` so a `historyRefreshMsg` doesn't wipe them, and SHALL clear when the user submits any non-empty input. The routine controller SHALL call `m.notify` / `m.notifyError` directly; the legacy `appendSystemError` helper still exists but routes to `notifyError` under the hood for older callers.

#### Scenario: Notifications survive a history refresh

- **GIVEN** a routine notification is showing
- **WHEN** a `historyRefreshMsg` is handled
- **THEN** the notification survives because it lives outside `m.messages`

#### Scenario: Notification clears on next input

- **GIVEN** a routine notification is showing
- **WHEN** the user submits any non-empty input
- **THEN** the notification clears

### Requirement: Routine status row

When `m.activeRoutine != nil`, the chat View SHALL render a single styled row immediately above the input box. While auto-advancing or mid-turn the trailing segment SHALL be a passive preview:

```
routine: demo — AUTO — sent: 5/10 — next: <preview>
```

When the routine is awaiting user input — manual mode, or auto with `paused` set, with no turn in flight and steps remaining — the trailing segment SHALL switch to a call-to-action so the user sees both what the next message is and that the routine is parked on them:

```
routine: demo — MANUAL — sent: 5/10 — ▶ Press Enter to send: <preview>
```

`AUTO`/`MANUAL` SHALL reflect the mode; `(paused)` SHALL be appended when `paused` is set. The row SHALL be coloured by mode — amber (`execClr`) for auto, cyan (`userClr`) for manual — so the driver is legible at a glance without the row competing with the purple chat header. The renderer is `routineStatusLine` + `routineStatusStyle` in `routines_chat.go`; the preview length is computed from `m.width` so it grows with the terminal (floor 20 chars, fallback 40 when width is not yet known). `applyLayout()` (`completion.go`) SHALL subtract one row from the viewport height when a routine is active so the status row doesn't push the input off-screen.

#### Scenario: Preview while advancing

- **GIVEN** an active routine that is auto-advancing or mid-turn
- **WHEN** the chat View renders
- **THEN** the status row shows `routine: <name> — AUTO — sent: N/M — next: <preview>` above the input box

#### Scenario: Call-to-action when parked on the user

- **GIVEN** an active routine awaiting user input (manual, or auto with `paused`, no turn in flight, steps remaining)
- **WHEN** the chat View renders
- **THEN** the trailing segment reads `▶ Press Enter to send: <preview>`
- **AND** `(paused)` is appended when `paused` is set

#### Scenario: Row is coloured by mode and preserves input space

- **WHEN** the status row renders
- **THEN** it is amber (`execClr`) for auto and cyan (`userClr`) for manual, and `applyLayout()` subtracts one viewport row so the status row doesn't push the input off-screen

### Requirement: Routine key bindings

The chat view SHALL bind these keys while relevant to routines:

| Key | Behaviour |
|---|---|
| Shift+Tab | Cycle mode (auto ↔ manual). No-op when no routine is active. Mirrors Claude Code's mode-cycle gesture. Yields to slash-menu cycling when the completion menu is active. |
| Esc | When a routine is active: end the routine and (if streaming) cancel the in-flight turn. Otherwise behaves as before (`/cancel`-equivalent or transcript back). |
| Enter (empty input) | When a routine is active and idle (manual or paused): send the next step. Otherwise no-op. |

#### Scenario: Shift+Tab cycles mode

- **GIVEN** a routine is active and the completion menu is not showing
- **WHEN** the user presses Shift+Tab
- **THEN** the mode cycles auto ↔ manual; with no routine active it is a no-op, and it yields to slash-menu cycling when the completion menu is active

#### Scenario: Esc ends an active routine

- **GIVEN** a routine is active
- **WHEN** the user presses Esc
- **THEN** the routine ends and, if streaming, the in-flight turn is cancelled; with no routine active Esc behaves as before

#### Scenario: Enter sends the next step when idle

- **GIVEN** a routine is active and idle (manual or paused)
- **WHEN** the user presses Enter on an empty input
- **THEN** the next step is sent; otherwise it is a no-op

### Requirement: Verification coverage

The routines feature SHALL be pinned by unit tests and a manual smoke procedure. The unit tests SHALL include:

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

The manual smoke procedure SHALL be:

1. Drop a routine at `~/.lucinate/routines/demo/STEPS.md` with `mode: manual`. `/routine demo` dispatches step 0; status row reads `MANUAL — sent: 1/N — ▶ Press Enter to send: …`.
2. Press Enter on an empty input — step 1 dispatches.
3. Step through to the end. After the final step's reply, a `Routine "demo" completed.` notification appears, the status row disappears, and the input returns to plain chat.
4. Shift+Tab flips status to `AUTO`. Subsequent step finals auto-advance.
5. Have step N's reply emit `/routine:stop` on its own line — routine ends; "Routine 'demo' stopped by assistant." notification appears above the input and survives the post-turn `refreshHistory`.
6. Repeat with `log: ./routine.log` set — verify a run header and ISO-timestamped `user:` / `assistant:` lines.
7. Activate a routine, run `/agents` — confirm the gate prompt; `n` keeps the routine running, `y` ends it cleanly and returns to the picker.

#### Scenario: Unit tests pin the behaviour

- **WHEN** the routines unit-test suite runs
- **THEN** parsing, directives, storage, logging, the stale-run filter, the per-final resync, gen-bump semantics, the merge contract, notifications, the duplicate flow, and manual completion are all covered

#### Scenario: Manual smoke exercises the end-to-end flow

- **GIVEN** a `demo` routine on disk
- **WHEN** an operator follows the smoke procedure
- **THEN** manual stepping, completion notification, auto-advance, an assistant `/routine:stop`, logging output, and the navigation gate all behave as described
