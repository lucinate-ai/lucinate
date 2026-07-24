## Context

The Hermes backend (`internal/backend/hermes`) currently speaks Hermes' OpenAI-Responses-compatible HTTP API over `/v1/responses`, sharing the `internal/backend/httpcommon` request/SSE/event plumbing with the OpenAI backend. That transport carries only chat text, which forces two client-side workarounds (a `prompts.jsonl` shadow log and a 3-hop `previous_response_id` history walk-back) and leaves most TUI features dark on Hermes connections.

Upstream Hermes rebuilt its own TUI on a Python JSON-RPC backend, `tui_gateway`, and exposes that same dispatcher over WebSocket at `/api/ws`. That server is run by **`hermes dashboard`** (default port 9119) — the desktop app and web dashboard are clients of this exact protocol, the first-party integration surface. `hermes dashboard` and the WS gateway ship from the v0.16.0 release (`v2026.6.5`), which is the supported floor.

The RPC/event surface described here was **verified against a live `nousresearch/hermes-agent:v2026.6.5` container** during the Phase 0 spike (2026-07-19). The concrete golden payloads live in [phase0-fixtures.md](phase0-fixtures.md); the Reference appendix below summarises the shapes at design altitude. Several original read-from-source assumptions were wrong and are corrected throughout (see the callouts).

A `backend.Backend` interface plus optional sub-interfaces (`StatusBackend`, `UsageBackend`, `CompactBackend`, `ExecBackend`, …) already exist and the OpenClaw backend already implements the full set. This change fills those interfaces in for Hermes rather than inventing new abstraction. The TUI stays backend-agnostic; the lingua franca remains `openclaw-go/protocol` types.

## Goals / Non-Goals

**Goals:**

- Replace the HTTP/SSE Hermes backend entirely with a JSON-RPC-over-WebSocket client against `hermes dashboard` — one transport, no fallback.
- Feature parity with OpenClaw wherever Hermes has the primitive: tool call cards, `/stats` + live header usage, `/compact`, real server-side abort, real session list / history / delete.
- Delete the client-side state directory and both HTTP workarounds; let the server be the source of truth for history.
- Durable verification: a live Hermes container in Docker in the CI/CD suite, mirroring the OpenClaw harness — an echo-model leg for zero-cost CI, a scripted tool-call leg for deterministic tool-event coverage, and a version-matrixed smoke job as a drift alarm.

**Non-Goals (this change, Phases 0–1):**

- `!!` remote exec (`shell.exec`), agent-initiated approval prompts, and `/model` switch via `command.dispatch` — Phase 2.
- Remote / gated auth (OAuth or password login) — Phase 3. Phase 1 is loopback-token only, which covers "Hermes on my machine / my box over an SSH tunnel".
- `/think` — schema spike required before committing `ThinkingBackend`. (`cron.manage` turned out to work out of the box; see Decisions — `CronBackend` is a cheap follow-on but still out of scope here to keep the core swap tight.)
- Hermes profile CRUD — `AgentManagement` stays `false`; profiles remain server-managed.
- Multi-session UI beyond what the TUI already renders for OpenClaw.

## Decisions

### Full replacement, no dual-transport fallback

**Decision:** Delete the `/v1/responses` path outright rather than keep it as a fallback or a second Hermes connection type.

**Rationale:** There are no real users of the Hermes integration today, so a clean break costs nothing and a dual-transport backend would double the surface we test and maintain forever. Existing connections pointing at the legacy API server keep their entry but fail `Connect` with a targeted migration error, because the user has to switch their server process (`gateway run` → `dashboard`) regardless — a human step no auto-migration can remove.

**Alternative considered:** Keep both transports behind a capability probe. Rejected — permanent complexity to serve a user base of zero.

### `rpc.Client` is small and Hermes-agnostic; no generated client

**Decision:** A new `internal/backend/hermes/rpc` package provides a generic newline-delimited JSON-RPC 2.0 client over WebSocket (`Call(ctx, method, params, &result)`, `Notifications() <-chan Notification`, `Close()`). The backend calls the ~18 methods it needs with typed param/result structs local to the package; we do not generate a client for the full registry.

