#!/usr/bin/env bash
#
# Sets up the local integration test environment for the OpenAI-compatible
# backend, talking directly to a host-side Ollama instance — no OpenClaw
# gateway, no Docker. Mirrors setup.sh's structure but is much shorter
# because there's no device-pairing flow.
#
# Steps:
#   1. Check prerequisites (ollama, go).
#   2. Start `ollama serve` if it isn't already running.
#   3. Pull the test model (default qwen2.5:0.5b — smallest model that
#      gives coherent chat output).
#   4. Warm the model with a single /api/generate call so the first test
#      doesn't pay the lazy-load cost.
#   5. Probe the OpenAI backend wiring end-to-end.
#   6. Write .env.openai with the env vars the integration tests read.
#
# Usage:
#   ./test/integration/setup-openai.sh [--model MODEL]
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

MODEL="${MODEL:-qwen2.5:0.5b}"
BASE_URL="http://localhost:11434/v1"
OLLAMA_HEALTH_URL="http://localhost:11434/api/tags"
OLLAMA_GEN_URL="http://localhost:11434/api/generate"
OLLAMA_BOOT_TIMEOUT=30

# --- Parse args -----------------------------------------------------------

while [[ $# -gt 0 ]]; do
    case "$1" in
        --model) MODEL="$2"; shift 2 ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

# --- Helpers --------------------------------------------------------------

info()  { printf "\033[1;34m==>\033[0m %s\n" "$*"; }
ok()    { printf "\033[1;32m  ✓\033[0m %s\n" "$*"; }
warn()  { printf "\033[1;33m  !\033[0m %s\n" "$*"; }
fail()  { printf "\033[1;31m  ✗\033[0m %s\n" "$*" >&2; exit 1; }

check_prereq() {
    command -v "$1" &>/dev/null || fail "$1 is not installed. $2"
}

# --- Prerequisites --------------------------------------------------------

info "Checking prerequisites"
check_prereq ollama "Install Ollama: brew install ollama"
check_prereq go     "Install Go: https://go.dev/dl/"
ok "All prerequisites found"

# --- Ollama ---------------------------------------------------------------

info "Checking Ollama"
if ! curl -fsS "$OLLAMA_HEALTH_URL" &>/dev/null; then
    warn "Ollama is not running — starting it"
    ollama serve &>/dev/null &
    OLLAMA_PID=$!
    for i in $(seq 1 "$OLLAMA_BOOT_TIMEOUT"); do
        if curl -fsS "$OLLAMA_HEALTH_URL" &>/dev/null; then
            break
        fi
        if [ "$i" -eq "$OLLAMA_BOOT_TIMEOUT" ]; then
            fail "Ollama failed to start within ${OLLAMA_BOOT_TIMEOUT}s"
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

# --- Warm the model -------------------------------------------------------
#
# Ollama lazy-loads models on first request — the first chat completion
# pays a 1-3s load cost. Warm it now so the first integration test runs
# at steady-state speed.

info "Warming model"
curl -fsS "$OLLAMA_GEN_URL" \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"$MODEL\",\"prompt\":\"hi\",\"stream\":false}" \
    >/dev/null
ok "Model warm"

# --- Probe the OpenAI backend --------------------------------------------

info "Probing OpenAI backend wiring"
LUCINATE_OPENAI_BASE_URL="$BASE_URL" \
LUCINATE_OPENAI_DEFAULT_MODEL="$MODEL" \
    go run "$SCRIPT_DIR/openai/probe/main.go" 2>&1 | sed 's/^/    /'
ok "Backend probe succeeded"

# --- Write .env.openai for test runs --------------------------------------

info "Writing test .env.openai"
cat > "$PROJECT_ROOT/.env.openai" <<EOF
LUCINATE_OPENAI_BASE_URL=$BASE_URL
LUCINATE_OPENAI_DEFAULT_MODEL=$MODEL
EOF
ok "Wrote .env.openai"

# --- Done -----------------------------------------------------------------

echo ""
info "OpenAI integration test environment is ready"
echo ""
echo "  Backend:  OpenAI-compatible (Ollama, host-side)"
echo "  Base URL: $BASE_URL"
echo "  Model:    $MODEL"
echo ""
echo "  Run tests:    make test-integration-openai"
echo "  Tear down:    make test-integration-openai-teardown"
echo ""
