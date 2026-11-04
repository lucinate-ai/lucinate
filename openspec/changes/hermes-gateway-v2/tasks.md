# Tasks — Hermes gateway v2

## 1. Project paths (WSL/UNC)

- [x] 1.1 `internal/rooms/paths.go`: `NormaliseProjectPath` (WSL UNC → POSIX, drive → `/mnt/<d>`, other backslash → unknown) with table tests.
- [x] 1.2 `OrientationFromEnv` / `OrientationFromWorktree` normalise `Path`; the `worktree Orca:` selector is left untouched.
- [x] 1.3 `RoomsBase` and `WithRoomDir` fall back to the working directory when the base is empty or not a directory; `EnsureRoomDir` refuses a backslash base on non-Windows. Tests.
- [x] 1.4 Remove the stray `internal/tui/\\wsl.localhost\...` artefact.

## 2. Rooms protocol 2

- [x] 2.1 `DriverStatus.MemberActivity` and typed `PendingAction` parsed from `groups.state`.
- [x] 2.2 `Client.Approve`, `Client.Retry` covered by tests against `internal/testgateway` (plus `Rename` and `Stop` already there).
- [x] 2.3 `internal/testgateway` grows to protocol 2: `member_activity`, `pending_actions`, `approve`/`retry`, no `groups.member_model` by default.
- [x] 2.4 TUI: live rows from `member_activity` (spinner 120 ms), removed when the member stops; deltas remain optional.
- [x] 2.5 TUI: blocked banner from `pending_actions` + `/approve @handle once|deny`, `/retry`, `/stop`; `/help` lists them.
- [x] 2.6 CLI: `rooms approve`, `rooms retry`, `rooms rename`; `rooms state` prints active members and pending actions.
- [x] 2.7 Roster bound stays 2–6 (verified live: `groups.create` answers 5111 outside 2–6; 128 is the storage cap, not the product rule).

## 3. Member model

- [x] 3.1 `/model` in a room reports the profile as the source when the gateway lacks `groups.member_model`; no request is sent.
- [x] 3.2 Roster line keeps showing the member's model (from the gateway or `profile default`).
- [x] 3.3 Spec text (`/help`, docs) stops advertising a per-member seat.

## 4. Hermes backend: multiplexed profiles

- [x] 4.1 `profiles.list` → `ListAgents` (profile name as agent id, model per profile); synthetic fallback on `-32601`.
- [x] 4.2 `session.create` / `session.resume` / `session.list` / `session.delete` carry `profile`; session keys are profile-qualified.
- [x] 4.3 `SessionPatchModel` dispatches `/model <ref>` through `slash.exec` and surfaces the gateway's answer.
- [x] 4.4 Tests with a fake gateway: two profiles, two sessions, model switch success and refusal.

## 5. `/rooms` guard

- [x] 5.1 `/rooms` on a non-Hermes connection renders "not available on this connection" and dials nothing. Test.

## 6. Documentation and specs

- [x] 6.1 `docs/rooms.md` (lessons: protocol 2, no deltas, approvals, path normalisation, the 2–6 vs 128 trap) + `docs/README.md` row.
- [x] 6.2 `docs/backend_hermes.md`: multiplexed profiles, model switch via `slash.exec`.
- [x] 6.3 `README.md`: rooms + Hermes profile selection.
- [ ] 6.4 `CHANGELOG.md` — the release process owns it; do not edit by hand (recorded here so nobody "fixes" the omission).
- [ ] 6.5 Archive this change (`/opsx:archive`) to merge the deltas into `openspec/specs/` when the branch is merged.

## 7. Verification

- [x] 7.1 `go vet ./...`, `gofmt -l .`, `make test`, `make smoke`.
- [x] 7.2 Live harness (`scripts/live-hermes.sh`, `make test-live-hermes`) against `hermes dashboard` v0.22.5: profiles, room lifecycle, activity, one addressed turn, disband.
