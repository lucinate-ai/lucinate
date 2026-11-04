# Tasks — asks Phase 2

## 1. Backend asks (internal/backend/hermes)

- [x] 1.1 `askPayload` gains `question`, `choices`, `command`, `cwd` (all optional); a decode failure
      on any of them leaves that field empty rather than failing the event.
- [x] 1.2 `pump` stops auto-declining `approval`/`clarify` and emits an ask instead; `sudo` /
      `secret` keep the auto-decline and its notice. *(The notification path this task was written
      against does not exist on the gateway — see group 5, which is what actually shipped.)*
- [x] 1.3 A per-ask timeout (`askTimeout`, default 10m, settable for tests) denies when it fires; an
      answer cancels it.
- [x] 1.4 The answer goes back to the gateway and an answer arriving after expiry is dropped and
      logged. *(Delivered as a JSON-RPC response, not as `approval.respond` — see 5.3.)*
- [x] 1.5 Tests: event emitted with question/choices; no immediate answer; the answer reaches the
      gateway with the right argument; timeout denies; sudo/secret unchanged.

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
- [x] 3.4 Answering sends the answer, clears the panel, and leaves a one-line record in the
      transcript (`answered: allow once` / `answered: deny` / `answered: <text>`).
- [x] 3.5 Tests: `y` → `allow-once`, `n`/`esc` → `deny`, `a` only when offered, clarify choice and
      free text, draft untouched, panel cleared.

## 4. Verification

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
      — the member's next tool call shows as failed. Clarify was verified live the same day on an
      isolated dashboard (:9120, `--isolated`, token `lucinate-live`, connection URL restored
      afterwards): the agent called `clarify` with `Którą opcję wybierasz?` and choices
      `1) A (Recommended)  2) B`, pressing `2` answered it and the agent replied `Wybrałeś B.`
      (`✓ called clarify`). `s` (allow for session) and `request.cancel` remain covered by fixtures
      and unit tests only — not exercised live; this line says so rather than implying otherwise.
- [x] 4.3 `docs/backend_hermes.md` gains a short section: which asks are interactive, which stay
      declined, and the timeout.

## 5. Transport — the live run proved asks are server→client requests

The first cut assumed notifications (`approval.request` with `request_id`). A live run against
`hermes dashboard` showed the gateway never sends those: it sends JSON-RPC **requests**
(`{"id":"srq-…","method":"approval","params":{…}}`), and only to a client that advertised
`client.capabilities {server_requests: true}`. Without the advertisement the request is withdrawn
immediately and the agent is told nobody could answer — the exact symptom seen live. Group 1 is
rewritten by this section.

- [x] 5.1 `internal/backend/hermes/rpc/client.go`: `dispatch` routes a frame that has BOTH `id` and
      `method` to an inbound-request channel instead of dropping it; `Respond` and `RespondError`
      echo the id verbatim (the gateway's ids are strings like `srq-1a2b3c4d5e6f`, so the frame id
      is `json.RawMessage`, not `uint64`). Tests: an inbound string-id request is surfaced and
      answered with the same id; a numeric response still settles our own call; an unknown id shape
      is ignored without a panic.
- [x] 5.2 Backend advertises the capability once per connection:
      `client.capabilities {"server_requests": true}` right after the handshake, recording what the
      gateway returned (`hermes server_requests enabled` in the debug log is the evidence).
- [x] 5.3 The backend turns inbound `approval` and `clarify` requests into `ask` agent events
      carrying the JSON-RPC id, kind, command, description, question(s) and choices; the answer is
      the JSON-RPC response for that id (`{"choice": …}` / `{"answers": {qid: …}, "outcome":
      "submitted"}`). A choice outside `params.choices` is replaced by `deny`.
- [x] 5.4 `sudo` and `secret` inbound requests are answered `-32601` (fast-fail), never left open.
- [x] 5.5 A `request.cancel` notification for an open id closes the panel for that id.
- [x] 5.6 Re-run the live check (4.2): a `manual` approvals profile, one destructive command,
      `y` allows it, `n` blocks it. See 4.2 for the exact screen lines.
