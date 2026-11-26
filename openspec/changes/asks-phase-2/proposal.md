# Asks Phase 2 — answer the gateway's questions in the TUI instead of declining them

## Why

When a Hermes turn blocks on a question to the client — `approval.request`, `clarify.request`,
`sudo.request`, `secret.request` — lucinate answers it on the user's behalf and moves on. The
plumbing is explicit: `internal/backend/hermes/backend.go:270` fires `go b.declineAsk(cli, ask)` for
every ask, and the comment at `:281-284` says the quiet part out loud — *"Phase 2 wires
approval.request into the TUI's exec-approval prompt instead."*

The cost is not cosmetic. A profile configured with `approval_mode: ask` cannot be used from
lucinate at all: every command is denied before the user sees it, and every clarifying question is
answered with the empty string, so the agent guesses. The notice the user gets
(`declineNotice`, `backend.go:276-280`) tells them to configure the profile for autonomous
approval — which is exactly the safety setting they turned on deliberately.

The gateway already carries everything needed: `Ask{Type, SessionID, RequestID, Payload}`
(`internal/backend/hermes/translate.go:25-30`) with the raw payload intact, and the four respond
RPCs are already being called (`approval.respond`, `clarify.respond`, …). Only the destination is
wrong: a hard-coded denial instead of a question on screen.

## What changes

- **The ask reaches the TUI.** The backend stops declining and emits the ask as an agent event
  (`stream: "ask"`) carrying the session key, request id, kind, question text, the options the
  payload offers, and — for an approval — the command and working directory. The TUI renders it in
  a panel above the composer.
- **The user answers with a key.** Approval: `y` allows once, `a` allows always (only when the
  payload offers it), `n`/`esc` denies. Clarify: the payload's numbered choices with `1`–`9`, or
  free text when it offers none; `enter` submits, `esc` skips with an empty answer. The panel is
  modal only for its own keys while the composer is empty — a typed draft is never eaten.
- **The answer goes back through the RPC that already exists**, via a new optional backend
  capability (`backend.AskBackend`) so other backends are unaffected.
- **No hang, no race.** An ask that nobody answers is denied after a bounded wait (`askTimeout`,
  default 10 minutes) — the promise that a turn can never hang silently survives. Once the user
  answers, the timer is abandoned; an answer for an ask that has already expired is logged and
  dropped.

## Out of scope

- **`sudo.request` and `secret.request` stay auto-declined.** They need masked input and a
  password path that a terminal chat client should not own; the notice keeps saying so. Deferred
  deliberately, not forgotten.
- Storing an "always allow" policy of lucinate's own — `a` asks the gateway for `always` and the
  gateway owns the policy.
- Any change to the exec approval flow for commands the user typed (`!cmd`), which is
  auto-approved by design (`internal/tui/commands.go:732-746`).
