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
- **Asks are not events.** `approval`/`clarify`/`sudo`/`secret` arrive as server→client
  JSON-RPC requests, not notifications, and only after the client advertises
  `server_requests` — see the next section. An ask *timeout* notice still rides the
  streaming text as a `System:` line, because a chat *error* event would finalise the run
  in the TUI while the server keeps streaming (history rendering strips `System:` lines).

## Interactive asks are server→client JSON-RPC requests

A live run corrected the first cut of this section twice over. The gateway does **not**
send `approval.request`/`clarify.request` notifications, and there is no
`approval.respond` RPC. It sends JSON-RPC **requests** to the client —
`{"jsonrpc":"2.0","id":7,"method":"approval","params":{…}}` — and only to a client that
first advertised the capability:

```
→ client.capabilities {"server_requests": true}
← {"server_requests": ["approval","clarify","sudo","secret", …]}
```

Without the advertisement the gateway withdraws the request immediately and tells the
agent nobody could answer — the live symptom was a command refused with no question ever
reaching the screen. The backend therefore calls `client.capabilities` once per
connection, right after the handshake.

The RPC layer (`internal/backend/hermes/rpc`) routes frames by shape: a frame with **both**
an id and a method is an inbound request on `Requests()`; an id without a method is a
response to one of our calls. The two id-spaces are independent, so the old code that
treated every id as a response silently dropped every ask. Answers go back as the JSON-RPC
**response frame** for that id (`Respond`/`RespondError`), not as a separate RPC call.

Ids are kept as **raw JSON**, not `uint64`: the gateway ids its server→client requests with
strings (`"srq-1a2b3c4d5e6f"`, `tui_gateway/server_requests.py`) while our own call ids are
numbers. A numeric-only decoder makes `json.Unmarshal` fail on the whole frame, and the
read loop used to skip it silently — which is exactly why the panel never appeared live.
The raw id travels unchanged into the ask event (`rpcId`) and back out in the response; a
non-numeric, non-string id on a response is logged and ignored.

`approval` params carry `prompt_id` (older casts used `request_id`; both accepted,
`prompt_id` wins), `command`, `description`, `choices` and sometimes `cwd`. The backend
turns them into an `ask` agent event carrying `rpcId`, `requestId`, `kind`, `command`,
`description`, `choices`; the TUI answers with the choice the user picked and the backend
writes `{"choice": …}` for that rpc id. The gateway **forces a deny when the choice is not
one of the offered `choices`**, so the panel maps its keys onto the payload's vocabulary —
`y` → `once`/`allow-once`/`approve`, `s` → `session`, `a` → `always`, `n`/`esc` → `deny` —
and the backend replaces anything outside the list with `deny`.

`clarify` params carry `questions[]` (each `qid`, `question`, `choices`, `multi_select`).
This client renders a single-question panel (the first question) and answers the rest
`null`: the result is `{"answers": {qid: text|null}, "outcome": "submitted"|"cancelled"}`.

`sudo` and `secret` are answered **`-32601` at once** (the documented fast-fail for a
client without that handler), so the gateway settles them instead of waiting. They need
masked input this client does not own.

A `request.cancel` notification withdraws an open ask: the backend drops its pending entry
(no late answer) and emits an `ask` event of kind `cancel`, which closes the panel.

Every pending ask has an **`askTimeout`** (default 10 minutes, `Options.AskTimeout`). If
nobody answers, the backend responds deny (approval) or cancelled (clarify) for the same
rpc id and shows a timeout line in the transcript, so a turn can never hang unnoticed.
Answering stops the timer; an answer that arrives after expiry is logged and dropped.

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

## One gateway, many profiles

Since the multiplexing gateway (2026-09, first shipped stable `v0.22.5` / `v2026.9.24`), one
machine-level `hermes dashboard` serves **every** profile: `profiles.list` reports the roster
over the same WebSocket, and `session.create`/`session.list`/`session.resume` take a
`profile`. The backend therefore reports the gateway's profiles as agents and sends the
selected one on every session call.

Two consequences worth remembering:

- A session's identity is `profile` + stored id. The TUI key is profile-qualified
  (`<profile>::<id>`) because a stored id is only unique inside one profile's `state.db`;
  resuming with the wrong profile reports "session not found".
- Empty sessions are still not listed until they hold a message, and `session.usage` still
  reports zeros — `message.complete.payload.usage` remains the authoritative per-turn usage.

Model switching has no dedicated RPC. The dashboard's chat uses `/model <ref>`, so the backend
dispatches the same thing through `slash.exec` and returns the gateway's own answer: a
refusal is an error, never a reported switch. The switch is session-scoped; the profile's
config is untouched.

## Shapes the wire actually carries

`model.options` is provider-centric and **not** stable in the way a struct tag assumes: on
`v0.22.5` a provider's `models` is a list of plain `"provider/model"` strings, while older
payloads carried `{"id","name"}` objects. Decoding only the object shape made `/model` fail with
`cannot unmarshal string into Go struct` — the picker was dead on the live gateway while the
unit test (whose fake sent objects) stayed green. Decode each entry through `json.RawMessage` and
accept both; the active model also comes from the top-level `"model"`.

`session.list` rows are the TUI's contract, not ours: the browser reads `key`, `derivedTitle`,
`lastMessagePreview`, `updatedAt`. Emitting `title`/`lastMessage` instead renders every row as a
bare session key, twice (title fallback plus the description), which is what shipped until a live
pass caught it. The field names live in `internal/tui/sessions.go`; treat them as the wire shape
for this backend.

The `hermes-smoke` CI matrix pins `v2026.6.5` (oldest supported, pre-multiplexing) and the
current stable. Both legs matter: the profile roster must degrade to the synthetic agent on a
gateway that answers `-32601` to `profiles.list`.
