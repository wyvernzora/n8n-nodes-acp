# OpenCode ACP Harness

Sidecar container for exposing OpenCode's stdio ACP server over TCP.

```sh
docker build -f harness/opencode/Dockerfile -t ghcr.io/wyvernzora/n8n-acp/harness-opencode:dev .
docker run --rm -e ACP_HOST=0.0.0.0 -p 127.0.0.1:8080:8080 ghcr.io/wyvernzora/n8n-acp/harness-opencode:dev
```

Defaults:

- listens on `127.0.0.1:8080`
- runs `opencode acp` per TCP connection
- sets OpenCode permissions to `allow`
- disables builtin OpenCode tools in `opencode.jsonc`

For a sidecar, point the n8n node at `127.0.0.1:8080`.
