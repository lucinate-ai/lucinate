## 1. Phase 0 — Spike (de-risk before writing backend code)

- [x] 1.1 Stand up a pinned container locally and confirm the WS handshake — **done** against `v2026.6.5`. Launch command is `hermes dashboard --host 127.0.0.1 --port 9119 --no-open --skip-build` (there is no `hermes serve`); `/api/ws?token=` connects and pushes `gateway.ready`.
- [ ] 1.2 Settle the harness host-reachability topology: confirm whether `dashboard --insecure --host 0.0.0.0` on a published port keeps token-mode (dropping the `alpine/socat` sidecar) or whether the loopback-bind + socat shape is required; validate `network_mode: host` on Linux CI. (`--skip-build` serving the prebuilt SPA at `/opt/hermes/hermes_cli/web_dist` is **confirmed**.)
- [x] 1.3 Capture golden payloads from the live gateway — **done**, recorded in [phase0-fixtures.md](phase0-fixtures.md): `agents.list`, `session.create`/`list`/`history`/`usage`/`status`, `model.options`, `cron.manage`, and the event stream (`session.info`, `message.start`/`delta`/`complete`, `thinking.delta`, `reasoning.available`, `tool.generating`/`start`/`complete`, `error`). Still to capture: the `session.interrupt` aborted-event shape and `approval.request`/`clarify.request` (need targeted triggers). Convert these into `internal/backend/hermes/testdata` JSON when implementing task 3.2.
- [x] 1.4 Confirm the auth-failure signal — **done**: a bad/missing token is rejected at the **WS upgrade with HTTP 403** (no `4401`/`4403` close frame). The backend keys auth recovery off the 403 upgrade status.
- [x] 1.5 Record spike findings in the change's `design.md` and `phase0-fixtures.md` — **done** (command, auth, RPC registry, event shapes, two session id-spaces, `tool_id` presence, inline usage; corrected the stale assumptions).

## 2. RPC client (`internal/backend/hermes/rpc`)

- [ ] 2.1 Add the `nhooyr.io/websocket` (`github.com/coder/websocket`) dependency; run `go mod tidy` and confirm the build
- [ ] 2.2 Implement `rpc.Client`: WS dial, newline-delimited JSON-RPC 2.0 read/write loop, id-correlated `Call(ctx, method, params, &result)` with per-call timeout, notification fan-out on `Notifications() <-chan Notification`, and `Close()` semantics
- [ ] 2.3 Unit-test `rpc.Client` against an in-process fake WS server (`client_test.go`): id correlation, timeout/cancel, notification routing, close behaviour

## 3. Event translation (`translate.go`)

- [ ] 3.1 Implement the pure `translate(n Notification) []protocol.Event` function covering the event table (chat deltas/final-with-inline-usage/error, tool start/result paired by the server-supplied `tool_id`, error state from `result.error`/`exit_code`, thinking/reasoning)
- [ ] 3.2 Table-test `translate` exhaustively against the Phase 0 golden fixtures, including the error-result tool-card variant

## 4. Backend core rewrite (`backend.go`)

- [ ] 4.1 Rewrite `Connect`: derive `ws(s)://…/api/ws` from the connection URL, dial with the gateway token as `?token=`, await `gateway.ready` with timeout, then `agents.list`; map an **HTTP 403 upgrade rejection** → canonical `api key required`
- [ ] 4.2 Implement the legacy-endpoint migration error (URL on `:8642` or with a `/v1` path → targeted "run `hermes dashboard`, repoint to :9119, paste gateway token" error)
- [ ] 4.3 Implement `ListAgents` (single synthetic `hermes` agent from the profile model; fallback when the gateway lists none) and keep `CreateAgent`/`DeleteAgent` rejecting
- [ ] 4.4 Implement server-side sessions: `SessionsList` (`session.list`), `CreateSession` (`session.create`/`resume`), `SessionDelete` (`session.delete`), `ChatHistory` (`session.history`)
- [ ] 4.5 Implement `ChatSend` (`prompt.submit` with idempotency-key run id, skills-catalogue preamble on turn 1) and `ChatAbort` (`session.interrupt`), wiring the notification pump through `translate.go` into `Events`
- [ ] 4.6 Implement `Supervise` as a real backoff reconnect loop: `gateway.ready` liveness, connection-state transitions to the TUI banner, `session.resume` on reconnect before reporting healthy, in-flight calls fail fast with a retryable error
- [ ] 4.7 Auto-decline interactive agent asks (`approval.request`/`clarify.request`/`sudo.request`/`secret.request`) with a visible system message so a turn never hangs

