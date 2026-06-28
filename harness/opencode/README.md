# OpenCode ACP Harness

Sidecar container for exposing OpenCode's stdio ACP server over TCP, with a
small Go proxy for ACP-transport MCP tools.

```sh
docker build -f harness/opencode/Dockerfile -t ghcr.io/wyvernzora/n8n-acp/harness-opencode:dev .
docker run --rm \
  -e ACP_HOST=0.0.0.0 \
  -p 127.0.0.1:8080:8080 \
  ghcr.io/wyvernzora/n8n-acp/harness-opencode:dev
```

Defaults:

- listens on `127.0.0.1:8080`
- runs `opencode acp` per TCP connection
- enables only the OpenAI provider
- uses `openai/gpt-5.1-codex` by default
- uses `openai/gpt-5.1-codex-mini` for OpenCode's small-model tasks
- expects OpenAI credentials to be configured with `opencode providers login`
- rewrites ACP `mcpServers` into per-connection stdio MCP bridges for OpenCode
- sets OpenCode permissions to `allow`
- disables builtin OpenCode tools in `opencode.jsonc`

For a sidecar, point the n8n node at `127.0.0.1:8080`.

To sign in inside the sidecar:

```sh
kubectl exec -it <n8n-pod> -c <opencode-container> -- opencode providers login --provider openai
```

OpenCode stores provider auth under its data directory. Mount that path if the
login should survive pod replacement.
