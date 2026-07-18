# Cron Jobs Specification

## Purpose

Provide an in-TUI browser (`internal/tui/crons.go`) that lets users list, inspect, run, edit, create, and delete the gateway's scheduled jobs without leaving lucinate. Cron is gateway-side scheduling, not a lucinate concept — only backends that implement `backend.CronBackend` expose the view. This spec covers the two entry points, the capability surface, the four substates (list, detail, form, confirm-delete), the transcript view, raw-patch edit semantics, payload field mapping, the duplicate flow, and what is deliberately out of scope.

## Requirements

### Requirement: Cron browser availability gated on CronBackend

The cron browser SHALL only be exposed for backends that implement `backend.CronBackend`, because cron is gateway-side scheduling rather than a lucinate concept. Capability SHALL also be reported as `Capabilities.Cron` so embedders can hide the entry up-front.

#### Scenario: Backend does not implement CronBackend

- **GIVEN** an active backend that does not implement `backend.CronBackend`
- **WHEN** the user attempts to open the cron browser
- **THEN** the view is not exposed for that connection

#### Scenario: Capability reported for embedders

- **WHEN** an embedder inspects the backend's capabilities
- **THEN** cron availability is reported as `Capabilities.Cron` so the entry can be hidden up-front

### Requirement: Entry from chat via /crons and /crons all

The system SHALL provide two chat slash commands, `/crons` and `/crons all`, both emitting `showCronsMsg{filterAgentID, filterLabel}`. The slash-command handler (`internal/tui/commands.go`) SHALL type-assert the active backend against `backend.CronBackend`. If the assertion fails, the handler SHALL show the system message `"/crons is not available on this connection"` with no view transition (the same pattern as `/compact` on Hermes). If the assertion succeeds, the message SHALL be emitted: `/crons` sets the filter to the chat's current agent, and `/crons all` clears the filter so jobs across every agent are listed. The `AppModel` SHALL record the opening view in `cronsReturn` so `esc`/Back returns to chat (mirrors `configReturn`).

#### Scenario: Command on a non-cron connection

- **GIVEN** the active backend does not implement `backend.CronBackend`
- **WHEN** the user runs `/crons`
- **THEN** the handler shows `"/crons is not available on this connection"` and does not transition views

#### Scenario: /crons filters to the current agent

- **GIVEN** the active backend implements `backend.CronBackend`
- **WHEN** the user runs `/crons` from chat
- **THEN** `showCronsMsg` is emitted with the filter set to the chat's current agent
- **AND** `cronsReturn` records chat as the return view

#### Scenario: /crons all clears the filter

- **WHEN** the user runs `/crons all`
- **THEN** `showCronsMsg` is emitted with the filter cleared so jobs across every agent are listed

### Requirement: Entry from the agent picker via the k action

The agent picker SHALL offer a `k: View crons` action (`internal/tui/select.go`), gated on the same `backend.CronBackend` assertion so it only appears for OpenClaw connections. Because no agent is selected on the picker, it SHALL always open unfiltered (`filterAgentID: ""`, like `/crons all`). The `k` binding SHALL be repurposed from the list's vim-style up-navigation, which is dropped in `newSelectModel`, keeping `↑`. The `AppModel` SHALL record the opening view in `cronsReturn` so `esc`/Back returns to the picker (mirrors `configReturn`).

#### Scenario: Picker action opens unfiltered

- **GIVEN** an OpenClaw connection implementing `backend.CronBackend`
- **WHEN** the user selects `k: View crons` in the agent picker
- **THEN** the cron browser opens unfiltered with `filterAgentID: ""`
- **AND** `cronsReturn` records the picker as the return view

#### Scenario: Action absent on non-cron connections

- **GIVEN** a connection that does not implement `backend.CronBackend`
- **THEN** the `k: View crons` action does not appear in the agent picker

#### Scenario: k no longer navigates up

- **WHEN** the picker list is built in `newSelectModel`
- **THEN** the vim-style up-navigation on `k` is dropped and `k` is bound to View crons, with `↑` retained for up-navigation

### Requirement: CronBackend capability surface wraps six gateway RPCs

`backend.CronBackend` (`internal/backend/backend.go`) SHALL wrap the gateway RPCs mapped below:

