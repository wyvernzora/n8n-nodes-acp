# n8n Nodes ACP

n8n community node for running workflow items through ACP-compatible agent harnesses.

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