## 5. Sub-interfaces (`usage.go`)

- [ ] 5.1 Implement `StatusBackend`: `BackendStatus` with type, gateway URL, auth mode, model, and active session; keep the OpenClaw gateway-health block gated off for Hermes
- [ ] 5.2 Implement `UsageBackend` (`session.usage` → header/`/stats` fields) and `CompactBackend` (session-compress RPC)
- [ ] 5.3 Report the phase-1 `Capabilities` set (GatewayStatus true, AgentManagement false, AuthRecovery APIKey/gateway-token; Usage/Compact/Status implemented; Exec/Thinking/Cron off)

## 6. Config, secrets, form, removals

- [ ] 6.1 Repurpose the per-connection secret slot as the gateway token; confirm the `secretAwareHermesBackend` shim and `StoreAPIKey` persistence still work; relabel the auth modal copy for Hermes ("gateway token")
- [ ] 6.2 Update the Hermes connections form preset: `Base URL = http://localhost:9119`, drop the `/v1` hint and the model field; verify preset-switch prefill clearing
- [ ] 6.3 Delete the old transport: `prompts.go` + tests, the `/v1/responses` SSE path, the history walk-back, Hermes' use of `internal/backend/httpcommon`, and the `~/.lucinate/hermes/<conn-id>/` state handling

## 7. Unit tests for the backend

- [ ] 7.1 Rewrite `backend_test.go` against a fake `rpc` server: connect/handshake, agents, sessions round-trip, chat stream reassembly, abort, usage/compact, migration error, HTTP-403-upgrade auth-recovery mapping
- [ ] 7.2 Ensure `make test` and `make fmt` pass; remove obsolete assertions tied to the deleted transport

## 8. Integration harness (durable, in CI/CD)

- [ ] 8.1 Retool `test/integration/hermes/` compose to run `hermes dashboard --no-open --skip-build` with the Phase 0 networking topology (task 1.2); update `setup-hermes.sh` to poll the gateway on the published port and write `LUCINATE_HERMES_BASE_URL` + `LUCINATE_HERMES_TOKEN` to `.env.hermes` (keep the two synced `HERMES_TAG` pins)
- [ ] 8.2 Update the probe (`test/integration/hermes/probe`) to dial the WS, await `gateway.ready`, and round-trip `session.create` + `session.list`
- [ ] 8.3 Extend `test/integration/echomodel` with a scripted mode: on a magic marker (e.g. `[[tool:shell echo lucinate]]`) return an OpenAI-format `tool_calls` response, then a plain-text follow-up carrying the tool result
- [ ] 8.4 Write `integration_test.go` (`//go:build integration_hermes`): connect/handshake + bad-token HTTP-403 upgrade, legacy-endpoint rejection, sessions + history round-trip, chat streaming order, abort, tool events (scripted leg), usage/compact, and reconnect (`docker compose restart hermes` mid-session → resume)
- [ ] 8.5 Add the `hermes-smoke` job to `.github/workflows/integration.yml` (echo leg, matrixed across `v2026.6.5` oldest-supported and the current stable pin) as the protocol-drift alarm
- [ ] 8.6 Confirm `make test-integration-hermes` is green on both the echo and Ollama legs

## 9. Docs and close-out

- [ ] 9.1 Rewrite `docs/backend_hermes.md` for the WebSocket backend (the design record lives in this change's `design.md`)
- [ ] 9.2 Note the breaking change for Hermes connections in the release notes surface the release process reads (do NOT hand-edit CHANGELOG.md)
- [ ] 9.3 Sync the delta specs into `openspec/specs/` and archive the change once implementation is verified