| Method | Wire call | Used for |
|---|---|---|
| `CronsList` | `cron.list` | List substate |
| `CronRuns` | `cron.runs` | Run history in detail substate |
| `CronAdd` | `cron.add` | Create form submit |
| `CronUpdate` | `cron.update` (typed) | Toggle enable / disable |
| `CronUpdateRaw` | `cron.update` (raw map) | Edit form submit — see the raw-patch edit semantics requirement |
| `CronRemove` | `cron.remove` | Confirm-delete substate |
| `CronRun` | `cron.run` (`mode=force`) | Manual run-now |

#### Scenario: Method-to-wire-call mapping

- **WHEN** a cron browser action fires
- **THEN** it invokes the corresponding `backend.CronBackend` method, which calls the mapped gateway wire call (e.g. `CronRun` calls `cron.run` with `mode=force`)

### Requirement: cronsModel single view with four substates

`cronsModel` SHALL be a single view with four substates:

| Substate | Purpose |
|---|---|
| `cronSubList` | Default — paginated, filtered job list |
| `cronSubDetail` | Drill-down into a selected job + run history |
| `cronSubForm` | Create or edit form |
| `cronSubConfirmDelete` | y/n prompt before `CronRemove` fires |

Each substate SHALL expose its own discoverable actions through `Actions()`, and `TriggerAction(id)` SHALL be the single dispatcher through which both keystrokes and embedder-issued `TriggerActionMsg`s flow.

#### Scenario: Actions dispatched through a single path

- **WHEN** either a keystroke or an embedder-issued `TriggerActionMsg` triggers an action
- **THEN** it flows through `TriggerAction(id)` as the single dispatcher
- **AND** the available actions come from the current substate's `Actions()`

### Requirement: List substate loads and renders jobs with local filtering

The list view SHALL load on `Init()` via `loadJobs()`, which calls `CronsList(Enabled: "all", SortBy: "nextRunAtMs", SortDir: "asc")`. The full slice SHALL be cached on the model so the agent-filter toggle (`a` key) can re-apply locally without a round-trip — server-side filtering is not exposed in `CronListParams`. Each row SHALL render:

- **Line 1**: bold name + dim relative-time chip (`in 8h`, `due`, `—`).
- **Line 2**: chips for session target (`main`/`isolated`), wake mode (`now`/`heartbeat`), agent ID, and a status badge (`ok`/`error`/`disabled`/`idle`).

The list substate SHALL bind: `enter` opens detail; `r` refreshes; `n` opens the create form; `d` opens the create form pre-populated from the highlighted job (the duplicate flow — see the duplicate flow requirement); `esc` emits `goBackFromCronsMsg{}` to return to the view that opened the browser (chat, or the agent picker).

#### Scenario: Initial load and sort

- **WHEN** the list substate initialises via `Init()`
- **THEN** `loadJobs()` calls `CronsList(Enabled: "all", SortBy: "nextRunAtMs", SortDir: "asc")` and caches the full slice on the model

#### Scenario: Agent-filter toggle applies locally

- **GIVEN** the full job slice is cached on the model
- **WHEN** the user presses `a` to toggle the agent filter
- **THEN** the filter is re-applied locally without a round-trip, because server-side filtering is not exposed in `CronListParams`

#### Scenario: Row rendering

- **WHEN** a job row is rendered
- **THEN** line 1 shows the bold name plus a dim relative-time chip and line 2 shows chips for session target, wake mode, agent ID, and a status badge

#### Scenario: List key bindings

- **WHEN** the user presses `enter`, `r`, `n`, `d`, or `esc` in the list
- **THEN** respectively detail opens, the list refreshes, the create form opens, the create form opens pre-populated from the highlighted job, or `goBackFromCronsMsg{}` returns to the opening view

### Requirement: Detail substate shows job fields and run history

Pressing `enter` on the list SHALL emit a `loadRuns(jobID)` command alongside the substate transition; `cronRunsLoadedMsg` SHALL populate the run-history table (most recent 10 entries, formatted by `formatRunLogEntry`). The detail view SHALL render:

