# Phase 0 spike — verified against live `nousresearch/hermes-agent:v2026.6.5`

Captured 2026-07-19 by driving a WebSocket JSON-RPC client against a real
`hermes dashboard` container (loopback bind, token auth), inference via
OpenRouter (`openai/gpt-4o-mini`) through the first-class `openrouter` provider.
These are the golden fixtures the `rpc` structs and `translate.go` tests are built
against. Payloads are lightly trimmed; ids/timestamps are real samples.

## Boot / transport

- **Command:** `hermes dashboard --host 127.0.0.1 --port 9119 --no-open --skip-build`
  (there is **no** `hermes serve`). Default port **9119**, endpoint **`/api/ws`**.
- `--skip-build` serves the prebuilt SPA at `/opt/hermes/hermes_cli/web_dist` — no npm build in CI.
- **Auth:** loopback `?token=<HERMES_DASHBOARD_SESSION_TOKEN>`. Bad/missing token →
  **HTTP 403 at the WS upgrade** (the socket never opens). There is **no** `4401`/`4403`
  WS close frame — the design's close-code mapping is wrong.
- **Event envelope key is `session_id`** (not `sid`): `{"method":"event","params":{"type":…,"session_id":…,"payload":{…}}}`.
- **App-level JSON-RPC error codes:** `-32601` unknown method, `4001` session not found,
  `4004` empty command, `4006` session_id required, `4018` invalid command.

## `event: gateway.ready` (pushed on connect)

```json
{"jsonrpc":"2.0","method":"event","params":{"type":"gateway.ready","payload":{"skin":{"name":"default","colors":{…},"branding":{"agent_name":"Hermes Agent",…}}}}}
```

## RPC registry (verified)

| Method | Params | Result / error | Notes |
|---|---|---|---|
| `agents.list` | — | `{"processes": []}` | key is `processes`; empty → synthetic-agent fallback |
| `session.create` | — | `{session_id, stored_session_id, message_count, messages, info}` | two id-spaces (below) |
| `session.list` | — | `{sessions:[{id,title,preview,started_at,message_count,source}]}` | `id` = **stored_session_id**; only sessions **with messages** appear |
| `session.active_list` | — | `{"sessions": []}` | currently-attached sessions |
| `session.resume` | `{session_id}` | (needs id; `4006` without) | |
| `session.delete` | `{session_id}` | (needs id) | |
| `session.history` | `{session_id}` | `{count, messages}` | |
| `session.interrupt` | `{session_id}` | exists (`4001` without a live session) | abort |
| `session.usage` | `{session_id}` | `{calls, input, output, total}` | on-demand; richer usage is inline in `message.complete` |
| `session.compress` | `{session_id}` | exists | `/compact` |
| `session.status` | `{session_id}` | `{"output": "<pre-rendered text blob>"}` | **not structured** |
| `prompt.submit` | `{session_id, text}` | `{"status": "streaming"}` | events stream separately |
| `model.options` | — | `{providers:[…], model, provider}` | **provider-centric**, not a flat model list |
| `cron.manage` | — | `{success, count, jobs}` | **works out of the box** |
| `command.dispatch` | `{command}` | exists (`4018` for invalid) | Phase 2 `/model` |
| `shell.exec` | `{command}` | exists (`4004` empty) | Phase 2 `!!` |
| `session.context_breakdown` | — | **`-32601` does not exist** | drop from UsageBackend |
| `verification.status` | — | **`-32601` does not exist** | drop from StatusBackend |

### Two session id-spaces (important)

`session.create` returns **both**:
- `session_id` — short live handle (e.g. `82a46f28`), used by `prompt.submit`/`history`/`usage`/`interrupt` during the connection.
- `stored_session_id` — timestamped persisted id (e.g. `20260719_015025_36f295`), the `id` that appears in `session.list`.

```json
{"session_id":"5780a6a0","stored_session_id":"20260719_013653_ec4b02","message_count":0,"messages":[],
 "info":{"model":"anthropic/claude-opus-4.6","tools":{},"skills":{},"cwd":"/opt/hermes","branch":"","lazy":true,"desktop_contract":1,"profile_name":"default"}}
```

`session.list` after two turns:

```json
{"sessions":[
 {"id":"20260719_015025_36f295","title":"Terminal Command Output Verification","preview":"Use your terminal tool…","started_at":1784425825.32,"message_count":4,"source":"tui"},
 {"id":"20260719_014855_e35a87","title":"Simple Greetings…","preview":"Reply with exactly: hello world","started_at":1784425735.91,"message_count":2,"source":"tui"}]}
```

## Event stream (verified)

Plain turn: `session.info → message.start → thinking.delta* → message.delta* → reasoning.available → message.complete`
Tool turn: `session.info → message.start → thinking.delta* → tool.generating → tool.start → tool.complete → message.delta* → reasoning.available → message.complete`

