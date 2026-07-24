## RENAMED Requirements

- FROM: `### Requirement: Create and edit form constrained to cron schedules and agentTurn payloads`
- TO: `### Requirement: Create and edit form constrained to cron/every schedules and agentTurn payloads`

## MODIFIED Requirements

### Requirement: Create and edit form constrained to cron/every schedules and agentTurn payloads

The create and edit form SHALL share `cronForm` and the `cronFormField` enum (14 fields, tab-ordered). The gateway protocol exposes union types (`CronSchedule.Kind` ∈ `at`/`every`/`cron`; `CronPayload.Kind` ∈ `systemEvent`/`agentTurn`); to keep the TUI form tractable it SHALL model the two common shapes — `cron` and `every` schedules, and `agentTurn` payloads — and route the rest to the CLI. A `scheduleKind` toggle (`cron`/`every`) SHALL select between a cron-expression input and an interval input; the interval SHALL be a human duration (e.g. `15m`, `1h30m`) parsed to `everyMs`. Because the form does not surface an `every` schedule's `anchorMs`/`staggerMs`, those SHALL be carried off the source job and re-emitted on save so an edit does not silently shift the job's run phase.

Editing a job whose schedule kind is `at` (or whose payload kind is `systemEvent`) SHALL load the form in a refused state showing the banner:

> Edit not supported for schedule kind "at". Use the openclaw CLI.

The save path SHALL be suppressed in this state — the system surfaces the brittleness rather than silently round-tripping a truncated representation.

Tab/Shift+Tab SHALL navigate fields. Space SHALL toggle the cycle/checkbox controls (`scheduleKind`, `sessionTarget`, `wakeMode`, `deliveryMode`, `enabled`). Inside the payload `textarea`, Enter SHALL insert a newline; Ctrl+S (or Alt+Enter) SHALL save from anywhere; Esc SHALL cancel and return to whichever substate opened the form.

#### Scenario: Editing an every schedule is supported

- **GIVEN** a job whose schedule kind is `every` and payload kind is `agentTurn`
- **WHEN** the user opens the edit form
- **THEN** the form loads with `scheduleKind=every`, the interval pre-populated from `everyMs`, and the `anchorMs`/`staggerMs` carried so a save re-emits them
- **AND** submitting sends a `schedule` of kind `every` carrying `everyMs` (and no cron `expr`/`tz`)

#### Scenario: Editing an unmodelled kind is refused

- **GIVEN** a job whose schedule kind is `at` or whose payload kind is not `agentTurn`
- **WHEN** the user opens the edit form
- **THEN** the form loads in a refused state showing `Edit not supported for schedule kind "at". Use the openclaw CLI.` and the save path is suppressed

#### Scenario: Interval is validated on save

- **GIVEN** the edit form with `scheduleKind=every`
- **WHEN** the user submits with an empty or unparseable interval
- **THEN** the save is refused with an interval error and no gateway call is made

#### Scenario: Form navigation and controls

- **WHEN** the user interacts with the form
- **THEN** Tab/Shift+Tab navigate fields, Space toggles the cycle/checkbox controls (`scheduleKind`, `sessionTarget`, `wakeMode`, `deliveryMode`, `enabled`), Enter inside the payload `textarea` inserts a newline, Ctrl+S or Alt+Enter saves from anywhere, and Esc cancels back to the opening substate

### Requirement: Duplicate flow pre-populates a new job

Pressing `d` on the list substate SHALL open the same form as `n`, but pre-populated from the highlighted job. The form SHALL stay in `mode=create` with `editingID=""`, so submission goes through `CronAdd` (not `CronUpdateRaw`) and produces a brand-new job rather than mutating the source. The name SHALL be prefixed with `Copy of ` so the duplicate is visually distinguishable in the list before the user edits it; every other field — schedule (cron expression/timezone or every-interval), agent, model, payload, session/wake, delivery, enabled — SHALL be copied verbatim. `populateFormFromJob` SHALL be shared with `newEditForm` so the two flows can't drift in which fields they carry over. Duplicating a job whose schedule kind is `at` (or whose payload kind is anything other than `agentTurn`) SHALL be refused with the same banner the edit flow shows, for the same reason: the TUI form would silently truncate the unmodelled union fields.

#### Scenario: Duplicating a supported job

- **WHEN** the user presses `d` on a `cron`/`agentTurn` job in the list
- **THEN** the create form opens in `mode=create` with `editingID=""`, the name prefixed `Copy of `, and every other field copied verbatim, so submission via `CronAdd` produces a new job

#### Scenario: Shared populate path

- **WHEN** either the duplicate flow or `newEditForm` populates the form
- **THEN** both use `populateFormFromJob` so the fields carried over cannot drift

#### Scenario: Duplicating an unmodelled kind is refused

- **GIVEN** a job whose schedule kind is `at` or payload kind is not `agentTurn`
- **WHEN** the user presses `d`
- **THEN** the duplicate is refused with the same banner the edit flow shows

### Requirement: Out-of-scope behaviours

The cron browser SHALL NOT implement the following, which are deliberately out of scope:

- Server-side cron filtering by agent (would need `agentId` added to `CronListParams` upstream).
- Edit support for `at` schedules and `systemEvent` payloads (`cron` and `every` schedules are modelled).
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

#### Scenario: at schedules and systemEvent payloads route to the CLI

- **GIVEN** a job whose schedule kind is `at` or whose payload kind is `systemEvent`
- **WHEN** the user opens it for edit or duplicate
- **THEN** the form loads refused and the user is pointed at the openclaw CLI, because those unions are not modelled by the form
