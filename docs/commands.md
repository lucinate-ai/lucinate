# Slash commands — lessons and rationale

The behavioural contract for slash commands lives in
[`openspec/specs/commands/spec.md`](../openspec/specs/commands/spec.md) — dispatch and
interception, the full built-in command catalogue, tab completion, the yes/no confirmation
pattern, and the routine-active navigation gate are all captured there as requirements and
scenarios. This file keeps the hard-won lessons, pitfalls, and design rationale behind those
commands: the "why it works this odd way" that the spec's requirements don't dwell on.

## `model not allowed`: patch with the qualified reference

Both switch paths — the `/model <name>` command and the `/models` picker — patch with the
**qualified `<provider>/<id>` reference** produced by `qualifiedModelRef()`
(`internal/tui/models.go`), not the bare id. `models.list` reports a provider-local id (e.g.
`deepseek/deepseek-v4-pro`) alongside a separate `provider` field (e.g. `openrouter`), but
`sessions.patch` — like the agent's configured `model.primary` — validates against the
fully-qualified form (`openrouter/deepseek/deepseek-v4-pro`). Sending the bare id makes the
gateway reject the switch with `INVALID_REQUEST: model not allowed: <id>`. Backends that
leave `provider` empty (openai, hermes) keep the bare id unchanged.

## `/cron`: names aren't unique, and `/crons` wins the switch

`/cron` requires a name argument, and bare `/cron` points at `/crons`. That works because the
plural browser is matched by the switch first, so a `/cron ` prefix never swallows it.

Cron job names are **not unique**, so `matchCronJobs()` is tiered — exact (case-insensitive)
`Name`, then exact `ID`, then substring on either — and returns every job in the winning tier.
When more than one lands, the ambiguity row lists each candidate's name, `id`, and schedule and
tells the user to re-run `/cron <id>`, because IDs *are* unique and that's the only reliable way
to disambiguate. Since names live on the gateway, resolution is asynchronous and `/cron <name>`
always re-lists to resolve the actual run — the tab-completion snapshot may have gone stale
mid-session.

## `/header`: no fallback to a global setting

Overrides are scoped per agent and stored under a nested `agents` sub-object in `config.json`,
keyed by agent ID, so future per-agent settings can sit alongside `headerColor` without growing
the file horizontally. When the active agent has no ID (e.g. an unbound chat), `/header`
reports an error rather than falling back to a global setting — there's deliberately no global
header colour to fall back to.

## Tab completion: LCP drives it, curated order no longer does

The curated order in `slashCommands` (e.g. `/agents` before `/agent`, `/model` before
`/models`) now only breaks ties for the inline ghost-hint and the legacy `completeSlashCommand`
callers — Tab uses the longest-common-prefix extension, so the order no longer steers it. The
cycle-mode snapshot persists across presses because Tab returns early before
`refreshCompletionMenu` runs; any non-Tab keystroke clears cycling and recomputes candidates.

Agent- and cron-name completion silently degrade to a no-op when the list hasn't loaded or the
backend errored — the matcher returns nil, the candidate list is empty, and the menu simply
stays hidden rather than surfacing an error.

### Menu vs ghost-hint fallback

The live menu is the primary surface, but it can't always render. The single-line greyed-out
ghost hint (`slashCommandHint` / `agentNameHint`) exists purely as a fallback for short
terminals: the menu suppresses itself entirely when the baseline can't leave at least
`completionMenuViewportFloor` rows for the conversation, and Tab still does LCP extension on the
underlying state even with the menu hidden. So completion never depends on the menu being
visible.

## Confirmation pattern: drop the prompt so `(y/n)` doesn't linger

Commands that need a yes/no (`/compact`, `/reset`, `/cron <name>`) use a two-step confirmation
to prevent accidental data loss. The subtlety worth remembering: on the answering Enter,
`removeConfirmPrompt()` drops the tagged prompt row either way — a brief `Confirmed.` /
`Cancelled.` notification reports which — so the `(y/n)` question doesn't linger in the
scrollback once answered.

When `runningStatus` is set, the handler animates the same braille spinner used for in-flight
assistant turns, and the result handler calls `replacePendingSystem` to swap the placeholder for
the outcome line **in place** — no stale "Compacting session…" stuck above the result.

## Routine-active navigation gate: keep the two flows apart

Slash commands that strand or replace the chat model route through `gateNavigation()`
(`internal/tui/routines_chat.go`) when a routine is active, so navigating away doesn't silently
abandon a running routine. The `pendingNavConfirm` state is kept **independent** of the generic
`pendingConfirmation` on purpose, so the two confirmation flows don't compete over the same UI
state. See the `routines` spec (slash commands and gating) for the full rationale.
