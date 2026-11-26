# Hermes gateway v2 — multiplexed profiles, room protocol 2, honest live state

## Why

The Hermes gateway lucinate targets has moved. The installed stable (`v0.22.5`, 2026-09-24)
runs **one machine-level gateway for every profile** (multiplexing, on by default), speaks
hosted-room `protocol_version: 2` with 18 `groups.*` methods, and deliberately never writes
streaming deltas into a room log. lucinate still assumes one connection per profile, one
`groups.member_model` seat that no longer exists, a 6-member roster, and delta-driven streaming.

A live pass against `hermes dashboard` (see `docs/rooms.md`) confirmed the drift:

- `groups.member_model` answers `-32601 unknown method`; room sessions are created with
  `follow_profile_config: true`, so a member always runs the profile's model.
- The room log carries `turn.started` → `message.member` → `turn.settled` only; member text
  deltas go to a plugin hook (`on_room_member_activity`), never to the durable log.
- `groups.state` reports `member_activity`, `pending_actions`, `blocked` — the only live signal.
- A member blocked on an approval cannot be resolved from lucinate at all.
- The gateway accepts up to 128 room members; the client refuses above 6.
- `ORCA_WORKTREE_ID` on WSL carries a `\\wsl.localhost\<distro>\...` path, which lucinate used
  verbatim: the room directory was created outside the project and members were told to use a
  path that does not exist in their filesystem.

## What changes

- **Profiles are agents.** `ListAgents` reports the gateway's own profiles (`profiles.list`);
  `CreateSession`/`SessionList`/`session.resume` carry the selected `profile`, so one
  connection drives every profile on the machine.
- **Model switching works.** `SessionPatchModel` dispatches `/model <ref>` through
  `slash.exec`, and reports the gateway's own "session only" answer.
- **Rooms show what is real.** Per-member working indicators come from
  `groups.state.driver_status.member_activity`; text deltas stay supported as forward
  compatibility but are no longer the source of the live row.
- **Rooms can be unblocked.** `groups.approve`, `groups.retry`, and `groups.stop` are exposed:
  `/approve @handle once|deny`, `/retry`, `/stop` in the transcript, plus `rooms approve`,
  `rooms retry`, `rooms rename` in the CLI.
- **The 2–6 roster bound is verified, not changed:** the gateway's discussion layer still
  refuses anything outside 2–6 (code 5111); `MAX_MEMBERS = 128` is only the persistence cap.
- **Per-member model is retired as a client feature** — the member runs the profile's model;
  the client says so instead of offering a seat the gateway cannot honour.
- **Project paths are normalised** (WSL/UNC → POSIX; a Windows drive on Linux falls back to
  the working directory), so `.rooms/<id>` always lands inside the real project.
- **`/rooms` is Hermes-only** and says "not available on this connection" elsewhere.

## Capabilities

### New Capabilities

None — all changes land in existing capabilities.

### Modified Capabilities

- `backend-hermes`: profiles as agents, per-profile sessions, model switching via `slash.exec`.
- `rooms`: activity-based live rows, approvals/retry/stop, roster 128, member model from the
  profile, normalised project paths.
- `commands`: `/rooms` in the built-in catalogue with its Hermes-only guard.

## Impact

- `internal/backend/hermes` — profile list/routing, model switch, sessions list per profile.
- `internal/rooms` — `MemberActivity`, approve/retry, roster bound, path normalisation.
- `internal/tui` — agent list, room activity rows, new transcript commands, `/rooms` guard.
- `internal/cli/rooms.go` — `approve`, `retry`, `rename`.
- `docs/rooms.md`, `docs/backend_hermes.md`, `README.md`, `CHANGELOG.md`.
- Tests — `internal/testgateway` grows to protocol 2; new live harness.
