---
name: concord
description: Catch OpenSpec concurrency hazards with concord before they clobber your specs. Use when working in a repo with an openspec/ directory — especially before archiving a change, after rebasing or merging the base branch, or when a change's delta touches a requirement that already exists on the base branch. Runs `concord check` (stale-base drift, missing/renamed targets, name collisions) and `concord overlap` (a requirement claimed by two open changes), and explains how to resolve each finding.
allowed-tools: Bash(concord:*), Bash(npx @lucinate-ai/concord:*), Read, Grep, Glob
license: Apache-2.0
compatibility: Requires a repo with an openspec/ directory and git history. Uses the concord CLI (`@lucinate-ai/concord`).
metadata:
  author: Concord Contributors
  version: "1.1.0"
  source: "https://github.com/lucinate-ai/concord/blob/v1.1.0/skills/concord/SKILL.md"
---

# concord — catch OpenSpec drift and overlap before archiving

concord is a CLI companion for [OpenSpec](https://github.com/Fission-AI/OpenSpec) repos. An
OpenSpec `MODIFIED` delta carries the **full new text** of a requirement and replaces that block
by name at archive time, recording nothing about the base it was derived from. So if the
requirement moved on the base branch after your delta was written, archiving **silently
overwrites** that change — no git conflict, no validation error. concord reconstructs the
before/after from git history and turns that silent data loss into a loud, early failure.

You (the agent) are usually the one authoring changes, rebasing branches, and archiving deltas.
Run concord at the moments below so a clobber can't slip through.

## When to run concord

Run **`concord check`** (and, when more than one change is open, **`concord overlap`**):

- **Before archiving a change.** Never archive while a `drift`, `removed-upstream`, or
  `target-missing` finding is unresolved — archiving would apply a stale or invalid block.
- **After rebasing or merging the base branch** into a change branch. The merge-base has moved,
  so previously-clear findings may now fire and previously-failing ones may clear. Re-run.
- **When opening or editing a change whose delta targets a requirement that already exists** on
  the base branch (any `MODIFIED`, `REMOVED`, or `RENAMED` op, or an `ADDED`/`RENAMED`-to name
  that might already be taken). This is where drift and name collisions are introduced.
- **In CI**, as a gate on every PR that touches `openspec/` — already wired up here, see
  [Running concord in CI](#running-concord-in-ci).

If the user asks you to review, land, or archive OpenSpec work and you haven't run concord this
session, run it first.

## How to invoke

Three commands:

```bash
concord check      # every open change's delta targets vs the base branch
concord overlap    # any requirement claimed by more than one open change
concord ci         # check + overlap together — one gate, one exit code (the usual CI gate)
```

If the `concord` binary is not on `PATH`, fall back to npx (no install needed):

```bash
npx @lucinate-ai/concord check
npx @lucinate-ai/concord overlap
npx @lucinate-ai/concord ci
```

Useful flags (`check` unless noted):

| Flag | Purpose |
|---|---|
| `--json` | machine-readable output — **prefer this when you need to reason over findings** |
| `--base <ref>` | base ref to compare against (default: origin's default branch). In CI, `git fetch origin main` first, then `--base origin/main` |
| `--change <id>` | check a single change only (the change directory name under `openspec/changes/`) |
| `--dir <path>` | OpenSpec directory relative to the repo root (default `openspec`) |
| `-C, --cwd <path>` | run as if started in `<path>` |
| `-C`, `--dir`, `--json` | also accepted by `overlap` |
| `-C`, `--dir`, `--base`, `--change`, `--json` | all accepted by `ci` (it takes check's flags) |

When you need to enumerate findings programmatically, run with `--json` and read the structured
result rather than parsing the human-rendered text:

```bash
concord check --json    # -> { base, findings: [...], changesChecked, operationsChecked, ... }
concord overlap --json  # -> { overlaps: [...] }
concord ci --json       # -> { check: { ... }, overlap: { ... } }
```

## Exit codes

concord's exit code is a CI contract — use it, don't just scan stdout:

- **`0`** — clean, no findings. Safe to proceed.
- **`1`** — findings or overlaps were reported (for `ci`, either one). Resolve them (see below)
  before archiving.
- **`2`** — usage or environment error (bad flag, not a git repo, no `openspec/` dir, base ref
  can't be resolved). This is **not** a spec finding — fix the invocation or environment; don't
  treat it as drift/overlap.

## Findings and how to resolve them

`concord check` emits these kinds:

- **`drift`** — a requirement your delta modifies/removes/renames **changed on the base branch**
  since your branch diverged. Archiving this delta would silently discard that base-branch change.
  *Fix:* re-derive the affected delta block against the current base text, then merge or rebase
  the base branch so the merge-base advances and the finding clears. concord prints a redline
  (baseline at the merge-base vs current at the base tip) to show exactly what moved.
- **`removed-upstream`** — your delta's target requirement was **deleted or renamed** on the base
  branch. *Fix:* your op can no longer apply as written — point it at the requirement's new name,
  or drop/rework the op if the requirement is gone.
- **`target-missing`** — the requirement name in your delta **matches nothing** on the base (a
  typo or a name that never existed). Archive can't apply it. *Fix:* correct the requirement name
  in the delta to match the real header exactly (names are matched trim-only, case-sensitive
  beyond that — copy the header from the spec).
- **`name-collision`** — an `ADDED` (or `RENAMED`-to) name **already exists** on the base branch,
  so archiving would collide. *Fix:* rename your addition, or switch the op to `MODIFIED` if you
  actually mean to change the existing requirement.

`concord overlap` emits:

- **`overlap`** — the same requirement is claimed by **more than one open change**. *Fix:* the two
  changes need to coordinate — split the requirement, sequence the changes, or fold them together
  — so only one open change owns each requirement at archive time.

## Running concord in CI

This repo already runs concord on every PR via
[`.github/workflows/concord.yml`](../../../.github/workflows/concord.yml), which checks out with
full history and calls `lucinate-ai/concord/actions/ci@v1`. Don't hand-roll an
`npx @lucinate-ai/concord` step alongside it — the action installs a pinned concord, runs it, and
renders findings and overlaps as pull request annotations and a job step summary.

Three composite actions are available, referenced by path:

| Action | Runs | Use when |
|---|---|---|
| `lucinate-ai/concord/actions/ci` | `concord ci` | combined check + overlap gate — **the usual default, and what this repo uses** |
| `lucinate-ai/concord/actions/check` | `concord check` | only drift / target / collision checks |
| `lucinate-ai/concord/actions/overlap` | `concord overlap` | only cross-change overlap |

Two things to get right if you touch that workflow:

- **Keep the floating major tag pinned** — `uses: lucinate-ai/concord/actions/ci@v1` — so patch
  and minor updates arrive without editing the workflow (Dependabot's `github-actions` ecosystem
  bumps the major when one lands).
- **Keep the full-history checkout** (`actions/checkout` with `fetch-depth: 0`) for the `ci` and
  `check` actions. They reconstruct the merge-base from git history, so a shallow checkout breaks
  drift detection and can report a false clean result. `overlap` needs no base and runs on a
  shallow checkout.

Each action takes inputs — `dir`, `version`, `fail-on-findings`, `annotations`, `summary`, plus
`base` and `change` on `ci`/`check` — with sensible defaults. See
[docs/github-actions.md](https://github.com/lucinate-ai/concord/blob/main/docs/github-actions.md)
for the full list.

## Notes

- concord reads git history and the working tree only — no sidecar files, no config, nothing to
  adopt. If the specs are in git, it works.
- Its parsers deliberately mirror OpenSpec v1.6.0's own header patterns, so a block concord
  recognises is exactly a block OpenSpec would archive.
- concord does **not** modify specs or deltas; it only reports. Resolving findings is ordinary
  git hygiene (re-derive the block, then rebase/merge).
- This skill is vendored from
  [lucinate-ai/concord](https://github.com/lucinate-ai/concord) at v1.1.0 (see `metadata.source`
  in the frontmatter). Re-sync it from upstream rather than editing the guidance in place, keeping
  the repo-specific CI notes above.
