PNPM ?= corepack pnpm
DOCKER ?= docker
GO ?= go
GOLANGCI_LINT ?= $(shell GOBIN=$$($(GO) env GOBIN 2>/dev/null); GOPATH=$$($(GO) env GOPATH 2>/dev/null); \
	if [ -n "$$GOBIN" ] && [ -x "$$GOBIN/golangci-lint" ]; then \
		printf "%s/golangci-lint" "$$GOBIN"; \
	elif [ -x "$$GOPATH/bin/golangci-lint" ]; then \
		printf "%s/bin/golangci-lint" "$$GOPATH"; \
	elif command -v golangci-lint >/dev/null 2>&1 && golangci-lint version >/dev/null 2>&1; then \
		command -v golangci-lint; \
	fi)

.PHONY: build typecheck lint test hooks-install \
	node-install node-build node-typecheck node-dev node-image \
	harness-build harness-lint harness-smoke opencode-image codex-image \
	e2e e2e-self e2e-docker e2e-kind

build: node-build harness-build

typecheck: node-typecheck

test: lint typecheck build e2e-self

lint:
	git diff --check
	$(MAKE) harness-lint

hooks-install:
	lefthook install

node-install:
	cd node && $(PNPM) install --frozen-lockfile

node-build:
	cd node && $(PNPM) build

node-typecheck:
	cd node && $(PNPM) typecheck

node-dev:
	cd node && $(PNPM) dev

node-image:
	$(DOCKER) build -f node/image/Dockerfile -t ghcr.io/wyvernzora/n8n-acp/node:dev .

harness-build:
	cd harness/runtime && $(GO) test ./...

harness-lint:
	@if [ -z "$(GOLANGCI_LINT)" ]; then \
		echo "golangci-lint not found. Install it with: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"; \
		exit 127; \
	fi
	$(GOLANGCI_LINT) run ./harness/runtime/...

harness-smoke:
	scripts/smoke-opencode-acp.sh

opencode-image:
	$(DOCKER) build -f harness/opencode/Dockerfile -t ghcr.io/wyvernzora/n8n-acp/opencode:dev .

codex-image:
	$(DOCKER) build -f harness/codex/Dockerfile -t ghcr.io/wyvernzora/n8n-acp/codex:dev .

e2e: e2e-self

e2e-self:
	node e2e/self-check.js

e2e-docker: node-build
	e2e/run-docker.sh

e2e-kind:
	e2e/run-kind.sh
