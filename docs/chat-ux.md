# Chat UX — lessons and rationale

The behavioural contract for the chat surface lives in
[`openspec/specs/chat-ux/spec.md`](../openspec/specs/chat-ux/spec.md) — the key bindings, streaming
animation, header bar, view region order, notifications, routine status, tool activity, history
depth and resync, connect timeout, and the mouse/scrolling model are all captured there as
requirements and scenarios. This file keeps the hard-won lessons, pitfalls, and design rationale
behind that contract: the "why it works this odd way" that the spec's requirements don't dwell on.

## The 999% peg: why context usage is session-scoped

The right-hand context-usage percentage is scoped to the *current session* on purpose. The older
approach of calling `SessionUsage("")` returned gateway-wide aggregates and pegged the value at
999%. `loadContextUsage()` fixes this by calling `SessionsList(agentID)`, finding the entry whose
`key` matches `m.sessionKey`, and reading `totalTokens` (numerator — a per-turn prompt-token
snapshot of `input + cacheRead + cacheWrite`, intentionally excluding output) and `contextTokens`
(denominator), falling back to `defaults.contextTokens` when the entry omits the window.

The handler discards results whose `sessionKey` no longer matches `m.sessionKey` so a
navigated-away-and-back race can't apply a stale snapshot. And the renderer still caps the
displayed percentage at 999% regardless — a runaway numerator never widens the header past three
digits.

## Mid-turn history resync: why the merge is not a wholesale replacement

After a turn finalises, the chat view fetches `chat.history` and merges it into `m.messages`. The
merge is deliberately *not* a wholesale replacement — that would wipe live state (the next routine
step's placeholder, a system row the user just took an action on).

The mechanism is a generation counter (`chatModel.gen`, a monotonic `uint64` starting at 1). Every
`appendMessage(...)` stamps the current `m.gen` onto the row. A successful `final` (or
`error`/`aborted`) calls `bumpGen()`, capturing the current value as the "boundary" before
advancing; that boundary is what `refreshHistoryAt(boundary)` carries. When the resulting
`historyRefreshMsg` lands, `mergeHistoryRefresh(server, boundary)` keeps every existing row with
`gen > boundary` (the live tail — appended by the post-bump `drainQueue` / `maybeAdvanceRoutine` /
recovery path) and prepends the server-fetched canonical state.

The non-obvious consequences worth remembering:

- Rows imported from `chat.history` carry `gen=0` (the chatMessage zero value), so any subsequent
  refresh treats them as history-side and replaces them cleanly.
- Tool activity is not part of `m.messages` at all — it lives in `chatModel.activeTools` — so the
  merge neither preserves nor wipes it; it's reset per turn independently.
- Empty `final` acks (the gateway ping that arrives before any real content) intentionally do NOT
  bump the gen, because no turn has actually completed.

Tests pin the contract: `TestMergeHistoryRefresh_PreservesLiveTail`,
`TestMergeHistoryRefresh_NoLiveTail`, `TestHandleEvent_FinalBumpsGen`,
`TestHandleEvent_FinalEmptyAckDoesNotBumpGen`. Treat them as a regression fence.

## Per-agent header colour: why it's keyed on the agent ID

The header background override is stored against the agent ID under
`prefs.Agents[agentID].HeaderColor`, and the renderer calls `prefs.HeaderColorFor(m.agentID)` each
frame. Keying it on the agent (rather than a single global setting) is what lets switching to
another agent pick up that agent's own colour, or fall back to the default accent purple. The
colour is applied to both `headerStyle` and the warn-badge background so the badges stay legible
against a customised header.

## Update check: the escape hatches and why they exist

The daily startup update check is owned by `internal/update`: a single
`GET https://lucinate.ai/latest.json` with a 5-second timeout, fired once per day from
`AppModel.Init()`. It's designed to fail silently — offline, captive portal, malformed manifest, or
a non-stable build all make `update.Check` return `nil, nil`, so a flaky network never produces a
badge or an error.

Two env-var escape hatches exist for good reason:

- `LUCINATE_DISABLE_UPDATE_CHECK=1` is an unconditional opt-out on top of the `/settings` toggle,
  useful in CI where you don't want any outbound request.
- `LUCINATE_UPDATE_MANIFEST_URL` overrides the manifest URL for local testing without touching the
  production endpoint.

The badge is suppressed once seen: `prefs.LatestSeenVersion` records the manifest version on every
successful check, and the badge only reappears when the manifest moves *past* that version.

## Why mouse capture is on by default

Mouse capture (SGR cell-motion tracking) is on by default because it's what lets wheel scrolling,
click-drag selection, and Up/Down input recall coexist without fighting. With tracking on, the
terminal never translates the wheel into arrow keys, so scrolling can't collide with the bash-style
input-recall walk — the arrow keys stay exclusively input recall. The cost of `/mouse off` is
losing wheel scrolling (it hands click-drag back to the terminal's native selection), which is why
capture-on is the default rather than the opt-in.

One related nicety: if you've scrolled up, new streaming output does not yank you back down; new
messages re-anchor to the bottom only when you're already there (`GotoBottom()` after each update).

## Why Alt+Enter, not Shift+Enter, inserts a newline

Newline is bound to Alt+Enter via `ta.KeyMap.InsertNewline.SetKeys("alt+enter")`. Shift+Enter is
deliberately not supported: enabling it would require `ReportAllKeysAsEscapeCodes`, which is
disabled precisely to preserve shifted punctuation input.

## Notifications replaced transient system rows

The `chatModel.notifications` store (`internal/tui/notifications.go`) exists because appending
`chatMessage{role:"system"}` rows for transient state was the wrong home for it. Notifications live
outside `m.messages`, so they survive `historyRefreshMsg` and never reach the gateway, and they're
cleared at the top of the Enter handler on the user's next action — the assumption being that any
state worth showing has been read or no longer applies by then.

Persistent client-side rows (the inline `! cmd` / `!! cmd` shell-execution scrollback, gateway
connect/disconnect notes, the `/help` / `/stats` / `/skills` info dumps) still go in `m.messages`
because their value is in being scrollable history, not in a one-shot read.

## Connect timeout: bump it for cold-starting local LLMs

The per-attempt handshake deadline (default 15s) is loaded by `app.DefaultBackendFactory`
(`app/factory.go`) for every backend dispatch, so the one setting governs both the initial connect
and the supervisor's reconnect attempts. The practical note: bump it when targeting a slow local
LLM that cold-starts on first request, or the first connect will time out before the model is ready.
