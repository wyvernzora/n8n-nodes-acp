# E2E Tests

This directory keeps the ACP side of the test stack deployable without pulling UI automation into the first pass.

## Fast protocol check

```sh
make e2e-self
```

The self-check starts `e2e/harness/fake-acp-harness.js`, connects as an ACP client, advertises one MCP-over-ACP toolset, and verifies that `session/prompt` can round-trip through `mcp/connect`, `tools/list`, `tools/call`, and `mcp/disconnect`.

## n8n pod smoke

For a faster n8n-level check without Kubernetes:

```sh
make e2e-docker
```

This runs the cached or pulled n8n image, mounts this checkout as a custom extension, starts the fake ACP harness on the same loopback interface, imports the fixtures, executes them, and checks the outputs.

For the Kubernetes sidecar shape:

```sh
make e2e-kind
```

The kind runner builds the custom-node init image, deploys one pod with:

- `n8nio/n8n:2.26.8` as the main container by default
- the custom node copied into an `emptyDir`
- the fake ACP harness as a loopback sidecar

The n8n container idles so the test can use `n8n import:*` and `n8n execute` without colliding with a long-running `n8n start` process.

Override images or binaries with environment variables:

```sh
N8N_IMAGE=n8nio/n8n:2.26.8 KIND_CLUSTER=n8n-acp-e2e make e2e-kind
```

By default the runner loads only the locally built custom-node image into kind and lets Kubernetes pull upstream images. Set `E2E_KIND_LOAD_UPSTREAM_IMAGES=1` to preload `N8N_IMAGE` and `FAKE_HARNESS_IMAGE` from the local Docker cache.

The runner imports `e2e/fixtures/acp-credentials.json`, imports the workflow fixtures, executes both workflows with `n8n execute --rawOutput`, and asserts:

- text-only ACP Agent output is `hello, world!`
- attached Code Tool output flows through MCP-over-ACP as `tool:tool-ok:e2e`
