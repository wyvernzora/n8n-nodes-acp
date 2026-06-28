# OpenCode ACP Harness

This image runs OpenCode as an ACP harness for the n8n ACP Agent node. It wraps
`opencode acp` with the Go runtime from `harness/runtime`, exposes ACP over TCP,
and keeps one OpenCode worker warm for the sidecar lifetime.

The image is meant to run either as an n8n sidecar or as a standalone Docker
container during development.

## Image

Published image:

```sh
ghcr.io/wyvernzora/n8n-acp/opencode:dev
```

Build locally:

```sh
docker build -f harness/opencode/Dockerfile \
  -t ghcr.io/wyvernzora/n8n-acp/opencode:dev .
```

## Docker

Run the harness on localhost:

```sh
docker run -d --name opencode-acp \
  -e ACP_HOST=0.0.0.0 \
  -p 127.0.0.1:8080:8080 \
  -v opencode-data:/home/opencode/.local/share \
  -v opencode-state:/home/opencode/.local/state \
  ghcr.io/wyvernzora/n8n-acp/opencode:dev
```

Sign in to OpenAI inside the running container:

```sh
docker exec -it opencode-acp opencode providers login --provider openai
```

Then point the n8n ACP Agent credential at:

```text
tcp://127.0.0.1:8080
```

## Kubernetes Sidecar

Run this image in the same pod as n8n and keep `ACP_HOST` at the default
loopback binding unless another container needs to reach it from outside the
pod.

Example sign-in command:

```sh
kubectl exec -it <n8n-pod> -c <opencode-container> -- \
  opencode providers login --provider openai
```

Mount persistent storage for OpenCode's data and state directories if sign-in
should survive pod replacement:

- `/home/opencode/.local/share`
- `/home/opencode/.local/state`

Set the n8n ACP Agent credential to:

```text
tcp://127.0.0.1:8080
```

## Defaults

- ACP listen address: `127.0.0.1:8080`
- OpenCode working directory: `/workspace`
- Enabled provider: `openai`
- Main model: `openai/gpt-5.1-codex`
- Small model: `openai/gpt-5.1-codex-mini`
- OpenCode permission mode: `allow`
- Built-in OpenCode tools: disabled in `opencode.jsonc`

Configuration lives in [opencode.jsonc](opencode.jsonc). The entrypoint copies
it into OpenCode's config directory on startup.

## Environment

- `ACP_HOST` - TCP listen host, default `127.0.0.1`.
- `ACP_PORT` - TCP listen port, default `8080`.
- `OPENCODE_CWD` - working directory passed to OpenCode, default `/workspace`.

The runtime also honors `ACP_WORKER_COMMAND`, `ACP_WORKER_ARGS`, and
`ACP_PROXY_BRIDGE_COMMAND`; those are mostly useful when building a different
harness image.
