# Honest stream failures — a 200 is not a reply, and silence is not patience

## Why

Three ways a run could look finished while it had actually failed, found by the
2026-11-04 audit (`audyty/lucinate-2026-11-04.md`, findings AUD-002/003/004):

1. **`200` with no event stream was reported as a successful empty answer.** A
   proxy, a captive portal, or a base URL pointing at a web page all reply
   `200` with HTML. The parser only understands `data:` lines, so the run ended
   on a clean EOF, emitted `final` with empty text, persisted no assistant turn,
   and logged nothing. The user saw a blank success.
2. **A silent endpoint could hold a caller forever.** The transport bounded dial
   and TLS but not the wait for response headers, and the stream had no idle
   bound; `lucinate send` called a backend that accepted TCP and said nothing
   never returned. `SIGINT` killed the process rather than cancelling the run.
3. **A cancelled turn never settled.** `turn.cancelled` was not in the terminal
   set, so after `/stop` the member's row kept claiming it was still answering
   (`ActiveStreams` never dropped it) — the transcript contradicted the gateway.

The common thread is the product's promise: a row states what really happened.
A blank success, a hang, and a spinner that never stops all break it.

## What changes

- **Non-SSE `200` fails with evidence.** A non-empty `Content-Type` that is not
  `text/event-stream` fails the run with the type and the first bytes; a stream
  with no parseable frame fails with "no data frames". `/compact`'s
  summarisation call gets the same guard.
- **Silence is bounded.** `ResponseHeaderTimeout` (60 s) bounds the wait for
  headers; `streamIdleTimeout` (2 min, reset per frame) cancels a run that goes
  quiet and says so. Both apply to every OpenAI-compatible connection.
- **The CLI has an outer bound and a real cancel.** `send`/`ask` take
  `--timeout` (default 10m, `0` = forever) and `cli.Run` installs
  `signal.NotifyContext`, so Ctrl+C cancels the run instead of killing it.
- **`turn.cancelled` is terminal.** It settles the stream and decrements the
  discussion budget, like `turn.failed`/`turn.deferred`.

## Out of scope

- Retrying a non-SSE response or an idle stream (the user fixes the URL or the
  server; a retry would hide the misconfiguration).
- Applying the CLI `--timeout` to the TUI (the TUI has its own abort path).
- A total-duration cap on server-side runs (`--detach` semantics unchanged).