**Rationale:** We consume a small, stable slice of the surface. A hand-written client keeps the dependency graph flat and the typed structs close to where they are used. The openclaw-go SDK transport is not reusable (it implements the OpenClaw gateway protocol, not generic JSON-RPC), but its supervision pattern is the model.

### Event translation is a pure function — and Hermes provides tool ids

**Decision:** `translate(n Notification) []protocol.Event` is pure and table-tested exhaustively without a socket.

**Correction from the spike:** the original design assumed Hermes sends no tool-call id and the backend must synthesise stable ids by pairing `tool.start`/`tool.complete`. **Not so** — `tool.start` and `tool.complete` both carry a `tool_id` (OpenAI-style `call_…`). The backend uses that id directly to pair start/result and feed the existing tool cards, so the id-synthesis logic is dropped entirely. `tool.start` carries `{tool_id, name, context}` (a human summary), `tool.complete` carries `{tool_id, name, args, duration_s, result:{output, exit_code, error}}`; error state comes from `result.error`/`exit_code`, not an `isError` field.

**Rationale:** The event mapping is the headline feature (tool cards) and the part most exposed to upstream shape drift. Isolating it as a pure function makes it the natural home for golden-fixture tests and keeps the socket lifecycle out of the unit tests.

### Usage comes inline on `message.complete`

**Decision:** `UsageBackend` and the streamed-final path both read the `usage` object embedded in `message.complete` rather than issuing a follow-up call on every turn.

**Correction from the spike:** the design assumed a separate `session.usage` fetch to attach usage to the final event. In reality `message.complete.payload.usage` is rich and inline (`input/output/total/calls/context_used/context_max/context_percent/cost_usd/…`), so the final event already has everything the header needs. `session.usage` still exists (`{calls,input,output,total}`) for an on-demand `/stats` refresh, but the per-turn path is free. `session.context_breakdown` does **not** exist — dropped.

### WebSocket library: reuse `gorilla/websocket` (already a dependency)

**Decision:** Build the `rpc` client on `github.com/gorilla/websocket` — no new dependency.

**Rationale:** gorilla is already a direct dependency: the openclaw-go gateway SDK rides it, and `internal/client`'s dial tests use it in-tree, so it is in the dependency graph permanently. Its `Dialer.DialContext` covers the context-aware dial, and read/write deadlines cover per-call timeouts. Adding `nhooyr.io/websocket` (the original proposal) would mean two WS libraries for one job. The openclaw-go transport itself is still not reusable (it implements the OpenClaw gateway protocol, not generic JSON-RPC), but its supervision pattern is the model.

### Auth detection: HTTP 403 on the WS upgrade

**Decision:** `Connect` treats a **failed WebSocket upgrade with HTTP 403** as the auth-recovery trigger, mapping it to the canonical `api key required` error.

**Correction from the spike:** the design claimed a bad/missing token yields a post-open WS **close code 4401** (and `4403` for host/origin). In reality a bad or missing token is rejected at the **HTTP upgrade with status 403** — the socket never opens, so there is no 4401 close frame to observe. The backend keys auth recovery off the 403 upgrade status instead.

### Durable Docker-in-CI verification, echo-leg first

**Decision:** Retool the existing `test/integration/hermes/` harness rather than replace it, matching the OpenClaw harness shape: pinned upstream image in Docker, host-side Ollama for local dev, an echo-model leg for CI, a Go probe that fails setup fast, and `.env.hermes` consumed by build-tagged Go tests. Extend `test/integration/echomodel` with a scripted mode that emits an OpenAI-format `tool_calls` response on a magic marker so Hermes executes a real tool and emits real `tool.*` frames — the assertion is on protocol structure, not model behaviour. A new `hermes-smoke` CI job runs the echo leg matrixed across Hermes image tags (oldest supported + current stable).

