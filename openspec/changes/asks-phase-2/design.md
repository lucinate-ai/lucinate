# Design — asks Phase 2

## What the gateway sends (corrected by a live run)

The first draft of this change assumed the ask payload carried `request_id` — the clarify fixture in
this repo does (`internal/backend/hermes/testdata/events/clarify_request.json`). A live run proved
that wrong for approvals, and the failure is instructive: the ask arrived, the client looked for
`request_id`, found nothing, and dropped it, while the gateway's own `_on_approval` answered `deny`
for a client callback that did not exist. The user saw a refused command and no question.

The gateway's real shapes (`hermes_cli/gateway_agent.py`, `_on_approval` / `_on_clarify`):

```
approval.request  {prompt_id, execution_generation, choices:["deny","once","session","always"],
                   command, description}
approval.respond  {session_id, execution_generation, prompt_id, choice}   // choice ∈ choices
clarify.request   {prompt_id, question, choices, multi_select, execution_generation}
clarify.respond   {session_id, execution_generation, prompt_id, answer}
```

Consequences the implementation must honour:

- the id is `prompt_id`, `request_id` is only a historical alias — accept both, prefer `prompt_id`;
- `execution_generation` travels back on the respond RPC; a respond without it addresses nothing;
- `choice` MUST be one of the payload's `choices`. Sending `allow-once` to a gateway that offered
  `once` is a denial by another name (the gateway forces `deny` for an unknown choice). The panel
  therefore maps its keys onto the offered vocabulary: `y` → `once`, else `allow-once`, else
  `approve`; `a` → `always`, else `allow-always`; `n`/`esc` → `deny`. When none of the mappings is
  offered, the key is not shown.
- approvals carry `description` as well as `command`: show both.

## Decision 6 — the real transport (found by the live run, supersedes the notification path)

The gateway does **not** send approvals as notifications. It sends them as **server→client JSON-RPC
requests** (`tui_gateway/server_requests.py`, `send_async("approval", sid, payload, on_result)`), and
only to a client that advertised the capability:

```
→ client.capabilities {"server_requests": true}
← {"server_requests": ["approval","clarify","sudo","secret", …]}

← {"jsonrpc":"2.0","id":7,"method":"approval","params":{
     "request_id":"r1","command":"…","description":"…",
     "choices":["once","session","always","deny"],"pattern_keys":[…],
     "allow_permanent":true,"allow_session":true,"smart_denied":false,"tool_name":"terminal"}}
→ {"jsonrpc":"2.0","id":7,"result":{"choice":"once"}}          // choice ∈ choices

← {"jsonrpc":"2.0","id":8,"method":"clarify","params":{
     "questions":[{"qid":"q0","question":"…","choices":[…]|null,"multi_select":false}]}}
→ {"jsonrpc":"2.0","id":8,"result":{"answers":{"q0":"…"},"outcome":"submitted"}}
```

A client that never advertises is treated as "predates server→client requests": the gateway
withdraws the request immediately and the agent is told nobody could answer
(`server_requests._unanswerable`, `_emit_approval_request`). That is exactly the live failure:
the command was refused, no question ever reached the screen.

Consequences (these replace the notification-based design above):

1. `internal/backend/hermes/rpc.Client` must read **inbound requests** (a frame with both `id` and
   `method`) — today `dispatch` (`rpc/client.go:268`) treats any frame with an id as a response to
   one of our calls and drops the unknown ones — and must be able to answer one:
   `Respond(id uint64, result any) error` and `RespondError(id uint64, code int, msg string)`.
2. The backend advertises `client.capabilities {server_requests: true}` right after the handshake
   and forwards `approval`/`clarify` requests to the client as `ask` events carrying the JSON-RPC
   id. The answer is the JSON-RPC **response** for that id — not a `*.respond` RPC call.
3. `sudo` and `secret` are answered with `-32601` ("method not found") so the gateway settles them
   at once instead of waiting; that is the documented fast-fail path for a client without that
   handler, and it keeps the current "this client does not own a password surface" behaviour.
