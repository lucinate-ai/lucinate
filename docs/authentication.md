# Authentication — lessons and rationale

The behavioural contract for authentication lives in
[`openspec/specs/authentication/spec.md`](../openspec/specs/authentication/spec.md) — the
device-pairing flow, identity storage, auth-recovery modals, reconnection, and scopes are all
captured there as requirements and scenarios. This file keeps the hard-won lessons, pitfalls,
and design rationale behind that flow: the "why it works this odd way" that the spec's
requirements don't dwell on.

## Re-dial after first-time pairing: the silent stall

This is the least obvious thing in the whole connect path, and it cost real debugging time.

The bootstrap connect for a freshly approved device authenticates by Ed25519 device-key
signature alone — the token field on `connect` is empty because the client doesn't have one
yet, and the gateway issues the first device token in `hello-ok`.

The trap: **reusing that bootstrap connection for scoped RPCs leaves `sessions.create` silently
stalled** — no error, just a hang. The OpenClaw protocol expects scoped operations to run on a
connection that authenticated *with* the token at connect time. So `internal/client/client.go`
handles it inline: when `dial` observes it presented an empty token AND the gateway returned a
non-empty `hello.Auth.DeviceToken`, it persists the token, closes the bootstrap
`gateway.Client`, and dials a second time so the surviving connection carries the issued token.
On subsequent launches (token already on disk → bootstrap presents it → gateway returns no new
one) the second dial is skipped. `internal/client/dial_test.go` pins both branches — treat them
as a regression fence.

Defence in depth: the TUI wraps `CreateSession` in a per-call `context.WithTimeout` of `2×` the
configured connect timeout, so if this region ever stalls again it surfaces as a UI error in the
agent picker rather than freezing the view.

## Per-endpoint identity is deliberate

Each gateway endpoint gets its own Ed25519 keypair and device token under
`~/.lucinate/identity/<endpoint>/`. The point is that switching `OPENCLAW_GATEWAY_URL` must not
overwrite credentials you've already paired elsewhere — flipping between gateways should be
lossless, not a re-pairing every time.

## Token save is non-fatal, on purpose

If persisting a freshly issued device token fails, we log a warning and carry on rather than
aborting the session. A transient disk hiccup shouldn't cost you a working connection you just
paired; worst case you re-pair next launch.

## Auth recovery lives on the connect path, not the reconnect path

The mid-session supervisor (`internal/client/supervisor.go`) reconnects automatically on a
dropped socket, but if the gateway rejects the device token mid-session (`gateway token
mismatch` / `token missing`) it **stops retrying** and tells you to switch connections via
`/connections`. It deliberately does *not* try to drive the auth-recovery modal itself — that
flow only exists on the connect path. Keeping the modal off the reconnect path avoids two code
paths fighting over the same UI state; the cost is that a mid-session credential revocation
needs a manual reconnect.

Related: an interrupted stream is abandoned, not resumed. The gateway has no resume protocol for
an in-flight run, so on a drop we just clear the streaming placeholder to make the input usable
again rather than pretending we can pick the reply back up.

## Pitfall: a stray `OPENCLAW_OPERATOR_SCOPES` in `.env`

The gateway grants the *intersection* of the scopes you request and the scopes your device
token actually carries. Requesting scopes beyond what the pairing grants is rejected outright as
a scope mismatch, which is why `OPENCLAW_OPERATOR_SCOPES` exists — to *bound* the request down to
what a limited pairing carries, e.g.:

```
OPENCLAW_OPERATOR_SCOPES=operator.read,operator.write,operator.approvals
```

The pitfall: because `config.Load()` reads `.env` from the working directory, a stray
`OPENCLAW_OPERATOR_SCOPES` left in a `.env` silently bounds scopes for **every** connection,
regardless of which gateway you selected. If admin-only operations such as creating an agent
fail with `missing scope: operator.admin` despite a token that should carry admin, check for a
forgotten `OPENCLAW_OPERATOR_SCOPES`. Admin-only client operations fail fast with a message
pointing here rather than surfacing the raw gateway error — that fail-fast exists precisely
because this was confusing to diagnose the first time.

## Historical note

Bootstrap tokens were removed in v0.10.2. Device pairing is now the only setup path — if you
find a reference to bootstrap-token setup anywhere, it's stale.
