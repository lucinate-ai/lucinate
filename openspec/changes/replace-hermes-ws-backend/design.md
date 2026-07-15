## Context

The Hermes backend (`internal/backend/hermes`) currently speaks Hermes' OpenAI-Responses-compatible HTTP API over `/v1/responses`, sharing the `internal/backend/httpcommon` request/SSE/event plumbing with the OpenAI backend. That transport carries only chat text, which forces two client-side workarounds (a `prompts.jsonl` shadow log and a 3-hop `previous_response_id` history walk-back) and leaves most TUI features dark on Hermes connections.

Upstream Hermes (v0.11.0, "The Interface Release") rebuilt its own TUI on a Python JSON-RPC backend, `tui_gateway`, and exposes that same dispatcher over WebSocket at `/api/ws` (served by `hermes serve`, default port 9119). The desktop app and web dashboard are clients of this exact protocol — it is the first-party integration surface. `hermes serve` and the hardened WS auth ship from v0.16.0 (`v2026.6.5`).

This document records the design-level decisions and their rationale; the concrete reference material read from upstream source — wire protocol, the full RPC/event mapping, and the harness topology — is folded into the **Reference** appendix at the end so the change stands on its own.

A `backend.Backend` interface plus optional sub-interfaces (`StatusBackend`, `UsageBackend`, `CompactBackend`, `ExecBackend`, …) already exist and the OpenClaw backend already implements the full set. This change fills those interfaces in for Hermes rather than inventing new abstraction. The TUI stays backend-agnostic; the lingua franca remains `openclaw-go/protocol` types.

## Goals / Non-Goals

**Goals:**

- Replace the HTTP/SSE Hermes backend entirely with a JSON-RPC-over-WebSocket client against `hermes serve` — one transport, no fallback.
- Feature parity with OpenClaw wherever Hermes has the primitive: tool call cards, `/stats` + live header usage, `/compact`, real server-side abort, real session list / history / delete.
- Delete the client-side state directory and both HTTP workarounds; let the server be the source of truth for history.
- Durable verification: a live Hermes container in Docker in the CI/CD suite, mirroring the OpenClaw harness — an echo-model leg for zero-cost CI, a scripted tool-call leg for deterministic tool-event coverage, and a version-matrixed smoke job as a drift alarm.

**Non-Goals (this change, Phases 0–1):**

- `!!` remote exec (`shell.exec`), agent-initiated approval prompts, and `/model` switch via `command.dispatch` — Phase 2.
- Remote / gated auth (OAuth or password login + single-use `?ticket=`) — Phase 3. Phase 1 is loopback-token only, which covers "Hermes on my machine / my box over an SSH tunnel".
- `/crons` and `/think` — schema spikes required before committing `CronBackend` / `ThinkingBackend`.
- Hermes profile CRUD — `AgentManagement` stays `false`; profiles remain server-managed.
- Multi-session UI beyond what the TUI already renders for OpenClaw.

## Decisions

### Full replacement, no dual-transport fallback

**Decision:** Delete the `/v1/responses` path outright rather than keep it as a fallback or a second Hermes connection type.

**Rationale:** There are no real users of the Hermes integration today, so a clean break costs nothing and a dual-transport backend would double the surface we test and maintain forever. Existing connections pointing at the legacy API server keep their entry but fail `Connect` with a targeted migration error, because the user has to switch their server process (`gateway run` → `serve`) regardless — a human step no auto-migration can remove.

**Alternative considered:** Keep both transports behind a capability probe. Rejected — permanent complexity to serve a user base of zero.

### `rpc.Client` is small and Hermes-agnostic; no generated client

**Decision:** A new `internal/backend/hermes/rpc` package provides a generic newline-delimited JSON-RPC 2.0 client over WebSocket (`Call(ctx, method, params, &result)`, `Notifications() <-chan Notification`, `Close()`). The backend calls the ~20 methods it needs with typed param/result structs local to the package; we do not generate a client for the ~120-method registry.

