# Agent Skills Specification

## Purpose

Agent skills are locally-stored prompt templates that users can inject into a chat session via slash commands (e.g. `/review`). They allow users to define reusable instructions without modifying gateway configuration. A skill can be invoked on its own (`/review`) or referenced mid-message (`use /review on the diff`). This spec covers the skill file format, startup discovery, catalog injection into the session, activation and reference expansion, tab completion, UI commands, and how new skills are added.

## Requirements

### Requirement: Skill file format

Each skill SHALL live in its own directory and MUST contain a `SKILL.md` file with YAML frontmatter. The frontmatter MUST provide both a `name` and a `description` — both are required. The body is arbitrary markdown and SHALL be sent verbatim to the agent when the skill is activated.

```
---
name: review
description: Perform a code review
---

Please review the following code for correctness, style, and potential bugs. Be concise.
```

#### Scenario: Well-formed skill file

- **GIVEN** a directory containing a `SKILL.md` with `name` and `description` in its frontmatter and a markdown body
- **WHEN** the skill is activated
- **THEN** the body is sent verbatim to the agent

#### Scenario: Missing required frontmatter

- **GIVEN** a `SKILL.md` missing either `name` or `description`
- **WHEN** skills are discovered
- **THEN** the file is not a valid skill because both `name` and `description` are required

### Requirement: Startup skill discovery

Skills SHALL be discovered at startup by `discoverSkills()` in `internal/tui/skills.go`, which scans these directories in order:

1. `<cwd>/.agents/skills/*/SKILL.md`
2. `<cwd>/.agent/skills/*/SKILL.md`
3. `~/.agents/skills/*/SKILL.md`
4. `~/.agent/skills/*/SKILL.md`

CWD directories SHALL be scanned first — if two skills share the same `name`, the first one found wins. Symlinked skill directories SHALL be resolved via `os.Stat`. Discovery SHALL run as a Bubble Tea command in `chatModel.Init()` and return a `skillsDiscoveredMsg` which stores the skill list in `chatModel.skills`. Skills are discovered once at startup, so a restart is required to pick up new files.

#### Scenario: Scan order and duplicate name resolution

- **GIVEN** two skills with the same `name`, one under a CWD directory and one under a home directory
- **WHEN** `discoverSkills()` scans the four directories in order
- **THEN** the CWD skill is found first and wins

#### Scenario: Symlinked skill directory

- **GIVEN** a skill directory reached via a symlink
- **WHEN** discovery runs
- **THEN** it is resolved via `os.Stat` and included

#### Scenario: Discovery result stored in the model

- **WHEN** discovery completes
- **THEN** a `skillsDiscoveredMsg` is returned and the skill list is stored in `chatModel.skills`

### Requirement: Skill catalog injection into the session

The agent SHALL NOT receive skill information unless it is explicitly included in a message. The chat layer SHALL pass the discovered skills through `ChatSendParams.Skills`, and the OpenClaw backend's `takePendingCatalog()` (`internal/backend/openclaw/openclaw.go`) SHALL prepend a skill listing to the **first user message** of each session:

```
System: Available agent skills (activate with /skill-name):
System:   - review: Perform a code review
```

Lines SHALL be prefixed with `System: ` using the convention described in the `message-rendering` spec. The catalog SHALL be sent only once per session — a per-session flag, mutex-guarded against concurrent sends, prevents re-emission. See the `backend-openclaw` spec for the backend-side detail.

#### Scenario: Catalog prepended to the first message

- **GIVEN** discovered skills passed through `ChatSendParams.Skills`
- **WHEN** the first user message of a session is sent
- **THEN** `takePendingCatalog()` prepends the `System: Available agent skills` listing with one `System:   - name: description` line per skill

#### Scenario: Catalog sent once per session

- **GIVEN** the catalog has already been emitted for a session
- **WHEN** further messages are sent, including concurrently
- **THEN** the per-session flag, mutex-guarded, prevents re-emission

### Requirement: Skill activation and reference expansion

A `/skill-name` token SHALL be recognised when it sits at the start of a message *or* mid-prose preceded by whitespace, with token characters `[A-Za-z0-9_-]`. Matching SHALL be case-insensitive.

`handleSlashCommand()` (`commands.go`) SHALL return `(false, nil)` for any `/`-prefixed input whose first token names a known skill, deferring to the regular Enter-handler send path. Unknown slashes that aren't built-ins SHALL still produce an error system message there.

The send path SHALL run `expandSkillReferences(text, skills)` (`skills.go`) before dispatching. When at least one matched skill is found it SHALL produce a payload of the form:

```
Please use the following skill:

<local-agent-skill name="review">
<skill body>
</local-agent-skill>

run the "review" skill above on the diff
```

`expandSkillReferences` SHALL apply these rules:

- Each unique matched skill produces one `<local-agent-skill name="...">…</local-agent-skill>` block. Order follows first-occurrence in the prose.
- Each `/skill-name` token in the prose is replaced with `the "<canonical-name>" skill above`. The canonical name comes from the skill's frontmatter, regardless of how the user typed it.
- Multiple distinct skills produce a plural preamble (`Please use the following skills:`) and one envelope per skill.
- A "bare" message — the entire trimmed input is a single `/skill-name` — collapses the prose to `use the "<name>" skill above immediately`.

