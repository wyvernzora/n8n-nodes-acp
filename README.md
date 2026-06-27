# n8n ACP

n8n community node plus ACP harness sidecar pieces.

## Layout

- `node/` - `n8n-nodes-acp` package.
- `harness/runtime/` - generic Go ACP proxy and MCP stdio bridge.
- `harness/opencode/` - OpenCode sidecar image.
- `e2e/` - cross-component smoke tests.

## Development

```sh
make node-install
make typecheck
make build
```

## ACP harness

```sh
make node-image
make harness-opencode-image
docker push ghcr.io/wyvernzora/n8n-acp/node:dev
docker push ghcr.io/wyvernzora/n8n-acp/harness-opencode:dev
make harness-smoke
```

See `harness/opencode/` for sidecar details.

## Status

Early skeleton. The public contract is still settling.
