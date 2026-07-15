## Why

The Hermes backend rides Hermes' OpenAI-Responses-compatible HTTP API (`/v1/responses`), a transport that only carries chat text. Everything the TUI gates on a richer backend renders "not available on this connection": tool call cards, live usage stats, `/compact`, `/stats`, `/crons`, `!!`, real abort, and real session history. We also carry two client-side workarounds — the `prompts.jsonl` shadow log and the 3-hop `previous_response_id` walk-back — purely because the HTTP API has no session or history surface. Upstream has since shipped the interface we actually need: the `tui_gateway` JSON-RPC-over-WebSocket protocol (`hermes serve`, `/api/ws`) that Hermes' own desktop app and web dashboard already use. That is the first-party integration surface and a far better long-term bet than the Responses API.

## What Changes

- **BREAKING** — Replace the Hermes backend transport entirely: HTTP/SSE against `/v1/responses` → a JSON-RPC-over-WebSocket client speaking the `tui_gateway` protocol against `hermes serve` (`/api/ws`). No dual-transport fallback. There are no real users of the Hermes integration today, so a clean break is acceptable.
- **BREAKING** — Existing Hermes connections that point at the legacy API server (`:8642/v1`) stop connecting. They keep their entry but `Connect` fails with a targeted migration error telling the user to run `hermes serve` and repoint the URL (default `http://localhost:9119`) with the gateway token. The user must switch their server process (`gateway run` → `serve`), so no silent auto-migration is possible.
- Unlock feature parity with OpenClaw where Hermes has the primitive: tool call cards, `/stats` + live header usage, `/compact`, real server-side abort, real session list / history / delete, and `!!` remote exec.
- Delete the client-side state directory `~/.lucinate/hermes/<conn-id>/` (`last_response_id`, `prompts.jsonl`), the history walk-back, the `/v1/responses` SSE path, and Hermes' use of `internal/backend/httpcommon`.
- Add a new WebSocket JSON-RPC client (`internal/backend/hermes/rpc`) and a pure event-translation layer, plus real reconnect/supervision (the current `Supervise` is a no-op stub).
- Add durable verification: a live Hermes container in Docker wired into the CI/CD suite, mirroring the OpenClaw integration harness — a zero-cost echo-model leg for CI, a scripted tool-call leg for deterministic tool-event coverage, and a version-matrixed smoke job as a protocol-drift alarm.
- Deliver in phases: Phase 0 spike (harness topology + golden JSON fixtures for the read-from-source RPC/event shapes), Phase 1 core swap, Phase 2 interactive (`!!`, approvals, `/model`), Phase 3 remote/gated auth. This proposal scopes Phases 0–1; Phases 2–3 are follow-on.

## Capabilities

### New Capabilities
<!-- None. Verification (the Docker/CI harness) is implementation detail captured in design.md and tasks.md, not a user-facing behaviour contract. -->

### Modified Capabilities
- `backend-hermes`: the whole behaviour contract is rewritten — transport (JSON-RPC/WS not HTTP/SSE), capability surface (usage/compact/tool-cards/abort/history now supported), removal of the local state files and history walk-back, real session list/history/delete, connect/handshake and gateway-token auth, and reconnect/supervision. Scoped to Phase 0–1 behaviour; interactive (`!!`/approvals) and remote-auth requirements are noted as out of scope for now.
- `connections`: Hermes connection facts change — the URL shape (`:8642/v1` → `:9119`), the auth mode (bearer `API_SERVER_KEY` → gateway session token), the claim that Hermes shares `httpcommon` HTTP plumbing with OpenAI (no longer true), and the Hermes capability/sub-interface set reported to the picker.

## Impact

- **Code:** `internal/backend/hermes` rewritten (`backend.go`, `translate.go`, `usage.go`, new `rpc/` package); `prompts.go` deleted. `internal/backend/hermes` stops importing `internal/backend/httpcommon`. `app/factory.go` `secretAwareHermesBackend` shim reused (secret slot repurposed as the gateway token). Connections form for Hermes drops the `/v1` hint and the DefaultModel field.
- **Dependencies:** new WebSocket library `nhooyr.io/websocket` (a.k.a. `github.com/coder/websocket`) — context-native, no CGo. The openclaw-go SDK transport is not reusable (different protocol) but its supervision pattern is the model.
- **State / migration:** `~/.lucinate/hermes/<conn-id>/` removed. Stored per-connection secret is reused as the gateway token; users paste the new token via the existing auth modal on the first `4401` close.
- **Verification / CI:** `test/integration/hermes/` retooled (`setup-hermes.sh`, `make test-integration-hermes*`); `test/integration/echomodel` grows a scripted tool-call mode; new `hermes-smoke` job in `.github/workflows/integration.yml`.
- **Docs:** `docs/backend_hermes.md` rewritten. The earlier standalone design draft has been folded into this change's `design.md` (see its Reference appendix).
- **Supported version floor:** Hermes `v2026.6.5` (v0.16.0 — first release with `hermes serve` + hardened WS auth); harness pinned to current stable.
