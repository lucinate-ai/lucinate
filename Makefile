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

.PHONY: coverage
coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

.PHONY: coverage-html
coverage-html: coverage
	go tool cover -html=coverage.out -o coverage.html

.PHONY: demo
demo: build
	PATH="$(CURDIR):$(PATH)" vhs docs/demo.tape