**Rationale:** This is the user's explicit ask — verification must be durable and live in CI/CD, not a one-off local check. The version matrix is the drift alarm: upstream moves fast and a protocol change should break a pinned CI leg, not a user. The spike confirmed the harness can drive real chat and tool turns against the container with an OpenAI-compatible provider, which is exactly what the echomodel leg needs.

### Harness runs `hermes dashboard --insecure`; no socat sidecar

**Decision:** The container runs `hermes dashboard --host 0.0.0.0 --port 9119 --no-open --skip-build --insecure` and publishes the port directly — a single compose service, no socat sidecar.

**Correction from the spike:** the design proposed a loopback bind + `alpine/socat` sidecar (its self-described "riskiest single assumption") because a non-loopback bind was assumed to force OAuth. **Tested and false.** Driving `--insecure --host 0.0.0.0` from a genuine non-loopback peer (a sibling container over a Docker network) connected cleanly with `?token=` and returned `gateway.ready`; bad/missing token still gave HTTP 403; a non-loopback `Host` header was accepted (no host-guard rejection). So `--insecure` keeps token-mode on a non-loopback bind and the socat trickery is unnecessary. `--skip-build` serves the prebuilt SPA at `/opt/hermes/hermes_cli/web_dist` (confirmed present).

### Phased delivery; this change is Phases 0–1

**Decision:** Phase 0 (spike: harness topology + golden JSON fixtures) and Phase 1 (core swap: rpc client, backend rewrite, migration error, harness + CI leg, tests) are in scope. Phases 2 (interactive) and 3 (remote auth) are follow-on changes.

**Rationale:** Phase 0 was the critical de-risking step, and it earned its keep — the live spike corrected the launch command (`dashboard`, not `serve`), the auth-failure signal (HTTP 403, not a 4401 close), the tool-id assumption, the usage-delivery path, and several method/payload shapes. The corrected fixtures now anchor Phase 1's structs and `translate.go` tests. Phase 1 delivers a working, tested backend without betting on the interactive or remote-auth surfaces, which have their own open questions.

## Risks / Trade-offs

- **Two session id-spaces** → `session.create` returns a short live `session_id` (used by `prompt.submit`/`history`/`usage`/`interrupt`) *and* a timestamped `stored_session_id`, which is the `id` that appears in `session.list`. The backend must track both: the live handle for the active turn, the stored id for listing/resume. Empty sessions aren't listed until they have a message, so a freshly created session won't round-trip through `session.list` immediately.
- **Upstream churn — the WS surface is not a stability-guaranteed public API** → version-pinned CI matrix plus a supported floor (`v2026.6.5`); treat a break like an openclaw-go protocol bump. The spike already showed the design was drifting from a source read; the pinned live harness is what keeps us honest.
- **WS disconnects on long turns and gateway idle-dormancy** → both are reconnect-and-resume problems; `Supervise` becomes a real backoff loop that re-issues `session.resume` on reconnect before reporting healthy, and the integration suite restarts the container mid-session to prove it.
- **Interactive server→client asks (`approval.request` et al.) can block a turn** → Phase 1 policy is auto-deny/cancel via the paired `*.respond` RPC (`approval.respond {choice:"deny"}`) with a visible system message so a turn can never hang the TUI silently; Phase 2 wires them into the existing exec-approval prompt. `clarify.request` was captured live (`{question, choices, request_id}`); the `approval.request` payload still needs capturing before Phase 2.
- **Delta coalescing** → `message.delta` frames arrive incrementally; no TUI impact expected (OpenClaw deltas are burstier), but the streaming integration test asserts ordering explicitly so a reorder regression is caught.

## Migration Plan

