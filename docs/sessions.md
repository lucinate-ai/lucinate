# Sessions — lessons and rationale

The behavioural contract for sessions lives in
[`openspec/specs/sessions/spec.md`](../openspec/specs/sessions/spec.md) — the session
lifecycle, the session browser, the compact and reset commands, and message queueing are all
captured there as requirements and scenarios. This file keeps the hard-won lessons, pitfalls,
and design rationale behind that flow: the "why it works this odd way" that the spec's
requirements don't dwell on.

## Why session keys are deterministic

The session key is deterministic for non-default agents (based on agent ID) so the same session
is restored on restart rather than spawning a fresh conversation each launch. The same
default-key rule is reused by the one-shot CLI mode (`lucinate send`): it uses `MainKey` for the
default agent and the literal `"main"` for any other agent, so a scripted dispatch lands on the
same conversation as "open the picker, pick the agent, hit enter". Keeping both entry points on
the same key rule is the whole point — otherwise a scripted send and an interactive pick would
drift onto different conversations.

The `lucinate chat --session <key>` override is deliberately **one-shot** — cleared once
consumed in the `viewSelect` block so a follow-up agent pick on the same picker doesn't keep
landing on the original key.

## Why history and stats load in parallel

On `chatModel.Init()`, `loadHistory()` and `loadStats()` run as two async commands in parallel
rather than in sequence — neither depends on the other, so serialising them would just add
latency to the first paint.

## Compact: server-side vs local streaming

The distinction that catches people out: on OpenClaw the gateway runs the compaction pass
server-side, but on the OpenAI-compatible backend the pass runs **locally** (a streaming
`POST /v1/chat/completions` against the agent's configured model). Same `/compact` command, two
very different execution paths — worth remembering when a compaction behaves differently between
backends.

## Reset is delete-then-recreate

`/reset` doesn't clear a session in place; it calls `SessionDelete()` to permanently remove the
session and then immediately creates a replacement via `CreateSession()`. The new session key
comes back as `sessionClearedMsg{newSessionKey}` — the chat model reinitialises against a fresh
key rather than reusing the old one.

## Queueing gotcha: exec results also drain the queue

While a response is in flight, new input is appended to `m.pendingMessages` rather than sent, so
fast typing doesn't drop messages. The non-obvious part is that local (`!`) and remote (`!!`)
exec results **also** trigger `drainQueue()` — not just chat responses. If you change the exec
flow, remember it shares the same drain path; miss that and queued messages silently stall after
an exec.

`lucinate chat <message>` pre-seeds the same queue and drains it from the `historyLoadedMsg`
handler *after* the scrollback has rendered, so the launch message appears after the loaded
history — matching what a human typing the same message would see, rather than jumping ahead of
it.

## Scheduled sessions sort separately

Sessions whose key contains `:cron:` are split into their own **Scheduled** list in the session
browser, separate from regular **Conversations**. The `:cron:` marker in the key is what drives
the grouping — there's no other flag distinguishing an automated session from a hand-started one.
