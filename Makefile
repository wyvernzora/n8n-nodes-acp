PNPM ?= corepack pnpm
DOCKER ?= docker
GO ?= go

.PHONY: build typecheck lint test hooks-install \
	node-install node-build node-typecheck node-dev node-image \
	harness-build harness-smoke harness-opencode-image \
	e2e e2e-self e2e-docker e2e-kind

build: node-build harness-build

typecheck: node-typecheck

test: lint typecheck build e2e-self

lint:
	git diff --check

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

harness-smoke:
	scripts/smoke-opencode-acp.sh

harness-opencode-image:
	$(DOCKER) build -f harness/opencode/Dockerfile -t ghcr.io/wyvernzora/n8n-acp/harness-opencode:dev .

e2e: e2e-self

e2e-self:
	node e2e/self-check.js

e2e-docker: node-build
	e2e/run-docker.sh

e2e-kind:
	e2e/run-kind.sh
