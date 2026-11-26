# Tasks — honest-stream-failures

Implemented 2026-11-04. Archive is pending: the `openspec` CLI is not installed in
this environment, so `openspec archive honest-stream-failures` must be run where it is.

## 1. A 200 without an event stream is an error

- [x] `internal/backend/openai/backend.go` — reject a non-empty `Content-Type` that
      is not `text/event-stream`, naming the type and the first 512 bytes.
- [x] Count parseable frames; `frames == 0` after the scan fails with "no data
      frames" instead of emitting an empty `final`.
- [x] Same guard on `summarise` (the `/compact` path).
- [x] `TestBackend_ChatSendRejectsANonEventStream` — error event, no assistant
      history entry.
- [x] `TestBackend_ChatSendEmptyEventStreamIsAnError`.
- [x] `TestBackend_ChatSendFinishOnlyChunkIsASuccess` — an empty answer that
      speaks the protocol is still a success.

## 2. Silence is bounded

- [x] `internal/backend/httpcommon/client.go` — `ResponseHeaderTimeout` (60 s) on
      `NewDefaultTransport`, `TestNewDefaultTransport_BoundsResponseHeaders`.
- [x] `internal/backend/openai/backend.go` — `streamIdleTimeout` watchdog, reset
      per frame, idle cancellation wins over the read error.
- [x] `TestBackend_ChatSendIdleStreamEmitsError`.

## 3. The CLI has an outer bound and a real cancel

- [x] `app.SendOptions.Timeout`; `app.Send` waits on a derived context and reports
      `send: no reply within <duration>`.
- [x] `--timeout` on `send` and `ask` (default 10m, `0` = forever);
      `TestNewAskFlagSet_TimeoutDefaultsAndOverrides`.
- [x] `cli.Run` — `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)`.
- [x] `TestSend_TimeoutReturnsErrorWithoutAReply`.

## 4. A cancelled turn is terminal

- [x] `internal/rooms/stream.go` — `turn.cancelled` joins the terminal set.
- [x] `internal/rooms/discussion.go` — `turn.cancelled` decrements `openTurns`.
- [x] `TestStreamsFor_CancelledTurnSettlesTheStream`,
      `TestStreamsFor_CancelledBeforeTextIsEmptyAndNotActive`.

## 5. Verification

- [x] `go build ./...`, `go vet ./...`, `gofmt -l .`
- [x] `go test -count=1 ./...`
- [x] `go test -race -count=1 ./internal/rooms/ ./internal/backend/... ./internal/tui/`
- [x] CI: `go test -race -count=1 -timeout 15m ./...`; fixed the race in
      `TestAppModel_RoutesShowConnectionsMsg`.
