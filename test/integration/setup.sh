#!/usr/bin/env bash
#
# Sets up the local integration test environment:
#   1. Checks/starts Ollama and pulls the test model
#   2. Starts the OpenClaw gateway in Docker
#   3. Pairs the local device identity with the test gateway
#
# Prerequisites:
#   - Docker Desktop running
#   - Ollama installed (brew install ollama)
#   - jq installed (brew install jq)
#
# Usage:
#   ./test/integration/setup.sh [--model MODEL]
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
IDENTITY_DIR="$HOME/.openclaw-go/identity"
BACKUP_FILE="$IDENTITY_DIR/device-token.backup"

MODEL="${MODEL:-qwen2.5:3b}"
GATEWAY_URL="http://localhost:18789"
PAIR_TIMEOUT=60
HEALTH_TIMEOUT=60

# --- Parse args -----------------------------------------------------------

while [[ $# -gt 0 ]]; do
    case "$1" in
        --model) MODEL="$2"; shift 2 ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

# --- Helpers ---------------------------------------------------------------

info()  { printf "\033[1;34m==>\033[0m %s\n" "$*"; }
ok()    { printf "\033[1;32m  ✓\033[0m %s\n" "$*"; }
warn()  { printf "\033[1;33m  !\033[0m %s\n" "$*"; }
fail()  { printf "\033[1;31m  ✗\033[0m %s\n" "$*" >&2; exit 1; }

check_prereq() {
    command -v "$1" &>/dev/null || fail "$1 is not installed. $2"
}

# --- Prerequisites ---------------------------------------------------------

info "Checking prerequisites"
check_prereq docker   "Install Docker Desktop: https://www.docker.com/products/docker-desktop/"
check_prereq ollama   "Install Ollama: brew install ollama"
check_prereq jq       "Install jq: brew install jq"
check_prereq go       "Install Go: https://go.dev/dl/"
ok "All prerequisites found"

# --- Ollama ----------------------------------------------------------------

info "Checking Ollama"
if ! curl -fsS http://localhost:11434/api/tags &>/dev/null; then
    warn "Ollama is not running — starting it"
    ollama serve &>/dev/null &
    OLLAMA_PID=$!
    # Wait for Ollama to be ready.
    for i in $(seq 1 30); do
        if curl -fsS http://localhost:11434/api/tags &>/dev/null; then
            break
        fi
        if [ "$i" -eq 30 ]; then
            fail "Ollama failed to start"
        fi
        sleep 1
    done
    ok "Ollama started (pid $OLLAMA_PID)"
else
    ok "Ollama is running"
fi

info "Pulling model: $MODEL"
ollama pull "$MODEL"
ok "Model ready"

# --- Gateway ---------------------------------------------------------------

info "Starting OpenClaw gateway"
docker compose -f "$COMPOSE_FILE" up -d --wait 2>&1 | sed 's/^/    /'
ok "Gateway is healthy"

# --- Device pairing --------------------------------------------------------

info "Pairing device with test gateway"

# Back up any existing device token so we don't clobber a production token.
mkdir -p "$IDENTITY_DIR"
if [ -f "$IDENTITY_DIR/device-token" ]; then
    cp "$IDENTITY_DIR/device-token" "$BACKUP_FILE"
    rm "$IDENTITY_DIR/device-token"
    ok "Backed up existing device token"
fi

# Start the pairing client in the background. It will block until approved.
OPENCLAW_GATEWAY_URL="$GATEWAY_URL" go run "$SCRIPT_DIR/pair/main.go" &
PAIR_PID=$!

# Poll for pending device pairing requests and auto-approve.
APPROVED=false
for i in $(seq 1 "$PAIR_TIMEOUT"); do
    # Try to list pending devices. The CLI output format may vary, so we
    # try --json first, then fall back to plain text parsing.
    PENDING=$(docker compose -f "$COMPOSE_FILE" exec -T \
        -e OPENCLAW_GATEWAY_TOKEN=repclaw-integration-test \
        gateway openclaw device list --pending --json 2>/dev/null || echo "")

    if [ -n "$PENDING" ] && [ "$PENDING" != "[]" ] && [ "$PENDING" != "null" ]; then
        # Extract device ID — handle both .id and .deviceId field names.
        DEVICE_ID=$(echo "$PENDING" | jq -r '
            if type == "array" then
                .[0] | (.id // .deviceId // .device_id // empty)
            else
                (.id // .deviceId // .device_id // empty)
            end' 2>/dev/null || echo "")

        if [ -n "$DEVICE_ID" ]; then
            info "Approving device: $DEVICE_ID"
            docker compose -f "$COMPOSE_FILE" exec -T \
                -e OPENCLAW_GATEWAY_TOKEN=repclaw-integration-test \
                gateway openclaw device approve "$DEVICE_ID" 2>&1 | sed 's/^/    /'
            APPROVED=true
            break
        fi
    fi
    sleep 1
done

if [ "$APPROVED" = false ]; then
    kill "$PAIR_PID" 2>/dev/null || true
    fail "No pending device found after ${PAIR_TIMEOUT}s. Check gateway logs: docker compose -f $COMPOSE_FILE logs gateway"
fi

# Wait for the pairing client to finish (it should succeed now).
if wait "$PAIR_PID"; then
    ok "Device paired"
else
    fail "Device pairing failed. Check gateway logs: docker compose -f $COMPOSE_FILE logs gateway"
fi

# --- Write .env for test runs ---------------------------------------------

info "Writing test .env"
cat > "$PROJECT_ROOT/.env" <<EOF
OPENCLAW_GATEWAY_URL=$GATEWAY_URL
EOF
ok "Wrote .env with OPENCLAW_GATEWAY_URL=$GATEWAY_URL"

# --- Done ------------------------------------------------------------------

echo ""
info "Integration test environment is ready"
echo ""
echo "  Gateway:  $GATEWAY_URL"
echo "  Model:    ollama/$MODEL"
echo "  Identity: $IDENTITY_DIR"
echo ""
echo "  Run tests:     make test-integration"
echo "  Tear down:     make test-integration-teardown"
echo ""
