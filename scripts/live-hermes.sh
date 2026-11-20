#!/usr/bin/env bash
#
# Live harness for the Hermes backend and rooms: boots one machine-level
# `hermes dashboard` on a spare port with a deterministic token, points a
# throwaway lucinate data dir at it, and runs a smoke pass over the surfaces a
# hermetic test cannot prove — the gateway's own protocol answers.
#
# Usage:
#   ./scripts/live-hermes.sh            # caps + profiles + a room lifecycle
#   KEEP=1 ./scripts/live-hermes.sh     # leave the dashboard running for the TUI
#
# The room pass costs real turns only if PROFILES is set to profiles whose
# models answer; by default it creates a two-profile room, does not send, and
# disbands it. Set SEND=1 to post one message addressed to the first profile.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PORT="${PORT:-9121}"
TOKEN="${TOKEN:-lucinate-live}"
PROFILES="${PROFILES:-default,librarian}"
ROOM_ID="${ROOM_ID:-live-$$}"
SEND="${SEND:-0}"
KEEP="${KEEP:-0}"

tmp="$(mktemp -d)"
bin="$tmp/lucinate"
dash_log="$tmp/dashboard.log"
started_dash=0

cleanup() {
  if [[ -x "$bin" ]]; then
    LUCINATE_DATA_DIR="$tmp/data" "$bin" rooms disband "$ROOM_ID" --yes >/dev/null 2>&1 || true
  fi
  if [[ "$started_dash" == "1" && "$KEEP" != "1" ]]; then
    pid="$(ss -ltnp 2>/dev/null | awk -v p=":$PORT" '$0 ~ p {match($0,/pid=([0-9]+)/,a); print a[1]}' | head -1)"
    [[ -n "${pid:-}" ]] && kill "$pid" 2>/dev/null || true
  fi
  [[ "$KEEP" == "1" ]] && echo "dashboard left on http://127.0.0.1:$PORT (token: $TOKEN, data dir: $tmp/data)"
  echo "artifacts: $tmp"
}
trap cleanup EXIT

echo "==> build"
(cd "$ROOT" && go build -o "$bin" .)

echo "==> dashboard on :$PORT"
if curl -sf -m 2 -o /dev/null "http://127.0.0.1:$PORT/api/status"; then
  echo "    already running — reusing it"
else
  HERMES_DASHBOARD_SESSION_TOKEN="$TOKEN" \
    hermes dashboard --port "$PORT" --skip-build --no-open >"$dash_log" 2>&1 &
  started_dash=1
  for _ in $(seq 1 60); do
    curl -sf -m 2 -o /dev/null "http://127.0.0.1:$PORT/api/status" && break
    sleep 1
  done
  curl -sf -m 2 -o /dev/null "http://127.0.0.1:$PORT/api/status" || {
    echo "dashboard did not come up; log:" >&2
    tail -20 "$dash_log" >&2
    exit 1
  }
fi

echo "==> lucinate data dir"
mkdir -p "$tmp/data/secrets"
cat >"$tmp/data/connections.json" <<JSON
{"connections":[{"id":"live","name":"live","type":"hermes","url":"http://127.0.0.1:$PORT",
"createdAt":"2026-01-01T00:00:00Z","lastUsed":"2026-01-01T00:00:00Z"}],"defaultId":"live"}
JSON
printf '{"apiKeys":{"live":"%s"}}\n' "$TOKEN" >"$tmp/data/secrets/secrets.json"
chmod 600 "$tmp/data/secrets/secrets.json"
export LUCINATE_DATA_DIR="$tmp/data"

echo "==> capabilities"
if ! "$bin" rooms caps; then
  echo "    a reused dashboard was probably started with another token — pass TOKEN=<its token>" >&2
  exit 1
fi

echo "==> profiles"
"$bin" rooms profiles | head -5

echo "==> room lifecycle ($PROFILES)"
"$bin" rooms create --name "Live $ROOM_ID" --profiles "$PROFILES" --room-id "$ROOM_ID" | head -5

if [[ "$SEND" == "1" ]]; then
  first="${PROFILES%%,*}"
  echo "==> send (one addressed turn)"
  "$bin" rooms send "$ROOM_ID" "@$first Odpowiedz jednym slowem: live-ok"
  for _ in $(seq 1 20); do
    sleep 3
    "$bin" rooms state "$ROOM_ID" | sed -n '2,4p'
    "$bin" rooms state "$ROOM_ID" | grep -q 'working=false' && break
  done
  "$bin" rooms log "$ROOM_ID" --since 0 | tail -6
fi

echo "==> state"
"$bin" rooms state "$ROOM_ID" | head -5

echo "==> disband"
"$bin" rooms disband "$ROOM_ID" --yes
trap - EXIT
"$bin" rooms disband "$ROOM_ID" --yes >/dev/null 2>&1 || true
if [[ "$started_dash" == "1" && "$KEEP" != "1" ]]; then
  pid="$(ss -ltnp 2>/dev/null | awk -v p=":$PORT" '$0 ~ p {match($0,/pid=([0-9]+)/,a); print a[1]}' | head -1)"
  [[ -n "${pid:-}" ]] && kill "$pid" 2>/dev/null || true
fi
echo "OK — live pass completed"
