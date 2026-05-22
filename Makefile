BIN  = wmdl
VERSION ?= $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)
BUILD = go build -ldflags="-X main.version=$(VERSION)" -o $(BIN) ./cmd/wmdl

.PHONY: all build test vet fmt lint clean release run-discover run-review run-process run-check

all: fmt vet lint test build

build:
	$(BUILD)

test:
	go test ./...

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
