<div align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="../../docs/assets/banner-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="../../docs/assets/banner-light.svg">
    <img alt="ACP Harness Runtime" src="../../docs/assets/banner-light.svg" width="720">
  </picture>
  <h1>ACP Harness Runtime</h1>
  <p><b>Bridge stdio ACP harnesses to TCP with long-lived MCP-over-ACP tools.</b></p>
</div>
<hr/>

This is the Go runtime used by harness images that need to expose a stdio ACP
agent over TCP for the n8n ACP Agent node.

It is intentionally small: start one harness worker, accept ACP client
connections, and bridge n8n tool calls without spawning a per-workflow MCP
process.

## Layout

- `cmd/acp-proxy/` - command startup, flags, and bridge compatibility wiring.
- `pkg/proxy/` - import surface for harness binaries.
- `internal/proxy/` - ACP transport, worker multiplexing, and MCP bridge logic.

## Runtime Modes

Default mode:

```sh
acp-proxy
```

The proxy listens for TCP ACP clients and multiplexes them through one
long-lived stdio ACP worker. By default that worker is:

```sh
opencode acp --cwd /workspace
```

Bridge compatibility mode:

```sh
acp-proxy bridge <socket> <acp-id>
```

Bridge mode is retained for runtimes that need a stdio MCP command, but the
default runtime path uses the proxy-owned loopback HTTP MCP listener.

## MCP-over-ACP

n8n tool nodes live inside the n8n process, while the harness worker lives in a
sidecar or separate container. The node sends ACP `mcp/*` requests over the same
ACP client connection that owns the session. The runtime exposes those tools to
the harness through one loopback HTTP MCP listener shared by all sessions.

The runtime rewrites ACP `session/new` tool server definitions from:

```json
{"type":"acp","id":"tools-1","name":"n8n-tools"}
```

to a streamable HTTP MCP server owned by the runtime:

```json
{
  "type": "http",
  "name": "n8n-tools",
  "url": "http://127.0.0.1:<port>/mcp/tools-1",
  "headers": []
}
```

The runtime handles MCP `initialize`, `tools/list`, and `tools/call` requests
from the harness. It forwards the actual tool traffic as `mcp/connect` and
`mcp/message` requests to the owning n8n ACP client. Ownership is tracked by ACP
tool ID, MCP connection ID, and ACP session ID so concurrent workflow executions
can share the same warm harness worker.

When a session has attached tools, the runtime waits briefly for the harness to
list those tools before forwarding the first `session/prompt`. This keeps MCP
startup timing in the runtime instead of leaking harness-specific settle delays
into the n8n node.

## Custom ACP Harnesses

For a harness that already speaks ACP over stdio, include the `acp-proxy`
binary in your image and point it at the harness command:

```sh
acp-proxy \
  --host 127.0.0.1 \
  --port 8080 \
  --worker-command your-harness \
  --worker-args "acp --cwd /workspace"
```

The same settings can be supplied with environment variables:

- `ACP_HOST`
- `ACP_PORT`
- `ACP_MCP_HOST`
- `ACP_MCP_PORT`
- `ACP_WORKER_COMMAND`
- `ACP_WORKER_ARGS`
- `ACP_PROXY_BRIDGE_COMMAND`

For a custom Go entrypoint, import `pkg/proxy`:

```go
package main

import (
	"context"
	"log"

	"github.com/wyvernzora/n8n-acp/harness/runtime/pkg/proxy"
)

func main() {
	if err := proxy.Run(context.Background(), proxy.ConfigFromEnv()); err != nil {
		log.Fatal(err)
	}
}
```

Harnesses that are not ACP-over-stdio need a different worker-facing adapter.
The n8n-facing side can still use the same ACP concepts, but this default
runtime does not speak non-ACP app-server protocols.

## Development

From the repo root:

```sh
make harness-build
make harness-lint
```