```json
// message.start — no payload
{"type":"message.start","session_id":"…"}

// message.delta — incremental assistant text
{"type":"message.delta","session_id":"…","payload":{"text":"hello"}}

// thinking.delta — Hermes' decorative animation ("…analyzing…")
{"type":"thinking.delta","session_id":"…","payload":{"text":"٩(๑❛ᴗ❛๑)۶ analyzing..."}}

// reasoning.available — model reasoning / final answer text (whole, not delta)
{"type":"reasoning.available","session_id":"…","payload":{"text":"hello world"}}

// tool.generating — model is drafting the call; name only, no id yet
{"type":"tool.generating","session_id":"…","payload":{"name":"terminal"}}

// tool.start — HAS a tool_id (OpenAI-style). context = human summary of args
{"type":"tool.start","session_id":"…","payload":{"tool_id":"call_QyqcRwF2…","name":"terminal","context":"echo lucinate-tool-ok"}}

// tool.complete — pairs by tool_id; structured args + result; error via result.error/exit_code
{"type":"tool.complete","session_id":"…","payload":{"tool_id":"call_QyqcRwF2…","name":"terminal","args":{"command":"echo lucinate-tool-ok"},"duration_s":1.99,"result":{"output":"lucinate-tool-ok","exit_code":0,"error":null}}}

// message.complete — carries FULL usage inline; status "complete" | "interrupted"
{"type":"message.complete","session_id":"…","payload":{"text":"…","status":"complete",
  "usage":{"model":"openai/gpt-4o-mini","input":224,"output":32,"cache_read":28032,"cache_write":0,"reasoning":0,
           "prompt":28256,"completion":32,"total":28288,"calls":2,"context_used":14151,"context_max":65536,
           "context_percent":22,"compressions":0,"cost_status":"estimated","cost_usd":0.0021552}}}

// error — agent/turn failure
{"type":"error","session_id":"…","payload":{"message":"agent init failed: No LLM provider configured…"}}
```

## Abort (verified)

`session.interrupt {session_id}` returns `{"status":"interrupted"}`. The interrupted turn does **not** emit a distinct `aborted`/`interrupted` event — it ends with a **`message.complete` whose `payload.status` is `"interrupted"`**:

```json
// session.interrupt result
{"jsonrpc":"2.0","id":3,"result":{"status":"interrupted"}}
// terminal event for the aborted turn
{"type":"message.complete","session_id":"…","payload":{"text":"Operation interrupted: waiting for model response (9.6s elapsed).","status":"interrupted","usage":{…zeroed…}}}
```

So `translate.go` branches on `message.complete.payload.status`: `"complete"` → final, `"interrupted"` → aborted.

## Interactive asks (clarify verified live; approval from source)

Each blocking ask carries a `request_id` and is answered by a paired `*.respond` RPC.

```json
// clarify.request (LIVE — triggered via the clarify tool)
{"type":"clarify.request","session_id":"…","payload":{"question":"Which file should I edit?","choices":null,"request_id":"d7987369"}}
```

Response methods (from `@method` handlers in `tui_gateway/server.py`):

| Ask event | Response RPC | Params |
|---|---|---|
| `clarify.request` | `clarify.respond` | `{session_id, answer, request_id}` |
| `sudo.request` | `sudo.respond` | `{session_id, password, request_id}` |
| `secret.request` | `secret.respond` | `{session_id, value, request_id}` |
| `approval.request` | `approval.respond` | `{session_id, choice, all}` — **decline = `choice:"deny"`** |

Phase 1's auto-decline policy: on any of these, call the matching `*.respond` to cancel/deny
(`approval.respond {session_id, choice:"deny"}`) and render a system message. The live
`approval.request` payload wasn't captured (the `terminal` tool auto-runs; approval needs a
shell-hook or a risky tool to trigger) — capture it before Phase 2.

## Harness host-reachability — version-dependent; loopback + socat is the settled shape

On **`v2026.6.5`**, `dashboard --insecure --host 0.0.0.0` was tested from a genuine non-loopback
peer: good `?token=` connected (`gateway.ready`), bad/missing token → HTTP 403, non-loopback
`Host` accepted. Token-mode survived a non-loopback bind on that version.

On **`v2026.7.7.2`**, the CI matrix caught the completed hardening: the dashboard **refuses any
non-loopback bind without a registered auth provider** and exits at boot — "There is no
unauthenticated public-bind option — to keep it local, bind 127.0.0.1 and tunnel in." `--insecure`
no longer opts out.

The harness therefore binds loopback inside the container and forwards the published port with an
`alpine/socat` sidecar in the same network namespace — token-mode active on every supported
version. Verified live on both tags via the CI matrix (and locally through socat on v2026.6.5).

Not observed (need targeted triggers): `reasoning.delta` (reasoning models only — gpt-4o-mini
emits `reasoning.available` instead), `tool.output_risk`, `status.update`, and the live
`approval.request` payload (the `terminal` tool auto-runs; approval needs a shell-hook / risky tool).

## Harness-run discoveries (echo leg, v2026.6.5)

Found while bringing the retooled harness up against the live gateway:

- **`session.delete` refuses an attached session** — code `4023 cannot delete an
  active session`. The live handle must be detached first via
  **`session.close {session_id: <live>}` → `{"closed": true}`**; after close the
  live handle no longer resolves (`4007 session not found`), so the delete must
  address the **stored** id. The backend's `SessionDelete` does close-then-delete.
- **`session.usage` returns zeroed totals even after a completed turn** on this
  version (`cost_status: "unknown"`). The authoritative per-turn usage is the
  inline `message.complete.payload.usage`; `/stats` may render zeros on older
  gateways.
- The inline `custom` provider config (base_url + api_key seeded in
  `config.yaml`) **works** against the gateway — the earlier OpenRouter-inline
  failure was endpoint-specific, not a general custom-provider break. The echo
  and Ollama legs both ride `provider: "custom"`.

## Still open after this spike

- Live `approval.request` payload (response method `approval.respond {choice:"deny"}` is confirmed; only the request payload is uncaptured) — capture before Phase 2.
- `reasoning.delta`, `tool.output_risk`, `status.update` payloads — capture opportunistically during Phase 1.
