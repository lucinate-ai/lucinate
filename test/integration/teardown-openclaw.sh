#!/usr/bin/env bash
#
# Tears down the local OpenClaw integration test environment (shared
# between the Ollama, Bedrock, and echo provider setups):
#   1. Stops and removes the OpenClaw gateway container
#   2. Stops the echomodel server, if running
#   3. Restores any backed-up device token
#
# Usage:
#   ./test/integration/teardown-openclaw.sh
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
IDENTITY_DIR="$HOME/.lucinate/identity/localhost_18789"
BACKUP_FILE="$IDENTITY_DIR/device-token.backup"
ECHO_PID_FILE="$SCRIPT_DIR/echomodel.pid"

info()  { printf "\033[1;34m==>\033[0m %s\n" "$*"; }
ok()    { printf "\033[1;32m  ✓\033[0m %s\n" "$*"; }

# --- Stop gateway ----------------------------------------------------------

info "Stopping OpenClaw gateway"
docker compose -f "$COMPOSE_FILE" down 2>&1 | sed 's/^/    /'
ok "Gateway stopped"

# --- Stop echomodel --------------------------------------------------------

if [ -f "$ECHO_PID_FILE" ]; then
    if kill "$(cat "$ECHO_PID_FILE")" 2>/dev/null; then
        ok "Stopped echomodel"
    fi
    rm -f "$ECHO_PID_FILE" "$SCRIPT_DIR/echomodel.bin" "$SCRIPT_DIR/echomodel.log"
fi

# --- Clean state directory -------------------------------------------------

STATE_DIR="$SCRIPT_DIR/state"
if [ -d "$STATE_DIR" ]; then
    rm -rf "$STATE_DIR"
    ok "Removed gateway state directory"
fi

# --- Restore device token --------------------------------------------------

if [ -f "$BACKUP_FILE" ]; then
    info "Restoring backed-up device token"
    mv "$BACKUP_FILE" "$IDENTITY_DIR/device-token"
    ok "Device token restored"
fi

echo ""
info "Integration test environment torn down"
