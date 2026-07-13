VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -X github.com/lucinate-ai/lucinate/internal/version.Version=$(VERSION)

.PHONY: build
build:
	go build -ldflags "$(LDFLAGS)" -o lucinate .

.PHONY: build-prod
build-prod:
	go build -ldflags "$(LDFLAGS) -s -w" -trimpath -o lucinate .

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: install
install:
	go install -ldflags "$(LDFLAGS)" .

.PHONY: run
run:
	go run -ldflags "$(LDFLAGS)" . $(filter-out $@,$(MAKECMDGOALS))

.PHONY: test
test:
	go test ./...

# smoke runs the startup smoke test in isolation. The smoke test
# constructs the AppModel in every entry-view variant the startup
# resolver produces and feeds it the initial WindowSizeMsg the
# bubbletea program would emit on a real terminal. CI runs this so a
# regression that panics before any user input is caught before
# release. Hermetic — no gateway, no terminal required.
.PHONY: smoke
smoke:
	go test -count=1 -run TestStartupSmoke ./internal/tui/

.PHONY: coverage
coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

.PHONY: coverage-html
coverage-html: coverage
	go tool cover -html=coverage.out -o coverage.html

.PHONY: test-integration-openclaw-ollama-setup
test-integration-openclaw-ollama-setup:
	./test/integration/setup-openclaw-ollama.sh

.PHONY: test-integration-openclaw-bedrock-setup
test-integration-openclaw-bedrock-setup:
	./test/integration/setup-openclaw-bedrock.sh

.PHONY: test-integration-openclaw-openrouter-setup
test-integration-openclaw-openrouter-setup:
	./test/integration/setup-openclaw-openrouter.sh

.PHONY: test-integration-openclaw
test-integration-openclaw:
	go test -tags integration -count=1 -v ./internal/tui/

.PHONY: test-integration-openclaw-echo-setup
test-integration-openclaw-echo-setup:
	./test/integration/setup-openclaw-echo.sh

# Connection + chat smoke test. Runs only the version-agnostic smoke test, so
# it works with the instant echo model (the queue-ordering test assumes a
# model with latency). Used by the CI gateway-version matrix.
.PHONY: test-integration-openclaw-smoke
test-integration-openclaw-smoke:
	go test -tags integration -run TestConnectionSmoke_Integration -count=1 -v ./internal/tui/

.PHONY: test-integration-openclaw-teardown
test-integration-openclaw-teardown:
	./test/integration/teardown-openclaw.sh

# --- Bootstrap: rapid, interactive evaluation of OpenClaw ------------------
# Stand a local OpenClaw gateway up in Docker and chat with it from lucinate.
# Reuses the integration-test standup + pairing flow; see docs/bootstrap.md.
# Override the provider/model:  make bootstrap-openclaw-up PROVIDER=ollama MODEL=qwen2.5:1.5b
.PHONY: bootstrap-openclaw-up
bootstrap-openclaw-up:
	./test/integration/bootstrap-openclaw.sh --provider $(or $(PROVIDER),echo) $(if $(MODEL),--model $(MODEL),)

# Launch the interactive TUI against the bootstrapped gateway.
.PHONY: bootstrap-openclaw-run
bootstrap-openclaw-run:
	OPENCLAW_GATEWAY_URL=http://localhost:18789 go run -ldflags "$(LDFLAGS)" .

# Show gateway container + health status.
.PHONY: bootstrap-openclaw-status
bootstrap-openclaw-status:
	@docker compose -f test/integration/docker-compose.yml ps
	@printf "health: "; curl -fsS http://localhost:18789/healthz && echo || echo "unreachable"

.PHONY: bootstrap-openclaw-down
bootstrap-openclaw-down:
	./test/integration/teardown-openclaw.sh

.PHONY: test-integration-openai-setup
test-integration-openai-setup:
	./test/integration/setup-openai.sh

.PHONY: test-integration-openai
test-integration-openai:
	go test -tags integration_openai -count=1 -v ./internal/backend/openai/

.PHONY: test-integration-openai-teardown
test-integration-openai-teardown:
	./test/integration/teardown-openai.sh

.PHONY: test-integration-hermes-setup
test-integration-hermes-setup:
	./test/integration/setup-hermes.sh

.PHONY: test-integration-hermes
test-integration-hermes:
	go test -tags integration_hermes -count=1 -v ./internal/backend/hermes/

.PHONY: test-integration-hermes-teardown
test-integration-hermes-teardown:
	./test/integration/teardown-hermes.sh

.PHONY: demo
demo: build
	PATH="$(CURDIR):$(PATH)" vhs docs/demo.tape
