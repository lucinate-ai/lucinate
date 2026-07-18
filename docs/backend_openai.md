# OpenAI-compatible backend — lessons and rationale

The behavioural contract for the OpenAI-compat backend lives in
[`openspec/specs/backend-openai/spec.md`](../openspec/specs/backend-openai/spec.md) — the
declared capabilities, status payload, connect and auth behaviour, local agent storage, session
and history handling, skill-catalogue injection, streaming, and local compaction are all captured
there as requirements and scenarios. This file keeps the hard-won lessons, pitfalls, and design
rationale behind that behaviour: the "why it works this odd way" that the spec's requirements
don't dwell on.

## Why `/think` is a no-op

`/think` reports `Thinking: false` so it's gated off, even though several OpenAI-compatible
providers expose reasoning controls in their own request shapes (OpenAI's `reasoning.effort`,
Ollama's `think` flag on reasoning models, DeepSeek's `<think>` tags, Anthropic-via-proxy's
`thinking` block). Wiring `/think` to translate into the active provider's reasoning shape is a
known gap — tracked in [#80](https://github.com/lucinate-ai/lucinate/issues/80).

## IDENTITY.md and SOUL.md composition rationale

The two files are seeded with editable placeholders so users can shape an agent on disk without
going through the TUI — `SystemPrompt(agentID)` reloads from disk on every chat send, so edits
between sessions are picked up on the next send. `DefaultIdentity(name)` interpolates the agent's
chosen name as the `Name:` header so the model addresses itself by the right label from turn one.

`SystemPrompt` handles all four presence combinations — both files, identity only, soul only, and
neither — rather than assuming both exist. The "neither" case returns an empty string so the model
gets no preamble instead of a stray `# Identity` / `# Soul` skeleton.

## Delete vs archive

The keep-vs-delete-files toggle exists so a user can retire an agent without losing its files.
`DeleteFiles=false` renames the agent dir into a sibling `.archive/<id>-<unixts>/` with
IDENTITY.md, SOUL.md, history.jsonl, and agent.json surviving verbatim for manual recovery, rather
than deleting anything.

`AgentStore.List` filters by parsable `agent.json` at the top of each direct child of the picker
root, so the `.archive` directory is naturally skipped (it has no `agent.json` of its own and
`LoadMeta` returns an error). No special-case is needed.

`DeleteAgent` calls `LoadMeta` first to surface a "not found" error rather than silently
succeeding on a stale agent ID — important because the UI presence-toggles its `confirm-delete`
action on `nameMatches()`, which can theoretically pass while the underlying agent has already
vanished.

## Why local compaction is needed, and its keep-tail / min-history reasoning

`/compact` runs locally because there is no gateway-side compactor to lean on — `SessionCompact`
issues its own summarisation request against the agent's configured model. `compactKeepTail`
preserves the most recent messages verbatim after the summary so the tail of the conversation
stays intact, while `compactMinHistory` gates the no-op-when-too-small case (it returns success
without a network round-trip, so short sessions don't pay for a pointless model call).

Streaming (rather than non-streaming) is intentional: some Ollama setups, particularly with
reasoning-capable models, return an empty `message.content` on the non-streaming path while the
streamed `delta.content` deltas produce the actual answer. Reusing the streaming code path means
/compact works across the same compatibility matrix the regular chat send already covers.

The `Summary` flag is what distinguishes a compact-produced digest from the legacy "skip stored
system messages" defence in `runStream`: messages with `Summary: true` are forwarded on every turn
after compaction, while any other `role: "system"` line in `history.jsonl` is still ignored.
`ChatHistory` mirrors the same rule so the digest renders in the chat view rather than vanishing on
history refresh.

A previously-compacted session that gets compacted again folds the existing summary into the new
one — `renderTranscriptForCompact` includes prior summaries under a `summary:` label so multiple
compactions don't lose detail accumulated across earlier passes.

The transcript is dumped as labelled text inside a single `role: user` message, not forwarded as
the literal user/assistant message sequence. Forwarding raw turns ends the request on
`role: assistant` — and OpenAI-compatible servers (Ollama, vLLM, llama.cpp) interpret that as "the
conversation is complete" and respond with empty content, defeating the summarisation. Wrapping the
transcript in a user turn lets the model treat it as input data and produce the summary as a normal
reply.

## Why the status message count is capped

`/status` counts `history.jsonl` messages by counting newlines, but files larger than
`historyCountMaxBytes` (1 MiB) skip the count and fall back to size-only. The cap exists so an
interactive command never blocks on a huge transcript.

## Slug IDs must round-trip through the protocol

The agent ID is derived from the user-supplied name via `slugify` (lowercase, alphanumerics and
hyphens only). The constraint isn't cosmetic: the ID is also the session key, so it has to
round-trip through gateway-protocol fields without escaping.
