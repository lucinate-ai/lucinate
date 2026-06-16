#!/usr/bin/env bash
#
# Sets up the local OpenClaw integration test environment with a zero-cost
# echo model — no remote provider, no API charge — for CI smoke tests:
#   1. Checks prerequisites
#   2. Builds and starts the echomodel server (OpenAI/Ollama-compatible stub)
#   3. Starts the OpenClaw gateway in Docker, pointed at echomodel
#   4. Pairs the local device identity via the setup-code bootstrap flow
#
# The gateway image is selected with OPENCLAW_IMAGE so CI can run a matrix
# across gateway versions, e.g.
#   OPENCLAW_IMAGE=ghcr.io/openclaw/openclaw:2026.5.28 ./setup-openclaw-echo.sh
#
# Prerequisites:
#   - Docker running
#   - jq, go
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
IDENTITY_DIR="$HOME/.lucinate/identity/localhost_18789"
BACKUP_FILE="$IDENTITY_DIR/device-token.backup"
ECHO_PID_FILE="$SCRIPT_DIR/echomodel.pid"

GATEWAY_URL="http://localhost:18789"
GATEWAY_WS_URL="ws://127.0.0.1:18789/ws"
ECHO_PORT="${ECHO_PORT:-18080}"
# The setup-code bootstrap profile grants read/write/approvals but not admin,
# so the issued device token is bounded to those scopes. Request the matching
# set on connect — asking for operator.admin is rejected as a scope mismatch.
OPERATOR_SCOPES="operator.read,operator.write,operator.approvals"

# --- Helpers ---------------------------------------------------------------

info()  { printf "\033[1;34m==>\033[0m %s\n" "$*"; }
ok()    { printf "\033[1;32m  ✓\033[0m %s\n" "$*"; }
warn()  { printf "\033[1;33m  !\033[0m %s\n" "$*"; }
fail()  { printf "\033[1;31m  ✗\033[0m %s\n" "$*" >&2; exit 1; }

check_prereq() {
    command -v "$1" &>/dev/null || fail "$1 is not installed. $2"
}

