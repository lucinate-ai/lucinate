# One-shot mode — lessons and rationale

The behavioural contract for one-shot mode lives in
[`openspec/specs/one-shot/spec.md`](../openspec/specs/one-shot/spec.md) — the `app.Send`
lifecycle, the default-session rule, detach semantics, the `ask` alias, the embedder seam,
reply-text extraction, and the deliberate non-goals are all captured there as requirements and
scenarios. This file keeps the hard-won lessons, pitfalls, and design rationale behind that
flow: the "why it works this odd way" that the spec's requirements don't dwell on.

## Why one-shot deliberately reuses the TUI's seams

`lucinate send` reuses the TUI's connection store, backend factory, and event channel on
purpose, so retry / auth / capability behaviour stays consistent across both modes. When you
touch one path, keep the other in mind — the value of the shared seams is that they don't drift.

## No supervision for one-shot turns

`Send` does not run `Backend.Supervise`. A one-shot turn is short-lived enough that
auto-reconnect is dead weight; a dropped socket surfaces as a clean failure ("backend event
channel closed before reply") rather than triggering exponential backoff. The TUI's
`app.Program` keeps Supervise — the lifetimes are different.

## The race-before-send gotcha

The `replyCollector` goroutine starts draining `backend.Events()` *before* `ChatSend` is called
so the final event cannot race past the consumer. Then, because events from concurrent turns on
the same session share the channel, the collector is told the run ID only *after* the `ChatSend`
ack lands, so those other turns' events are filtered out. Subscribe-first-then-filter is the
order that matters here; flipping it reintroduces the race.

## Why detach is not guaranteed to deliver a reply

`--detach` returns as soon as `ChatSend` resolves its RPC ack. That does guarantee validation
errors surface synchronously and that the gateway has accepted the turn and assigned a run ID.
It does **not** guarantee that the assistant will reply, or that a reply — if it arrives — ever
reaches stdout: the run continues server-side, and the streaming events are consumed nowhere in
detach mode. The reply is rendered the next time the user opens the TUI on that session, just as
if a previous TUI window had been closed mid-turn.

That is by design. Detach is intended for cron-style automation ("nudge the agent at 09:00 to
draft the morning digest, render the result on my next browse") and for fire-and-forget
shell-pipeline steps that don't care about the response text.

## The `ask` alias is not a second code path

`lucinate ask` is a thin wrapper over the same `app.Send` pipeline — there is no second code
path. The only real difference is where the flags' default values come from (saved
`AskDefaults`) and that it returns an `ask:`-prefixed error pointing at
`/settings ▸ Ask command defaults` for a blank connection or agent, rather than letting
`app.Send`'s `send:`-prefixed "required" error surface for what is, in `ask`'s world, a
configuration gap.

The `KEEP IN SYNC` reasoning: `config.AskDefaults` has one field per `send` flag, and three
files carry mutual `KEEP IN SYNC` comments (`internal/cli/send.go`, `internal/cli/ask.go`,
`internal/config/preferences.go`). A new field added to `send` should gain an `AskDefaults`
field and a row in the TUI sub-screen so `ask` stays a *complete* alias — miss any of the three
and the alias quietly falls behind `send`.

## Why the wire-format parser is a single seam

The shared parser at `internal/backend/chatevent.go` is deliberately the single place that
knows the two shapes a chat event's `Message` can take (a plain JSON string delta, or a
`{ content: [{type, text}, ...] }` final object). Both the TUI's chat view
(`internal/tui/events.go`) and `send`'s `replyCollector` call `ExtractChatText` so they agree on
what the visible body is. If a backend ever changes the wire shape, both consumers update
together — that's the whole point of keeping it one seam rather than two parsers that can drift.

`Send` ignores `ExtractChatThinking` blocks because the contract for
`--connection X --agent Y "msg"` is "the assistant's reply on stdout", not the deliberation that
led to it.

## Why the default-session rule is shared with the picker

The default-key rule is shared with the TUI agent picker (`internal/tui/select.go`) so
`lucinate send` and "select agent → main" land on the same gateway-side session. The
consequence worth knowing: `lucinate send --connection X --agent Y "hello"` repeats into the
same conversation forever unless `--session` is supplied — `CreateSession` resumes the existing
key if it's there and provisions one otherwise.

The literal-`"main"` fallback for non-default agents exists to match what the TUI passes when
the user picks a non-default agent and accepts the picker's "main" session. Backends that keep
no server-side session state (OpenAI, Hermes) ignore the key shape and route by `agentID`
regardless — the shared rule costs them nothing.

## Non-goals, and why

- **Slash-command parsing.** A message body of `/help` is sent verbatim to the agent; the TUI's
  command dispatcher is intentionally not on this path. Scripts that want a command's effect
  should call the corresponding RPC directly rather than typing it as chat input.
- **Skill catalog injection.** The TUI sends `Skills` in `ChatSendParams` so the gateway can
  advertise local skills to the model; `Send` deliberately omits it. Skills are a TUI-discovery
  concept (slash-command activation, mid-message expansion); revisit if scripted workflows ever
  want `lucinate send … "/review the diff"` to expand the same way.
- **Auth recovery modals.** The TUI routes `token-mismatch` / `401` into modal flows; `Send`
  lets them bubble. Scripts that need to bootstrap auth should run `lucinate` once interactively
  and let the TUI's auth flow seed the secrets store.
