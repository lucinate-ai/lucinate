## Why

The in-TUI cron form only modelled `cron`-expression schedules, so opening any `every`-interval job for edit or duplicate loaded a refused state banner (`Edit not supported for schedule kind "every". Use the openclaw CLI.`). Because every plain "run every N minutes" job trips it, this was the most common refusal users hit — a routine edit forced them out to the CLI.

## What Changes

- Model `every` schedules in the create/edit/duplicate form alongside `cron`, so interval jobs can be edited without leaving lucinate.
- Add a `scheduleKind` toggle (`cron`/`every`, Space to switch) that swaps the cron-expression input for an interval input. The interval is a human duration (`15m`, `1h30m`) parsed to `everyMs`, and rendered back so an untouched interval round-trips exactly.
- Preserve an `every` schedule's `anchorMs`/`staggerMs` — which the form does not surface — through an edit, so changing an unrelated field does not silently shift the job's run phase. Per-kind schedule builders emit only the keys valid for the selected kind (an `every` patch never carries the cron `expr`/`tz`).
- Validate the interval on submit; suppress the save with an error when it is empty or unparseable.
- `at` schedules and `systemEvent` payloads remain out of scope and still route to the CLI (the refusal banner now names `at`).

## Capabilities

### New Capabilities

_None — this extends an existing capability._

### Modified Capabilities

- `crons`: the create/edit form now models `cron` **and** `every` schedules (previously `cron` only); the refusal / out-of-scope requirements narrow to `at` schedules and `systemEvent` payloads; the duplicate flow's supported-kind boundary moves in step.

## Impact

- Code: `internal/tui/crons.go` (form fields, schedule-kind toggle, interval parse/format, per-kind schedule builders, populate + validation) and `internal/tui/crons_test.go`.
- Docs: `docs/crons.md` rationale for the form's modelled kinds and the preserve-what-you-can't-edit discipline.
- No protocol, backend, or wire-contract changes — `protocol.CronSchedule` already carries `everyMs`/`anchorMs`/`staggerMs`; the gateway RPCs (`cron.add`, `cron.update`) are unchanged.
- No breaking changes.
