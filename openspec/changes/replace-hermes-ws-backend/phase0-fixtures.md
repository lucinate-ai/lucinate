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

// message.complete — carries FULL usage inline
{"type":"message.complete","session_id":"…","payload":{"text":"…","status":"complete",
  "usage":{"model":"openai/gpt-4o-mini","input":224,"output":32,"cache_read":28032,"cache_write":0,"reasoning":0,
           "prompt":28256,"completion":32,"total":28288,"calls":2,"context_used":14151,"context_max":65536,
           "context_percent":22,"compressions":0,"cost_status":"estimated","cost_usd":0.0021552}}}

// error — agent/turn failure
{"type":"error","session_id":"…","payload":{"message":"agent init failed: No LLM provider configured…"}}
```

Not observed in these turns (need targeted triggers, follow-up spike): `reasoning.delta`
(reasoning models only — gpt-4o-mini emits `reasoning.available` instead), `tool.output_risk`,
`approval.request`, `clarify.request`, `status.update`, and the exact `session.interrupt`
mid-stream aborted-event shape.

## Still open after this spike

- **Harness host-reachability topology** (socat sidecar vs `dashboard --insecure --host 0.0.0.0`):
  loopback token-mode is confirmed; whether `--insecure` keeps token-mode on a published port
  (removing the socat sidecar) is unverified.
- Abort (`session.interrupt`) mid-stream event shape.
- `approval.request` / `clarify.request` shapes (need a tool requiring approval).
