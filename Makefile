GO ?= go
BINARY ?= bin/opf-cli

.PHONY: build build-linux build-gui build-tui test test-integration docker-test-image clean

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BINARY) ./cmd/opf-cli

build-linux:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags="-s -w" -o bin/opf-cli-linux-amd64 ./cmd/opf-cli

build-gui:
	mkdir -p bin
	CGO_ENABLED=1 $(GO) build -trimpath -ldflags="-s -w" -o bin/opf-gui ./cmd/opf-gui

build-tui:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o bin/opf-tui ./cmd/opf-tui

test:
	CGO_ENABLED=0 $(GO) test ./...

test-integration:
	CGO_ENABLED=0 $(GO) test -tags=integration -count=1 -timeout=30m ./...

docker-test-image:
	docker build -f docker/ubuntu-22.04.Dockerfile -t opf/ubuntu-22.04:f0 docker

clean:
	rm -rf bin
