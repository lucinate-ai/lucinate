# Agent Skills

Agent skills are locally-stored prompt templates that users can inject into a chat session via slash commands (e.g. `/review`). They allow users to define reusable instructions without modifying gateway configuration.

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

The agent doesn't receive skill information unless it's explicitly included in a message. On the **first user message** of a session, `withSkillCatalog()` (`chat.go`) prepends a skill listing to the outgoing text before it is sent to the gateway:

```
System: Available agent skills (activate with /skill-name):
System:   - review: Perform a code review
```

Lines are prefixed with `System: ` using the convention described in [message-rendering.md](message-rendering.md). The catalog is sent only once per session (`skillCatalogSent` flag).

## Activation

When the user types `/<name>`, `handleSlashCommand()` (`commands.go`) matches it against loaded skills (case-insensitive). On a match:

1. The skill body is wrapped with a `[Skill: <name>]` header.
2. Every line is prefixed with `System: ` (see [message-rendering.md](message-rendering.md)).
3. The resulting text is sent to the gateway as the message content.
4. The chat display shows the user message as `/<name>` (not the full body).

Unknown slash commands that don't match a built-in or a skill name show an error.

Tab completion (`completeSlashCommand()` in `commands.go`) includes skill names alongside built-in commands.

## UI commands

| Command | Behaviour |
|---|---|
| `/skills` | Lists all discovered skills with their descriptions |
| `/<name>` | Activates the named skill |
| `/help` | Mentions how many skills are loaded |

The count of loaded skills also appears in the `/stats` table.

## Adding a new skill

Create a directory under one of the scanned paths:

```
~/.agents/skills/my-skill/SKILL.md
```

Skills are discovered once at startup — a restart is required to pick up new files.
