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

`completeSlashCommand(prefix)` iterates the static `slashCommands` slice and then the loaded skill names. The first prefix match wins. Order in `slashCommands` is significant where one command is a prefix of another: `/agents` is listed before `/agent` and `/model` before `/models` so that tab-completing `/age` and `/mod` resolves to the longer-established command — extending a completion by one character is cheaper than backspacing.

The Tab handler (`chat.go`) operates on the slash token at the cursor — not just at the start of input — so completion works mid-message and within multi-line input. `findSlashTokenAt(value, cursorByte)` walks back from the cursor to a `/` that is at the start of input or preceded by whitespace, requiring the cursor to sit at the end of the token (next character is whitespace or EOF). `setTextareaToValueWithCursor` performs the in-place replacement and repositions the cursor at the end of the inserted completion. `slashCommandHint(value, cursorByte)` returns the typed prefix and the suffix shown greyed-out in the input bar.

### Agent name completion

After `/agent ` the next token is treated as an agent name and completed against the cached agent list. The chat model fetches the list once on init via `loadAgentNames()` and stores display names in `m.agentNames`; completion silently degrades to no-op if the list hasn't loaded yet or the backend errored. `findAgentArgAt(value, cursorByte)` recognises the argument context only when the cursor sits at end-of-line and the line begins with `/agent ` (single space); the entire tail of the line is the token, so agent names containing spaces are completed in one shot. `completeAgentName` does a case-insensitive prefix match — empty prefix completes to the first known agent. `agentNameHint` mirrors `slashCommandHint` and feeds the same greyed-out hint renderer.

## Confirmation pattern

Destructive commands (`/compact`, `/reset`) use a two-step confirmation. On first invocation a `pendingConfirmation` struct is stored on the model containing the prompt string and an action closure. The prompt is displayed as a system message. On the next Enter keypress, if the input is `y` or `yes` the closure is executed; anything else cancels. This prevents accidental data loss.
