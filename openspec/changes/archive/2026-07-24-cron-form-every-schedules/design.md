## Context

The cron browser's create/edit/duplicate form (`internal/tui/crons.go`) was built to model only `cron`-expression schedules and `agentTurn` payloads. The gateway's `CronSchedule` is a per-kind union (`at`/`every`/`cron`) and `CronPayload` is another (`systemEvent`/`agentTurn`); the form deliberately refused kinds it didn't model, pointing the user at the openclaw CLI, rather than round-trip a truncated representation.

`every` — a fixed-interval schedule (`everyMs`, with optional `anchorMs`/`staggerMs`) — is the most common kind after `cron`. Every "run every N minutes" job hit the refusal banner on edit, which is the pain this change addresses. The form's rendering has a fixed field-to-line map (`fieldLineStarts` indexed by the `cronFormField` enum) used to scroll the focused field into view, so the set of rendered rows must stay 1:1 with the enum.

## Goals / Non-Goals

**Goals:**
- Let users edit, create, and duplicate `every`-interval cron jobs entirely within the TUI.
- Round-trip an `every` schedule faithfully: an untouched interval saves the same `everyMs`, and `anchorMs`/`staggerMs` the form can't show are preserved.
- Keep the change contained to the cron form; no protocol, backend, or wire-contract changes.

**Non-Goals:**
- Modelling `at` schedules (one-shot timestamps) or `systemEvent` payloads — those stay routed to the CLI.
- Surfacing `anchorMs`/`staggerMs` as editable fields — they are preserved, not exposed.
- Hiding the inactive schedule-value input based on the selected kind (see Decisions).

## Decisions

**Reuse the always-render field idiom rather than conditionally hiding rows.** The form already renders every field unconditionally and marks irrelevant ones via label (e.g. "Delivery target (unused while mode=none)"). The focus/scroll machinery (`fieldLineStarts`, `ensureFocusVisible`) assumes a fixed row set indexed by the enum, so skipping rows would desynchronise the index. Decision: add a `scheduleKind` toggle plus a dedicated `interval` input, keep both the cron-expression and interval rows visible, and mark the inactive one unused via `cronExprLabel`/`intervalLabel`. Alternative considered — a single dual-purpose input whose meaning switches with the kind — was rejected because two inputs preserve each value across a toggle and read more clearly.

**Interval as a human duration parsed with `time.ParseDuration`.** Users think in `15m`/`1h30m`, not milliseconds. `parseEveryInterval` wraps `time.ParseDuration` and rejects non-positive values (a zero interval would busy-loop the gateway); `formatEveryInterval` renders `everyMs` back as `h/m/s/ms` components that parse back to the exact same value. Alternative — a raw millisecond number field — was rejected as hostile to read and edit.

**Preserve `anchorMs`/`staggerMs` off the source job.** The form doesn't show them, but dropping them on save would shift a job's run phase when the user only meant to rename it. Decision: stash them on `cronForm` in `populateFormFromJob` and re-emit them from the schedule builders. This is the same "don't truncate on round-trip" discipline behind `CronUpdateRaw`.

**Per-kind schedule builders.** `buildSchedule` (typed, for `CronAdd`) and `buildScheduleMap` (wire map, for `CronUpdateRaw`) emit only the keys valid for the selected kind — an `every` schedule never carries the cron `expr`/`tz`, matching the gateway's per-kind union schema. `anchorMs`/`staggerMs` are included only when the source job carried them, so a fresh `every` job doesn't invent a phase.

## Risks / Trade-offs

- **Both schedule-value inputs are always on screen** → the inactive one is clearly labelled "(unused while kind=…)", consistent with the existing delivery-target field; net simpler than reworking the focus/scroll index.
- **Interval precision loss on exotic values** → `formatEveryInterval` emits down to milliseconds, so any `everyMs` round-trips exactly; realistic intervals (whole seconds/minutes) are unaffected.
- **`at`/`systemEvent` still refused** → intentional; those unions are genuinely harder to model in a TUI and are lower-traffic. The refusal banner now names `at`, and the out-of-scope requirement records the boundary.

## Migration Plan

No data or API migration. The change is additive within the TUI: existing `cron` jobs behave exactly as before, and `every` jobs that previously refused now open in the form. Rollback is a straight revert of the `internal/tui/crons.go` and test changes.
