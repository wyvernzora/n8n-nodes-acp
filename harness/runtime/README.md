# ACP Harness Runtime

Generic Go runtime used by concrete harness images.

- `cmd/acp-proxy/` wires process startup and flags.
- `pkg/proxy/` is the import surface for customized harness runtimes.
- `internal/proxy/` holds the non-public ACP/MCP proxy implementation.

The runtime presents ACP to n8n. The worker-facing leg is harness-specific:
OpenCode uses stdio ACP, while a future Codex harness should speak Codex's JRPC
app server instead of assuming Codex is an ACP server.

The proxy has two modes:

- default: listen for TCP ACP connections and multiplex them through one
  long-lived harness stdio ACP process
- `bridge`: expose a per-session stdio MCP server that tunnels tool calls back
  over the owning ACP client connection
