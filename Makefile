BIN  = wmd
BUILD = go build -o $(BIN) ./cmd/wmd

.PHONY: all build test vet fmt lint clean run-discover run-review run-process run-check

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
	go run ./cmd/wmd discover

run-review:
	go run ./cmd/wmd review

run-process:
	go run ./cmd/wmd process

clean:
	rm -f $(BIN)
