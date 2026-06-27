# n8n ACP

n8n community node plus ACP harness sidecar pieces.

## Layout

- `node/` - `n8n-nodes-acp` package.
- `harness/runtime/` - generic ACP proxy and MCP stdio bridge.
- `harness/opencode/` - OpenCode sidecar image.
- `e2e/` - cross-component smoke tests.

## Development

```sh
corepack pnpm install
corepack pnpm typecheck
corepack pnpm build
```

## ACP harness

```sh
corepack pnpm node:docker:build
corepack pnpm harness:docker:build
docker push ghcr.io/wyvernzora/n8n-acp/node:dev
docker push ghcr.io/wyvernzora/n8n-acp/harness-opencode:dev
corepack pnpm harness:smoke
```

See `harness/opencode/` for sidecar details.

## Status

Early skeleton. The public contract is still settling.