- **Deploy:** ship the rewritten backend; no data migration runs automatically.
- **Existing connections:** URLs pointing at the legacy API server (`:8642` or a `/v1` path) keep their entry but fail `Connect` with a targeted error instructing the user to run `hermes dashboard`, repoint the URL to the gateway (default `http://localhost:9119`), and paste the gateway token. A failed WS upgrade with **HTTP 403** maps to the canonical `api key required` error so the auth modal opens.
- **Secrets:** the stored per-connection secret slot (formerly the `API_SERVER_KEY` bearer key) is reused as the gateway token (`HERMES_DASHBOARD_SESSION_TOKEN`); users paste the new token via the existing modal on the first 403.
- **Removed state:** `~/.lucinate/hermes/<conn-id>/` (`last_response_id`, `prompts.jsonl`) is no longer written or read.
- **Rollback:** revert the change; the legacy backend and its state files return. No persisted schema changes block a revert. With no users of the Hermes integration, this ships as a normal feature change — no breaking-change callout is needed.

## Open Questions

- **Live `approval.request` payload** — the response method (`approval.respond {choice:"deny"}`) is confirmed and `clarify.request` was captured live, but the `approval.request` payload itself needs a shell-hook / risky tool to trigger; capture before Phase 2.
- **`/think`** — no single-RPC surface confirmed; still a spike before committing `ThinkingBackend`.

## Reference (verified against `v2026.6.5`)

Full golden payloads are in [phase0-fixtures.md](phase0-fixtures.md). Summary:

### Server, endpoint, auth

- **Launch:** `hermes dashboard --host <h> --port <p> --no-open --skip-build` (no `serve` command). Default port **9119**, WS endpoint **`/api/ws`**. Legacy `gateway run` on 8642 is unchanged and does not serve `/api/ws`.
- **Auth:** loopback bind + `?token=<HERMES_DASHBOARD_SESSION_TOKEN>`. Bad/missing token → **HTTP 403 on the WS upgrade** (no 4401/4403 close). `--insecure` permits non-localhost binds with the token.
- **Wire:** newline-delimited JSON-RPC 2.0. On connect the server pushes `event:gateway.ready`. Server→client pushes are `method:"event"` with `params:{type, session_id, payload}` (envelope key is `session_id`, not `sid`). App-level error codes: `-32601` unknown method, `4001` session not found, `4004` empty command, `4006` session_id required, `4018` invalid command.

### Backend method → RPC mapping

| `backend.Backend` method | Hermes RPC | Notes |
|---|---|---|
| `Connect` | dial + `gateway.ready` + `agents.list` | 403 upgrade → `api key required` |
| `Close` | WS close | server reaps/detaches sessions on disconnect |
| `Events` | notification pump → `translate` | event table below |
| `Supervise` | real reconnect loop | re-issues `session.resume` on reconnect |
| `ListAgents` | `agents.list` → `{processes:[]}` | empty → one synthetic profile agent (profile from `session.create` `info.profile_name`) |
| `CreateAgent` / `DeleteAgent` | — | rejected; `AgentManagement: false` |
| `SessionsList` | `session.list` | entries `{id(=stored_session_id), title, preview, started_at, message_count, source}`; only sessions with messages listed |
| `CreateSession` | `session.create` / `session.resume` | returns **two** ids: live `session_id` + `stored_session_id` |
| `SessionDelete` | `session.close` (detach live) + `session.delete {session_id: stored}` | gateway refuses deleting an attached session (4023) |
| `ChatSend` | `prompt.submit {session_id, text}` → `{status:"streaming"}` | run id = idempotency key; skills catalogue preamble on turn 1 |
| `ChatAbort` | `session.interrupt {session_id}` | real server-side interrupt (aborted-event shape TBC) |
| `ChatHistory` | `session.history {session_id}` → `{count, messages}` | full transcript from the server |
| `ModelsList` | `model.options` → `{providers:[…], model, provider}` | provider-centric; active model = top-level `model` |
| `SessionPatchModel` | `command.dispatch {"command":"/model …"}` (Phase 2) | error in Phase 1 |

Sub-interfaces: `StatusBackend` (compose from `session.create` `info` + `session.usage`; **`verification.status` does not exist**, and `session.status` returns a pre-rendered text blob, not structured fields), `UsageBackend` (inline `message.complete.usage` per turn + `session.usage` for on-demand; **no `session.context_breakdown`**), `CompactBackend` (`session.compress`), `ExecBackend` (`shell.exec`, Phase 2), `ThinkingBackend` (spike). `cron.manage` exists and works (`{success,count,jobs}`) — `CronBackend` is a cheap follow-on, still out of scope here.

