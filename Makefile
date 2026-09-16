VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/fcsonline/tailmount/internal/version.Version=$(VERSION)

.PHONY: build test lint clean

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/tailmount ./cmd/tailmount

test:
	go test -race ./...

lint:
	@test -z "$$(gofmt -l cmd internal)" || { gofmt -l cmd internal; echo "gofmt: files need formatting"; exit 1; }
	go vet ./...

clean:
	rm -rf bin
