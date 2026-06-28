#!/usr/bin/env bash
#
# Sets up the local OpenClaw integration test environment with the
# OpenRouter provider:
#   1. Checks prerequisites
#   2. Starts the OpenClaw gateway in Docker (configured for OpenRouter)
#   3. Pairs the local device identity with the test gateway
#
# Prerequisites:
#   - Docker Desktop running
#   - jq installed (brew install jq)
#   - An OpenRouter API key (https://openrouter.ai/keys). Provide it via the
#     OPENROUTER_API_KEY env var, or the script will prompt for it.
#
# Usage:
#   ./test/integration/setup-openclaw-openrouter.sh [--model MODEL]
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
IDENTITY_DIR="$HOME/.lucinate/identity/localhost_18789"
BACKUP_FILE="$IDENTITY_DIR/device-token.backup"

MODEL="${MODEL:-deepseek/deepseek-v4-flash}"
GATEWAY_URL="http://localhost:18789"
GATEWAY_WS_URL="ws://127.0.0.1:18789/ws"
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

# An OpenRouter API key is required. Resolution order, mirroring how the Go
# tests pick up their own env files via godotenv (an existing env var wins):
#   1. OPENROUTER_API_KEY already exported in the environment.
#   2. The repo-root .env file (gitignored — holds the real key locally).
#   3. Interactive prompt (hidden input).
ENV_FILE="$SCRIPT_DIR/../../.env"
if [ -z "${OPENROUTER_API_KEY:-}" ] && [ -f "$ENV_FILE" ]; then
    # Extract just the value rather than sourcing the whole file, so we don't
    # execute arbitrary content. Tolerates an optional `export ` prefix and
    # surrounding single/double quotes.
    env_key="$(sed -nE 's/^[[:space:]]*(export[[:space:]]+)?OPENROUTER_API_KEY=["'\'']?([^"'\'']*)["'\'']?[[:space:]]*$/\2/p' "$ENV_FILE" | tail -n1)"
    if [ -n "$env_key" ]; then
        OPENROUTER_API_KEY="$env_key"
        ok "Loaded OPENROUTER_API_KEY from .env"
    fi
fi
if [ -z "${OPENROUTER_API_KEY:-}" ]; then
    warn "OPENROUTER_API_KEY is not set"
    if [ -t 0 ]; then
        read -rsp "    Enter your OpenRouter API key (https://openrouter.ai/keys): " OPENROUTER_API_KEY
        echo
    fi
    [ -n "${OPENROUTER_API_KEY:-}" ] || \
        fail "OPENROUTER_API_KEY is required. Get one at https://openrouter.ai/keys"
fi
export OPENROUTER_API_KEY
ok "OpenRouter API key found"

ok "All prerequisites found"

# --- Gateway ---------------------------------------------------------------

info "Preparing gateway state directory"
STATE_DIR="$SCRIPT_DIR/state"
# Wipe any leftover state so the gateway starts with no paired devices —
# otherwise the local keypair may match a previously-paired entry and the
# device skips the pending-registration step the script relies on.
rm -rf "$STATE_DIR"
mkdir -p "$STATE_DIR"
cp "$SCRIPT_DIR/openclaw.openrouter.json" "$STATE_DIR/openclaw.json"

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
info "OpenClaw (OpenRouter) integration test environment is ready"
echo ""
echo "  Provider: openrouter"
echo "  Gateway:  $GATEWAY_URL"
echo "  Model:    openrouter/$MODEL"
echo "  Identity: $IDENTITY_DIR"
echo ""
echo "  To list available OpenRouter models:"
echo "    docker compose -f $COMPOSE_FILE exec -T gateway \\"
echo "      openclaw models list --json \\"
echo "      --token $GATEWAY_TOKEN --url $GATEWAY_WS_URL"
echo ""
echo "  Run tests:     make test-integration-openclaw"
echo "  Tear down:     make test-integration-openclaw-teardown"
echo ""
