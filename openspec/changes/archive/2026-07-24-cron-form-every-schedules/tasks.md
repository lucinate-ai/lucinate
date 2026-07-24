## 1. Form model

- [x] 1.1 Add `formScheduleKind` and `formInterval` to the `cronFormField` enum (between description and cron expression / timezone), keeping the enum 1:1 with the rendered rows
- [x] 1.2 Add `interval textinput.Model`, `scheduleKind string`, and the carried `scheduleAnchorMs *int64` / `scheduleStaggerMs *int` fields to `cronForm`
- [x] 1.3 Initialise `scheduleKind="cron"` and the interval input (placeholder `15m`) in `newCreateForm`

## 2. Schedule read/write

- [x] 2.1 Add `parseEveryInterval` (human duration → `everyMs`, rejecting non-positive) and `formatEveryInterval` (`everyMs` → compact duration that round-trips exactly)
- [x] 2.2 Add `buildSchedule` (typed) and `buildScheduleMap` (wire map) that emit only the keys valid for the selected kind, re-emitting `anchorMs`/`staggerMs` when present
- [x] 2.3 Route `buildAddParams` and `buildJobPatchMap` through the new schedule builders
- [x] 2.4 In `populateFormFromJob`, branch on `Schedule.Kind`: populate cron expr/timezone for `cron`, interval + carried anchor/stagger for `every`; narrow the unsupported banner to `at` (and non-`agentTurn` payloads)

## 3. Form interaction

- [x] 3.1 Handle Space on `formScheduleKind` to toggle `cron`/`every`; wire `formInterval` into `activeInput` and `refocus`
- [x] 3.2 Branch `submitForm` validation on schedule kind: require a parseable interval for `every`, require a non-empty expression for `cron`
- [x] 3.3 Render the schedule-kind toggle and interval rows in `viewForm`; add `cronExprLabel`/`intervalLabel` to mark the inactive input unused

## 4. Tests

- [x] 4.1 Repoint the two "refuses unsupported kind" tests from `every` to `at`
- [x] 4.2 Add tests: `every` edit populate (incl. anchor/stagger carry), build add + raw patch for `every`, interval parse/format round-trip and rejection, the kind toggle, and submit validation
- [x] 4.3 Run `make test`, `go vet`, and `gofmt` — all green

## 5. Spec & docs

- [x] 5.1 Update `openspec/specs/crons/spec.md` (form-constraint, duplicate-flow, and out-of-scope requirements)
- [x] 5.2 Update `docs/crons.md` rationale (modelled kinds; preserve-what-you-can't-edit discipline)
- [x] 5.3 `openspec validate --specs` passes
