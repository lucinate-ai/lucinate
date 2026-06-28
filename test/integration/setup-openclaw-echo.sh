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
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
IDENTITY_DIR="$HOME/.lucinate/identity/localhost_18789"
BACKUP_FILE="$IDENTITY_DIR/device-token.backup"
ECHO_PID_FILE="$SCRIPT_DIR/echomodel.pid"

GATEWAY_URL="http://localhost:18789"
GATEWAY_TOKEN="lucinate"
ECHO_PORT="${ECHO_PORT:-18080}"

# --- Helpers ---------------------------------------------------------------

info()  { printf "\033[1;34m==>\033[0m %s\n" "$*"; }
ok()    { printf "\033[1;32m  ✓\033[0m %s\n" "$*"; }
warn()  { printf "\033[1;33m  !\033[0m %s\n" "$*"; }
fail()  { printf "\033[1;31m  ✗\033[0m %s\n" "$*" >&2; exit 1; }

check_prereq() {
    command -v "$1" &>/dev/null || fail "$1 is not installed. $2"
}

# Shared register + owner-approval device pairing (pair_device,
# write_integration_env). BACKUP_FILE/IDENTITY_DIR etc. are consumed there.
# shellcheck source=test/integration/lib/openclaw-pair.sh
source "$SCRIPT_DIR/lib/openclaw-pair.sh"

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

info "Starting OpenClaw gateway (${OPENCLAW_IMAGE:-ghcr.io/openclaw/openclaw:2026.5.28})"
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

# --- Device pairing (register + owner approval) ----------------------------

pair_device
write_integration_env

echo ""
info "OpenClaw (echo model) integration test environment is ready"
echo ""
echo "  Provider: echomodel (no API charge)"
echo "  Gateway:  $GATEWAY_URL  (${OPENCLAW_IMAGE:-2026.5.28})"
echo "  Run tests:     make test-integration-openclaw"
echo "  Tear down:     make test-integration-openclaw-teardown"
echo ""
