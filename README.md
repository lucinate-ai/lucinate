# repclaw

The terminal client for [OpenClaw](https://github.com/daggerhashimoto/openclaw-nerve).

[![CI](https://github.com/outofcoffee/repclaw/actions/workflows/ci.yml/badge.svg)](https://github.com/outofcoffee/repclaw/actions/workflows/ci.yml)

![repclaw demo](docs/demo.gif)

## What it does

repclaw is the terminal-native client for OpenClaw. Connect to your gateway, pick an agent, and chat — with streaming responses, markdown rendering, and a keyboard-driven interface that doesn't make you reach for the mouse.

No file browsers, no task boards, no dashboards. Just chat.

### Highlights

- **Create agents** directly from the TUI — name, workspace, done
- **Markdown rendering** for assistant messages via Glamour
- **Remote command execution** — prefix with `!` to run commands on the gateway host
- **Message queueing** — keep typing while the agent is responding
- **Local agent skills** — drop skills into `~/.agents/skills/` and use them as slash commands
- **Live token/cost stats** in the header bar

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/outofcoffee/repclaw/main/install/repclaw.sh | sh
```

Or, if you have Go 1.24+:

```sh
go install github.com/outofcoffee/repclaw@latest
```

Or build from source:

```sh
git clone https://github.com/outofcoffee/repclaw.git
cd repclaw
go build -o repclaw .
```

## Getting started

### 1. Create an API token on your gateway

Generate an operator token using the OpenClaw CLI on the machine running your gateway:

```sh
openclaw token create --role operator --scopes operator:read,operator:write,operator:admin --name repclaw
```

Copy the token it prints — you'll need it in the next step.

### 2. Configure repclaw

Create a `.env` file in the directory you'll run repclaw from (or export the variables in your shell):

```sh
OPENCLAW_GATEWAY_URL=https://your-gateway-host
OPENCLAW_GATEWAY_TOKEN=<token-from-step-1>
```

The gateway URL can use `https`, `http`, `wss`, or `ws` schemes. repclaw derives the WebSocket endpoint automatically.

### 3. Connect and approve the device

```sh
repclaw
```

On first connection, the gateway will prompt you to approve device pairing. On the gateway host, run:

```sh
openclaw device list --pending
```

You should see repclaw's device ID. Approve it:

```sh
openclaw device approve <device-id>
```

Then restart repclaw — subsequent connections will use the paired device identity automatically. The identity (Ed25519 keypair and device token) is stored at `~/.openclaw-go/identity/`.

### 4. Start chatting

Select an agent from the list, then start chatting. Press `n` on the agent list to create a new agent.

| Key | Context | Action |
|-----|---------|--------|
| `n` | Agent list | Create a new agent |
| `Enter` | Agent list | Select agent |
| `Enter` | Chat | Send message |
| `Shift+Enter` | Chat | Insert newline |
| `Ctrl+W` | Chat | Delete word |
| `PgUp` / `PgDn` | Chat | Scroll chat history |
| `Esc` | Chat | Back to agent list |
| `Ctrl+C` | Quit |

## Commands

Type these in the chat input. Tab autocompletes partial commands.

| Command | Action |
|---------|--------|
| `/help` | Show available commands |
| `/model` | List available models |
| `/model <name>` | Switch model (fuzzy match) |
| `/stats` | Show token usage and cost breakdown |
| `/clear` | Clear chat display |
| `/back` | Return to agent list |
| `/quit` | Quit repclaw |

## Remote commands

Prefix input with `!` to run a command on the gateway host. The input border turns amber to indicate remote execution mode.

```
!hostname
!ls -la /tmp
!uptime
```

The gateway's exec security policy controls which commands are allowed. If a command is denied, you'll see an error message. Configure exec permissions on the gateway host using `openclaw config`.

## Built on

repclaw uses the [openclaw-go](https://github.com/a3tai/openclaw-go) SDK for gateway communication. The TUI is built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Bubbles](https://github.com/charmbracelet/bubbles), and [Lip Gloss](https://github.com/charmbracelet/lipgloss), with markdown rendered via [Glamour](https://github.com/charmbracelet/glamour).

Device identity (Ed25519 keypair) is stored at `~/.openclaw-go/identity/` and shared with other openclaw-go clients.

## License

Apache 2.0