**Rationale:** We consume a small, stable slice of the surface. A hand-written client keeps the dependency graph flat and the typed structs close to where they are used. The openclaw-go SDK transport is not reusable (it implements the OpenClaw gateway protocol, not generic JSON-RPC), but its supervision pattern is the model.

### Event translation is a pure function

**Decision:** `translate(n Notification) []protocol.Event` is pure and table-tested exhaustively without a socket. Hermes has no OpenClaw-style `toolCallId`; the backend assigns stable ids per `(sid, tool-invocation)` as it pairs `tool.start` / `tool.complete`, matching the shape the existing tool cards in `internal/tui/events.go` expect.

**Rationale:** The event mapping is the headline feature (tool cards) and the part most exposed to upstream shape drift. Isolating it as a pure function makes it the natural home for golden-fixture tests and keeps the socket lifecycle out of the unit tests.

### WebSocket library: `nhooyr.io/websocket` (`github.com/coder/websocket`)

**Decision:** Adopt `nhooyr.io/websocket` for the WS transport.

**Rationale:** Context-native API (fits our `Call(ctx, …)` shape), no CGo, actively maintained. `gorilla/websocket` is heavier and less context-friendly; the openclaw-go transport is the wrong protocol.

### Durable Docker-in-CI verification, echo-leg first

**Decision:** Retool the existing `test/integration/hermes/` harness rather than replace it, matching the OpenClaw harness shape: pinned upstream image in Docker, host-side Ollama for local dev, an echo-model leg for CI, a Go probe that fails setup fast, and `.env.hermes` consumed by build-tagged Go tests. Extend `test/integration/echomodel` with a scripted mode that emits an OpenAI-format `tool_calls` response on a magic marker so Hermes executes a real tool and emits real `tool.*` frames — the assertion is on protocol structure, not model behaviour. A new `hermes-smoke` CI job runs the echo leg matrixed across Hermes image tags (oldest supported + current stable).

**Rationale:** This is the user's explicit ask — verification must be durable and live in CI/CD, not a one-off local check. The version matrix is the drift alarm: upstream moves fast and a protocol change should break a pinned CI leg, not a user.

### Harness stays in token mode via a loopback bind + socat sidecar

**Decision:** Run `hermes serve --host 127.0.0.1` inside the container (keeping loopback token auth, no OAuth in CI) and forward the published port with an `alpine/socat` sidecar sharing the container's network namespace. `network_mode: host` is the simpler Linux-only alternative; the socat shape is the default because it also works on Docker Desktop for macOS.

**Rationale:** Upstream's June-2026 hardening forces an auth provider on any non-loopback bind, so a naive published port would demand the full ticket flow in CI. The sidecar keeps the server seeing a loopback bind, loopback peer, and a `Host: localhost` alias the host guard accepts. This is the riskiest single assumption in the plan and is the first thing the Phase 0 spike validates.

### Phased delivery; this change is Phases 0–1

**Decision:** Phase 0 (spike: harness topology + golden JSON fixtures) and Phase 1 (core swap: rpc client, backend rewrite, migration error, harness + CI leg, tests) are in scope. Phases 2 (interactive) and 3 (remote auth) are follow-on changes.

**Rationale:** Phase 0 is the critical de-risking step — the RPC method names and event shapes in the design were read from upstream source, not observed on a live server. Committing fixtures first de-risks every later phase. Phase 1 delivers a working, tested backend without betting on the interactive or remote-auth surfaces, which have their own open questions.

## Risks / Trade-offs