4. The choice vocabulary is whatever `params.choices` offers (`once`, `session`, `always`, `deny`);
   the panel maps `y` → `once` (fallback `approve`), `a` → `always`, `n`/`esc` → `deny`, and shows
   `s` → `session` when offered. A choice outside the list is refused by the server contract.
5. `request.cancel` (notification) withdraws an open request; the panel must close for that id.
6. Clarify carries `questions[]` (1–5), each with `qid`, `question`, `choices`, `multi_select`; the
   answer is `{answers: {qid: text|null}, outcome: "submitted"|"cancelled"}` and the client only
   owns a single-question panel in this slice (the first question; the rest answered `null`), which
   the delta spec states plainly rather than pretending to support a multi-question form.



## Decision 1 — the ask travels as an agent event, not a new protocol type

`protocol` comes from the `openclaw-go` SDK (`local replace ../openclaw-go`), so adding an event
constant means editing a second repository. `protocol.EventAgent` already carries
`{stream, data}` and the TUI already routes it (`internal/tui/events.go:216`,
`handleAgentEvent` at `:455`), dropping every stream it does not know. A new `stream: "ask"` fits
without touching the SDK, and no other backend is affected.

Payload:

```json
{"sessionKey":"<sid>","requestId":"d7987369","kind":"approval|clarify",
 "question":"…","choices":["once","always","deny"],"command":"…","cwd":"…"}
```

## Decision 2 — an optional capability, not a wider Backend interface

`internal/backend.Backend` is shared by three backends; only Hermes has asks. Add:

```go
type AskBackend interface {
	RespondAsk(ctx context.Context, resp AskResponse) error
}
type AskResponse struct {
	SessionKey, RequestID, Kind string // kind: approval | clarify
	Choice                      string // approval: allow-once | allow-always | deny
	Answer                      string // clarify: the user's text ("" = skip)
}
```

The TUI type-asserts it (the same pattern as `ExecBackend`, `CompactBackend`). A backend without it
renders the ask as a plain line and keeps today's behaviour.

## Decision 3 — the panel is modal only for its own keys, and only on an empty composer

The composer's draft is the user's work; an ask must not eat it. The panel therefore:

- renders above the composer,
- owns only `y`/`a`/`n`/`esc` (approval) or `1`–`9`/`enter`/`esc` and text input (clarify) **while
  the composer is empty**,
- keeps its own answer buffer for free text, so nothing typed into the panel lands in the draft.

This mirrors the rule the rooms surface already uses for `m`/`x`/`p` (`internal/tui/rooms.go:1461`):
a letter is a letter first, a shortcut second.

## Decision 4 — timeout, because a declined ask used to be instant

Today's decline is immediate, so a turn never hangs. With a panel, a user might walk away. Keep the
promise: the backend starts a timer per ask (`askTimeout`, 10 minutes, a field so tests can shrink
it) and denies when it fires. Answering cancels the timer. An answer that arrives after expiry is
logged and dropped rather than answered twice — the gateway would reject it anyway, but the log
must say why.

## Decision 5 — sudo and secret keep the old path

`sudo.request` and `secret.request` need masked input and a password flow this client should not
own; they keep `declineAsk` and the existing notice, which now says "not supported on this
connection" only for those two kinds. This is a deliberate scope line, recorded here so the next
pass does not mistake it for an oversight.

## Tests that must exist afterwards

- Backend: a `clarify.request` notification produces an `ask` agent event carrying question and
  choices (extend the existing fixture-driven test) and does **not** immediately answer the RPC;
  `RespondAsk` sends `clarify.respond` with the answer and `approval.respond` with the choice.
- Backend: an unanswered ask denies itself when the timeout fires (shrink it in the test).
- Backend: `sudo.request`/`secret.request` still auto-decline.
- TUI: with a pending approval, `y` calls `RespondAsk` with `allow-once` and clears the panel; `n`
  sends `deny`; `esc` sends `deny`; a non-empty composer draft is untouched by all three.
- TUI: with a pending clarify offering two choices, `2` answers with the second choice's text; with
  no choices, typing and `enter` answers with what was typed; `esc` answers with the empty string.
