<p align="center">
  <img src="docs/hero.png" alt="lucinate" width="600" />
</p>

<h1 align="center">lucinate — the terminal-native AI chat client</h1>

<p align="center">
  Chat with your <a href="https://github.com/openclaw/openclaw">OpenClaw</a> agents — or any OpenAI-compatible endpoint, including <a href="https://ollama.com">Ollama</a>, vLLM, LM Studio, and OpenAI itself — from the terminal. Streaming responses, markdown rendering, no mouse required.
</p>

<p align="center">
  <a href="https://github.com/lucinate-ai/lucinate/actions/workflows/ci.yml"><img src="https://github.com/lucinate-ai/lucinate/actions/workflows/ci.yml/badge.svg" alt="CI" /></a>
</p>

<p align="center">
  <img src="docs/demo.gif" alt="lucinate demo" width="600" />
</p>

No file browsers, no task boards, no dashboards. Just chat.

### Highlights

- **Multiple backends** — connect to an [OpenClaw](https://github.com/openclaw/openclaw) gateway, or any OpenAI-compatible endpoint ([Ollama](https://ollama.com), vLLM, LM Studio, llamafile, OpenAI proper). Switch between saved connections with `/connections`.
- **Streaming responses, conversation history, and multi-agent support**
- **Create agents** directly from the TUI — gateway-managed for OpenClaw, or local `IDENTITY.md` + `SOUL.md` markdown for OpenAI-compatible backends
- **Markdown rendering** for assistant messages
- **Shell commands** — run locally with `!` or remotely on the gateway with `!!`
- **Message queueing** so you can keep typing while the agent is responding
- **Local agent skills** loaded from `~/.agents/skills/` as slash commands
- **Live token/cost stats** in the header bar (OpenClaw)
- **Thinking level control** via `/think` — tune reasoning depth per session (OpenClaw)

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/lucinate-ai/lucinate/main/install/lucinate.sh | sh
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

On first launch lucinate opens a **Connections** picker so you can add a backend without setting any env vars. Saved connections live in `~/.lucinate/connections.json`; the most recently used is selected automatically next time.

Two connection types are supported:

- **OpenClaw** — connect to an OpenClaw gateway over WebSocket. Auth uses Ed25519 device pairing. See [docs/backend_openclaw.md](docs/backend_openclaw.md).
- **OpenAI-compatible** — connect to any `/v1/chat/completions` endpoint (Ollama, vLLM, LM Studio, llamafile, OpenAI proper). Agents are stored locally as IDENTITY.md + SOUL.md markdown under `~/.lucinate/agents/<connection-id>/<agent-id>/`, composed into the system prompt at runtime. See [docs/backend_openai.md](docs/backend_openai.md).

Prefer env vars? Either is recognised on first run and auto-added as a connection:

```sh
OPENCLAW_GATEWAY_URL=https://your-gateway-host
LUCINATE_OPENAI_BASE_URL=http://localhost:11434/v1
LUCINATE_OPENAI_API_KEY=sk-...           # optional
LUCINATE_OPENAI_DEFAULT_MODEL=llama3.2   # optional
```

OpenClaw URLs can use `https`, `http`, `wss`, or `ws` — lucinate derives the WebSocket endpoint automatically. OpenAI-compatible URLs are HTTP(S) base URLs ending in `/v1`.

Switch between saved connections at any time with `/connections` from the chat view. Use `n` to add a new one, `e` to edit, `d` to delete (with confirmation).

### Flags

| Flag | Description |
|------|-------------|
| `--version`, `-v` | Print version and exit |

### 2. Connect and approve the device

```sh
lucinate
```

On first run, lucinate generates an Ed25519 device identity under `~/.lucinate/identity/<endpoint>/` (keyed by gateway host) and sends a pairing request to the gateway. On the gateway host, run:

```sh
openclaw device list --pending
```

You should see lucinate's device ID. Approve it:

```sh
openclaw device approve <device-id>
```

Then restart lucinate — subsequent connections use the stored device token automatically.

### 3. Pick an agent

Select an agent from the list to start chatting.

| Key | Action |
|-----|--------|
| `Enter` | Select agent |
| `n` | Create a new agent |
| `Ctrl+C` | Quit |

### 4. Chat

| Key | Action |
|-----|--------|
| `Enter` | Send message |
| `Alt+Enter` | Insert newline |
| `Ctrl+W` | Delete word |
| `PgUp` / `PgDn` | Scroll chat history |
| `Tab` | Autocomplete slash command |
| `Esc` | Back to agent list |
| `Ctrl+C` | Quit |

### Preferences

Use `/config` to open the preferences view. Settings are persisted to `~/.lucinate/config.json`.

| Setting | Default | Description |
|---------|---------|-------------|
| Completion notification | On | Ring the terminal bell when a response completes |
| History limit | 50 | Number of messages loaded when restoring a session (range 10–500) |

In the config view, use `Space` to toggle checkboxes and `←`/`→` to adjust numeric values.

## Commands

Type these in the chat input. Tab autocompletes partial commands.

| Command | Action |
|---------|--------|
| `/help` | Show available commands |
| `/agents` | Return to agent picker |
| `/clear` | Clear chat display |
| `/compact` | Compact session context (with confirmation) — OpenClaw only |
| `/config` | Open preferences |
| `/connections` | Switch backend connection |
| `/model` | List available models |
| `/model <name>` | Switch model (fuzzy match) |
| `/reset` | Delete session and start fresh (with confirmation) |
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

## Built on

lucinate uses the [openclaw-go](https://github.com/a3tai/openclaw-go) SDK for gateway communication. The TUI is built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Bubbles](https://github.com/charmbracelet/bubbles), and [Lip Gloss](https://github.com/charmbracelet/lipgloss), with markdown rendered via [Glamour](https://github.com/charmbracelet/glamour).

Device identity (Ed25519 keypair and device token) is stored at `~/.lucinate/identity/<endpoint>/`, isolated per gateway endpoint.

## License

Apache 2.0

---

<sub>OpenClaw is a trademark of its respective owner(s), including Peter Steinberger. This site is not affiliated with, endorsed by, or sponsored by OpenClaw or its contributors.</sub>
