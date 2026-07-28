## 1. Messaging-session helper

- [x] 1.1 Add `backend.PickMessagingSessionKey` in `internal/backend/session_pick.go`, reading `sessions.list` structured fields (`channel`/`kind`/`origin`/`deliveryContext`) rather than the opaque key
- [x] 1.2 Qualify a row as a messaging conversation (`direct` with both a channel and a peer); pick the most-recently-updated
- [x] 1.3 Fail safe: return `""` on any error or no match so callers fall back
- [x] 1.4 Split the pure selection into `pickMessagingKeyFromRaw` for unit testing

## 2. Wire the open-agent sites

- [x] 2.1 TUI picker (`internal/tui/app.go`, `viewSelect`): run the lookup in the async create command; prefer the messaging key, else `MainKey`/`"main"`, keeping the `--session` override winning
- [x] 2.2 In-chat `/agent` switch (`internal/tui/commands.go`): prefer the messaging key, else `"main"`
- [x] 2.3 One-shot `lucinate send` (`app/send.go`): prefer the messaging key, else `defaultSessionKey`

## 3. Tests

- [x] 3.1 Table-driven unit tests for `pickMessagingKeyFromRaw` (selection, field precedence, group/cron/main exclusion, malformed input)
- [x] 3.2 Cover `PickMessagingSessionKey` error and happy paths
- [x] 3.3 TUI open-path test that resumes the messaging conversation over a newer `main`
- [x] 3.4 Run `go build`, `go vet`, `go test` — all green

## 4. Spec & docs

- [x] 4.1 Add the `sessions` and `one-shot` delta specs for the messaging-conversation default
- [x] 4.2 Update `docs/sessions.md` rationale (structured-field matching, most-recent heuristic, fail-safe fallback)
- [x] 4.3 `openspec validate default-to-messaging-session --strict` passes
