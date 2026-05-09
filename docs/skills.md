# Agent Skills

Agent skills are locally-stored prompt templates that users can inject into a chat session via slash commands (e.g. `/review`). They allow users to define reusable instructions without modifying gateway configuration. A skill can be invoked on its own (`/review`) or referenced mid-message (`use /review on the diff`) — see [Activation](#activation).

## Skill file format

Each skill lives in its own directory and must contain a `SKILL.md` file with YAML frontmatter:

```
---
name: review
description: Perform a code review
---

Please review the following code for correctness, style, and potential bugs. Be concise.
```

Both `name` and `description` are required. The body is arbitrary markdown and is sent verbatim to the agent when the skill is activated.

## Discovery

Skills are discovered at startup by `discoverSkills()` in `internal/tui/skills.go`. It scans these directories in order:

1. `<cwd>/.agents/skills/*/SKILL.md`
2. `<cwd>/.agent/skills/*/SKILL.md`
3. `~/.agents/skills/*/SKILL.md`
4. `~/.agent/skills/*/SKILL.md`

CWD directories are scanned first — if two skills share the same `name`, the first one found wins. Symlinked skill directories are resolved via `os.Stat`.

Discovery runs as a Bubble Tea command in `chatModel.Init()` and returns a `skillsDiscoveredMsg` which stores the skill list in `chatModel.skills`.

## Catalog injection

The agent doesn't receive skill information unless it's explicitly included in a message. The chat layer passes the discovered skills through `ChatSendParams.Skills`; the OpenClaw backend's `takePendingCatalog()` (`internal/backend/openclaw/openclaw.go`) prepends a skill listing to the **first user message** of each session:

```
System: Available agent skills (activate with /skill-name):
System:   - review: Perform a code review
```

Lines are prefixed with `System: ` using the convention described in [message-rendering.md](message-rendering.md). The catalog is sent only once per session — a per-session flag, mutex-guarded against concurrent sends, prevents re-emission. See [backend_openclaw.md](backend_openclaw.md) for the backend-side detail.

## Activation

A `/skill-name` token is recognised when it sits at the start of a message *or* mid-prose preceded by whitespace, with token characters `[A-Za-z0-9_-]`. Matching is case-insensitive.

`handleSlashCommand()` (`commands.go`) returns `(false, nil)` for any `/`-prefixed input whose first token names a known skill, deferring to the regular Enter-handler send path. Unknown slashes that aren't built-ins still produce an error system message there.

The send path runs `expandSkillReferences(text, skills)` (`skills.go`) before dispatching. When at least one matched skill is found it produces a payload of the form:

```
Please use the following skill:

<local-agent-skill name="review">
<skill body>
</local-agent-skill>

run the "review" skill above on the diff
```

Rules applied by `expandSkillReferences`:

- Each unique matched skill produces one `<local-agent-skill name="...">…</local-agent-skill>` block. Order follows first-occurrence in the prose.
- Each `/skill-name` token in the prose is replaced with `the "<canonical-name>" skill above`. The canonical name comes from the skill's frontmatter, regardless of how the user typed it.
- Multiple distinct skills produce a plural preamble (`Please use the following skills:`) and one envelope per skill.
- A "bare" message — the entire trimmed input is a single `/skill-name` — collapses the prose to `use the "<name>" skill above immediately`.

The visible chat row shows the user-typed text. On history reload the rendered text reflects the post-substitution payload (the gateway has no record of the original prose) — this is a known cosmetic divergence.

`stripLocalAgentSkillBlocks` (`history.go`) elides the preamble line and every `<local-agent-skill>...</local-agent-skill>` block when restoring user messages from history, alongside the `System:` line strip used for the catalog.

### Tab completion

Skill names participate in the same completion menu as built-in slash commands. `matchingSlashCommands(prefix)` (`completion.go`) returns every match for the current prefix — built-ins in curated order, then skills — and the menu rendered between the viewport and input lists them all. Tab extends the input to the longest common prefix; if multiple skills share a prefix, repeated Tab cycles them (Shift+Tab cycles back). Mid-message completion still works: `use /rev<TAB>` resolves the slash token under the cursor regardless of position. See [commands.md → Tab completion](commands.md#tab-completion) for the cycle/LCP machinery.

Completion only fires when the cursor sits at the end of the slash token (next character is whitespace or end-of-buffer); inserting mid-token would clobber the trailing characters and `findSlashTokenAt` returns ok=false.

## UI commands

| Command | Behaviour |
|---|---|
| `/skills` | Lists all discovered skills with their descriptions |
| `/<name>` | Activates the named skill (alone or with prose) |
| `/help` | Mentions how many skills are loaded |

The count of loaded skills also appears in the `/stats` table.

## Adding a new skill

Create a directory under one of the scanned paths:

```
~/.agents/skills/my-skill/SKILL.md
```

Skills are discovered once at startup — a restart is required to pick up new files.
