# n8n ACP Agent Node

`n8n-nodes-acp` is an n8n community node that runs workflow items through an
ACP-capable agent harness.

The goal is to keep the workflow contract close to n8n's built-in AI Agent
node while moving model execution into a sidecar harness. That makes room for
agents that need longer tool loops, subscription-backed model sign-in, or
runtime-specific setup that does not fit cleanly into an n8n credential.

Screenshot placeholder: add an ACP Agent node screenshot here.

## What Is Included

- `node/` - the `n8n-nodes-acp` custom node package.
- `node/image/` - an init-container style image that copies the built node into
  `N8N_CUSTOM_EXTENSIONS`.
- `harness/runtime/` - a Go ACP proxy runtime with MCP-over-ACP tool bridging.
- `harness/opencode/` - an OpenCode harness sidecar image.
- `harness/codex/` - a Codex ACP harness sidecar image.
- `e2e/` - protocol, Docker, and kind smoke tests.

## How It Works

The n8n node connects to a harness over ACP, one ACP session per input item. It
sends the prompt, optional connected n8n tool nodes, optional output parser
instructions, and node-selected config values such as model and reasoning
effort when the harness advertises them.

The harness handles model-provider authentication itself. The n8n credential
only stores the ACP endpoint, for example `tcp://127.0.0.1:8080` when the
harness runs as an n8n sidecar.

## Images

The GitHub Actions workflow publishes multi-arch images on pushes to `main`:

- `ghcr.io/wyvernzora/n8n-acp/node:dev`
- `ghcr.io/wyvernzora/n8n-acp/opencode:dev`
- `ghcr.io/wyvernzora/n8n-acp/codex:dev`

Each image also gets a `sha-*` tag for the source revision.

Build locally:

```sh
make node-image
make opencode-image
make codex-image
```

## OpenCode Harness

Use the OpenCode harness when you want a ready sidecar for local testing or an
n8n pod. It starts one long-lived `opencode acp` worker and exposes it on TCP.

See [harness/opencode/README.md](harness/opencode/README.md) for Docker,
Kubernetes, and provider sign-in instructions.

See [harness/codex/README.md](harness/codex/README.md) for the Codex ACP
harness image.

## Development

```sh
make node-install
make typecheck
make build
make lint
```

Useful checks:

```sh
make e2e-self
make e2e-docker
make harness-smoke
```

## Status

This is still early. The package version is `0.0.0`, and the public contract may
change while the node and harness runtime settle.

## License

MIT. See [LICENSE](LICENSE).
