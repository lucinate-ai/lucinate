# Design — Hermes gateway v2

## Decisions

### Per-member model is not a client feature (Option A)

Upstream never shipped `groups.member_model`; a room member runs on a hidden `room_plumbing`
session created with `follow_profile_config: true`, so the member's model **is** the profile's
model. lucinate's `/model @handle` therefore probes `groups.capabilities`, finds nothing, and
refuses. We keep the probe (a future gateway that adds the method lights the feature up), but
we stop selling it as available: `/help` and the transcript picker say the member runs the
profile's model and point at the profile, not at a seat.

Rejected: mutating the profile through `profiles.configure` — that changes the user's Hermes
profile from a chat view, which is precisely what the current spec forbids.

### Live rows come from `groups.state`, not from the log

The durable log is append-only and byte-budgeted; upstream keeps deltas out of it on purpose.
The only client-visible live signal is `groups.state.driver_status.member_activity`
(`[{member_id, status}]`), plus `blocked` and `pending_actions`. The TUI polls state every
1.5 s already, so the live row is derived from that poll. `message.member.delta` handling in
`rooms.StreamsFor` stays (forward compatibility; older/other gateways may emit it), but a room
whose gateway never emits deltas no longer pretends to stream.

### Approvals are first-class

`groups.approve` requires the exact pending coordinates (`member_id`, `task_id`,
`execution_generation`, `request_id`) and a choice of `once`/`deny`; `groups.retry` retries an
indeterminate task; `groups.stop` cancels queued and running turns. The TUI renders
`pending_actions` as a blocked banner naming the member, and `/approve @handle once|deny`
resolves the one pending action for that member. `/retry` and `/stop` mirror the CLI verbs.
The action's coordinates are never guessed: an approve with no matching pending action is
refused, and a stale one is reported with the gateway's own error.

### The roster bound is 2–6, and that was verified, not assumed

`gateway/hosted_rooms.py` has `MAX_MEMBERS = 128`, but that is the persistence
cap. The discussion layer that `groups.create` runs enforces
`MIN/MAX_DISCUSSION_MEMBERS = 2/6` (`gateway/hosted_room_discussion.py`) and
answers `5111 members must contain between 2 and 6 entries` for anything else.
A live create with eight profiles proved it. The client's 2–6 stays as-is; the
earlier plan to raise it to 128 was wrong and is recorded here so nobody
"fixes" it again.

### Project paths are normalised, never trusted

`ORCA_WORKTREE_ID` carries a host-native path for the Orca host, which may be Windows while the
gateway runs in WSL. `NormaliseProjectPath` maps:

- `\\wsl.localhost\<distro>\a\b` and `\\wsl$\<distro>\a\b` → `/a/b`;
- `C:\a\b` → `/mnt/c/a/b` when that directory exists, else "" (unknown);
- a path containing `\` that is not one of the above → "" (unknown);
- everything else unchanged.

`OrientationFromEnv`/`OrientationFromWorktree` normalise `Path`; `RoomsBase` and
`WithRoomDir` refuse a base that is empty or not an existing directory and fall back to the
working directory, so `.rooms/<id>` can never be created from a backslash path again. The
`worktree Orca:` selector is left untouched — it is an Orca argument, not a filesystem path.

### Polling stays

There is no public push channel for hosted rooms on the WS. Polling the open room at 1.5 s is
cheap and correct; rooms the user is not looking at are not polled. Unread notifications are
out of scope for this change.
