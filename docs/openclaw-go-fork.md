# openclaw-go fork — lessons and process

The hard constraints for the fork live in
[`openspec/specs/openclaw-go-fork/spec.md`](../openspec/specs/openclaw-go-fork/spec.md) —
the `require`+`replace` wiring, pinning a tag rather than a branch or SHA, and retiring the
fork once upstream reaches parity are all captured there as requirements. This file keeps
the rationale behind those constraints and the operational procedure: why the SDK is
forked, how to bump and re-sync it, the patch-queue model, and the conditions under which
it is retired.

## Why we fork

Lucinate's gateway client depends on **our fork** of `openclaw-go`
([`outofcoffee/openclaw-go`](https://github.com/outofcoffee/openclaw-go)),
not the upstream module directly. This is deliberate and expected to last:
upstream (`a3tai/openclaw-go`) tracks the openclaw gateway via a release-sync
process that lags the gateway itself by weeks, so fixes we need (e.g. gateway
protocol v4 support) are not yet available upstream. The fork lets us ship
without waiting on an upstream merge.

The `replace` points at a **tag** on the fork, never a branch HEAD or a one-off feature
SHA. Reproducibility is the whole point: a build resolved today should resolve the same way
next month, and every dependency bump should be a deliberate, reviewable change to `go.mod`
rather than something that drifts when a branch moves under us.

## Patch-queue model (fork side)

The fork carries our not-yet-upstreamed patches as a queue on top of upstream:

- **One feature per branch**, each cut from `upstream/main` and opened as its
  own upstream PR (so it *can* merge when upstream catches up):
  - `feat/protocol-v4-range` — advertise a v3–v4 protocol range.
  - `feat/bootstrap-token-connect` — setup-code bootstrap token + node→operator
    handoff.
- **`lucinate` integration branch** = `upstream/main` + every patch above,
  cherry-picked. This is the only thing lucinate depends on. It is tagged
  `v1.20260430.<base>-lucinate.<n>`, where the date is the upstream base and
  `<n>` increments per patch-set revision.

Keeping each patch as a standalone branch and PR is what lets the queue shrink over time:
when upstream accepts one, we simply stop carrying it rather than untangling it from a
combined branch.

## Bumping / re-syncing (when upstream moves)

When upstream cuts a new release-sync we want, or merges one of our patches:

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

Then in lucinate:

```sh
go mod edit -replace github.com/a3tai/openclaw-go=github.com/outofcoffee/openclaw-go@v<NEW>-lucinate.<n+1>
go mod tidy && go build ./... && go test ./...
```

If a patch has landed upstream, drop it from the cherry-pick list (and close
its PR); the rest of the queue carries forward unchanged.

## Exit

When upstream `openclaw-go` ships gateway protocol v4 and the setup-code
bootstrap support, remove the `replace`, bump the `require` to that release,
delete the merged fork branches, and this document.
