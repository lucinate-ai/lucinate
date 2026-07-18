# Developer docs — lessons and rationale

This directory holds maintainer-level **lessons, pitfalls, and design rationale** for lucinate's
subsystems: the "why it works this odd way" that doesn't have a first-class home in a spec.

The **behavioural contract** for each subsystem — what it does, as requirements and scenarios —
lives in [`openspec/specs/<domain>/spec.md`](../openspec/specs). Start there for "what should
happen"; come here for "why it's built this way, and what to watch out for". When you change
behaviour, the spec is the source of truth to keep in sync; a doc here is updated only when the
*reasoning* changes.

## Index

| Lessons doc | Behavioural contract |
|---|---|
| [authentication.md](authentication.md) | [`specs/authentication`](../openspec/specs/authentication/spec.md) |
| [connections.md](connections.md) | [`specs/connections`](../openspec/specs/connections/spec.md) |
| [backend_openclaw.md](backend_openclaw.md) | [`specs/backend-openclaw`](../openspec/specs/backend-openclaw/spec.md) |
| [backend_openai.md](backend_openai.md) | [`specs/backend-openai`](../openspec/specs/backend-openai/spec.md) |
| [backend_hermes.md](backend_hermes.md) | [`specs/backend-hermes`](../openspec/specs/backend-hermes/spec.md) |
| [agents.md](agents.md) | [`specs/agents`](../openspec/specs/agents/spec.md) |
| [sessions.md](sessions.md) | [`specs/sessions`](../openspec/specs/sessions/spec.md) |
| [crons.md](crons.md) | [`specs/crons`](../openspec/specs/crons/spec.md) |
| [routines.md](routines.md) | [`specs/routines`](../openspec/specs/routines/spec.md) |
| [commands.md](commands.md) | [`specs/commands`](../openspec/specs/commands/spec.md) |
| [one-shot.md](one-shot.md) | [`specs/one-shot`](../openspec/specs/one-shot/spec.md) |
| [chat-launch.md](chat-launch.md) | [`specs/chat-launch`](../openspec/specs/chat-launch/spec.md) |
| [shell-execution.md](shell-execution.md) | [`specs/shell-execution`](../openspec/specs/shell-execution/spec.md) |
| [export-and-recording.md](export-and-recording.md) | [`specs/export-and-recording`](../openspec/specs/export-and-recording/spec.md) |
| [skills.md](skills.md) | [`specs/skills`](../openspec/specs/skills/spec.md) |
| [chat-ux.md](chat-ux.md) | [`specs/chat-ux`](../openspec/specs/chat-ux/spec.md) |
| [message-rendering.md](message-rendering.md) | [`specs/message-rendering`](../openspec/specs/message-rendering/spec.md) |
| [logging.md](logging.md) | [`specs/logging`](../openspec/specs/logging/spec.md) |
| [openclaw-go-fork.md](openclaw-go-fork.md) | [`specs/openclaw-go-fork`](../openspec/specs/openclaw-go-fork/spec.md) |

[key-conventions.md](key-conventions.md) has no spec of its own — the cross-view keyboard
vocabulary it describes is cross-cutting, so it lives in
[`openspec/config.yaml`](../openspec/config.yaml)'s `context` (injected into every OpenSpec
proposal). The doc remains here as the human-readable reference.
