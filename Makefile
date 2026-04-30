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

.PHONY: test-integration-setup
test-integration-setup:
	./test/integration/setup.sh

.PHONY: test-integration-setup-bedrock
test-integration-setup-bedrock:
	./test/integration/setup.sh --provider bedrock

.PHONY: test-integration
test-integration:
	go test -tags integration -count=1 -v ./internal/tui/

.PHONY: test-integration-teardown
test-integration-teardown:
	./test/integration/teardown.sh

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
