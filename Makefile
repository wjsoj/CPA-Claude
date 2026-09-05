SHELL := /bin/bash

WEB_DIR := internal/admin/web
WEB_DIST := $(WEB_DIR)/dist
GO_MAIN := ./cmd/server
BIN := bin/cpa-claude

# Pin the Go toolchain. cc-core's uTLS transport panics on the first OAuth
# refresh under Go 1.27 — that took hypitoken's production down twelve times at
# v0.36.85 — and the system Go is already 1.27, so an unpinned build here picks
# up the broken one. This must stay in step with go.mod's `go` directive and
# with the go-version in both CI workflows.
GOTOOLCHAIN ?= go1.26.2
export GOTOOLCHAIN

.PHONY: all build web web-install web-dev generate tidy lint lint-go fmt clean help

all: build

help:
	@echo "Targets:"
	@echo "  make build        — build admin SPA and Go binary (default, Go $(GOTOOLCHAIN))"
	@echo "  make web          — build admin SPA only (bun run build)"
	@echo "  make web-dev      — run Vite dev server with API proxy to :8317"
	@echo "  make web-install  — install frontend deps"
	@echo "  make generate     — run go generate (invokes bun build)"
	@echo "  make tidy         — go mod tidy"
	@echo "  make lint         — golangci-lint run ./... (CI gate)"
	@echo "  make fmt          — golangci-lint fmt ./..."
	@echo "  make clean        — remove dist, node_modules, bin"

web-install:
	cd $(WEB_DIR) && bun install

web: web-install
	cd $(WEB_DIR) && bun run build

web-dev:
	cd $(WEB_DIR) && bun run dev

build: web
	mkdir -p bin
	go build -o $(BIN) $(GO_MAIN)

generate:
	go generate ./...

tidy:
	go mod tidy

# Mirrors the lint-go CI job. Requires golangci-lint v2 (.golangci.yml is
# v2-schema; a v1 binary cannot parse it).
lint: lint-go

lint-go:
	golangci-lint run ./...

fmt:
	golangci-lint fmt ./...

clean:
	rm -rf $(WEB_DIST)/* $(WEB_DIR)/node_modules bin/
	touch $(WEB_DIST)/.gitkeep
