# Rooms — lessons and rationale

The behavioural contract lives in [`openspec/specs/rooms/spec.md`](../openspec/specs/rooms/spec.md).
This file keeps what the spec does not: the wire facts learned against a real gateway, the
traps that cost a debugging session, and why the client is shaped the way it is.

Verified against **Hermes v0.22.5 (2026.9.24)** on a machine-level `hermes dashboard` — the
multiplexed gateway that serves every profile from one process.

## The gateway is multiplexed; rooms are still per-gateway

`hermes profile list` shows every profile as `Gateway running` under one `gateway run`
process, and `hermes dashboard` is the machine-level server (a profile launch routes to it;
`--isolated` is the opt-out). Over one WebSocket, `profiles.list` reports the roster and
`session.create {"profile": …}` selects one. The chat backend follows that: agents are
profiles, and session keys are profile-qualified (`<profile>::<stored-id>`) so resuming a
session lands in the right profile's `state.db`.

Rooms sit on top of the same gateway but keep their own `groups.*` surface; they are not
per-profile. A room's members are profiles local to that gateway.

## Protocol version 2, and methods the client does not use

`groups.capabilities` answers `protocol_version: 2` and advertises 18 methods. The client uses
nine (`capabilities, list, create, state, send, log, rename, stop, disband`) plus the two this
change added (`approve, retry`). Advertised but unused: `replicate`, `replica_state`,
`promote`, `demote`, `peer.invite`, `peer.revoke`, `peer.register` — the RoomLink surface for
rooms spanning two installations, which is out of scope here. `room_link.enabled` is `true`
on a current gateway; the client reports it and does nothing with it.

## There is no streaming of member text — by design

