# Agent picker — lessons and rationale

The behavioural contract for the agent picker lives in
[`openspec/specs/agents/spec.md`](../openspec/specs/agents/spec.md) — where agents come from,
loading, filtering, navigation, auto-selection, and the create/select/delete flows are all
captured there as requirements and scenarios. This file keeps the hard-won lessons, pitfalls,
and design rationale behind that flow: the "why it works this odd way" that the spec's
requirements don't dwell on.

## Filtering is opt-in, unlike the model picker

The model picker drops straight into filtering, but the agent picker deliberately starts in
plain list mode. The single-letter action shortcuts (`n` new, `d` delete, `c` connections) must
stay reachable, so filtering is opt-in via `/`. Two consequences fall out of that decision:

- **Keystroke routing while filtering.** While the filter input is focused
  (`list.FilterState() == list.Filtering`), `handleKey` forwards every key except `enter` to the
  list so characters that collide with action shortcuts (e.g. `n`) type into the query instead of
  firing the action. Outside filtering, the normal action dispatch applies.
- **A typed `q` must not quit.** `selectModel.filtering()` reports whether the filter is focused;
  `app.go`'s `computeWantsInput()` consults it (alongside the create-agent form) so the app-level
  `q`-to-quit shortcut and the embedder input-focus signal treat a typed `q` as filter text rather
  than a quit request.

## Pitfall: a stale narrowed filter on re-entry

The picker reuses one `selectModel` across navigation, so leaving for another screen (config,
connections, chat) and coming back would otherwise restore a stale narrowed view. `AppModel.Update`
calls `selectModel.resetFilter()` on every transition *into* `viewSelect`, clearing the query so the
list reopens showing every agent. It's a no-op when no filter was active.

## `--agent` auto-pick runs before the single-agent branch, on purpose

`selectModel.autoPickName` runs its ID-then-case-insensitive-name match **before** the
single-agent and post-create branches. The reason is deliberate: a `--agent` mismatch in a
single-agent connection should surface as an error banner on the picker rather than silently
picking the only available agent. The override is one-shot — cleared on consume so a later
`agentsLoadedMsg` (e.g. after a user-driven create) doesn't re-fire it. See the `chat-launch`
spec for the full override-consumption story.

## Deleting an agent: the safety design

The confirm-delete view is deliberately loud, and several of its choices exist to make a
destructive action hard to fire by accident:

- **Keep files is the default.** `tab` flips `m.keepFiles`, which becomes `!DeleteFiles` on the
  `backend.DeleteAgentParams`. It defaults to **keep files** (`keepFiles=true` → `DeleteFiles=false`),
  so a mistaken confirmation preserves the agent's file content — the user has to toggle off to
  destroy it. The view's description line switches with the toggle so the user can read what the
  current mode will do before pressing enter.
- **Type-to-confirm as the disable mechanism.** `confirm-delete` is only emitted from `Actions()`
  when the typed name matches the agent's display name (case-insensitive, whitespace-trimmed) and no
  request is in flight. That presence-toggle *is* the disable mechanism for native-platform
  embedders — the `Action` struct has no `Enabled` flag.
- **Pending state is snapshotted** (`pendingDeleteID`, `pendingDeleteName`) at substate entry from
  the passed `agentItem`, never re-read from `list.SelectedItem()` afterwards. A list re-render
  mid-flight cannot resolve the destructive cmd to the wrong agent.
- **Plain `d` is not bound** inside the substate because it's a printable character the user might
  type as part of the agent name.

On error `pendingDeleteName` is preserved so the user can retry without retyping. Keystrokes are
ignored while `m.deleting` is true — the network call has already left.

## Delete semantics are per-backend

The destructive-vs-preserve interpretation differs by backend, and two of the choices are worth
calling out:

- **OpenClaw** — `Backend.DeleteAgent` sends `protocol.AgentsDeleteParams{AgentID, DeleteFiles: &flag}`
  with the pointer always set explicitly, so the gateway's implicit "preserve files" default never
  applies.
- **OpenAI-compatible** — when `DeleteFiles=false` the agent is moved to
  `<root>/.archive/<id>-<unixts>/` rather than wiped, so IDENTITY.md, SOUL.md, and history.jsonl stay
  recoverable on disk. See the `backend-openai` spec.
- **Hermes** — `DeleteAgent` returns a clear error pointing at `hermes profile delete`. The UI gate
  (`AgentManagement=false`) means the user shouldn't reach it; the reject is defensive.
