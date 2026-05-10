# Developer docs

This directory contains maintainer-level documentation for lucinate. It covers how the major subsystems are implemented — intended as a starting point when working on a feature area, not as a user guide.

## Index

| Document | Covers |
|---|---|
| [connections.md](connections.md) | Connection types overview, picker, startup decision tree, capability negotiation, secrets |
| [backend_openclaw.md](backend_openclaw.md) | OpenClaw backend: capability surface, skill catalog injection, device-token auth |
| [backend_openai.md](backend_openai.md) | OpenAI-compat backend: SSE streaming, on-disk agent storage, IDENTITY.md/SOUL.md composition, local-pass compaction |
| [backend_hermes.md](backend_hermes.md) | Hermes backend: `/v1/responses` chaining, synthetic single agent, server-side state |
| [authentication.md](authentication.md) | Device pairing, Ed25519 identity, gateway connection and token lifecycle |
| [agents.md](agents.md) | Agent picker, auto-selection, agent creation |
| [sessions.md](sessions.md) | Session lifecycle, session browser, compact/reset, message queueing |
| [crons.md](crons.md) | Gateway cron browser: list/detail/form substates, capability gating, raw-patch edit semantics |
| [routines.md](routines.md) | Local prompt routines: STEPS.md format, controller lifecycle, auto-advance hook, directives, manager view |
| [commands.md](commands.md) | Slash command dispatch, all built-in commands, tab completion, confirmation pattern |
| [one-shot.md](one-shot.md) | One-shot CLI mode: `lucinate send` lifecycle, default session rule, detach semantics, embedding |
| [chat-launch.md](chat-launch.md) | Pre-navigated TUI launch: `lucinate chat` override plumbing, consumption points, stale-clearing rules |
| [shell-execution.md](shell-execution.md) | `!` local exec and `!!` remote exec with two-phase approval |
| [skills.md](skills.md) | Skill file format, discovery, catalog injection, activation |
| [chat-ux.md](chat-ux.md) | Input key bindings, streaming animation, thinking levels, header bar, history depth |
| [message-rendering.md](message-rendering.md) | Message roles, `System:` prefix convention, history cleanup, markdown rendering |
