# Cron jobs — lessons and rationale

The behavioural contract for the cron browser lives in
[`openspec/specs/crons/spec.md`](../openspec/specs/crons/spec.md) — the two entry points, the
capability surface, the four substates, the transcript view, the form constraints, and what is
deliberately out of scope are all captured there as requirements and scenarios. This file keeps
the hard-won lessons, pitfalls, and design rationale behind that flow: the "why it works this
odd way" that the spec's requirements don't dwell on.

## `k` is repurposed from vim-style up-navigation

The agent picker's `k: View crons` action reuses a key that the list would otherwise bind to
vim-style up-navigation. That binding is dropped in `newSelectModel`, keeping `↑` for up. Worth
remembering if you ever wonder why `k` doesn't move the cursor in this one list.

## Run-now is `!`, not `R`

Run-now is bound to `!` rather than the more obvious `R` because the case-sensitive pair (`R`
run vs. `r` refresh) was a misfire trap on terminals that don't preserve shift on letter keys.

A `running` flag on `cronsModel` gates duplicate keystrokes while the request is in flight — the
detail view renders a transient `Triggering run...` banner the moment `!` fires, replaced by
`Run triggered.` (or `Run failed: <err>`) once `cronJobRanMsg` arrives.

## Transcript view: the run log is the source of truth

`T` rebuilds a read-only transcript rather than doing a `chat.history` round-trip, because cron
runs with `sessionTarget=isolated` don't persist a queryable session entry on the gateway —
especially when the run errors before `persistSessionEntry` fires. The run log itself is the
source of truth, and it's the same data the detail page's run-history previews already render,
so transcript content matches what the previews promise.

Repeating the payload per run in the transcript is intentional — each run is an independent
invocation of the same prompt, and the structure makes per-run timing and outcome obvious.

The action is gated by `hasTranscriptContent`: if no run carries a `Summary` or `Error`, the `T`
entry is suppressed so it doesn't dangle on jobs with nothing to show.

The transcript chat view has no input box, so `Esc` would otherwise be a no-op; the chat key
handler emits `goBackFromCronTranscriptMsg` when `chatModel.transcript` is set, and
`cronsModel.subset`/`selectedID` are preserved across the transcript hop so the user lands back
on the originating detail page.

## Form models `cron` + `every` schedules and `agentTurn` payloads

The gateway protocol exposes union types (`CronSchedule.Kind` ∈ `at`/`every`/`cron`;
`CronPayload.Kind` ∈ `systemEvent`/`agentTurn`). Rather than modelling all of them, the form
covers the two common shapes: `cron` and `every` schedules with an `agentTurn` payload. A
`scheduleKind` toggle swaps between the cron-expression input and an interval input; the interval
is a human duration (`15m`, `1h30m`) parsed to `everyMs` via `parseEveryInterval` and rendered
back with `formatEveryInterval` (the two round-trip exactly, so an untouched interval saves the
same millisecond value it loaded).

`every` originally sat with `at`/`systemEvent` in the "route to the CLI" bucket, and its refusal
banner (`Edit not supported for schedule kind "every"…`) was the most common one users hit —
every plain interval job tripped it. It's now modelled; only `at` schedules (one-shot timestamps)
and `systemEvent` payloads still load the form in a refused state:

> Edit not supported for schedule kind "at". Use the openclaw CLI.

The save path is suppressed in that state — we surface the brittleness rather than silently
round-trip a truncated representation. The duplicate flow refuses for the same reason, and
shares `populateFormFromJob` with `newEditForm` so the two flows can't drift in which fields
they carry over.

### Preserve the fields the form doesn't surface

An `every` schedule can also carry `anchorMs` (the interval's phase) and `staggerMs`, neither of
which the form shows. If an edit dropped them, changing an unrelated field (the name, the prompt)
would silently shift every future run. So `populateFormFromJob` stashes them on the form and the
schedule builders re-emit them verbatim. This is the same "don't truncate on round-trip"
discipline behind `CronUpdateRaw` below — the edit path must preserve what it can't edit. The
per-kind schedule builders (`buildScheduleMap`/`buildSchedule`) also emit only the keys valid for
the selected kind — an `every` patch never carries the cron `expr`/`tz`, since the gateway's
schedule schema is a per-kind union.

## Raw-patch edit semantics: why `CronUpdateRaw`

The toggle action and the create-form submit use the typed
`protocol.CronUpdateParams`/`CronAddParams`, but the **edit-form submit goes through
`CronUpdateRaw(jobID, patch map[string]any)` instead**. Every string field on
`protocol.CronJobPatch` and `CronPayload` is tagged `json:",omitempty"` — once Go marshals an
empty value, the field is dropped from the JSON, and the gateway can't distinguish "user cleared
this field" from "user didn't touch this field" (it keeps the prior value). The map-based path
emits empty strings verbatim (see `buildJobPatchMap`) so clearing model, description, or
delivery actually persists.

Toggle stays on the typed path because it only mutates a `*bool`, which doesn't have the
omitempty problem.

## Payload field mapping: `message` vs `text`

`protocol.CronPayload` exposes both `Text` and `Message` because the gateway's payload schema is
a union. For `agentTurn` the prompt travels in `message` (the `agentTurn` schema declares
`additionalProperties: false` and rejects `text`); for `systemEvent` it travels in `text`. The
TUI form models `agentTurn` only, so `buildAddParams` and `buildJobPatchMap` populate `message`
and never emit `text`. On the read side, `populateFormFromJob` and `cronPayloadText` prefer
`Message` and fall back to `Text` only for historical jobs that may still carry the prompt under
the systemEvent-style field.

## Capability gating mirrors `/compact`

The `/crons` slash command and the picker action are both gated on the same
`backend.CronBackend` type assertion, so the view only surfaces for OpenClaw connections. When
the assertion fails, `/crons` shows `"/crons is not available on this connection"` with no view
transition — the same pattern as `/compact` on Hermes. Capability is also reported as
`Capabilities.Cron` so embedders can hide the entry up-front rather than discovering it's
unavailable at use time.