- **Upstream RPC/event shapes are read-from-source, not live-verified** → Phase 0 spike captures golden JSON fixtures for `session.usage`, `session.history`, and the event stream before any backend code depends on them; `translate.go` is a pure function tested against those fixtures. We expect to feel our way here and adjust as the live server reveals shapes.
- **Harness auth topology (socat / `network_mode: host` / `--skip-build`) is unproven** → day-one spike validates both networking variants and the prebuilt-SPA image; fallback is configuring the bundled password provider and minting a `?ticket=` in the test client only (dragging a slice of Phase 3 forward).
- **Upstream churn — the WS surface is not a stability-guaranteed public API** → version-pinned CI matrix plus a supported floor (`v2026.6.5`); treat a break like an openclaw-go protocol bump.
- **WS disconnects on long turns and gateway idle-dormancy (upstream #48445, v0.18.0 drain)** → both are reconnect-and-resume problems; `Supervise` becomes a real backoff loop that re-issues `session.resume` on reconnect before reporting healthy, and the integration suite restarts the container mid-session to prove it.
- **Interactive server→client asks (`approval.request` et al.) can block a turn** → Phase 1 policy is auto-deny with a visible system message so a turn can never hang the TUI silently; Phase 2 wires them into the existing exec-approval prompt.
- **Delta coalescing (~30 fps server-side batches)** → no TUI impact expected (OpenClaw deltas are burstier), but the streaming integration test asserts ordering explicitly so a reorder regression is caught.

## Migration Plan

- **Deploy:** ship the rewritten backend; no data migration runs automatically.
- **Existing connections:** URLs pointing at the legacy API server (`:8642` or a `/v1` path) keep their entry but fail `Connect` with a targeted error instructing the user to run `hermes serve`, repoint the URL to the gateway (default `http://localhost:9119`), and paste the gateway token. A WS close code `4401` maps to the canonical `api key required` error so the auth modal opens.
- **Secrets:** the stored per-connection secret slot (formerly the `API_SERVER_KEY` bearer key) is reused as the gateway token; users paste the new token via the existing modal on the first `4401`.
- **Removed state:** `~/.lucinate/hermes/<conn-id>/` (`last_response_id`, `prompts.jsonl`) is no longer written or read.
- **Rollback:** revert the change; the legacy backend and its state files return. No persisted schema changes block a revert. This is called out as a breaking change for Hermes connections in the release notes.

## Open Questions

- `session.usage` and `session.history` payload shapes — method names and emit sites were read from source; the Phase 0 spike confirms them and commits fixtures.
- `cron.manage` and `/think` single-RPC action schemas — need a spike before committing `CronBackend` / `ThinkingBackend`; deliberately excluded from this change.
- Whether the published Hermes image ships a prebuilt SPA dist so `--skip-build` works, or whether the harness must build it — resolved in the Phase 0 spike.
- Exact WS close-code semantics beyond `4401` (bad credential) and `4403` (origin/host gate) under the hardened auth — confirmed against the live container during the spike.

## Reference (read from upstream source)

Everything here was read from `NousResearch/hermes-agent` at `main` (2026-07-15) — `tui_gateway/ws.py`, `tui_gateway/server.py`, `hermes_cli/web_server.py`, `hermes_cli/subcommands/dashboard.py` — and is unverified against a live server until the Phase 0 spike. Treat method names and payload shapes as provisional.

### Server and endpoint

`hermes serve --host <h> --port <p>` (default port **9119**) runs the headless "JSON-RPC/WebSocket gateway" that powers the desktop app; `hermes dashboard` boots the same server plus the browser SPA. The WS endpoint is **`/api/ws`**. This is **not** the legacy API server (`gateway run`, port 8642) the current backend talks to — that process does not register `/api/ws` (upstream issue #32882).

### Wire protocol

Newline-delimited JSON-RPC 2.0, identical in both directions, no extra framing or negotiation. On accept the server pushes:

```json
{"jsonrpc":"2.0","method":"event","params":{"type":"gateway.ready","payload":{"skin":"..."}}}
```

Requests are standard JSON-RPC calls (`id`, `method`, `params`); server → client pushes are `method:"event"` notifications with `params: {type, payload, sid}` where `sid` scopes the event to a session. High-frequency `*.delta` events are coalesced server-side into ~30 fps batches; ordering against non-delta frames is preserved.

### Auth (decided by bind address)

- **Loopback bind** (`127.0.0.1`, `localhost`, `::1`): no auth gate; connect with `?token=<session-token>`. The token is `HERMES_DASHBOARD_SESSION_TOKEN` when set in the server's environment, otherwise a random per-process value. Constant-time compared.
- **Non-loopback bind**: the June-2026 hardening makes an auth provider (bundled password or OAuth via Nous Portal) mandatory — `--insecure` is now a no-op. Clients authenticate to the dashboard auth API, then open the WS with a **single-use, 30s-TTL `?ticket=`**. This is the path native clients use (Phase 3).

An `Origin` header is only validated **when present**, so a native Go client (which sends none) passes the origin gate; a Host/DNS-rebinding check and a peer-IP check still apply. Rejections close the socket with **4401** (bad/missing credential) or **4403** (origin/host/embedded-chat gate).

### Version floor

`tui_gateway` exists from v0.11.0 (`v2026.4.23`), but `hermes serve` and the hardened WS auth ship from the v0.16.0 desktop release (`v2026.6.5`). Supported floor is **`v2026.6.5`**; the integration harness pins the newest stable tag (currently `v2026.7.7.2`, v0.18.2). Known upstream rough edges designed around: WS disconnects during long foreground turns (#48445) and gateway idle-dormancy/drain introduced in v0.18.0 — both reconnect-and-resume problems.

### Backend method → RPC mapping

| `backend.Backend` method | Hermes RPC | Notes |
|---|---|---|
| `Connect` | dial + `gateway.ready` + `agents.list` | see Decisions / Migration |
| `Close` | WS close | server reaps/detaches sessions on disconnect |
| `Events` | notification pump → `translate` | event table below |
| `Supervise` | real reconnect loop | no longer the HTTP "block forever" stub |
| `ListAgents` | `agents.list` | fall back to one synthetic profile agent if empty |
| `CreateAgent` / `DeleteAgent` | — | rejected; `AgentManagement: false` |
| `SessionsList` | `session.list` (+ `session.active_list`) | mapped to `protocol.SessionsListResult` JSON |
| `CreateSession` | `session.create` / `session.resume` | returns Hermes session id as the session key |
| `SessionDelete` | `session.delete` | replaces the old "wipe local pointer" `/reset` |
| `ChatSend` | `prompt.submit` `{sid, text}` | run id = generated idempotency key; skills catalogue prepended as a system preamble on turn 1 |
| `ChatAbort` | `session.interrupt` | real server-side interrupt |
| `ChatHistory` | `session.history` | full transcript from the server |
| `ModelsList` | `model.options` | read-only listing |
| `SessionPatchModel` | `command.dispatch {"command":"/model …"}` (Phase 2) | error in Phase 1 |

Sub-interfaces: `StatusBackend` (local + `session.status` / `verification.status`, Phase 1), `UsageBackend` (`session.usage`, `session.context_breakdown`, Phase 1), `CompactBackend` (`session.compress`, Phase 1), `ExecBackend` (`shell.exec`, Phase 2), `ThinkingBackend`/`CronBackend` (schema spike). The `!!` mapping is direct-execute: `ExecRequest` calls `shell.exec` and synthesises the `exec.finished` event; there is no gateway-side approval hop for *user*-initiated commands (`approval.request` is agent-initiated tool approval).

### Event translation table

`translate(n Notification) []protocol.Event` is pure and table-tested without a socket.

| Hermes event (`params.type`) | → `protocol.Event` | Notes |
|---|---|---|
| `message.start` | — (arms accumulator) | new assistant message begins |
| `message.delta` | `EventChat` state=`delta` | text accumulated per sid |
| `message.complete` | `EventChat` state=`final` | attach usage snapshot when a `session.usage` refresh is cheap |
| `error` | `EventChat` state=`error` | |
| interrupt ack (`session.interrupt` result) | `EventChat` state=`aborted` | |
| `tool.start` | `EventAgent` stream=`tool`, phase=`start` | `{toolCallId, name, args}` feeds existing tool cards in `events.go` |
| `tool.generating` | `EventAgent` stream=`tool`, phase=`update` | TUI ignores `update` today; forwarded for the expand/collapse follow-up |
| `tool.complete` | `EventAgent` stream=`tool`, phase=`result` | `isError` + result payload → success/error card state |
| `tool.output_risk` | `EventAgent` stream=`tool`, phase=`update` | annotation only |
| `thinking.delta`, `reasoning.delta` | `EventAgent` stream=`thinking` | ignored by the TUI today; enables a thinking indicator later |
| `reasoning.available`, `session.info`, `status.update` | internal | refresh cached session metadata |
| `gateway.ready` | internal | handshake / reconnect signal |

Hermes does not send OpenClaw-style `toolCallId`s under that name; the backend assigns stable ids per `(sid, tool-invocation)` as it pairs `tool.start`/`tool.complete`, matching the shape `toolEventData` expects in `internal/tui/events.go`.

### Package layout

```
internal/backend/hermes/
  backend.go        Backend impl: lifecycle, agents, sessions, chat
  translate.go      hermes event → protocol.Event mapping (pure, unit-testable)
  usage.go          UsageBackend / CompactBackend / StatusBackend impls
  exec.go           ExecBackend impl (shell.exec) — Phase 2
  rpc/
    client.go       WS dial + NDJSON JSON-RPC client
    client_test.go  against an in-process fake WS server
  backend_test.go   unit tests with a fake rpc server
  integration_test.go  //go:build integration_hermes — live container
```

### Harness topology (compose)

The container runs `hermes serve` bound to loopback (keeping token mode) with an `alpine/socat` sidecar forwarding the published port:

```yaml
services:
  hermes:
    image: nousresearch/hermes-agent:${HERMES_TAG:-v2026.7.7.2}
    command: ["serve", "--host", "127.0.0.1", "--port", "9119", "--skip-build"]
    ports: ["19119:19119"]
    extra_hosts: ["host.docker.internal:host-gateway"]
    volumes: ["./state:/opt/data"]
    environment:
      HERMES_DASHBOARD_SESSION_TOKEN: "lucinate"
  wsproxy:
    image: alpine/socat
    network_mode: "service:hermes"
    command: ["TCP-LISTEN:19119,fork,bind=0.0.0.0", "TCP:127.0.0.1:9119"]
```

The server sees a loopback bind (token mode stays active), a loopback peer, and a `Host: localhost:19119` header the host guard accepts as a loopback alias. `network_mode: host` is a simpler Linux-only alternative; the socat shape is the default because it also works on Docker Desktop for macOS. `--skip-build` assumes the published image ships a prebuilt SPA dist (`/api/ws` itself does not need the SPA) — validated in the spike.

Three inference legs, same as OpenClaw: **Ollama** (`qwen2.5:0.5b` on host, local dev), **Echo** (echomodel stub, zero-cost deterministic CI), **Echo scripted** (echomodel tool-call script, tool-event and approval-path tests). The scripted mode triggers on a magic marker (e.g. `[[tool:shell echo lucinate]]`): the stub replies with an OpenAI-format `tool_calls` response for Hermes' shell tool, then a plain-text follow-up carrying the tool result, so Hermes executes the tool for real and emits real `tool.*` frames.

### CI matrix

New `hermes-smoke` job in `.github/workflows/integration.yml` (sibling of `openclaw-smoke`), echo-leg only, matrixed as the drift alarm:

```yaml
hermes-smoke:
  strategy:
    matrix:
      hermes: ["v2026.6.5",   # v0.16.0 — oldest supported (serve + WS hardening)
               "v2026.7.7.2"] # current stable pin
```