The visible chat row SHALL show the user-typed text. On history reload the rendered text reflects the post-substitution payload (the gateway has no record of the original prose) — this is a known cosmetic divergence.

`stripLocalAgentSkillBlocks` (`history.go`) SHALL elide the preamble line and every `<local-agent-skill>...</local-agent-skill>` block when restoring user messages from history, alongside the `System:` line strip used for the catalog.

#### Scenario: Slash command deferred to the send path

- **GIVEN** input whose first `/`-prefixed token names a known skill
- **WHEN** `handleSlashCommand()` processes it
- **THEN** it returns `(false, nil)` and defers to the regular Enter-handler send path

#### Scenario: Unknown slash command

- **GIVEN** a `/`-prefixed token that is neither a built-in nor a known skill
- **WHEN** `handleSlashCommand()` processes it
- **THEN** an error system message is produced

#### Scenario: Mid-prose reference expanded

- **GIVEN** the message `run /review on the diff`
- **WHEN** `expandSkillReferences` runs before dispatch
- **THEN** a `<local-agent-skill name="review">` block wraps the skill body
- **AND** the `/review` token is replaced with `the "review" skill above`

#### Scenario: Multiple distinct skills

- **GIVEN** prose referencing two distinct known skills
- **WHEN** the references are expanded
- **THEN** the plural preamble `Please use the following skills:` is used and one envelope is emitted per skill, ordered by first occurrence

#### Scenario: Bare skill message

- **GIVEN** input whose entire trimmed text is a single `/skill-name`
- **WHEN** the reference is expanded
- **THEN** the prose collapses to `use the "<name>" skill above immediately`

#### Scenario: History reload strips skill envelopes

- **WHEN** user messages are restored from history
- **THEN** `stripLocalAgentSkillBlocks` elides the preamble line, every `<local-agent-skill>...</local-agent-skill>` block, and the `System:` catalog lines

### Requirement: Skill name tab completion

Skill names SHALL participate in the same completion menu as built-in slash commands. `matchingSlashCommands(prefix)` (`completion.go`) SHALL return every match for the current prefix — built-ins in curated order, then skills — and the menu rendered between the viewport and input lists them all. Tab SHALL extend the input to the longest common prefix; if multiple skills share a prefix, repeated Tab cycles them and Shift+Tab cycles back. Mid-message completion SHALL still work: `use /rev<TAB>` resolves the slash token under the cursor regardless of position. See the `commands` spec (Tab completion) for the cycle/LCP machinery.

Completion SHALL only fire when the cursor sits at the end of the slash token (next character is whitespace or end-of-buffer); inserting mid-token would clobber the trailing characters and `findSlashTokenAt` returns ok=false.

#### Scenario: Completion menu lists built-ins then skills

- **GIVEN** a slash-token prefix
- **WHEN** `matchingSlashCommands(prefix)` runs
- **THEN** it returns built-in matches in curated order followed by skill matches

#### Scenario: Cycling shared prefixes with Tab

- **GIVEN** multiple skills sharing a prefix
- **WHEN** the user presses Tab repeatedly
- **THEN** the input extends to the longest common prefix and repeated Tab cycles the skills (Shift+Tab cycles back)

#### Scenario: Mid-message completion under the cursor

- **GIVEN** the input `use /rev` with the cursor at the end of the slash token
- **WHEN** the user presses Tab
- **THEN** the slash token under the cursor is resolved regardless of position

#### Scenario: No completion mid-token

- **GIVEN** the cursor sitting inside a slash token rather than at its end
- **WHEN** completion is attempted
- **THEN** `findSlashTokenAt` returns ok=false and completion does not fire

### Requirement: Skill UI commands

The system SHALL provide the following commands and reporting behaviour:

| Command | Behaviour |
|---|---|
| `/skills` | Lists all discovered skills with their descriptions |
| `/<name>` | Activates the named skill (alone or with prose) |
| `/help` | Mentions how many skills are loaded |

The count of loaded skills SHALL also appear in the `/stats` table.

#### Scenario: Listing skills

- **WHEN** the user runs `/skills`
- **THEN** all discovered skills are listed with their descriptions

#### Scenario: Loaded-skill count reported

- **WHEN** the user runs `/help` or `/stats`
- **THEN** `/help` mentions how many skills are loaded and the count also appears in the `/stats` table

### Requirement: Adding a new skill

To add a skill, the user SHALL create a directory under one of the scanned paths containing a `SKILL.md`, for example:

```
~/.agents/skills/my-skill/SKILL.md
```

Because skills are discovered once at startup, a restart SHALL be required to pick up new files.

#### Scenario: New skill picked up after restart

- **GIVEN** a new `SKILL.md` created under a scanned path such as `~/.agents/skills/my-skill/SKILL.md`
- **WHEN** the application is restarted
- **THEN** the new skill is discovered
