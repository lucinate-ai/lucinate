# Slash commands

Slash commands are local TUI commands that begin with `/`. They are intercepted by `handleSlashCommand()` in `internal/tui/commands.go` before any message is sent to the gateway. The function returns `(handled bool, cmd tea.Cmd)` — if `handled` is true, the input is consumed locally and never forwarded.

## Dispatch

Input that starts with `/` and contains no spaces is matched case-insensitively against a `switch` statement of built-in commands. Commands that accept an argument (e.g. `/agent foo`, `/model sonnet`, `/think high`) are matched by prefix check after the switch.

Slash input that isn't a built-in is checked against the loaded skill names: if the first token (`/foo` from `/foo bar`) matches a skill, `handleSlashCommand` returns `(false, nil)` and lets the regular send path expand it via `expandSkillReferences` — see [skills.md](skills.md#activation). If it matches neither a built-in nor a skill, an error system message is shown.

## Built-in commands

| Command | What it does |
|---|---|
| `/agent` | Return to the agent picker (alias for `/agents`) |
| `/agent <name>` | Switch to a named agent without going through the picker — see below |
| `/agents` | Return to the agent picker by emitting `goBackMsg{}` |
| `/cancel` | Cancel the in-progress response (also triggered by Escape) — see [chat-ux.md](chat-ux.md) |
| `/clear` | Wipe `m.messages` from the local display (does not affect gateway history) |
| `/compact` | Compact the session context — see [sessions.md](sessions.md#compact-and-reset) |
| `/config` | Open the preferences view by emitting `showConfigMsg{}` |
| `/connections` | Open the connections picker mid-session, tearing down the active backend — see [connections.md](connections.md) |
| `/crons` | Open the cron browser filtered to the current agent — see [crons.md](crons.md) — **OpenClaw only** |
| `/crons all` | Open the cron browser unfiltered (jobs across all agents) — **OpenClaw only** |
| `/exit`, `/quit` | Exit via `tea.Quit` |
| `/help`, `/commands` | Print static help text; appends skill count if any are loaded |
| `/model <name>` | Switch model — see below |
| `/models` | Open the model picker (filter as you type) |
| `/reset` | Delete the session and start fresh — see [sessions.md](sessions.md#compact-and-reset) |
| `/routine <name>` | Activate a stored routine in the current session — see [routines.md](routines.md) |
| `/routines` | Open the routines manager (list/view/edit/delete) — see [routines.md](routines.md) |
| `/sessions` | Open the session browser — see [sessions.md](sessions.md#session-browser) |
| `/skills` | List discovered skills — see [skills.md](skills.md) |
| `/stats` | Show a token usage and cost table for the current session — **OpenClaw only** |
| `/status` | Show backend status — **OpenClaw only** |
| `/think` | Show the current thinking level — **OpenClaw only** |
| `/think <level>` | Set the thinking level — see [chat-ux.md](chat-ux.md#thinking-levels) — **OpenClaw only** |

Backend-only commands render a "not available on this connection" system message on connections that don't support them — see [connections.md](connections.md#capability-negotiation).

### /agent

`handleAgentCommand()` covers both shapes. With no argument it emits `goBackMsg{}`, returning to the agent picker just like `/agents`. With a name it calls `client.ListAgents()`, fuzzy-matches case-insensitively against agent names and IDs (exact match first, then substring), then calls `client.CreateSession(agentID, "main")` and emits the same `sessionCreatedMsg` the picker selection path uses — so the chat view rebuild is identical to picking from the list. Lookup failures (no match, or backend error) are rendered inline as a system message via `agentSwitchFailedMsg` rather than bouncing the user back to the picker.

### /model

`handleModelCommand()` requires a name argument; bare `/model` emits an inline hint pointing at `/models`. With a name it calls `client.ModelsList()` to retrieve available models from the gateway, fuzzy-matches against model IDs and names (exact match first, then substring), then calls `client.SessionPatchModel(sessionKey, modelID)` and updates `m.modelID` in the header. `/models` (plural) opens the picker via `showModelPickerMsg`.

### /stats

Stats are loaded asynchronously via `client.SessionUsage()` on chat init and after each message exchange. `/stats` formats `m.stats` through `formatStatsTable()` in `internal/tui/render.go`, which produces a text table of input/output/cache tokens and cost breakdown.

## Tab completion

A live menu (rendered between the conversation viewport and the input) appears as soon as the cursor sits at the end of a completable token with at least one matching candidate. Two sources feed the same menu and the same Tab/Shift+Tab semantics:

- **Slash commands and skills** — `matchingSlashCommands(prefix)` (`completion.go`) returns built-ins from the static `slashCommands` slice in their curated order, followed by skill names. Source detection: `findSlashTokenAt(value, cursorByte)`.
- **Agent names** — the argument of `/agent <name>`. `matchingAgentNames(prefix)` returns every loaded agent whose lowercased form has the prefix as a prefix, preserving each agent's original casing. Source detection: `findAgentArgAt(value, cursorByte)`, which treats the entire tail of the line after `/agent ` as the token (so names with spaces complete in one shot).

`completionAtCursor()` resolves the active source — slash commands take priority — and returns a `completionContext{start, cursorByte, prefix, candidates}`. The Tab handler dispatches a single `handleCompletionTab(ctx)` over this context, applying bash-style menu-complete semantics:

- One match → full completion in place.
- Multiple matches with a longest common prefix beyond what the user typed → the input is extended up to that LCP. `longestCommonPrefixFold(strs)` computes a case-insensitive LCP using the first candidate's casing — agent names like `Main` and `Mail` collapse to `Mai`, slash candidates (already lowercase) behave identically to the byte-wise variant.
- Multiple matches at the LCP → enter cycle mode. The first Tab snapshots the candidate list into `m.completion.cycleCandidates`, sets `cycleIndex = 0`, and replaces the input with the first candidate. Subsequent Tab presses advance the index modulo the snapshot length; Shift+Tab decrements with the same wraparound. The snapshot persists across presses because Tab returns early before `refreshCompletionMenu` runs.

Any non-Tab keystroke routes through `refreshCompletionMenu`, which clears `cycling` and recomputes `candidates` from the current textarea contents via `completionAtCursor()`. The menu auto-hides when no source applies (whitespace breaks a slash token, cursor leaves end-of-line in the agent-arg context, the input is cleared, or the message is sent — `Reset()` calls in the Enter handler explicitly invoke `refreshCompletionMenu` so the menu doesn't outlive the input).

The curated order in `slashCommands` (e.g. `/agents` before `/agent`, `/model` before `/models`) now only breaks ties for the inline ghost-hint and the legacy `completeSlashCommand` callers — Tab uses LCP, so the order no longer steers it.

`setTextareaToValueWithCursor` performs the in-place replacement and repositions the cursor at the end of the inserted text.

### Inline ghost-hint fallback

`slashCommandHint` and `agentNameHint` still drive a single-line greyed-out hint in the help bar — but only as a fallback for short terminals where the menu can't render. With the menu visible, the help line switches to `Tab: extend · Shift+Tab: back · N matches`.

### Layout

`chatModel.baseViewportHeight` records the viewport height with the menu hidden; `applyLayout()` shrinks the viewport by `menuRowsToRender()` whenever menu state changes, so the conversation pane reflows cleanly. The menu suppresses itself entirely when the baseline cannot leave at least `completionMenuViewportFloor` rows for the conversation — Tab still does LCP extension on the underlying state. Candidate counts above `completionMenuMaxRows` collapse the tail into a `+N more` line.

### Agent name source

The chat model fetches the agent list once on init via `loadAgentNames()` and stores display names in `m.agentNames`; completion silently degrades to a no-op when the list hasn't loaded yet or the backend errored (`matchingAgentNames` returns nil, so `completionAtCursor` reports an empty candidate list and the menu stays hidden). `findAgentArgAt` recognises the argument context only when the cursor sits at end-of-line and the line begins with `/agent ` (single space). Empty prefix matches every agent — Tab on `/agent ` opens the menu listing the full roster, with the LCP/cycle flow taking over from there.

## Confirmation pattern

Destructive commands (`/compact`, `/reset`) use a two-step confirmation. On first invocation a `pendingConfirmation` struct is stored on the model containing the prompt string, an optional `runningStatus` line, and an action closure. The prompt is rendered as an ephemeral notification above the input — see [chat-ux.md → Notifications](chat-ux.md#notifications). On the next Enter keypress, if the input is `y` or `yes` the closure is executed; anything else cancels. This prevents accidental data loss.

When `runningStatus` is set, the confirmation handler also appends a pending system row (`pending: true`) carrying that status text to the chat scrollback. The renderer animates the same braille spinner used for in-flight assistant turns next to the row, and `hasStreamingMessage` keeps `spinnerTickCmd` firing until the action returns. The result handler (`sessionCompactedMsg`, `sessionClearedMsg`) calls `replacePendingSystem` to swap the placeholder for the outcome line in place — no stale "Compacting session…" stuck above the result.

## Routine-active navigation gate

Slash commands that strand or replace the chat model — `/agents`, `/agent <name>`, `/sessions`, `/crons`, `/crons all`, `/connections`, `/routine <name>`, `/routines` — route through `gateNavigation()` (`internal/tui/routines_chat.go`) when a routine is active. A `pendingNavConfirm` is set, the prompt is rendered as a notification, and the Enter handler resolves it: `y` cancels any in-flight turn, ends the routine cleanly (closing the log file), and dispatches the navigation; `n` or Esc dismisses the prompt and the routine continues. The state is independent of the generic `pendingConfirmation` so the two flows don't compete. See [routines.md → Slash commands and gating](routines.md#slash-commands-and-gating) for the full rationale.
