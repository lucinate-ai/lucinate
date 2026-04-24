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

MODEL="${MODEL:-qwen3.5:9b}"
GATEWAY_URL="http://localhost:18789"
GATEWAY_WS_URL="ws://127.0.0.1:18789/ws"
HEALTH_TIMEOUT=60

GATEWAY_TOKEN="repclaw-integration-test"

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

info "Preparing gateway state directory"
STATE_DIR="$SCRIPT_DIR/state"
mkdir -p "$STATE_DIR"
cp "$SCRIPT_DIR/openclaw.json" "$STATE_DIR/openclaw.json"
ok "State directory ready at $STATE_DIR"

info "Starting OpenClaw gateway"
OPENCLAW_UID="$(id -u)" OPENCLAW_GID="$(id -g)" \
    docker compose -f "$COMPOSE_FILE" up -d --wait 2>&1 | sed 's/^/    /'
ok "Gateway is healthy"

# --- Device pairing --------------------------------------------------------
#
# Flow:
#   1. Back up any existing device token.
#   2. Seed the gateway token so the first connect authenticates.
#   3. Connect once — this registers the device as pending (NOT_PAIRED).
#   4. Approve the pending device via the gateway CLI.
#   5. Rotate the device token to get a proper device credential.
#   6. Save the rotated token locally.
#   7. Verify the client can now connect with the device token.

info "Pairing device with test gateway"

# Back up any existing device token so we don't clobber a production token.
mkdir -p "$IDENTITY_DIR"
if [ -f "$IDENTITY_DIR/device-token" ]; then
    cp "$IDENTITY_DIR/device-token" "$BACKUP_FILE"
    rm "$IDENTITY_DIR/device-token"
    ok "Backed up existing device token"
fi

# Seed the gateway token as the device token. The gateway accepts this as
# bearer auth on the first connect, which registers the device as pending.
echo -n "$GATEWAY_TOKEN" > "$IDENTITY_DIR/device-token"
ok "Seeded gateway token for initial authentication"

# First connect: registers the device as pending. The gateway rejects it with
# NOT_PAIRED — that is expected. A socket timeout is also possible if the
# gateway is busy; retry until a pending device appears.
info "Registering device with gateway"
REQUEST_ID=""
DEVICE_ID=""
for attempt in 1 2 3; do
    OPENCLAW_GATEWAY_URL="$GATEWAY_URL" go run "$SCRIPT_DIR/pair/main.go" 2>&1 | sed 's/^/    /' || true
    DEVICES_JSON="$(docker compose -f "$COMPOSE_FILE" exec -T gateway \
        openclaw devices list --json \
        --token "$GATEWAY_TOKEN" \
        --url "$GATEWAY_WS_URL" 2>/dev/null)"
    REQUEST_ID="$(echo "$DEVICES_JSON" | jq -r '.pending[0].requestId // empty')"
    DEVICE_ID="$(echo "$DEVICES_JSON" | jq -r '.pending[0].deviceId // empty')"
    if [ -n "$REQUEST_ID" ] && [ -n "$DEVICE_ID" ]; then
        break
    fi
    if [ "$attempt" -lt 3 ]; then
        warn "Attempt $attempt: no pending device found, retrying..."
        sleep 2
    fi
done

if [ -z "$REQUEST_ID" ] || [ -z "$DEVICE_ID" ]; then
    fail "No pending device found after 3 attempts. Check gateway logs: docker compose -f $COMPOSE_FILE logs gateway"
fi
ok "Found pending device: $DEVICE_ID (requestId: $REQUEST_ID)"

# Approve the pending device.
info "Approving device"
docker compose -f "$COMPOSE_FILE" exec -T gateway \
    openclaw devices approve "$REQUEST_ID" \
    --token "$GATEWAY_TOKEN" \
    --url "$GATEWAY_WS_URL" 2>&1 | sed 's/^/    /'
ok "Device approved"

# Rotate the device token to get a proper device credential.
info "Rotating device token"
ROTATE_JSON="$(docker compose -f "$COMPOSE_FILE" exec -T gateway \
    openclaw devices rotate \
    --device "$DEVICE_ID" \
    --role operator \
    --scope operator.read \
    --scope operator.write \
    --scope operator.admin \
    --scope operator.approvals \
    --json \
    --token "$GATEWAY_TOKEN" \
    --url "$GATEWAY_WS_URL" 2>/dev/null)"

DEVICE_TOKEN="$(echo "$ROTATE_JSON" | jq -r '.token // empty')"

if [ -z "$DEVICE_TOKEN" ]; then
    fail "Failed to rotate device token. rotate output: $ROTATE_JSON"
fi

# Save the rotated token as the active device token.
echo -n "$DEVICE_TOKEN" > "$IDENTITY_DIR/device-token"
ok "Device token saved"

# Verify connection with the new device token.
info "Verifying connection"
if OPENCLAW_GATEWAY_URL="$GATEWAY_URL" go run "$SCRIPT_DIR/pair/main.go" 2>&1 | sed 's/^/    /'; then
    ok "Device paired and verified"
else
    fail "Connection failed. Check gateway logs: docker compose -f $COMPOSE_FILE logs gateway"
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
