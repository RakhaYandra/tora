.PHONY: build run vet test race lint clean install ci e2e

PREFIX ?= $(HOME)/.local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/RakhaYandra/tora/internal/cli.version=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o bin/tora ./cmd/tora

release:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/tora-linux-amd64 ./cmd/tora
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/tora-linux-arm64 ./cmd/tora
	sha256sum dist/tora-linux-* > dist/SHA256SUMS

lint:
	test -z "$$(gofmt -l cmd internal tests)" || (gofmt -l cmd internal tests && exit 1)
	go vet ./...

install: build
	install -Dm755 bin/tora $(PREFIX)/bin/tora

run: build
	./bin/tora --help

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

ci: lint test
	go test -race ./internal/... ./tests/...

e2e: build
	./scripts/e2e.sh

clean:
	rm -rf bin/ dist/
