# openclaw-go Fork Specification

## Purpose

Lucinate's gateway client depends on **our fork** of `openclaw-go`
([`outofcoffee/openclaw-go`](https://github.com/outofcoffee/openclaw-go)), not the
upstream module directly. This is deliberate and expected to last: upstream
(`a3tai/openclaw-go`) tracks the openclaw gateway via a release-sync process that lags
the gateway itself by weeks, so fixes we need (e.g. gateway protocol v4 support) are not
yet available upstream. The fork lets us ship without waiting on an upstream merge. This
spec covers how the dependency is wired, the fork's patch-queue model, how it is bumped
and re-synced, and the conditions under which the fork is retired.

## Requirements

### Requirement: Fork dependency and rationale

The build SHALL consume our fork of `openclaw-go`
([`outofcoffee/openclaw-go`](https://github.com/outofcoffee/openclaw-go)) as the gateway
client dependency rather than the upstream module (`a3tai/openclaw-go`) directly. This
divergence is deliberate and expected to persist: upstream tracks the openclaw gateway
via a release-sync process that lags the gateway itself by weeks, so fixes we need (e.g.
gateway protocol v4 support) are not yet available upstream. The fork exists so we can
ship without waiting on an upstream merge.

#### Scenario: Client depends on the fork, not upstream

- **GIVEN** upstream `a3tai/openclaw-go` lags the gateway release by weeks and lacks fixes we need
- **WHEN** the gateway client is built
- **THEN** it consumes `outofcoffee/openclaw-go` (our fork)
- **AND** does not depend on the upstream module directly

### Requirement: go.mod require plus replace wiring

`go.mod` SHALL keep the upstream module path `github.com/a3tai/openclaw-go` in `require`
and redirect it to a **tag on our fork** with a `replace` directive:

```
require github.com/a3tai/openclaw-go v1.20260325.1-...   // upstream pseudo-version
replace github.com/a3tai/openclaw-go => github.com/outofcoffee/openclaw-go v1.20260430.0-lucinate.2
```

The module path SHALL stay `github.com/a3tai/openclaw-go` (the fork does not rename its
module), so upstream and fork stay drop-in compatible and the day upstream ships what we
need we delete the `replace` line and bump the `require`.

#### Scenario: Upstream path redirected to the fork tag

- **GIVEN** `go.mod` requires `github.com/a3tai/openclaw-go` at an upstream pseudo-version
- **WHEN** the module is resolved
- **THEN** a `replace` directive redirects it to `github.com/outofcoffee/openclaw-go` at a fork tag such as `v1.20260430.0-lucinate.2`
- **AND** the module path remains `github.com/a3tai/openclaw-go` because the fork does not rename its module, keeping upstream and fork drop-in compatible

### Requirement: Pin a tag, never a branch or SHA

The `replace` SHALL pin a **tag**, never a branch HEAD or a one-off feature SHA, so a
build is always reproducible and dependency bumps are deliberate.

#### Scenario: Reproducible pin

- **WHEN** the fork dependency is pinned in `go.mod`
- **THEN** it references a released tag
- **AND** never a branch HEAD or a one-off feature SHA, so builds are reproducible and bumps are deliberate

### Requirement: Fork patch-queue model

The fork SHALL carry our not-yet-upstreamed patches as a queue on top of upstream, with
**one feature per branch**, each cut from `upstream/main` and opened as its own upstream
PR (so it *can* merge when upstream catches up). The current feature branches are:

- `feat/protocol-v4-range` — advertise a v3–v4 protocol range.
- `feat/bootstrap-token-connect` — setup-code bootstrap token + node→operator handoff.

The **`lucinate` integration branch** SHALL equal `upstream/main` + every patch above,
cherry-picked. This is the only thing lucinate depends on. It SHALL be tagged
`v1.20260430.<base>-lucinate.<n>`, where the date is the upstream base and `<n>`
increments per patch-set revision.

#### Scenario: One feature per branch, each an upstream PR

- **GIVEN** patches not yet accepted upstream
- **WHEN** they are carried in the fork
- **THEN** each is a single feature on its own branch cut from `upstream/main` (e.g. `feat/protocol-v4-range`, `feat/bootstrap-token-connect`) and opened as its own upstream PR so it can merge when upstream catches up

#### Scenario: Integration branch is the only lucinate dependency

- **GIVEN** the `lucinate` integration branch equals `upstream/main` plus every feature patch cherry-picked
- **WHEN** lucinate pins the fork
- **THEN** it depends only on that integration branch, tagged `v1.20260430.<base>-lucinate.<n>` where the date is the upstream base and `<n>` increments per patch-set revision

### Requirement: Bumping and re-syncing when upstream moves

When upstream cuts a new release-sync we want, or merges one of our patches, the fork
SHALL be rebuilt on the new upstream base by resetting the integration branch to
`upstream/main` and cherry-picking only the patches upstream has NOT merged yet, then
cutting the next tag. In the fork repository:

```sh
cd ~/projects/openclaw-go
git fetch upstream --tags

# rebuild the integration branch on the new upstream base
git checkout lucinate
git reset --hard upstream/main
# cherry-pick only the patches upstream has NOT merged yet
git cherry-pick <feat/bootstrap-token-connect HEAD> <feat/protocol-v4-range HEAD>
go test ./... -race

# cut the next tag (new upstream date and/or next patch-set number)
git tag -a v<NEW>-lucinate.<n+1> -m "..."
git push --force-with-lease origin lucinate
git push origin v<NEW>-lucinate.<n+1>
```

Then in lucinate, the `replace` SHALL be repointed at the new tag and the module
re-tidied and rebuilt:

```sh
go mod edit -replace github.com/a3tai/openclaw-go=github.com/outofcoffee/openclaw-go@v<NEW>-lucinate.<n+1>
go mod tidy && go build ./... && go test ./...
```

If a patch has landed upstream, it SHALL be dropped from the cherry-pick list (and its
PR closed); the rest of the queue carries forward unchanged.

#### Scenario: Rebuild the integration branch on a new upstream base

- **GIVEN** upstream cuts a new release-sync we want, or merges one of our patches
- **WHEN** the fork is re-synced
- **THEN** the `lucinate` branch is reset hard to `upstream/main`, only the still-unmerged patches are cherry-picked, `go test ./... -race` is run, and the next tag `v<NEW>-lucinate.<n+1>` is cut and pushed with `--force-with-lease`

#### Scenario: Repoint lucinate at the new fork tag

- **GIVEN** a new fork tag `v<NEW>-lucinate.<n+1>` has been pushed
- **WHEN** lucinate is updated
- **THEN** `go mod edit -replace` repoints the `replace` at the new tag, followed by `go mod tidy && go build ./... && go test ./...`

#### Scenario: A patch has landed upstream

- **GIVEN** one of our patches has been merged upstream
- **WHEN** the fork is re-synced
- **THEN** that patch is dropped from the cherry-pick list and its PR closed
- **AND** the rest of the queue carries forward unchanged

### Requirement: Retire the fork on upstream parity

When upstream `openclaw-go` ships gateway protocol v4 and the setup-code bootstrap
support, the fork SHALL be retired: remove the `replace`, bump the `require` to that
release, delete the merged fork branches, and delete this document.

#### Scenario: Upstream reaches parity

- **GIVEN** upstream `openclaw-go` ships gateway protocol v4 and the setup-code bootstrap support
- **WHEN** the fork is retired
- **THEN** the `replace` is removed, the `require` is bumped to that upstream release, the merged fork branches are deleted, and this document is deleted
