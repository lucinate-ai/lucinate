# Agent skills — lessons and rationale

The behavioural contract for agent skills lives in
[`openspec/specs/skills/spec.md`](../openspec/specs/skills/spec.md) — the skill file format,
startup discovery, catalog injection, activation and reference expansion, tab completion, and UI
commands are all captured there as requirements and scenarios. This file keeps the hard-won
lessons, pitfalls, and design rationale behind that flow: the "why it works this odd way" that
the spec's requirements don't dwell on.

## The agent only learns about skills once, on the first turn

The agent doesn't receive any skill information unless a message carries it. Rather than repeat
the catalog on every message, the OpenClaw backend's `takePendingCatalog()`
(`internal/backend/openclaw/openclaw.go`) prepends the listing to the **first user message** of
each session and never again — a per-session flag, mutex-guarded against concurrent sends,
prevents re-emission. The mutex isn't decoration: two sends racing on a fresh session would
otherwise both see the flag unset and emit the catalog twice. The catalog lines ride the
`System: ` prefix convention (see the `message-rendering` spec) so they render as system context
rather than user prose.

## Why activation rewrites the message instead of calling a command

A `/review` token isn't dispatched as a command — `handleSlashCommand()` (`commands.go`) returns
`(false, nil)` for any slash input whose first token names a known skill and lets it fall through
to the normal send path. The rewriting happens in `expandSkillReferences(text, skills)`
(`skills.go`), and its four rules exist because a skill reference can appear anywhere in free
prose, not just at the start:

- Each unique matched skill produces one `<local-agent-skill name="...">…</local-agent-skill>`
  block, ordered by first occurrence — so the agent sees the skill bodies inline rather than
  having to fetch them.
- Each `/skill-name` token in the prose is replaced with `the "<canonical-name>" skill above`.
  The canonical name comes from the frontmatter regardless of how the user typed it, so
  case-insensitive or shorthand references still point the agent at the right block.
- Multiple distinct skills switch to a plural preamble and one envelope each — the agent needs to
  know it has several to choose from.
- A "bare" message — the whole trimmed input is a single `/skill-name` — collapses to `use the
  "<name>" skill above immediately`, because there's no surrounding prose to weave the reference
  into.

This is why the feature works mid-message (`use /review on the diff`) at all: activation is a
text substitution over the message, not a command with its own send path.

## History strips the skill envelopes — and a known cosmetic divergence

When restoring user messages from history, `stripLocalAgentSkillBlocks` (`history.go`) elides the
preamble line and every `<local-agent-skill>...</local-agent-skill>` block, alongside the
`System:` line strip used for the catalog — otherwise the machinery we injected would show up as
if the user had typed it.

The known gotcha: the visible chat row shows the user-typed text live, but on history reload the
rendered text reflects the post-substitution payload. The gateway has no record of the original
prose, so there's nothing to restore it from. This is a cosmetic divergence only — the agent
received the same thing either way.

## Discovery path ordering: CWD wins on purpose

`discoverSkills()` (`internal/tui/skills.go`) scans CWD paths (`.agents`/`.agent`) before home
paths, and the first skill found for a given `name` wins. The ordering is deliberate: a
project-local skill should be able to shadow a personal one of the same name, so per-project
overrides beat your global defaults rather than the other way round. Symlinked skill directories
are resolved via `os.Stat` so a linked skill folder is still picked up.

Discovery runs once at startup, so newly added skills need a restart to appear — there's no
watcher.
