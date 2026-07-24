## 1. Phase 0 — Spike (de-risk before writing backend code)

- [x] 1.1 Stand up a pinned container locally and confirm the WS handshake — **done** against `v2026.6.5`. Launch command is `hermes dashboard --host 127.0.0.1 --port 9119 --no-open --skip-build` (there is no `hermes serve`); `/api/ws?token=` connects and pushes `gateway.ready`.
- [x] 1.2 Settle the harness host-reachability topology — **done, version-dependent**: `--insecure --host 0.0.0.0` keeps token-mode on `v2026.6.5`, but `v2026.7.7.2` refuses any non-loopback bind without an auth provider (caught by the CI matrix — the drift alarm's first save). Settled shape: **loopback bind + `alpine/socat` sidecar**, token-mode on every version. `--skip-build` serving the prebuilt SPA is confirmed on both tags.
- [x] 1.3 Capture golden payloads from the live gateway — **done**, recorded in [phase0-fixtures.md](phase0-fixtures.md): `agents.list`, `session.create`/`list`/`history`/`usage`/`status`, `model.options`, `cron.manage`, and the event stream (`session.info`, `message.start`/`delta`/`complete`, `thinking.delta`, `reasoning.available`, `tool.generating`/`start`/`complete`, `error`). Still to capture: the `session.interrupt` aborted-event shape and `approval.request`/`clarify.request` (need targeted triggers). Convert these into `internal/backend/hermes/testdata` JSON when implementing task 3.2.
- [x] 1.4 Confirm the auth-failure signal — **done**: a bad/missing token is rejected at the **WS upgrade with HTTP 403** (no `4401`/`4403` close frame). The backend keys auth recovery off the 403 upgrade status.
- [x] 1.5 Record spike findings in the change's `design.md` and `phase0-fixtures.md` — **done** (command, auth, RPC registry, event shapes, two session id-spaces, `tool_id` presence, inline usage; corrected the stale assumptions).

## 2. RPC client (`internal/backend/hermes/rpc`)

- [x] 2.1 WebSocket dependency — resolved without adding one: reuse the existing `github.com/gorilla/websocket` direct dependency (required by the openclaw-go gateway SDK; `Dialer.DialContext` covers the context-aware dial)
- [x] 2.2 Implement `rpc.Client`: WS dial, newline-delimited JSON-RPC 2.0 read/write loop, id-correlated `Call(ctx, method, params, &result)` with per-call timeout, notification fan-out on `Notifications() <-chan Notification`, and `Close()` semantics — done; also exposes `Done()`/`Err()` for the supervisor and `*UpgradeError` (HTTP 403 → auth recovery)
- [x] 2.3 Unit-test `rpc.Client` against an in-process fake WS server (`client_test.go`): id correlation (out-of-order responses), timeout/cancel leaves client usable, notification routing, RPC error surfacing, close behaviour, server-drop signalling, 403 upgrade rejection, newline-batched frames — green under `-race`

## 3. Event translation (`translate.go`)

- [x] 3.1 Implement the translation layer (`translate.go`) — done as an I/O-free `Translator` (state = per-session delta accumulator, because the TUI expects cumulative text per delta while the gateway sends increments, plus per-session run ids). Covers deltas/final-with-inline-usage/interrupted→aborted/error, tool start/result paired by server `tool_id` with error from `result.error`/`exit_code` re-wrapped into the TUI's `{content:[{type,text}]}` shape, thinking/reasoning, and interactive asks surfaced as a typed `Ask` for the backend to decline
- [x] 3.2 Table-test the translator against the Phase 0 golden fixtures — done; fixtures committed under `internal/backend/hermes/testdata/events/` (13 live captures + 1 constructed error-result variant), tests green under `-race`

## 4. Backend core rewrite (`backend.go`)

- [x] 4.1 Rewrite `Connect`: derive `ws(s)://…/api/ws` from the connection URL, dial with the gateway token as `?token=`, await `gateway.ready` with timeout, then `agents.list`; map an **HTTP 403 upgrade rejection** → canonical `api key required`
- [x] 4.2 Implement the legacy-endpoint migration error (URL on `:8642` or with a `/v1` path → targeted "run `hermes dashboard`, repoint to :9119, paste gateway token" error)
- [x] 4.3 Implement `ListAgents` (single synthetic `hermes` agent from the profile model; fallback when the gateway lists none) and keep `CreateAgent`/`DeleteAgent` rejecting
- [x] 4.4 Implement server-side sessions: `SessionsList` (`session.list`), `CreateSession` (`session.create`/`resume`), `SessionDelete` (`session.delete`), `ChatHistory` (`session.history`)
- [x] 4.5 Implement `ChatSend` (`prompt.submit` with idempotency-key run id, skills-catalogue preamble on turn 1) and `ChatAbort` (`session.interrupt`; aborted turn ends with `message.complete` `status:"interrupted"`), wiring the notification pump through `translate.go` into `Events`
- [x] 4.6 Implement `Supervise` as a real backoff reconnect loop: `gateway.ready` liveness, connection-state transitions to the TUI banner, `session.resume` on reconnect before reporting healthy, in-flight calls fail fast with a retryable error
- [x] 4.7 Auto-decline interactive agent asks by calling the paired `<type>.respond` RPC with the ask's `request_id` (`approval.respond {choice:"deny"}`, `clarify.respond`, `sudo.respond`, `secret.respond`) plus a visible system message so a turn never hangs

## 5. Sub-interfaces (`usage.go`)

- [x] 5.1 Implement `StatusBackend`: `BackendStatus` with type, gateway URL, auth mode, model, and active session; keep the OpenClaw gateway-health block gated off for Hermes
- [x] 5.2 Implement `UsageBackend` (`session.usage` → header/`/stats` fields) and `CompactBackend` (session-compress RPC)
- [x] 5.3 Report the phase-1 `Capabilities` set (GatewayStatus true, AgentManagement false, AuthRecovery APIKey/gateway-token; Usage/Compact/Status implemented; Exec/Thinking/Cron off)

## 6. Config, secrets, form, removals

- [x] 6.1 Repurpose the per-connection secret slot as the gateway token; confirm the `secretAwareHermesBackend` shim and `StoreAPIKey` persistence still work; relabel the auth modal copy for Hermes ("gateway token")
- [x] 6.2 Update the Hermes connections form preset: `Base URL = http://localhost:9119`, drop the `/v1` hint and the model field; verify preset-switch prefill clearing
- [x] 6.3 Delete the old transport: `prompts.go` + tests, the `/v1/responses` SSE path, the history walk-back, Hermes' use of `internal/backend/httpcommon`, and the `~/.lucinate/hermes/<conn-id>/` state handling

## 7. Unit tests for the backend

- [x] 7.1 Rewrite `backend_test.go` against a fake `rpc` server: connect/handshake, agents, sessions round-trip, chat stream reassembly, abort, usage/compact, migration error, HTTP-403-upgrade auth-recovery mapping
- [x] 7.2 Ensure `make test` and `make fmt` pass; remove obsolete assertions tied to the deleted transport

## 8. Integration harness (durable, in CI/CD)

- [x] 8.1 Retool `test/integration/hermes/` compose to run `hermes dashboard` bound to loopback with an `alpine/socat` sidecar forwarding the published port (per the revised task 1.2 — newer tags refuse non-loopback binds); update `setup-hermes.sh` to poll the gateway on the published port and write `LUCINATE_HERMES_BASE_URL` + `LUCINATE_HERMES_TOKEN` to `.env.hermes` (keep the two synced `HERMES_TAG` pins)
- [x] 8.2 Update the probe (`test/integration/hermes/probe`) to dial the WS, await `gateway.ready`, and round-trip `session.create` + `session.list`
- [x] 8.3 Extend `test/integration/echomodel` with a scripted mode: on a magic marker (e.g. `[[tool:shell echo lucinate]]`) return an OpenAI-format `tool_calls` response, then a plain-text follow-up carrying the tool result
- [x] 8.4 Write `integration_test.go` (`//go:build integration_hermes`): connect/handshake + bad-token HTTP-403 upgrade, legacy-endpoint rejection, sessions + history round-trip, chat streaming order, abort, tool events (scripted leg), usage/compact, and reconnect (`docker compose restart hermes` mid-session → resume)
- [x] 8.5 Add the `hermes-smoke` job to `.github/workflows/integration.yml` (echo leg, matrixed across `v2026.6.5` oldest-supported and the current stable pin) as the protocol-drift alarm
- [x] 8.6 Confirm `make test-integration-hermes` is green — verified live on the echo leg against `v2026.6.5` (all tests pass; reconnect test skips where the docker CLI lacks the compose plugin and runs in CI). Ollama leg unchanged in shape; harness discoveries (session.close-before-delete, zeroed session.usage) folded back into backend + fixtures

## 9. Docs and close-out

- [x] 9.1 Rewrite `docs/backend_hermes.md` for the WebSocket backend (the design record lives in this change's `design.md`)
- [x] 9.2 Release-notes handling — resolved as **not applicable**: there are no users of the Hermes integration, so this ships as a normal feature change (no breaking-change marker; CHANGELOG stays owned by the release process as always)
- [ ] 9.3 Sync the delta specs into `openspec/specs/` and archive the change once implementation is verified
