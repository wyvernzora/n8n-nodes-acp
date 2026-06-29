<div align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="../../docs/assets/banner-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="../../docs/assets/banner-light.svg">
    <img alt="Codex ACP Harness" src="../../docs/assets/banner-light.svg" width="720">
  </picture>
  <h1>Codex ACP Harness</h1>
  <p><b>Expose Codex as a TCP ACP sidecar for n8n.</b></p>
</div>
<hr/>

This image runs `@agentclientprotocol/codex-acp` behind the shared ACP harness
runtime. It exposes Codex as a TCP ACP sidecar for the n8n ACP Agent node.

`codex-acp` starts Codex App Server internally and speaks stdio ACP to the
runtime. The runtime keeps one adapter process warm and multiplexes n8n ACP
connections through it.

## Image

Published image:

```sh
ghcr.io/wyvernzora/n8n-acp/codex:dev
```

Build locally:

```sh
docker build -f harness/codex/Dockerfile \
  -t ghcr.io/wyvernzora/n8n-acp/codex:dev .
```

## Docker

Run with mounted Codex state:

```sh
docker run -d --name codex-acp \
  -e ACP_HOST=0.0.0.0 \
  -v codex-home:/home/codex/.codex \
  -p 127.0.0.1:8080:8080 \
  ghcr.io/wyvernzora/n8n-acp/codex:dev
```

Sign in inside the running container with device auth:

```sh
docker exec -it codex-acp codex login --device-auth
```

Or run with an API key:

```sh
docker run -d --name codex-acp \
  -e ACP_HOST=0.0.0.0 \
  -e CODEX_API_KEY \
  -p 127.0.0.1:8080:8080 \
  ghcr.io/wyvernzora/n8n-acp/codex:dev
```

Then point the n8n ACP Agent credential at:

```text
tcp://127.0.0.1:8080
```

## Kubernetes Sidecar

Run this image in the same pod as n8n and point the n8n credential at:

```text
tcp://127.0.0.1:8080
```

For browserless sidecar deployments, the image sets `NO_BROWSER=1` by default.
Use mounted Codex state or API-key environment variables for auth:

- mount Codex state at `/home/codex/.codex`
- size the mounted Codex state above `1Gi`; `5Gi` is a safer floor for sidecar use
- run `codex login --device-auth` inside the container
- set `CODEX_API_KEY`
- set `OPENAI_API_KEY` as the fallback API key

Example device-auth sign-in:

```sh
kubectl exec -it <n8n-pod> -c <codex-container> -- \
  codex login --device-auth
```

## Environment

- `ACP_HOST` - TCP listen host, default `127.0.0.1`.
- `ACP_PORT` - TCP listen port, default `8080`.
- `CODEX_API_KEY` - API key used by `codex-acp`.
- `OPENAI_API_KEY` - fallback API key used by `codex-acp`.
- `CODEX_CONFIG` - JSON object merged into Codex session config.
- `CODEX_HOME` - Codex state directory, default `/home/codex/.codex`.
- `MODEL_PROVIDER` - model provider passed to Codex for new sessions.
- `DEFAULT_AUTH_REQUEST` - ACP auth request JSON used when Codex requires auth.
- `CODEX_AGENT_MODE` - harness mode: `auto-review` by default, or
  `full-bypass` / `read-only`. `auto` is an alias for `auto-review`.
- `INITIAL_AGENT_MODE` - raw adapter mode override: `read-only`, `agent`, or
  `agent-full-access`. When set, this takes precedence over `CODEX_AGENT_MODE`.
- `NO_BROWSER` - hide browser-based ChatGPT auth when set.
- `APP_SERVER_LOGS` - directory for adapter logs.

`CODEX_AGENT_MODE=auto-review` maps to Codex `agent` and injects
`{"approvals_reviewer":"auto_review"}` into `CODEX_CONFIG`, letting Codex use
its automatic permission reviewer instead of asking n8n for decisions.
`CODEX_AGENT_MODE=full-bypass` maps to Codex `agent-full-access`, which never
asks for approval and disables Codex sandboxing.

On startup the image copies a quiet default Codex config into `CODEX_HOME` only
when no `config.toml` exists. It disables transcript persistence, analytics,
feedback, and OpenTelemetry exporters. The entrypoint also removes Codex's
local diagnostic log database files (`logs_2.sqlite*`) before starting Codex;
auth and user config are left alone.

The image installs `@openai/codex` and `@agentclientprotocol/codex-acp` in a
builder stage, then copies only Node, the installed CLIs, their global modules,
and the Go proxy into the final image. npm is not present at runtime. Override
`CODEX_VERSION` or `CODEX_ACP_VERSION` at build time to test another release.
