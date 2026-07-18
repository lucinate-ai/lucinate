# OpenClaw backend — lessons and rationale

The behavioural contract for the OpenClaw backend lives in
[`openspec/specs/backend-openclaw/spec.md`](../openspec/specs/backend-openclaw/spec.md) — the
declared capabilities, connect and auth pass-throughs, agent and session model, skill-catalogue
injection, the pass-through method surface, and the status payload are all captured there as
requirements and scenarios. This file keeps the lessons, pitfalls, and design rationale behind
that adapter: the "why it works this odd way" that the spec's requirements don't dwell on.

## The adapter adds no behaviour, on purpose

`internal/backend/openclaw` is a thin adapter over `*client.Client` from the OpenClaw SDK. Every
TUI call site that used to hold a `*client.Client` now holds a `backend.Backend`; the OpenClaw
concrete type is recovered via type assertion at the few sites that still need gateway-only
affordances. Most methods forward straight through unchanged — the adapter exists to satisfy the
`backend.Backend` interface, not to add behaviour. Only the handful of methods below do anything
non-obvious, and they are the ones worth remembering.

## Skill catalogue is injected once, and the mutex matters

The catalogue block — `Available agent skills (activate with /skill-name): …` — is prepended only
to the first turn of each session via `takePendingCatalog(sessionKey, skills)`, after which
`catalogSent[sessionKey] = true` and later sends omit it.

The check-and-mark is mutex-guarded for a reason: two concurrent sends on the same session could
otherwise both see the flag unset and both emit the catalogue. The guard is what makes "once per
session" actually hold under concurrency, not just on the happy path.

Every line of the block is prefixed with `System:` so it does double duty: the gateway's prompt
assembler recognises it as a session-level system block (retained server-side across turns), and
`stripSystemLines` on the client side hides it from the visible transcript on history refresh.
Drop the prefix and it would either be treated as user input or leak into the visible transcript.

## `DeleteFiles: &flag` is always populated — the implicit default must never apply

`DeleteAgent` forwards to `Client.DeleteAgent(ctx, agentID, deleteFiles)`, which sends
`protocol.AgentsDeleteParams{AgentID, DeleteFiles: &flag}` over the wire. The `*bool` is a pointer
precisely so the gateway can distinguish "not set" from "set to false" — and lucinate always
populates it explicitly from the picker's keep-vs-delete-files toggle (see the `agents` spec,
deleting-an-agent) so the gateway's implicit "preserve files" default never applies. If we ever
passed a nil pointer here, a user who chose "delete files" could silently get them preserved.

## Health-fetch failure surfaces, it doesn't swallow

If the `/health` fetch fails, `Status` still returns a populated payload with `Gateway.Health =
nil` and passes the error back to the caller. The TUI then renders the body and the error as
separate system messages rather than swallowing either — a partial status plus a visible error is
more useful than a blank screen or a lost error.

## `agentID` and `sessionKey` are ignored deliberately

`Status(ctx, agentID, sessionKey)` takes those arguments to match the cross-backend signature but
ignores them: OpenClaw's per-session state lives on the gateway and is already reflected in the
health snapshot, so there is nothing local to look up. They are kept in the signature only so the
backend satisfies the shared interface.
