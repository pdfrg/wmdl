BIN  = wmdl
VERSION ?= $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)
BUILD = go build -ldflags="-X main.version=$(VERSION)" -o $(BIN) ./cmd/wmdl

.PHONY: all build test test-race test-integration coverage vet fmt lint clean release run-discover run-review run-process run-check

all: fmt vet lint test build

build:
	$(BUILD)

test:
	go test ./... -count=1

test-race:
	go test ./... -race -count=1 -coverprofile=coverage.out

test-integration:
	go test -tags=integration ./... -count=1

coverage: test-race
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

vet:
	go vet ./...

fmt:
	go fmt ./...

lint:
	golangci-lint run ./...

check: fmt vet lint test build

run-discover:
	go run ./cmd/wmdl discover

run-review:
	go run ./cmd/wmdl review

run-process:
	go run ./cmd/wmdl process

release:
	goreleaser release --clean

clean:
	rm -f $(BIN)