### Event translation table (verified)

Envelope: `{type, session_id, payload}`. Sequence (tool turn): `session.info → message.start → thinking.delta* → tool.generating → tool.start → tool.complete → message.delta* → reasoning.available → message.complete`.

| Hermes event | payload | → `protocol.Event` |
|---|---|---|
| `session.info` | `{model, tools, skills, …}` | internal — cache model/tools |
| `message.start` | *(none)* | — (arms accumulator) |
| `message.delta` | `{text}` | `EventChat` state=`delta` |
| `message.complete` | `{text, status, usage{…}}` | `status:"complete"` → `EventChat` state=`final`; `status:"interrupted"` → state=`aborted`; usage inline |
| `error` | `{message}` | `EventChat` state=`error` |
| `clarify.request` / `approval.request` / `sudo.request` / `secret.request` | `{…, request_id}` | Phase 1: auto-decline via `<type>.respond` (approval: `{choice:"deny"}`) + system message |
| `tool.generating` | `{name}` | `EventAgent` stream=`tool`, phase=`update` (pre-call) |
| `tool.start` | `{tool_id, name, context}` | `EventAgent` stream=`tool`, phase=`start` (id = `tool_id`) |
| `tool.complete` | `{tool_id, name, args, duration_s, result{output, exit_code, error}}` | `EventAgent` stream=`tool`, phase=`result`; error from `result.error`/`exit_code` |
| `thinking.delta` | `{text}` | `EventAgent` stream=`thinking` (decorative) |
| `reasoning.delta` | `{text}` | `EventAgent` stream=`thinking` (reasoning models; not seen from gpt-4o-mini) |
| `reasoning.available` | `{text}` | internal / optional thinking surface (whole reasoning text) |
| `gateway.ready` | `{skin}` | internal — handshake / reconnect signal |

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
  testdata/         golden fixtures captured in Phase 0 (see phase0-fixtures.md)
  backend_test.go   unit tests with a fake rpc server
  integration_test.go  //go:build integration_hermes — live container
```

### Harness topology (compose)

Single service — `hermes dashboard --insecure --host 0.0.0.0` with the port published directly (the spike proved token-mode survives a non-loopback bind, so no socat sidecar):

```yaml
services:
  hermes:
    image: nousresearch/hermes-agent:${HERMES_TAG:-v2026.7.7.2}
    command: ["dashboard", "--host", "0.0.0.0", "--port", "9119", "--no-open", "--skip-build", "--insecure"]
    ports: ["19119:9119"]
    extra_hosts: ["host.docker.internal:host-gateway"]
    volumes: ["./state:/opt/data"]
    environment:
      HERMES_DASHBOARD_SESSION_TOKEN: "lucinate"
```

`--skip-build` serves the prebuilt SPA (`/opt/hermes/hermes_cli/web_dist`, confirmed present). Three inference legs, same as OpenClaw: **Ollama** (`qwen2.5:0.5b` on host, local dev), **Echo** (echomodel stub, zero-cost deterministic CI), **Echo scripted** (echomodel tool-call script). The scripted mode triggers on a magic marker (e.g. `[[tool:shell echo lucinate]]`): the stub replies with an OpenAI-format `tool_calls` response, then a plain-text follow-up carrying the tool result, so Hermes executes the tool for real and emits real `tool.*` frames. (The spike proved this end to end against OpenRouter's `openai/gpt-4o-mini` via the first-class `openrouter` provider — the terminal tool ran and emitted real `tool.start`/`tool.complete` frames.)

### CI matrix

New `hermes-smoke` job in `.github/workflows/integration.yml` (sibling of `openclaw-smoke`), echo-leg only, matrixed as the drift alarm:

```yaml
hermes-smoke:
  strategy:
    matrix:
      hermes: ["v2026.6.5",   # v0.16.0 — oldest supported (dashboard + WS gateway)
               "v2026.7.7.2"] # current stable pin
```
