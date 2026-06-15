# openclaw-go fork

Lucinate's gateway client depends on **our fork** of `openclaw-go`
([`outofcoffee/openclaw-go`](https://github.com/outofcoffee/openclaw-go)),
not the upstream module directly. This is deliberate and expected to last:
upstream (`a3tai/openclaw-go`) tracks the openclaw gateway via a release-sync
process that lags the gateway itself by weeks, so fixes we need (e.g. gateway
protocol v4 support) are not yet available upstream. The fork lets us ship
without waiting on an upstream merge.

## How the dependency is wired

`go.mod` keeps the upstream module path in `require` and redirects it to a
**tag on our fork** with a `replace`:

```
require github.com/a3tai/openclaw-go v1.20260325.1-...   // upstream pseudo-version
replace github.com/a3tai/openclaw-go => github.com/outofcoffee/openclaw-go v1.20260430.0-lucinate.2
```

The module path stays `github.com/a3tai/openclaw-go` (the fork does not rename
its module), so upstream and fork stay drop-in compatible and the day upstream
ships what we need we delete the `replace` line and bump the `require`.

We pin a **tag**, never a branch HEAD or a one-off feature SHA, so a build is
always reproducible and dependency bumps are deliberate.

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
