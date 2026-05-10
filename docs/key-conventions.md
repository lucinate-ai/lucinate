# Keyboard conventions

Cross-view keyboard conventions for the lucinate TUI. New manager views, picker views, and forms should follow these so the user's mental model carries from one screen to the next.

The conventions are descriptive of what is in the code today (`internal/tui/crons.go`, `internal/tui/routines.go`, `internal/tui/sessions.go`, `internal/tui/connections.go`, `internal/tui/configview.go`, `internal/tui/select.go`), not aspirational. When you find yourself wanting to deviate, rename the action so the convention still holds, or update this document with the new convention and a short rationale.

## Action keys

| Key | Action | Where it appears |
|---|---|---|
| `n` | New (create a fresh job/routine/session/connection/agent) | crons list, routines list, sessions, connections, agent picker |
| `e` | Edit | crons detail, routines detail, connections |
| `x` | Delete | crons detail, routines detail |
| `d` | Duplicate | crons list, routines list (gated on non-empty list) |
| `r` (lowercase) | Refresh | crons list/detail, sessions, agent picker |
| `R` (uppercase) | Retry | crons list (only when error), sessions (only when error), agent picker (only when error) |
| `t` | Toggle (enable/disable) | crons detail |
| `T` (uppercase) | Transcript | crons detail (only when run-log has content) |
| `!` | Run now | crons detail |
| `a` | Toggle agent filter | crons list (only when filtering by agent) |
| `c` | Connections | agent picker (when management surface allows it) |
| `Space` | Toggle (boolean config item) | config view |

A few of these warrant explanation:

- **`x` for delete, not `d`.** `d` is reserved for duplicate. Routines used `d` for delete originally and were rebound to `x` so the two managers share a vocabulary.
- **`r` lowercase / `R` uppercase.** Lowercase = refresh (always available); uppercase = retry (only after an error). Where retry isn't applicable, only `r` is bound. Don't reverse this — terminals that don't reliably report shift would land on the wrong action.
- **`!` for run-now**, not `R` (uppercase). The case-sensitive `r`/`R` pair was easy to misfire on terminals that drop shift on letters; `!` is unambiguous.
- **`T` for transcript** stays uppercase because lowercase `t` is already toggle in the same view.

## Confirms

| Key | Action |
|---|---|
| `y` (or `Y`) | Confirm destructive action |
| `n` (or `N`) | Decline |
| `Esc` | Decline (alias) |

Confirm prompts are always one substate (e.g. `cronSubConfirmDelete`, `routinesSubConfirmDelete`). The substate change is the explicit handoff — the prompt UI replaces the help row, so the user sees what the keys mean before pressing them.

## Navigation

| Key | Action |
|---|---|
| `Esc` | Back / cancel |
| `Enter` | Select / submit |
| `Up` / `Down` (or `k` / `j`) | Move within a list |
| `Left` / `Right` (or `h` / `l`) | Decrease / increase numeric config items |
| `Tab` / `Shift+Tab` | Cycle focus within a form |

`Esc` is overloaded by context but always means "step backwards" — close a modal, leave a form without saving, return from detail to list, return from manager to chat. In chat view, it additionally cancels an in-flight turn or ends an active routine ([routines.md](routines.md#key-bindings) covers the chat-side semantics).

## Form keys

| Key | Action | Notes |
|---|---|---|
| `Tab` / `Shift+Tab` | Cycle focus through form fields | Wraps at the boundary |
| `Alt+S` | Save | Primary save key. **`Ctrl+S` is kept as a defensive alias** because some terminals interpret Ctrl+S as XOFF (software flow control) and never deliver the keystroke to the application. |
| `Alt+Enter` | Newline within a multi-line text area | Routines step textareas, crons payload textarea, chat input |
| `Enter` | Submit form | Plain `Enter` submits when not focused on a multi-line field |
| `Esc` | Cancel without saving | Form state is discarded |

Routine-form-specific keys (`internal/tui/routines.go`):

| Key | Action |
|---|---|
| `Alt+Up` | Insert blank step above the focused step |
| `Alt+Down` | Insert blank step below the focused step |
| `Alt+Delete` (or `Alt+Backspace`) | Remove the focused step (y/n confirm) |

## Reserved keys

These should be avoided for application actions because the terminal or the readline layer above us tends to consume them:

| Key | Why it's reserved |
|---|---|
| `Ctrl+S` | XOFF (software flow control). Many terminals never deliver this keystroke to the app. Bind `Alt+S` and keep `Ctrl+S` only as a no-cost alias. |
| `Ctrl+R` | Readline reverse-search. The chat textarea inherits readline-style bindings, so `Ctrl+R` is taken. |
| `Ctrl+W` | Delete word backward (readline). |
| `Ctrl+<letter>` (in general) | Most are bound by readline. Prefer `Alt+<letter>` for application-level shortcuts — that's why the routine mode toggle is `Alt+M` rather than `Ctrl+M`. |

The `Alt+M` rationale comment in `internal/tui/chat.go` captures the general principle: *Alt is used because every Ctrl+letter has a readline binding (Ctrl+R reverse-search, Ctrl+S XOFF, etc.).*

## Discovery

Every manager view exposes its key bindings through an `Actions() []Action` method. The host renders these as a help line so the user always has the current substate's bindings on screen — and so external menus (e.g. the action drawer) can drive the same actions without duplicating key dispatch. When you add a new key binding, register it in `Actions()` for the relevant substate **and** wire it in the substate's `handleKey` (or equivalent) so both surfaces stay in sync. The `TriggerAction(id)` pattern (cron and routines both implement it) lets a menu invoke the same code path the keystroke takes.

## When to deviate

If a view genuinely needs a key not covered here, document the choice with a short rationale comment near the binding (the `!` for run-now and the `Alt+M` for routine mode are good models). If the deviation reflects a new project-wide pattern, update this document so future views can follow it.