- Schedule (cron expression + timezone, or fallback for `at`/`every` kinds)
- Description, Agent, Model (from `payload.Model`), Session target, Wake mode
- Delivery (always shown — `none`, `announce (channel)`, or `webhook → URL`)
- Next run, Last run (with status), Payload body
- Run history table

The detail substate SHALL provide the actions: `!` run-now, `t` toggle enable, `e` edit, `x` delete (→ confirm substate), `T` open a read-only transcript reconstructed from the run log (see the transcript view requirement), `r` refresh, `esc` back to list.

#### Scenario: Entering detail loads run history

- **WHEN** the user presses `enter` on a list row
- **THEN** a `loadRuns(jobID)` command is emitted with the substate transition
- **AND** `cronRunsLoadedMsg` populates the run-history table with the most recent 10 entries formatted by `formatRunLogEntry`

#### Scenario: Detail fields rendered

- **WHEN** the detail substate renders a job
- **THEN** it shows schedule, description, agent, model, session target, wake mode, delivery (always shown), next run, last run with status, payload body, and the run history table

### Requirement: Run-now bound to ! with in-flight guard

Run-now SHALL be bound to `!` rather than `R` because the case-sensitive pair (`R` run vs. `r` refresh) was a misfire trap on terminals that don't preserve shift on letter keys. The detail view SHALL render a transient `Triggering run...` banner the moment `!` fires, replaced by `Run triggered.` (or `Run failed: <err>`) once `cronJobRanMsg` arrives. A `running` flag on `cronsModel` SHALL gate duplicate keystrokes while the request is in flight.

#### Scenario: Triggering a manual run

- **WHEN** the user presses `!` in the detail substate
- **THEN** a transient `Triggering run...` banner appears immediately and the `running` flag gates duplicate keystrokes
- **AND** once `cronJobRanMsg` arrives the banner becomes `Run triggered.` or `Run failed: <err>`

#### Scenario: Run-now key choice

- **GIVEN** terminals that don't preserve shift on letter keys
- **WHEN** run-now is bound
- **THEN** it uses `!` to avoid the `R` run vs. `r` refresh misfire trap

### Requirement: Read-only transcript reconstructed from the run log

Pressing `T` SHALL emit `cronTranscriptMsg{job, runs, agentName}`; the `AppModel` SHALL hand it to the chat view with `hideInput=true` and pre-seed `chatModel.messages` from `buildCronTranscriptMessages` (`internal/tui/history.go`). No `chat.history` round-trip SHALL be made — cron runs with `sessionTarget=isolated` don't persist a queryable session entry on the gateway (especially when the run errors before `persistSessionEntry` fires), so the run log itself is the source of truth. The run log is also the same data the detail page's run-history previews render, so transcript content matches what the previews promise.

The builder SHALL walk `m.runs` (newest-first as returned by `cron.runs sortDir=desc`) in reverse and emit, per run: a separator with `RunAtMs`, a user turn with the cron's `payload.Text` / `payload.Message`, and either an assistant turn with `Summary` (Glamour-rendered when it looks like markdown) or an `errMsg` assistant turn with `Error`. Repeating the payload per run is intentional — each run is an independent invocation of the same prompt, and the structure makes per-run timing and outcome obvious.

The action SHALL be gated by `hasTranscriptContent`: if no run carries a `Summary` or `Error`, the `T` entry SHALL be suppressed from `Actions()` so it doesn't dangle on jobs with nothing to show.

The transcript chat view SHALL set `chatModel.transcript = true`. With no input box to consume it, `Esc` would otherwise be a no-op, so the chat key handler SHALL emit `goBackFromCronTranscriptMsg` when the flag is set; `AppModel` SHALL switch state back to `viewCrons`, where `cronsModel.subset`/`selectedID` are preserved across the transcript hop, so the user lands back on the originating detail page.

#### Scenario: Transcript sourced from the run log

- **WHEN** the user presses `T` in the detail substate
- **THEN** `cronTranscriptMsg{job, runs, agentName}` is emitted and the chat view is seeded from `buildCronTranscriptMessages` with `hideInput=true`, with no `chat.history` round-trip because isolated cron runs have no queryable session entry

#### Scenario: Per-run transcript structure

