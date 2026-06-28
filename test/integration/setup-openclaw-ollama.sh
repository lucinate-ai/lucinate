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
GATEWAY_TOKEN="lucinate"

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

# Shared register + owner-approval device pairing (pair_device,
# write_integration_env). BACKUP_FILE/IDENTITY_DIR etc. are consumed there.
# shellcheck source=test/integration/lib/openclaw-pair.sh
source "$SCRIPT_DIR/lib/openclaw-pair.sh"

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

# --- Device pairing (register + owner approval) ----------------------------

pair_device
write_integration_env

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