The durable log is append-only and byte-budgeted. Upstream deliberately keeps token deltas
out of it (`tui_gateway/hosted_room_member_activity.py`: "Nothing is written to the room log:
deltas at room-log byte budgets would exhaust a room in minutes"). A client therefore sees
`turn.started` → … → `message.member` → `turn.settled`, and **no partial text**.

The only live signal is `groups.state.driver_status.member_activity`
(`[{member_id, status}]`). The transcript renders one spinner row per active member from that
poll. Deltas are still accepted if a gateway ever emits them (`rooms.StreamsFor`), but they are
not the source of the live row and their absence is not an error.

A corollary: do not expect `turn.started` to appear for every turn — short turns can go
straight from the user message to the member reply in the log.

## Blocked rooms are invisible without `pending_actions`

`groups.state` also reports `pending_actions`, `blocked` and `counts`. An approval blocking a
member carries `{kind: "approval", member_id, task_id, execution_generation, request_id}`;
resolving it is `groups.approve` with exactly those coordinates and `choice: once|deny`.
`groups.retry` takes the same task/generation fence for an indeterminate task. Without this
the room just looks silent while a member waits — the transcript now renders a blocked banner
and offers `/approve`, `/retry` and `/stop`.

## The roster is 2–6; 128 is not the rule

`gateway/hosted_rooms.py` has `MAX_MEMBERS = 128`, which reads like the roster limit. It is
the persistence cap. `groups.create` runs the discussion validation
(`gateway/hosted_room_discussion.py`, `MIN/MAX_DISCUSSION_MEMBERS = 2/6`) and refuses anything
else with code 5111 — a live create with eight profiles proved it. Keep the client at 2–6; do
not "fix" it to 128.

The roster is frozen at creation, and a disbanded id is tombstoned (4110 on re-create). The
client mints a fresh id (`<id>-<HHMMSS>`) and retries, which is what `/restart` relies on.

## Per-member model does not exist upstream

`groups.member_model` is not implemented by Hermes v0.22.5 (`-32601 unknown method`). A room
member runs on a hidden `room_plumbing` session created with `follow_profile_config: true`, so
its model is the profile's model. The client keeps the capability probe for a future gateway
that adds the method, but it must not advertise a seat the gateway cannot honour: `/model`
reports the profile as the source, and the roster line shows the model the gateway reports.

## Project paths from the Orca host need normalising

`ORCA_WORKTREE_ID` carries the Orca host's spelling of the worktree. On WSL that is
`\\wsl.localhost\Ubuntu\home\…`, which is meaningful to Windows and meaningless to a
filesystem call inside the distro. Used verbatim it created
`<cwd>/\\wsl.localhost\Ubuntu\home\…\.rooms/<id>` and told the members to write to a path they
could not open.

`rooms.NormaliseProjectPath` maps `\\wsl.localhost\<distro>\a\b` and `\\wsl$\<distro>\a\b` to
`/a/b`, and `C:\a\b` to `/mnt/c/a/b` only when that directory exists. Anything else with a
backslash is *unknown*, and an unknown path falls back to the working directory. The pure
function is table-tested; `EnsureRoomDir` refuses a backslash base on non-Windows hosts as a
second line of defence. The `worktree Orca:` selector itself is passed through unchanged — it
is an Orca argument, not a path.

## Polling replays events, so the transcript dedupes by event id

`groups.log` polls overlap (a page can be delivered twice, and a reconnect replays from the
cursor), and the live client appended everything it received. The transcript then held each
message twice, and `/find` printed every match twice — a bug only a live session shows.
`newRoomEvents` keeps the first occurrence of each event id (falling back to
`seq|kind|text` for older payloads) before anything enters the transcript.

Times are displayed in **local** time, like the transcript and the room log view. `MatchLine`
used UTC, so `/find` stamped a message at 22:55 while the transcript above it said 23:55.

The room's shared directory is created only after `groups.create` accepts the id. Creating it
for the requested slug first left an empty `.rooms/<requested-id>` behind whenever the gateway
answered 4110 (tombstoned id) and the client retried under a minted one.

## The room's rules live in one place

Who may speak, how many rounds a discussion gets, when a member may pass, and how a citation pulls
a peer into the next round are all decided by the gateway. [`room-rules.md`](room-rules.md) records
that policy verbatim (constants, the member prompt, what the log exposes) and what the client may
honestly claim about it; the in-room `/rules` overlay quotes the same text.

## Cost and usage

Member usage arrives inline on `message.complete.payload.usage` in the chat; in rooms the
gateway reports usage on member replies as well. `session.usage` still reports zeros (and
credit lines) on v0.22.5, so `/cost` reads the events, not the RPC. A gateway that reports no
tokens produces a "nothing reported yet" notice rather than a table of zeroes.

## Who a row names, and when a claim is a claim

The actor on a `turn.*` event is the gateway, so the transcript resolves the member from the payload instead: `rooms.MemberHandle(event, roster)` matches `member_id` against the roster first, falls back to `member_index`, and returns `""` when the roster cannot place it — in which case the row keeps the actor's own label rather than inventing a handle. A cancelled turn therefore reads `@alojz — przerwano: member turn cancelled`, not `@install:<hash>`. The CLI's `formatRoomEvent` takes the same roster (fetched once from `groups.list`), so `lucinate rooms log` and the TUI agree. Separately, a receipt-less reply is marked as a write claim only when it both uses a write marker and names a deliverable (a `.md/.go/.json/.html` path or an explicit `zapisałem`/`utworzyłem`/`wdrożyłem`/`wygenerowałem`); a denial (`nie zapisałem`) or a path quoted in passing is discussion, and gets no evidence row. `HANDOFF` receipts keep their verified/failed/duplicate verdicts unchanged.

## Testing

`internal/testgateway` speaks protocol 2 over a real WebSocket (approvals, retries, activity,
scripted drops). The hermetic suite covers the client and the model; a live pass against a
real gateway is the only way to catch protocol drift, because the durable log's event set and
the capability list are upstream-owned. The harness used for this change:

```sh
HERMES_DASHBOARD_SESSION_TOKEN=lucinate-probe \
  hermes dashboard --port 9121 --skip-build --no-open &
# then, with a temporary lucinate data dir holding one Hermes connection at :9121
LUCINATE_DATA_DIR=$(mktemp -d) lucinate rooms caps
```

Remember to `rooms disband` every room the pass created and to stop the dashboard.
