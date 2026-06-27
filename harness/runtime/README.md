# ACP Harness Runtime

Generic Go runtime used by concrete harness images.

`cmd/acp-proxy` has two modes:

- default: listen for TCP ACP connections and proxy each one to a harness stdio
  ACP process
- `bridge`: expose a per-session stdio MCP server that tunnels tool calls back
  over the active ACP connection
