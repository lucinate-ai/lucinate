# Slash commands

Slash commands are local TUI commands that begin with `/`. They are intercepted by `handleSlashCommand()` in `internal/tui/commands.go` before any message is sent to the gateway. The function returns `(handled bool, cmd tea.Cmd)` — if `handled` is true, the input is consumed locally and never forwarded.

## Dispatch

Input that starts with `/` and contains no spaces is matched case-insensitively against a `switch` statement of built-in commands. Commands that accept an argument (e.g. `/model sonnet`, `/think high`) are matched by prefix check after the switch.

Unrecognised slash commands fall through to a skill-name lookup. If no skill matches, an error system message is shown (see [skills.md](skills.md#activation)).

## Built-in commands

| Command | What it does |
|---|---|
| `/agents` | Return to the agent picker by emitting `goBackMsg{}` |
| `/clear` | Wipe `m.messages` from the local display (does not affect gateway history) |
| `/compact` | Compact the session context — see [sessions.md](sessions.md#compact-and-reset) |
| `/config` | Open the preferences view by emitting `showConfigMsg{}` |
| `/exit`, `/quit` | Exit via `tea.Quit` |
| `/help`, `/commands` | Print static help text; appends skill count if any are loaded |
| `/model` | List available models |
| `/model <name>` | Switch model — see below |
| `/reset` | Delete the session and start fresh — see [sessions.md](sessions.md#compact-and-reset) |
| `/sessions` | Open the session browser — see [sessions.md](sessions.md#session-browser) |
| `/skills` | List discovered skills — see [skills.md](skills.md) |
| `/stats` | Show a token usage and cost table for the current session |
| `/think` | Show the current thinking level |
| `/think <level>` | Set the thinking level — see [chat-ux.md](chat-ux.md#thinking-levels) |

### /model

`handleModelCommand()` calls `client.ModelsList()` to retrieve available models from the gateway. When a name argument is given it fuzzy-matches against model IDs and names (exact match first, then substring). On a match it calls `client.SessionPatchModel(sessionKey, modelID)` and updates `m.modelID` in the header.

### /stats

Stats are loaded asynchronously via `client.SessionUsage()` on chat init and after each message exchange. `/stats` formats `m.stats` through `formatStatsTable()` in `internal/tui/render.go`, which produces a text table of input/output/cache tokens and cost breakdown.

## Tab completion

`completeSlashCommand(prefix)` iterates the static `slashCommands` slice and then the loaded skill names. The first prefix match wins. `slashCommandHint()` derives the completion suffix shown greyed-out in the input bar. Pressing Tab inserts the full command via `m.textarea.SetValue(match)` and `CursorEnd()`.

## Confirmation pattern

Destructive commands (`/compact`, `/reset`) use a two-step confirmation. On first invocation a `pendingConfirmation` struct is stored on the model containing the prompt string and an action closure. The prompt is displayed as a system message. On the next Enter keypress, if the input is `y` or `yes` the closure is executed; anything else cancels. This prevents accidental data loss.
