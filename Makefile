VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOLANGCI_LINT ?= $(shell command -v golangci-lint 2>/dev/null || echo go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0)

.PHONY: build run test lint vet check snapshot clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/lazydev ./cmd/lazydev

run: build
	./bin/lazydev

test:
	go test -race ./...

vet:
	go vet ./...

lint:
	$(GOLANGCI_LINT) run ./...

check: vet lint test

snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -rf bin dist
