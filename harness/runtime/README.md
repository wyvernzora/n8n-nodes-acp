# ACP Harness Runtime

This is the Go runtime used by harness images that need to expose a stdio ACP
agent over TCP for the n8n ACP Agent node.

It is intentionally small: start one harness worker, accept ACP client
connections, and bridge n8n tool calls without requiring a per-workflow local
server.

## Layout

- `cmd/acp-proxy/` - command startup, flags, and the `bridge` subcommand.
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

Bridge mode:

```sh
acp-proxy bridge <socket> <acp-id>
```

Bridge mode is launched by the runtime after rewriting ACP `mcpServers` entries
into stdio MCP servers for the harness worker.

## MCP-over-ACP

n8n tool nodes live inside the n8n process, while the harness worker lives in a
sidecar or separate container. Rather than opening a loopback MCP server per
workflow execution, the node sends ACP `mcp/*` requests over the same ACP client
connection.

The runtime rewrites ACP `session/new` tool server definitions from:

```json
{"type":"acp","id":"tools-1","name":"n8n-tools"}
```

to a stdio MCP bridge command that the harness can launch:

```json
{
  "type": "stdio",
  "name": "n8n-tools",
  "command": "acp-proxy",
  "args": ["bridge", "<socket>", "tools-1"],
  "env": []
}
```

The bridge speaks MCP stdio to the harness and sends `mcp/connect`,
`mcp/message`, and `mcp/disconnect` back to the owning n8n ACP client. Ownership
is tracked by ACP tool ID, MCP connection ID, and ACP session ID so concurrent
workflow executions can share the same warm harness worker.

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
