#!/usr/bin/env bash
#
# Tears down the Hermes integration test environment:
#   1. docker compose down -v on the Hermes container.
#   2. Removes test/integration/hermes/state/ (the seeded profile config
#      and any state Hermes wrote to it).
#   3. Removes .env.hermes.
#
# Host Ollama is left running — it's a developer tool the test
# harness happens to use, not something we own.
#
# Usage:
#   ./test/integration/teardown-hermes.sh
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
HERMES_DIR="$SCRIPT_DIR/hermes"
COMPOSE_FILE="$HERMES_DIR/docker-compose.yml"

info() { printf "\033[1;34m==>\033[0m %s\n" "$*"; }
ok()   { printf "\033[1;32m  ✓\033[0m %s\n" "$*"; }

if [ -f "$COMPOSE_FILE" ]; then
    info "Stopping Hermes container"
    HERMES_UID="$(id -u)" \
    HERMES_GID="$(id -g)" \
    HERMES_TAG="${HERMES_TAG:-v2026.4.23}" \
        docker compose -f "$COMPOSE_FILE" down -v
    ok "Hermes container stopped"
fi

if [ -d "$HERMES_DIR/state" ]; then
    info "Removing $HERMES_DIR/state/"
    rm -rf "$HERMES_DIR/state"
    ok "State directory removed"
fi

ENV_FILE="$PROJECT_ROOT/.env.hermes"
if [ -f "$ENV_FILE" ]; then
    info "Removing .env.hermes"
    rm "$ENV_FILE"
    ok ".env.hermes removed"
fi

echo ""
info "Hermes integration test environment torn down"
