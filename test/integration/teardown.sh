#!/usr/bin/env bash
#
# Tears down the local integration test environment:
#   1. Stops and removes the OpenClaw gateway container
#   2. Restores any backed-up device token
#
# Usage:
#   ./test/integration/teardown.sh
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
IDENTITY_DIR="$HOME/.openclaw-go/identity"
BACKUP_FILE="$IDENTITY_DIR/device-token.backup"

info()  { printf "\033[1;34m==>\033[0m %s\n" "$*"; }
ok()    { printf "\033[1;32m  ✓\033[0m %s\n" "$*"; }

# --- Stop gateway ----------------------------------------------------------

info "Stopping OpenClaw gateway"
docker compose -f "$COMPOSE_FILE" down 2>&1 | sed 's/^/    /'
ok "Gateway stopped"

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
