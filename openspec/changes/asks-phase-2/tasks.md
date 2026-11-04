# Tasks — asks Phase 2

## 1. Backend asks (internal/backend/hermes)

- [x] 1.1 `askPayload` gains `question`, `choices`, `command`, `cwd` (all optional); a decode failure
      on any of them leaves that field empty rather than failing the event.
- [x] 1.2 `pump` emits an agent event with `stream: "ask"` for `approval.request` and
      `clarify.request` instead of calling `declineAsk`; `sudo.request` / `secret.request` keep the
      auto-decline and its notice.
- [x] 1.3 A per-ask timeout (`askTimeout`, default 10m, settable for tests) denies when it fires; an
      answer cancels it.
- [x] 1.4 `RespondAsk` implements `approval.respond{choice}` and `clarify.respond{answer}`, and drops
      (logs) an answer that arrives after expiry.
- [x] 1.5 Tests: event emitted with question/choices; no immediate respond; `RespondAsk` reaches the
      right RPC with the right argument; timeout denies; sudo/secret unchanged.

## 2. Backend capability (internal/backend)

- [x] 2.1 `AskBackend` optional interface + `AskResponse` struct, documented next to the other
      optional capabilities.

## 3. TUI panel (internal/tui)

- [x] 3.1 `handleAgentEvent` routes `stream: "ask"` into a pending-ask field on the chat model.
- [x] 3.2 The panel renders above the composer: kind, question (wrapped), the command and cwd for an
      approval, the numbered choices for a clarify.
- [x] 3.3 Keys, only while the composer is empty: approval `y` = allow once, `a` = allow always
      (only when the payload offers it), `n`/`esc` = deny; clarify `1`–`9` = that choice, `enter` =
      submit typed text, `esc` = skip. The draft is never modified.
- [x] 3.4 Answering calls `RespondAsk`, clears the panel, and leaves a one-line record in the
      transcript (`answered: allow once` / `answered: deny` / `answered: <text>`).
- [x] 3.5 Tests: `y` → `allow-once`, `n`/`esc` → `deny`, `a` only when offered, clarify choice and
      free text, draft untouched, panel cleared.

## 5. Transport — the live run proved asks are server→client requests

The first cut assumed notifications (`approval.request` with `request_id`). A live run against
`hermes dashboard` showed the gateway never sends those: it sends JSON-RPC **requests**
(`{"id":7,"method":"approval","params":{…}}`), and only to a client that advertised
`client.capabilities {server_requests: true}`. Without the advertisement the request is withdrawn
immediately and the agent is told nobody could answer — the exact symptom seen live. Tasks 1.2 and
1.4 are rewritten by this section.

- [x] 5.1 `internal/backend/hermes/rpc/client.go`: `dispatch` must route a frame that has BOTH `id`
      and `method` to a new inbound-request channel instead of dropping it; add
      `Respond(id uint64, result any) error` and `RespondError(id uint64, code int, msg string)`,
      each writing one JSON-RPC frame. Tests: an inbound request is surfaced; a response to an
      unknown id still settles our call (no behaviour change); `Respond` writes the frame.
- [x] 5.2 Backend advertises the capability once per connection:
      `client.capabilities {"server_requests": true}` right after the handshake, recording what the
      gateway returned. Test against the fake gateway.
- [x] 5.3 The backend turns inbound `approval` and `clarify` requests into `ask` agent events
      carrying the JSON-RPC id, kind, command, description, question(s) and choices; the answer is
      the JSON-RPC response for that id (`{"choice": …}` / `{"answers": {qid: …}, "outcome":
      "submitted"}`). A choice outside `params.choices` is replaced by `deny`.
- [x] 5.4 `sudo` and `secret` inbound requests are answered `-32601` (fast-fail), never left open.
- [x] 5.5 A `request.cancel` notification for an open id closes the panel for that id.
- [x] 5.6 Re-run the live check (4.2): a `manual` approvals profile, one destructive command,
      `y` allows it, `n` blocks it. See 4.2 for the exact screen lines.

- [x] 4.1 `gofmt -l .` empty, `go vet ./...` clean, `go test -count=1 ./...` green.
- [x] 4.2 Live check against the running gateway: a profile with `approval_mode: ask` runs one
      command through lucinate — the panel appears, `y` lets it run, `n` blocks it — and a clarify
      question is answered from the panel.
      Verified 2026-11-04 10:2x on `hermes dashboard` v0.22.5, profile `poradnia-test` with
      `approvals.mode: manual` (set for the run, restored afterwards — the file is byte-identical to
      the backup). The panel rendered `Hermes asks: run touch /tmp/ask-live4.txt && rm
      /tmp/ask-live4.txt && echo RM-OK?` with `delete in root path` and the key line
      `y allow once · s allow for session · a allow always · n deny`; `y` wrote `answered: allow
      once`, the tool ran and the agent reported `RM-OK — polecenie przeszło, exit 0`; `n` on the
      next request (`echo "ENV_WT=[$ORCA_WORKTREE_ID]" …`, `shell command via -c/-lc flag`) denied it
      — the member's next tool call shows as failed. Clarify was not exercised live (no turn asked a
      question); it is covered by the fixtures and the unit tests, and this line says so rather than
      implying otherwise.
- [x] 4.3 `docs/backend_hermes.md` gains a short section: which asks are interactive, which stay
      declined, and the timeout.
