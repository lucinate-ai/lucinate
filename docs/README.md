# Developer docs

This directory contains maintainer-level documentation for repclaw. It covers how the major subsystems are implemented — intended as a starting point when working on a feature area, not as a user guide.

## Index

| Document | Covers |
|---|---|
| [authentication.md](authentication.md) | Device pairing, Ed25519 identity, gateway connection and token lifecycle |
| [agents.md](agents.md) | Agent picker, auto-selection, agent creation |
| [sessions.md](sessions.md) | Session lifecycle, session browser, compact/reset, message queueing |
| [commands.md](commands.md) | Slash command dispatch, all built-in commands, tab completion, confirmation pattern |
| [shell-execution.md](shell-execution.md) | `!` local exec and `!!` remote exec with two-phase approval |
| [skills.md](skills.md) | Skill file format, discovery, catalog injection, activation |
| [chat-ux.md](chat-ux.md) | Input key bindings, streaming animation, thinking levels, header bar, history depth |
| [message-rendering.md](message-rendering.md) | Message roles, `System:` prefix convention, history cleanup, markdown rendering |
