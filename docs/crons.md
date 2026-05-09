# Cron jobs

The cron browser (`internal/tui/crons.go`) lets users list, inspect, run, edit, create, and delete the gateway's scheduled jobs without leaving the TUI. Cron is gateway-side scheduling, not a lucinate concept — only backends that implement `backend.CronBackend` expose the view.

## Entry points

`/crons` and `/crons all` are the only entry points. The chat view's slash-command handler (`internal/tui/commands.go`) type-asserts the active backend against `backend.CronBackend`:

- Assertion fails → `"/crons is not available on this connection"` system message, no view transition (same pattern as `/status`, `/compact`).
- Assertion succeeds → `showCronsMsg{filterAgentID, filterLabel}` is emitted. With `/crons` the filter is the chat's current agent; `/crons all` clears the filter so jobs across every agent are listed.

## Capability surface

`backend.CronBackend` (`internal/backend/backend.go`) wraps six gateway RPCs:

| Method | Wire call | Used for |
|---|---|---|
| `CronsList` | `cron.list` | List substate |
| `CronRuns` | `cron.runs` | Run history in detail substate |
| `CronAdd` | `cron.add` | Create form submit |
| `CronUpdate` | `cron.update` (typed) | Toggle enable / disable |
| `CronUpdateRaw` | `cron.update` (raw map) | Edit form submit — see [Raw-patch edit semantics](#raw-patch-edit-semantics) |
| `CronRemove` | `cron.remove` | Confirm-delete substate |
| `CronRun` | `cron.run` (`mode=force`) | Manual run-now |

Capability is also reported as `Capabilities.Cron` so embedders can hide the entry up-front.

## Substates

`cronsModel` is a single view with four substates:

| Substate | Purpose |
|---|---|
| `cronSubList` | Default — paginated, filtered job list |
| `cronSubDetail` | Drill-down into a selected job + run history |
| `cronSubForm` | Create or edit form |
| `cronSubConfirmDelete` | y/n prompt before `CronRemove` fires |

Each substate exposes its own discoverable actions through `Actions()`; `TriggerAction(id)` is the single dispatcher both keystrokes and embedder-issued `TriggerActionMsg`s flow through.

## List substate

The list view loads on `Init()` via `loadJobs()`, which calls `CronsList(Enabled: "all", SortBy: "nextRunAtMs", SortDir: "asc")`. The full slice is cached on the model so the agent-filter toggle (`a` key) can re-apply locally without a round-trip — server-side filtering is not exposed in `CronListParams`. Each row renders:

- **Line 1**: bold name + dim relative-time chip (`in 8h`, `due`, `—`).
- **Line 2**: chips for session target (`main`/`isolated`), wake mode (`now`/`heartbeat`), agent ID, and a status badge (`ok`/`error`/`disabled`/`idle`).

`enter` opens detail; `r` refreshes; `n` opens the create form; `d` opens the create form pre-populated from the highlighted job (the duplicate flow — see [Duplicate flow](#duplicate-flow)); `esc` emits `goBackFromCronsMsg{}` to return to chat.

## Detail substate

`enter` on the list emits a `loadRuns(jobID)` command alongside the substate transition; `cronRunsLoadedMsg` populates the run-history table (most recent 10 entries, formatted by `formatRunLogEntry`). Rendered fields:

- Schedule (cron expression + timezone, or fallback for `at`/`every` kinds)
- Description, Agent, Model (from `payload.Model`), Session target, Wake mode
- Delivery (always shown — `none`, `announce (channel)`, or `webhook → URL`)
- Next run, Last run (with status), Payload body
- Run history table

Actions: `!` run-now, `t` toggle enable, `e` edit, `x` delete (→ confirm substate), `T` open a read-only transcript reconstructed from the run log (see [Transcript view](#transcript-view)), `r` refresh, `esc` back to list.

Run-now is bound to `!` rather than the more obvious `R` because the case-sensitive pair (`R` run vs. `r` refresh) was a misfire trap on terminals that don't preserve shift on letter keys. The detail view renders a transient `Triggering run...` banner the moment `!` fires, replaced by `Run triggered.` (or `Run failed: <err>`) once `cronJobRanMsg` arrives — a `running` flag on `cronsModel` gates duplicate keystrokes while the request is in flight.

### Transcript view

`T` emits `cronTranscriptMsg{job, runs, agentName}`; the AppModel hands it to the chat view with `hideInput=true` and pre-seeds `chatModel.messages` from `buildCronTranscriptMessages` (`internal/tui/history.go`). No `chat.history` round-trip is made — cron runs with `sessionTarget=isolated` don't persist a queryable session entry on the gateway (especially when the run errors before `persistSessionEntry` fires), so the run log itself is the source of truth. The run log is also the same data the detail page's run-history previews already render, so transcript content matches what the previews promise.

The builder walks `m.runs` (newest-first as returned by `cron.runs sortDir=desc`) in reverse and emits, per run: a separator with `RunAtMs`, a user turn with the cron's `payload.Text` / `payload.Message`, and either an assistant turn with `Summary` (Glamour-rendered when it looks like markdown) or an `errMsg` assistant turn with `Error`. Repeating the payload per run is intentional — each run is an independent invocation of the same prompt, and the structure makes per-run timing and outcome obvious.

The action itself is gated by `hasTranscriptContent`: if no run carries a `Summary` or `Error`, the `T` entry is suppressed from `Actions()` so it doesn't dangle on jobs with nothing to show.

The transcript chat view sets `chatModel.transcript = true`. With no input box to consume it, `Esc` would otherwise be a no-op, so the chat key handler emits `goBackFromCronTranscriptMsg` when the flag is set; `AppModel` switches state back to `viewCrons`, where `cronsModel.subset`/`selectedID` are preserved across the transcript hop, so the user lands back on the originating detail page.

## Form substate

The create and edit form share `cronForm` and the `cronFormField` enum (12 fields, tab-ordered). To avoid a TUI form modelling every union the gateway protocol exposes (`CronSchedule.Kind` ∈ `at`/`every`/`cron`; `CronPayload.Kind` ∈ `systemEvent`/`agentTurn`), the form is **constrained to `cron` schedules and `agentTurn` payloads**. Editing a job whose existing kind is anything else loads the form in a refused state:

> Edit not supported for schedule kind "every". Use the openclaw CLI.

The save path is suppressed in this state — we surface the brittleness rather than silently round-trip a truncated representation.

Tab/Shift+Tab navigates fields. Space toggles the cycle/checkbox controls (`sessionTarget`, `wakeMode`, `deliveryMode`, `enabled`). Inside the payload `textarea`, Enter inserts a newline; Ctrl+S (or Alt+Enter) saves from anywhere; Esc cancels and returns to whichever substate opened the form.

### Duplicate flow

`d` on the list substate opens the same form as `n`, but pre-populated from the highlighted job. The form stays in `mode=create` with `editingID=""`, so submission goes through `CronAdd` (not `CronUpdateRaw`) and produces a brand-new job rather than mutating the source. The name is prefixed with `Copy of ` so the duplicate is visually distinguishable in the list before the user edits it; every other field — schedule, timezone, agent, model, payload, session/wake, delivery, enabled — is copied verbatim. `populateFormFromJob` is shared with `newEditForm` so the two flows can't drift in which fields they carry over. Duplicating a job whose schedule kind is anything other than `cron` (or whose payload kind is anything other than `agentTurn`) is refused with the same banner the edit flow shows, for the same reason: the TUI form would silently truncate the unmodelled union fields.

### Raw-patch edit semantics

The toggle action (`t` on detail) and the create-form submit use the typed `protocol.CronUpdateParams`/`CronAddParams`. The **edit-form submit goes through `CronUpdateRaw(jobID, patch map[string]any)` instead**, because every string field on `protocol.CronJobPatch` and `CronPayload` is tagged `json:",omitempty"` — once Go marshals an empty value, the field is dropped from the JSON, and the gateway can't distinguish "user cleared this field" from "user didn't touch this field" (it keeps the prior value). The map-based path emits empty strings verbatim (see `buildJobPatchMap`) so clearing model, description, or delivery actually persists. Toggle stays on the typed path because it only mutates a `*bool`, which doesn't have the omitempty problem.

### Payload field mapping (`message` vs `text`)

`protocol.CronPayload` exposes both `Text` and `Message` because the gateway's payload schema is a union. For `agentTurn` the prompt travels in `message` (the `agentTurn` schema declares `additionalProperties: false` and rejects `text`); for `systemEvent` it travels in `text`. The TUI form models `agentTurn` only, so `buildAddParams` and `buildJobPatchMap` populate `message` and never emit `text`. On the read side, `populateFormFromJob` and `cronPayloadText` prefer `Message` and fall back to `Text` only for historical jobs that may still carry the prompt under the systemEvent-style field.

## Confirm-delete substate

`x` on detail transitions to a y/n prompt. `y` calls `CronRemove(jobID)` and refreshes the list (returning to `cronSubList`). `n` or `esc` returns to detail without action.

## Out of scope

- Server-side cron filtering by agent (would need `agentId` added to `CronListParams` upstream).
- Edit support for `at`/`every` schedules and `systemEvent` payloads.
- Pagination of run history beyond the most recent 10 entries.
- Live updates via cron-related gateway events — there is no streaming for cron state changes today, so the user must press `r` to refresh.
- Replaying a cron run as a live chat session — the transcript is rebuilt from the run log, not from a queryable gateway session, so it's read-only by design.
