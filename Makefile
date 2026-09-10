.PHONY: assets build test check dev
VERSION ?= 0.1.0-dev
COMMIT := $(shell git rev-parse --short HEAD)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
assets:
	npm ci
	npm run build
build:
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.built=$(BUILD_TIME)" -o bin/mrkt ./cmd/mrkt
test:
	go test ./...
check:
	go vet ./...
	go test -race ./...
dev:
	docker compose -f compose.yaml -f compose.dev.yaml up -d --build