- **WHEN** `buildCronTranscriptMessages` walks `m.runs` in reverse
- **THEN** each run emits a separator with `RunAtMs`, a user turn with `payload.Text`/`payload.Message`, and either an assistant turn with `Summary` (Glamour-rendered when it looks like markdown) or an `errMsg` assistant turn with `Error`

#### Scenario: Transcript action suppressed when empty

- **GIVEN** no run carries a `Summary` or `Error`
- **WHEN** `Actions()` is computed via `hasTranscriptContent`
- **THEN** the `T` entry is suppressed so it doesn't dangle

#### Scenario: Returning from the transcript

- **GIVEN** the transcript chat view with `chatModel.transcript = true` and no input box
- **WHEN** the user presses `Esc`
- **THEN** the chat key handler emits `goBackFromCronTranscriptMsg`, `AppModel` switches back to `viewCrons`, and the preserved `cronsModel.subset`/`selectedID` land the user on the originating detail page

### Requirement: Create and edit form constrained to cron schedules and agentTurn payloads

The create and edit form SHALL share `cronForm` and the `cronFormField` enum (12 fields, tab-ordered). To avoid a TUI form modelling every union the gateway protocol exposes (`CronSchedule.Kind` ∈ `at`/`every`/`cron`; `CronPayload.Kind` ∈ `systemEvent`/`agentTurn`), the form SHALL be constrained to `cron` schedules and `agentTurn` payloads. Editing a job whose existing kind is anything else SHALL load the form in a refused state showing the banner:

> Edit not supported for schedule kind "every". Use the openclaw CLI.

The save path SHALL be suppressed in this state — the system surfaces the brittleness rather than silently round-tripping a truncated representation.

Tab/Shift+Tab SHALL navigate fields. Space SHALL toggle the cycle/checkbox controls (`sessionTarget`, `wakeMode`, `deliveryMode`, `enabled`). Inside the payload `textarea`, Enter SHALL insert a newline; Ctrl+S (or Alt+Enter) SHALL save from anywhere; Esc SHALL cancel and return to whichever substate opened the form.

#### Scenario: Editing an unmodelled kind is refused

- **GIVEN** a job whose schedule kind is not `cron` or whose payload kind is not `agentTurn`
- **WHEN** the user opens the edit form
- **THEN** the form loads in a refused state showing `Edit not supported for schedule kind "every". Use the openclaw CLI.` and the save path is suppressed

#### Scenario: Form navigation and controls

- **WHEN** the user interacts with the form
- **THEN** Tab/Shift+Tab navigate fields, Space toggles the cycle/checkbox controls (`sessionTarget`, `wakeMode`, `deliveryMode`, `enabled`), Enter inside the payload `textarea` inserts a newline, Ctrl+S or Alt+Enter saves from anywhere, and Esc cancels back to the opening substate

### Requirement: Duplicate flow pre-populates a new job

Pressing `d` on the list substate SHALL open the same form as `n`, but pre-populated from the highlighted job. The form SHALL stay in `mode=create` with `editingID=""`, so submission goes through `CronAdd` (not `CronUpdateRaw`) and produces a brand-new job rather than mutating the source. The name SHALL be prefixed with `Copy of ` so the duplicate is visually distinguishable in the list before the user edits it; every other field — schedule, timezone, agent, model, payload, session/wake, delivery, enabled — SHALL be copied verbatim. `populateFormFromJob` SHALL be shared with `newEditForm` so the two flows can't drift in which fields they carry over. Duplicating a job whose schedule kind is anything other than `cron` (or whose payload kind is anything other than `agentTurn`) SHALL be refused with the same banner the edit flow shows, for the same reason: the TUI form would silently truncate the unmodelled union fields.

#### Scenario: Duplicating a supported job

- **WHEN** the user presses `d` on a `cron`/`agentTurn` job in the list
- **THEN** the create form opens in `mode=create` with `editingID=""`, the name prefixed `Copy of `, and every other field copied verbatim, so submission via `CronAdd` produces a new job

#### Scenario: Shared populate path

- **WHEN** either the duplicate flow or `newEditForm` populates the form
- **THEN** both use `populateFormFromJob` so the fields carried over cannot drift

#### Scenario: Duplicating an unmodelled kind is refused

- **GIVEN** a job whose schedule kind is not `cron` or payload kind is not `agentTurn`
- **WHEN** the user presses `d`
- **THEN** the duplicate is refused with the same banner the edit flow shows

