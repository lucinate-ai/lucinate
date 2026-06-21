#!/usr/bin/env bash
#
# Sets up the local OpenClaw integration test environment with the
# Ollama provider:
#   1. Checks prerequisites
#   2. Checks/starts Ollama and pulls the test model
#   3. Starts the OpenClaw gateway in Docker
#   4. Pairs the local device identity with the test gateway
#
# Prerequisites:
#   - Docker Desktop running
#   - Ollama installed (brew install ollama)
#   - jq installed (brew install jq)
#
# Usage:
#   ./test/integration/setup-openclaw-ollama.sh [--model MODEL]
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
IDENTITY_DIR="$HOME/.lucinate/identity/localhost_18789"
BACKUP_FILE="$IDENTITY_DIR/device-token.backup"

MODEL="${MODEL:-qwen2.5:1.5b}"
GATEWAY_URL="http://localhost:18789"
GATEWAY_WS_URL="ws://127.0.0.1:18789/ws"
GATEWAY_TOKEN="lucinate"
# The setup-code bootstrap profile grants read/write/approvals but not admin,
# so the issued device token is bounded to those scopes; request the matching
# set on connect (operator.admin would be rejected as a scope mismatch).
OPERATOR_SCOPES="operator.read,operator.write,operator.approvals"

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
check_prereq docker "Install Docker Desktop: https://www.docker.com/products/docker-desktop/"
check_prereq jq     "Install jq: brew install jq"
check_prereq go     "Install Go: https://go.dev/dl/"
check_prereq ollama "Install Ollama: brew install ollama"
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
# Wipe any leftover state so the gateway starts with no paired devices —
# otherwise the local keypair may match a previously-paired entry and the
# device skips the pending-registration step the script relies on.
rm -rf "$STATE_DIR"
mkdir -p "$STATE_DIR"
cp "$SCRIPT_DIR/openclaw.ollama.json" "$STATE_DIR/openclaw.json"

# Substitute the model name in the config template.
sed -i.bak "s|__MODEL__|${MODEL}|g" "$STATE_DIR/openclaw.json"
rm -f "$STATE_DIR/openclaw.json.bak"

ok "State directory ready at $STATE_DIR"

info "Starting OpenClaw gateway"
OPENCLAW_UID="$(id -u)" OPENCLAW_GID="$(id -g)" \
    docker compose -f "$COMPOSE_FILE" up -d --wait 2>&1 | sed 's/^/    /'
ok "Gateway is healthy"

# --- Device pairing (setup-code bootstrap) ---------------------------------
#
# OpenClaw >= 2026.5.x binds operator scopes to a device's approved pairing
# record, so the old "seed token -> connect -> approve -> rotate" dance no
# longer works: an unpaired device cannot self-grant the pairing scope it
# would need to approve itself. Instead we use the headless setup-code flow:
# the gateway mints a short-lived bootstrap token, the client redeems it on
# connect, and the gateway establishes + approves the device and issues a
# durable device token. No manual approval step.
#
# Flow:
#   1. Back up any existing device token, then clear it so the first connect
#      presents only the bootstrap token + device identity.
#   2. Mint a setup code on the gateway host (openclaw qr) and decode its
#      bootstrap token.
#   3. Connect once with OPENCLAW_BOOTSTRAP_TOKEN — the gateway establishes
#      the device and issues a device token, which the client persists.
#   4. Verify the client can reconnect with the saved device token.

info "Pairing device with test gateway (setup-code bootstrap)"

# Back up any existing device token so we don't clobber a production token,
# and clear it so the bootstrap connect presents only the bootstrap token.
mkdir -p "$IDENTITY_DIR"
if [ -f "$IDENTITY_DIR/device-token" ]; then
    cp "$IDENTITY_DIR/device-token" "$BACKUP_FILE"
    rm "$IDENTITY_DIR/device-token"
    ok "Backed up existing device token"
fi

# Mint a setup code on the gateway host. `openclaw qr` writes the bootstrap
# token to local gateway state (devices/bootstrap.json) — no WS auth needed,
# which is what sidesteps the unpaired-device approval chicken-and-egg.
info "Minting setup code"
SETUP_CODE="$(docker compose -f "$COMPOSE_FILE" exec -T gateway \
    openclaw qr --setup-code-only --url "$GATEWAY_WS_URL" 2>/dev/null | tr -d '\r\n')"
if [ -z "$SETUP_CODE" ]; then
    fail "Failed to mint setup code. Check gateway logs: docker compose -f $COMPOSE_FILE logs gateway"
fi
BOOTSTRAP_TOKEN="$(setup_code_token "$SETUP_CODE")"
if [ -z "$BOOTSTRAP_TOKEN" ]; then
    fail "Failed to decode bootstrap token from setup code."
fi
ok "Setup code minted"

# Connect with the bootstrap token. The gateway establishes + approves this
# device and issues a durable device token, which the SDK persists locally.
# The pair client uses a generous handshake timeout to absorb the gateway's
# first-connect validator compilation after a cold start.
info "Establishing device via bootstrap token"
if OPENCLAW_GATEWAY_URL="$GATEWAY_URL" OPENCLAW_BOOTSTRAP_TOKEN="$BOOTSTRAP_TOKEN" \
    OPENCLAW_OPERATOR_SCOPES="$OPERATOR_SCOPES" \
    go run "$SCRIPT_DIR/pair/main.go" 2>&1 | sed 's/^/    /'; then
    ok "Device established"
else
    fail "Bootstrap connect failed. Check gateway logs: docker compose -f $COMPOSE_FILE logs gateway"
fi

if [ ! -s "$IDENTITY_DIR/device-token" ]; then
    fail "No device token was issued during bootstrap. Check gateway logs: docker compose -f $COMPOSE_FILE logs gateway"
fi

# Verify the issued device token now authenticates on its own.
info "Verifying connection"
if OPENCLAW_GATEWAY_URL="$GATEWAY_URL" OPENCLAW_OPERATOR_SCOPES="$OPERATOR_SCOPES" \
    go run "$SCRIPT_DIR/pair/main.go" 2>&1 | sed 's/^/    /'; then
    ok "Device paired and verified"
else
    fail "Connection failed. Check gateway logs: docker compose -f $COMPOSE_FILE logs gateway"
fi

# --- Write integration.env for test runs ----------------------------------

info "Writing test integration.env"
cat > "$SCRIPT_DIR/integration.env" <<EOF
OPENCLAW_GATEWAY_URL=$GATEWAY_URL
# The setup-code bootstrap profile grants read/write/approvals but not admin,
# so the device token is bounded to those scopes. Request the matching set;
# asking for operator.admin would be rejected as a scope mismatch.
OPENCLAW_OPERATOR_SCOPES=operator.read,operator.write,operator.approvals
EOF
ok "Wrote test/integration/integration.env with OPENCLAW_GATEWAY_URL=$GATEWAY_URL"

# --- Done ------------------------------------------------------------------

echo ""
info "OpenClaw (Ollama) integration test environment is ready"
echo ""
echo "  Provider: ollama"
echo "  Gateway:  $GATEWAY_URL"
echo "  Model:    ollama/$MODEL"
echo "  Identity: $IDENTITY_DIR"
echo ""
echo "  Run tests:     make test-integration-openclaw"
echo "  Tear down:     make test-integration-openclaw-teardown"
echo ""
