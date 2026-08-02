APP := morph
BUILD_DIR := build
GO ?= /opt/homebrew/Cellar/go/1.26.1/libexec/bin/go
GO_SQLITE_TAGS ?= sqlite_fts5
GOLANGCI_LINT_VERSION ?= 2.12.2
LIVE_CONFIG ?= $(CURDIR)/config.yaml
LIVE_ENV_FILE ?= $(CURDIR)/.env
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || printf dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
LD_FLAGS := -X github.com/wandxy/morph/internal/constants.AppVersion=$(VERSION) -X github.com/wandxy/morph/internal/constants.CommitHash=$(COMMIT)

.PHONY: install-tools install-lint install-hooks build-proto build build-sandbox test test-execution-docker test-agent-baseline test-spec test-live test-live-sqlite test-live-memory test-live-all host-deps lint install

install-tools:
	@$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
	@$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.76.0
	@$(MAKE) install-lint

install-lint:
	@$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v$(GOLANGCI_LINT_VERSION)

install-hooks:
	@git config core.hooksPath .githooks
	@chmod +x .githooks/commit-msg
	@echo "Installed git hooks from .githooks"

build-proto:
	@PATH="$(PATH):$(HOME)/go/bin" protoc \
		--go_out=. \
		--go_opt=paths=source_relative \
		--go-grpc_out=. \
		--go-grpc_opt=paths=source_relative \
		internal/rpc/proto/morph.proto

build: build-proto
	@mkdir -p $(BUILD_DIR)
	@CGO_ENABLED=1 $(GO) build -tags $(GO_SQLITE_TAGS) -ldflags "$(LD_FLAGS)" -o $(BUILD_DIR)/$(APP) ./cmd/morph

build-sandbox:
	@docker build --platform linux/amd64 -f containers/sandbox/Dockerfile -t morph-sandbox:test .

test-execution-docker: build-proto
	@MORPH_EXECUTION_DOCKER_TEST=1 MORPH_EXECUTION_DOCKER_IMAGE=morph-sandbox:test CGO_ENABLED=1 $(GO) test -tags '$(GO_SQLITE_TAGS) execution_docker' ./internal/execution/docker/... ./internal/e2e/tests/execution/... -count=1

test: build-proto
	@CGO_ENABLED=1 $(GO) test -tags $(GO_SQLITE_TAGS) ./...

test-agent-baseline: build-proto
	@CGO_ENABLED=1 $(GO) test -tags $(GO_SQLITE_TAGS) \
		./internal/host \
		./internal/host/context/compaction \
		./internal/host/context/summary \
		./internal/tui/app \
		./cmd/morph

test-spec:
	@CGO_ENABLED=1 $(GO) test -tags $(GO_SQLITE_TAGS) ./internal/e2e ./cmd/morph ./cmd/session ./cmd/trace ./cmd/daemon -count=1

test-live:
	@$(MAKE) test-live-sqlite

test-live-sqlite:
	@MORPH_E2E_LIVE=1 \
		MORPH_E2E_LIVE_CONFIG="$(LIVE_CONFIG)" \
		MORPH_E2E_LIVE_ENV_FILE="$$(if [ -f "$(LIVE_ENV_FILE)" ]; then printf '%s' "$(LIVE_ENV_FILE)"; fi)" \
		MORPH_STORAGE_BACKEND=sqlite \
		CGO_ENABLED=1 $(GO) test -tags $(GO_SQLITE_TAGS) ./cmd/morph -run '^Test_E2E_MorphLiveHarness_' -count=1

test-live-memory:
	@MORPH_E2E_LIVE=1 \
		MORPH_E2E_LIVE_CONFIG="$(LIVE_CONFIG)" \
		MORPH_E2E_LIVE_ENV_FILE="$$(if [ -f "$(LIVE_ENV_FILE)" ]; then printf '%s' "$(LIVE_ENV_FILE)"; fi)" \
		MORPH_STORAGE_BACKEND=memory \
		CGO_ENABLED=1 $(GO) test -tags $(GO_SQLITE_TAGS) ./cmd/morph -run '^Test_E2E_MorphLiveHarness_' -count=1

test-live-all: test-live-sqlite test-live-memory

host-deps:
	@$(GO) list -f 'direct imports for {{.ImportPath}}:{{range .Imports}}{{printf "\n  %s" .}}{{end}}' ./internal/host
	@printf '\ninternal/host transitive morph imports:\n'
	@$(GO) list -deps ./internal/host | grep '^github.com/wandxy/morph/' | sed 's/^/  /'

lint:
	@version="$$(golangci-lint version 2>/dev/null)"; \
		case "$$version" in \
			*"version $(GOLANGCI_LINT_VERSION) "*) ;; \
			*) printf 'golangci-lint $(GOLANGCI_LINT_VERSION) is required; run make install-lint\n' >&2; exit 2 ;; \
		esac
	@golangci-lint run ./...

install: build-proto
	@install_dir="$$( $(GO) env GOBIN )"; \
		if [ -z "$$install_dir" ]; then install_dir="$$( $(GO) env GOPATH )/bin"; fi; \
		rm -f "$$install_dir/$(APP)"
	@CGO_ENABLED=1 $(GO) install -tags $(GO_SQLITE_TAGS) -ldflags "$(LD_FLAGS)" ./cmd/morph
