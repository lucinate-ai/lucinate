<p align="center">
  <img src="docs/hero.png" alt="lucinate" width="600" />
</p>

<h1 align="center">lucinate — the terminal-native AI chat client</h1>

<p align="center">
  Chat with your <a href="https://github.com/openclaw/openclaw">OpenClaw</a> agents, a <a href="https://github.com/nousresearch/hermes-agent">Hermes</a> profile, or any OpenAI-compatible endpoint (<a href="https://ollama.com">Ollama</a>, vLLM, LM Studio, OpenAI proper) — from the terminal. Streaming responses, markdown rendering, no mouse required.
</p>

<p align="center">
  <a href="https://github.com/lucinate-ai/lucinate/actions/workflows/ci.yml"><img src="https://github.com/lucinate-ai/lucinate/actions/workflows/ci.yml/badge.svg" alt="CI" /></a>
</p>

<p align="center">
  <img src="docs/demo.gif" alt="lucinate demo" width="600" />
</p>

No file browsers, no task boards, no dashboards. Just chat.

### Highlights

- **Multiple backends** — connect to an [OpenClaw](https://github.com/openclaw/openclaw) gateway, a [Hermes Agent](https://github.com/nousresearch/hermes-agent) profile, or any OpenAI-compatible endpoint ([Ollama](https://ollama.com), vLLM, LM Studio, llamafile, OpenAI proper). Switch between saved connections with `/connections`.
- **Streaming responses, conversation history, and multi-agent support**
- **Create and delete agents** directly from the TUI — gateway-managed for OpenClaw, or local `IDENTITY.md` + `SOUL.md` markdown for OpenAI-compatible backends. Delete is type-the-name to confirm, with an optional "keep files" toggle so you can drop the listing without nuking the content. Hermes profiles are configured server-side via `hermes profile create`
- **Markdown rendering** for assistant messages
- **Tool call cards** — when the agent invokes a tool, an inline card shows what's running, what arguments it got, and whether it succeeded or failed (OpenClaw)
- **Shell commands** — run locally with `!` or remotely on the gateway with `!!`
- **Message queueing** so you can keep typing while the agent is responding
- **Local agent skills** loaded from `~/.agents/skills/` — invoke as a slash command (`/review`) or drop one mid-message (`use /review on the diff`)
- **Routines** — author multi-step prompt sequences once, replay them with `/routine <name>`. Auto-advance after each reply, or step through manually. Manage them in-TUI with `/routines`.
- **Live token/cost stats** in the header bar (OpenClaw)
- **Thinking level control** via `/think` — tune reasoning depth per session (OpenClaw)
- **Cron browser** — list, edit, run, create, and duplicate scheduled jobs without leaving the terminal (OpenClaw)

## Install

With [Homebrew](https://brew.sh) (macOS / Linux):

```sh
brew install lucinate-ai/tap/lucinate
```

Or via the install script (macOS / Linux):

```sh
curl -fsSL https://raw.githubusercontent.com/lucinate-ai/lucinate/main/install/lucinate.sh | sh
```

On Windows, in PowerShell:

```powershell
irm https://raw.githubusercontent.com/lucinate-ai/lucinate/main/install/lucinate.ps1 | iex
```

Or, if you have Go 1.24+:

```sh
go install github.com/lucinate-ai/lucinate@latest
```

Or build from source:

```sh
git clone https://github.com/lucinate-ai/lucinate.git
cd lucinate
go build -o lucinate .
```

## Getting started

### 1. Configure lucinate

On first launch lucinate opens a **Connections** picker so you can add a backend.

Three connection types are supported:

- **[OpenClaw](docs/backend_openclaw.md)** — connect to an OpenClaw gateway over WebSocket. Auth uses Ed25519 device pairing.
- **[OpenAI-compatible](docs/backend_openai.md)** — Ollama, vLLM, LM Studio, llamafile, OpenAI - connect to any `/v1/chat/completions` endpoint.
- **[Hermes (Nous Research)](docs/backend_hermes.md)** — connect to a Hermes Agent profile's `/v1/responses` API server. One Lucinate connection maps to one Hermes profile; chat state lives server-side.

OpenClaw URLs can use `https`, `http`, `wss`, or `ws` — lucinate derives the WebSocket endpoint automatically. OpenAI-compatible and Hermes URLs are HTTP(S) base URLs ending in `/v1`.

Switch between saved connections at any time with `/connections` from the chat view. Use `n` to add a new one, `e` to edit, `d` to delete (with confirmation).

### 2. Connect

```sh
lucinate
```

#### First-time pairing (OpenClaw only)

On first run, lucinate generates an Ed25519 device identity under `~/.lucinate/identity/<endpoint>/` (keyed by gateway host) and sends a pairing request to the gateway. The TUI shows a "pairing required" prompt with the next steps. On the gateway host, run:

```sh
openclaw device list --pending
```

You should see lucinate's device ID. Approve it:

```sh
openclaw device approve <device-id>
```

Press Enter in the lucinate prompt to retry — the connection completes in place, the gateway issues a device token, and you land in the agent picker. No restart needed.

### 3. Pick an agent

Select an agent from the list to start chatting.

| Key | Action |
|-----|--------|
| `Enter` | Select agent |
| `n` | Create a new agent |
| `d` | Delete the highlighted agent (type-to-confirm) |
| `Ctrl+C` | Quit |

### 4. Chat

| Key | Action |
|-----|--------|
| `Enter` | Send message |
| `Alt+Enter` | Insert newline |
| `Ctrl+W` | Delete word |
| `PgUp` / `PgDn` | Scroll chat history |
| `Tab` | Show slash-command menu, extend to common prefix, then cycle |
| `Shift+Tab` | Cycle backward through matches |
| `Esc` | Back to agent list |
| `Ctrl+C` | Quit |

### Preferences

Use `/config` to open the preferences view. Settings are persisted to `~/.lucinate/config.json`.

| Setting | Default | Description |
|---------|---------|-------------|
| Completion notification | On | Ring the terminal bell when a response completes (only if the terminal isn't focused) |
| Check for updates on startup | On | Once a day, fetch a tiny manifest from `lucinate.ai` and show a subtle `↑` badge in the chat header when a newer release is out. No telemetry — just a single GET. |
| History limit | 50 | Number of messages loaded when restoring a session (range 10–500) |
| Connect timeout | 15s | Per-attempt deadline for the initial connect and each reconnect (range 5–300s — bump it for slow local LLMs) |

In the config view, use `Space` to toggle checkboxes and `←`/`→` to adjust numeric values.

## Commands

Type these in the chat input. As soon as you type `/`, a menu shows every matching command and skill. Tab extends the input to the longest common prefix; press it again at the prefix to cycle through candidates (Shift+Tab to cycle back). The same menu and Tab cycling apply to agent names after `/agent `.

| Command | Action |
|---------|--------|
| `/help` | Show available commands |
| `/agents` | Return to agent picker |
| `/agent <name>` | Switch to a named agent (fuzzy match) |
| `/cancel` | Cancel the in-progress response (also: `Esc`) |
| `/clear` | Clear chat display |
| `/compact` | Compact session context — server-side on OpenClaw, local summarisation pass on OpenAI-compatible backends (with confirmation) |
| `/config` | Open preferences |
| `/connections` | Switch backend connection |
| `/crons` | List and manage gateway cron jobs (default: filter by current agent; `/crons all` shows global) — OpenClaw only |
| `/models` | Open the model picker (filter as you type) |
| `/model <name>` | Switch model (fuzzy match) |
| `/reset` | Delete session and start fresh (with confirmation) |
| `/routine <name>` | Activate a stored multi-step routine in the current session |
| `/routines` | List, view, edit, or delete routines |
| `/sessions` | Browse and restore previous sessions |
| `/skills` | List available agent skills |
| `/stats` | Show token usage and cost breakdown — OpenClaw only |
| `/status` | Show gateway health and agent status — OpenClaw only |
| `/think` | Show current thinking level — OpenClaw only |
| `/think <level>` | Set thinking level (`off`, `minimal`, `low`, `medium`, `high`) — OpenClaw only |
| `/quit`, `/exit` | Quit lucinate |

## Shell commands

### Local commands

Prefix input with `!` to run a command locally on your machine. The input border turns green to indicate local execution mode.

```
!ls -la
!git status
!cat README.md
```

### Remote commands

Prefix input with `!!` to run a command on the gateway host. The input border turns amber to indicate remote execution mode.

```
!!hostname
!!ls -la /tmp
!!uptime
```

The gateway's exec security policy controls which remote commands are allowed. If a command is denied, you'll see an error message. Configure exec permissions on the gateway host using `openclaw config`.

## Routines

Recurring prompt workflow you find yourself retyping every Tuesday? Save it as a routine. Each routine is an ordered list of steps; each step is a complete user message. Activate one with `/routine <name>` and lucinate fires the steps at the assistant in order, optionally auto-advancing after every reply.

Manage them with `/routines` — list, view, add, edit, delete. The form has one textarea per step plus `Alt+↑` / `Alt+↓` to insert above/below and `Alt+Delete` to remove. `Ctrl+S` saves.

Routine files live at `~/.lucinate/routines/<name>/STEPS.md`. The format is plain markdown with optional YAML frontmatter and `---` between steps:

```markdown
---
name: review-pr
mode: auto
log: ./review.log
---

list the files changed in the current PR

---

flag any obvious bugs or style issues, one per line

---

write a one-paragraph review summary
```

`mode: auto` advances on every assistant reply; `manual` (the default) waits for `Enter`. `log:` is optional — when set, each turn is appended to that file, prefixed `user: ...` / `assistant: ...` with ISO timestamps.

While a routine is running you'll see a status row above the input:

```
routine: review-pr — AUTO — sent: 2/3 — next: write a one-paragraph review summary
```

Useful keys mid-routine:

| Key | Action |
|-----|--------|
| `Shift+Tab` | Cycle the mode (auto ↔ manual) |
| `Enter` (empty input) | Send the next step (manual / paused mode) |
| `Esc` | Cancel the routine and any in-flight reply |

The assistant can steer the routine in its own reply by emitting one of these on its own line:

| Directive | Effect |
|-----------|--------|
| `/routine:stop` | End the routine immediately |
| `/routine:pause` | Pause until you press `Enter` |
| `/routine:continue` | Explicit no-op (also resumes a paused auto-mode routine) |
| `/routine:mode auto` | Switch to auto mode |
| `/routine:mode manual` | Switch to manual mode |

Routines live entirely client-side and work with every backend.

## One-shot mode

Not every prompt needs a TUI. `lucinate send` dispatches a single message through one of your saved connections, waits for the assistant's first complete reply, and prints it to stdout — nothing else. No streaming, no spinner, no chrome. Safe to capture into a shell variable, pipe into `jq`, or schedule from cron.

```sh
lucinate send --connection my-con --agent main "summarise this PR description"
```

| Flag | Description |
|------|-------------|
| `--connection`, `-c` | Saved connection name or ID (case-insensitive on name). Required. |
| `--agent`, `-a` | Agent name or ID within the connection (case-insensitive on name). Required. |
| `--session`, `-s` | Session key. Defaults to the agent's main session — same default the picker would pick. |
| `--detach`, `-d` | Dispatch the message and exit as soon as the gateway accepts the turn. No reply is awaited. |

The reply is written to stdout with a single trailing newline. Errors go to stderr; the exit code is non-zero on failure. Messages that begin with a dash should be preceded by `--` (the standard Unix escape) so the flag parser leaves them alone.

A couple of patterns this enables:

```sh
# capture a reply into a shell variable (short flags work too)
reply=$(lucinate send -c my-con -a main "what's the changelog entry for this commit?")
echo "$reply" | tee notes.md

# fire-and-forget from cron — the run continues server-side, the next TUI session sees the reply
lucinate send --connection my-con --agent main --detach "kick off the morning digest"
```

For the lifecycle, the default-session rule, embedding `app.Send` from Go, and the detach contract, see [docs/one-shot.md](docs/one-shot.md).

## Skip the pickers

Already know which connection, agent, and session you want? `lucinate chat` drops you straight in — same TUI as the bare invocation, just pre-navigated past the pickers. Hand it a message and it's your first turn, auto-submitted once history loads.

```sh
lucinate chat --connection my-con --agent main "kick things off"
```

| Flag | Description |
|------|-------------|
| `--connection`, `-c` | Saved connection name or ID (case-insensitive on name). Optional — defaults to the same auto-pick the bare `lucinate` uses. |
| `--agent`, `-a` | Agent name or ID to auto-select. A miss surfaces as an error on the picker rather than silently picking the wrong one. |
| `--session`, `-s` | Session key to open. Defaults to the agent's main session — same default the picker would pick. |

Every flag is optional. `lucinate chat` with no flags and no message is functionally identical to bare `lucinate`. Messages that begin with a dash should be preceded by `--`, same Unix escape as `send`.

Unlike `send`, this stays in the TUI — the auto-submitted message is just your opening turn, not a one-shot exit. For the override-plumbing internals, see [docs/chat-launch.md](docs/chat-launch.md).

### Command line flags

| Flag | Description |
|------|-------------|
| `--help`, `-h` | Show top-level usage and exit. `lucinate help <command>` prints command-specific usage. |
| `--version`, `-v` | Print version and exit |

### Environment variables

Prefer env vars? Either is recognised on first run and auto-added as a connection:

```sh
OPENCLAW_GATEWAY_URL=https://your-gateway-host
LUCINATE_OPENAI_BASE_URL=http://localhost:11434/v1
LUCINATE_OPENAI_API_KEY=sk-...           # optional
LUCINATE_OPENAI_DEFAULT_MODEL=llama3.2   # optional
```

Other knobs:

```sh
LUCINATE_DISABLE_UPDATE_CHECK=1          # opt out of the daily update check, regardless of the toggle in /config
```

## Built on

lucinate uses the [openclaw-go](https://github.com/a3tai/openclaw-go) SDK for gateway communication. The TUI is built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Bubbles](https://github.com/charmbracelet/bubbles), and [Lip Gloss](https://github.com/charmbracelet/lipgloss), with markdown rendered via [Glamour](https://github.com/charmbracelet/glamour).

Device identity (Ed25519 keypair and device token) is stored at `~/.lucinate/identity/<endpoint>/`, isolated per gateway endpoint.

## License

Apache 2.0

---

<sub>OpenClaw is a trademark of its respective owner(s), including Peter Steinberger. This site is not affiliated with, endorsed by, or sponsored by OpenClaw or its contributors.</sub>
