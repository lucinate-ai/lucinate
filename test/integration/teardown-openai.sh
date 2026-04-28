#!/usr/bin/env bash
#
# Tears down the OpenAI integration test environment by removing the
# .env.openai file. Ollama itself is left running — it's a host-side
# service the developer may want for non-test work.
#
# Usage:
#   ./test/integration/teardown-openai.sh
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

info() { printf "\033[1;34m==>\033[0m %s\n" "$*"; }
ok()   { printf "\033[1;32m  ✓\033[0m %s\n" "$*"; }

ENV_FILE="$PROJECT_ROOT/.env.openai"

if [ -f "$ENV_FILE" ]; then
    info "Removing .env.openai"
    rm "$ENV_FILE"
    ok ".env.openai removed"
fi

echo ""
info "OpenAI integration test environment torn down"