### Requirement: Raw-patch edit semantics preserve cleared fields

The toggle action (`t` on detail) and the create-form submit SHALL use the typed `protocol.CronUpdateParams`/`CronAddParams`. The edit-form submit SHALL instead go through `CronUpdateRaw(jobID, patch map[string]any)`, because every string field on `protocol.CronJobPatch` and `CronPayload` is tagged `json:",omitempty"` — once Go marshals an empty value, the field is dropped from the JSON, and the gateway can't distinguish "user cleared this field" from "user didn't touch this field" (it keeps the prior value). The map-based path SHALL emit empty strings verbatim (see `buildJobPatchMap`) so clearing model, description, or delivery actually persists. Toggle SHALL stay on the typed path because it only mutates a `*bool`, which doesn't have the omitempty problem.

#### Scenario: Edit submit persists cleared fields

- **GIVEN** the user clears model, description, or delivery in the edit form
- **WHEN** the form is submitted via `CronUpdateRaw(jobID, patch map[string]any)`
- **THEN** `buildJobPatchMap` emits the empty strings verbatim so the cleared values persist, rather than being dropped by `json:",omitempty"`

#### Scenario: Toggle stays on the typed path

- **WHEN** the user toggles enable/disable with `t`
- **THEN** the typed `protocol.CronUpdateParams` path is used, because it only mutates a `*bool` and has no omitempty problem

### Requirement: Payload field mapping between message and text

`protocol.CronPayload` SHALL expose both `Text` and `Message` because the gateway's payload schema is a union. For `agentTurn` the prompt travels in `message` (the `agentTurn` schema declares `additionalProperties: false` and rejects `text`); for `systemEvent` it travels in `text`. Because the TUI form models `agentTurn` only, `buildAddParams` and `buildJobPatchMap` SHALL populate `message` and never emit `text`. On the read side, `populateFormFromJob` and `cronPayloadText` SHALL prefer `Message` and fall back to `Text` only for historical jobs that may still carry the prompt under the systemEvent-style field.

#### Scenario: Writing an agentTurn prompt

- **WHEN** `buildAddParams` or `buildJobPatchMap` writes the prompt for an `agentTurn` job
- **THEN** it populates `message` and never emits `text`, because `agentTurn` declares `additionalProperties: false` and rejects `text`

#### Scenario: Reading a prompt with fallback

- **WHEN** `populateFormFromJob` or `cronPayloadText` reads the prompt
- **THEN** it prefers `Message` and falls back to `Text` only for historical jobs that carry the prompt under the systemEvent-style field

### Requirement: Confirm-delete substate

Pressing `x` on detail SHALL transition to a y/n prompt. `y` SHALL call `CronRemove(jobID)` and refresh the list (returning to `cronSubList`). `n` or `esc` SHALL return to detail without action.

#### Scenario: Confirming deletion

- **GIVEN** the confirm-delete prompt is showing
- **WHEN** the user presses `y`
- **THEN** `CronRemove(jobID)` is called and the list is refreshed, returning to `cronSubList`

#### Scenario: Cancelling deletion

- **WHEN** the user presses `n` or `esc` at the confirm-delete prompt
- **THEN** control returns to detail without any action

### Requirement: Out-of-scope behaviours

The cron browser SHALL NOT implement the following, which are deliberately out of scope:

- Server-side cron filtering by agent (would need `agentId` added to `CronListParams` upstream).
- Edit support for `at`/`every` schedules and `systemEvent` payloads.
- Pagination of run history beyond the most recent 10 entries.
- Live updates via cron-related gateway events — there is no streaming for cron state changes today, so the user must press `r` to refresh.
- Replaying a cron run as a live chat session — the transcript is rebuilt from the run log, not from a queryable gateway session, so it's read-only by design.

#### Scenario: No live cron updates

- **GIVEN** a cron state change on the gateway
- **WHEN** the user is viewing the cron browser
- **THEN** the view does not update automatically and the user must press `r` to refresh, because there is no streaming for cron state changes

#### Scenario: Run history is capped

- **WHEN** the run-history table is populated
- **THEN** it shows at most the most recent 10 entries, with no pagination beyond that