# Decodes the bootstrapToken from a base64url-encoded `openclaw qr` setup
# code, whose payload is JSON {url, bootstrapToken}.
setup_code_token() {
    local code="$1" b64
    b64="${code//-/+}"; b64="${b64//_//}"
    # Pad to a multiple of 4. A `printf '=%.0s' $(seq ...)` approach adds a
    # stray '=' when no padding is needed, which GNU base64 (Linux/CI) rejects
    # even though BSD base64 (macOS) tolerates it.
    while [ $(( ${#b64} % 4 )) -ne 0 ]; do b64="${b64}="; done
    printf '%s' "$b64" | base64 -d 2>/dev/null | jq -r '.bootstrapToken // empty'
}

# --- Prerequisites ---------------------------------------------------------

info "Checking prerequisites"
check_prereq docker "Install Docker: https://www.docker.com/"
check_prereq jq     "Install jq: brew install jq"
check_prereq go     "Install Go: https://go.dev/dl/"
ok "All prerequisites found"

# --- Echo model server -----------------------------------------------------

info "Starting echomodel on :$ECHO_PORT"
if [ -f "$ECHO_PID_FILE" ] && kill -0 "$(cat "$ECHO_PID_FILE")" 2>/dev/null; then
    kill "$(cat "$ECHO_PID_FILE")" 2>/dev/null || true
fi
ECHO_BIN="$SCRIPT_DIR/echomodel.bin"
go build -o "$ECHO_BIN" "$SCRIPT_DIR/echomodel/"
ECHO_ADDR=":$ECHO_PORT" nohup "$ECHO_BIN" >"$SCRIPT_DIR/echomodel.log" 2>&1 &
echo $! > "$ECHO_PID_FILE"
for i in $(seq 1 10); do
    if curl -fsS "http://localhost:$ECHO_PORT/v1/models" &>/dev/null; then break; fi
    [ "$i" -eq 10 ] && fail "echomodel did not become ready"
    sleep 1
done
ok "echomodel ready (pid $(cat "$ECHO_PID_FILE"))"

# --- Gateway ---------------------------------------------------------------

info "Preparing gateway state directory"
STATE_DIR="$SCRIPT_DIR/state"
rm -rf "$STATE_DIR"
mkdir -p "$STATE_DIR"
cp "$SCRIPT_DIR/openclaw.echo.json" "$STATE_DIR/openclaw.json"
ok "State directory ready at $STATE_DIR"

info "Starting OpenClaw gateway (${OPENCLAW_IMAGE:-ghcr.io/openclaw/openclaw:latest})"
# Don't use `--wait`: a fresh gateway installs bundled runtime deps on first
# start (~30s) during which the healthcheck fails; poll /healthz instead.
OPENCLAW_UID="$(id -u)" OPENCLAW_GID="$(id -g)" \
    docker compose -f "$COMPOSE_FILE" up -d 2>&1 | sed 's/^/    /'
info "Waiting for gateway health"
for i in $(seq 1 60); do
    if curl -fsS "$GATEWAY_URL/healthz" &>/dev/null; then break; fi
    [ "$i" -eq 60 ] && fail "Gateway did not become healthy. Logs: docker compose -f $COMPOSE_FILE logs gateway"
    sleep 3
done
ok "Gateway is healthy"

# --- Device pairing (setup-code bootstrap) ---------------------------------

info "Pairing device with test gateway (setup-code bootstrap)"
mkdir -p "$IDENTITY_DIR"
if [ -f "$IDENTITY_DIR/device-token" ]; then
    cp "$IDENTITY_DIR/device-token" "$BACKUP_FILE"
    rm "$IDENTITY_DIR/device-token"
    ok "Backed up existing device token"
fi

info "Minting setup code"
# /healthz can report live before the gateway is ready to mint a setup code,
# so retry a few times and surface the CLI's stderr if it never succeeds. Use
# `if out=$(...)` (set -e safe) rather than an assignment + `|| true`, and pass
# HOME so the CLI finds the gateway config regardless of how `compose exec`
# propagates the service environment.
SETUP_CODE=""
QR_ERR="$(mktemp)"
for attempt in 1 2 3 4 5; do
    if out=$(docker compose -f "$COMPOSE_FILE" exec -T -e HOME=/home/node gateway \
        openclaw qr --setup-code-only --url "$GATEWAY_WS_URL" </dev/null 2>"$QR_ERR"); then
        SETUP_CODE=$(printf '%s' "$out" | tr -d '\r\n')
        [ -n "$SETUP_CODE" ] && break
    fi
    warn "Attempt $attempt: setup-code mint not ready, retrying..."
    [ "$attempt" -lt 5 ] && sleep 3
done
if [ -z "$SETUP_CODE" ]; then
    warn "openclaw qr stderr:"
    sed 's/^/    /' "$QR_ERR" >&2 || true
    rm -f "$QR_ERR"
    fail "Failed to mint setup code. Logs: docker compose -f $COMPOSE_FILE logs gateway"
fi
rm -f "$QR_ERR"
BOOTSTRAP_TOKEN="$(setup_code_token "$SETUP_CODE")"
[ -n "$BOOTSTRAP_TOKEN" ] || fail "Failed to decode bootstrap token from setup code."
ok "Setup code minted"

info "Establishing device via bootstrap token"
if OPENCLAW_GATEWAY_URL="$GATEWAY_URL" OPENCLAW_BOOTSTRAP_TOKEN="$BOOTSTRAP_TOKEN" \
    OPENCLAW_OPERATOR_SCOPES="$OPERATOR_SCOPES" \
    go run "$SCRIPT_DIR/pair/main.go" 2>&1 | sed 's/^/    /'; then
    ok "Device established"
else
    fail "Bootstrap connect failed. Logs: docker compose -f $COMPOSE_FILE logs gateway"
fi
[ -s "$IDENTITY_DIR/device-token" ] || fail "No device token was issued during bootstrap."

info "Verifying connection"
if OPENCLAW_GATEWAY_URL="$GATEWAY_URL" OPENCLAW_OPERATOR_SCOPES="$OPERATOR_SCOPES" \
    go run "$SCRIPT_DIR/pair/main.go" 2>&1 | sed 's/^/    /'; then
    ok "Device paired and verified"
else
    fail "Connection failed. Logs: docker compose -f $COMPOSE_FILE logs gateway"
fi

# --- Write .env for test runs ---------------------------------------------

info "Writing test .env"
cat > "$PROJECT_ROOT/.env" <<EOF
OPENCLAW_GATEWAY_URL=$GATEWAY_URL
# The setup-code bootstrap profile grants read/write/approvals but not admin,
# so the device token is bounded to those scopes. Request the matching set.
OPENCLAW_OPERATOR_SCOPES=$OPERATOR_SCOPES
EOF
ok "Wrote .env"

echo ""
info "OpenClaw (echo model) integration test environment is ready"
echo ""
echo "  Provider: echomodel (no API charge)"
echo "  Gateway:  $GATEWAY_URL  (${OPENCLAW_IMAGE:-latest})"
echo "  Run tests:     make test-integration-openclaw"
echo "  Tear down:     make test-integration-openclaw-teardown"
echo ""
