# Hermes backend — lessons and rationale

The behavioural contract for the Hermes backend lives in
[`openspec/specs/backend-hermes/spec.md`](../openspec/specs/backend-hermes/spec.md) — the
transport, capabilities, session model, chat/abort semantics, and connect/auth flow are all
captured there as requirements and scenarios. This file keeps the hard-won lessons, pitfalls,
and design rationale behind that contract: the "why it works this odd way" that the spec's
requirements don't dwell on. Every wire fact below was verified against a live
`nousresearch/hermes-agent` container, not read from upstream source.

## The transport is upstream's own client protocol

The backend speaks the `tui_gateway` JSON-RPC protocol over a WebSocket to the `hermes
dashboard` gateway (`/api/ws`, default port 9119) — the same dispatcher Hermes' desktop app
and web dashboard ride. That makes it the first-party integration surface, but *not* a
stability-guaranteed public API: upstream moves fast, and the pinned version matrix in the
`hermes-smoke` CI job is the drift alarm. Treat a matrix break like an openclaw-go protocol
bump. The supported floor is `v2026.6.5` (v0.16.0, the first release with the dashboard WS
gateway); there is **no `hermes serve` command** — an earlier draft of this design assumed
one existed.

The generic JSON-RPC client lives in `internal/backend/hermes/rpc` — deliberately small
(id-correlated `Call`, a notifications channel, `Done`/`Err` for the supervisor) and built on
`gorilla/websocket` because the openclaw-go SDK already pins it; adding a second WS library
for one job would be pure surface.

## Auth fails at the upgrade, not after it

A bad or missing gateway token is rejected with **HTTP 403 at the WebSocket upgrade** — the
socket never opens, and there is no post-open `4401`/`4403` close frame (another corrected
draft assumption). Connect maps the 403 to the canonical `api key required` error so the
existing modal flow (relabelled "gateway token") takes over. The token is
`HERMES_DASHBOARD_SESSION_TOKEN` in the server's environment and rides `?token=` on the dial;
it reuses the per-connection secret slot the old bearer key occupied.

## Two session id-spaces

`session.create` returns a short **live** handle (`session_id`) and a timestamped
**stored** id (`stored_session_id`). The live handle drives `prompt.submit` /
`session.history` / `session.usage` / `session.interrupt` and dies with the connection; the
stored id is what `session.list` reports and what `session.resume` takes after a reconnect.
Three traps discovered live:

- **Empty sessions aren't listed.** A freshly created session is usable but won't appear in
  `session.list` until it holds a message.
- **You can't delete an attached session** (`4023 cannot delete an active session`).
  `SessionDelete` closes the live handle first (`session.close`), then deletes by stored id —
  after close, the live handle no longer resolves at all.
- **`session.usage` reports zeros** even after a completed turn on `v2026.6.5`
  (`cost_status: "unknown"`). The authoritative per-turn usage arrives **inline** on
  `message.complete.payload.usage` — rich (tokens, context %, estimated cost) and free. The
  translator attaches it to the final chat event; `/stats` uses the RPC as a best-effort
  refresh only.

## Event translation quirks

Translation is an I/O-free `Translator` (translate.go), table-tested against golden fixtures
captured from the live gateway (`testdata/events/`). The quirks worth knowing:

- **Deltas are increments; the TUI wants cumulative text.** The translator keeps a
  per-session accumulator — the only state it has.
- **Aborts are not a separate event.** `session.interrupt` acks `{"status":"interrupted"}`
  and the turn ends with a `message.complete` whose `status` is `"interrupted"`; the
  translator branches on that field to emit the aborted event.
- **Tool events carry a server-supplied `tool_id`** (OpenAI-style `call_…`) on both
  `tool.start` and `tool.complete` — no client-side id pairing needed. Error state is
  `result.error` / `result.exit_code`; there is no `isError` flag on the wire, so the
  translator derives one for the TUI's tool cards.
- **`thinking.delta` is decorative** ("٩(๑❛ᴗ❛๑)۶ analyzing…"); real reasoning text arrives
  as `reasoning.available` (whole, not a delta) on non-reasoning models.
- **Interactive asks** (`approval.request`, `clarify.request`, `sudo.request`,
  `secret.request`) each carry a `request_id` and block the turn until answered via the
  paired `<type>.respond` RPC. Phase 1 auto-declines (`approval.respond {choice:"deny"}` et
  al) and splices a `System:` line into the streaming text — a chat *error* event would
  finalise the run in the TUI while the server keeps streaming, which is why the notice rides
  the assistant row instead (history rendering already strips `System:` lines).

## Integration harness

`test/integration/hermes/` boots the pinned upstream image with `hermes dashboard` bound to
loopback, and an `alpine/socat` sidecar in the same network namespace forwards the published
port into that bind. Loopback is the only shape that keeps token auth on every supported
version: `--insecure --host 0.0.0.0` worked on `v2026.6.5` but `v2026.7.7.2` refuses any
non-loopback bind without a registered auth provider — a change the CI version matrix caught
on its very first run. `--skip-build` works because the published image ships a prebuilt SPA.
Inference routes to the host via `provider: "custom"` seeded in `state/config.yaml` — the
echo leg (`setup-hermes.sh --echo`) points it at the `echomodel` stub, the default leg at
Ollama. echomodel's scripted mode answers a `[[tool:shell CMD]]` marker with an OpenAI-format
`tool_calls` response, so Hermes executes a real tool and emits real `tool.*` frames —
deterministic tool-card coverage with zero inference cost. The `hermes-smoke` CI job runs the
echo leg across the oldest-supported and current-stable image tags.

Three pins must move together when tracking a new upstream release: `HERMES_TAG` defaults in
`setup-hermes.sh` and `hermes/docker-compose.yml`, and the current-stable entry in
`.github/workflows/integration.yml`.

## Migration from the legacy backend

Connections still pointing at the old API server (`:8642` or a `/v1` path) fail `Connect`
with a targeted hint to run `hermes dashboard` and repoint the URL. The old client-side state
(`~/.lucinate/hermes/<conn-id>/` with `last_response_id` + `prompts.jsonl`) is gone — those
files existed only because the HTTP API had no session surface. There is no auto-migration:
the user must switch the server process they run, so a human step was unavoidable anyway.
