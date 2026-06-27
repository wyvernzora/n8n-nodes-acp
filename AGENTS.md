# AGENTS.md

Drop-in operating instructions for coding agents working on **n8n-nodes-acp**. Read the
user-global rules first if available:

- `~/.agents/AGENTS.md` - universal agent-behavior rules.

This file holds project-specific context, learnings, and overrides only. Global rules
apply unless explicitly contradicted here.

Canonical references:

- [`README.md`](README.md) - terse public project description.
- [`Makefile`](Makefile) - root build/test/image orchestration.
- `node/package.json` - n8n package metadata and entrypoints.
- `node/src/` - live node and credential implementation.
- `harness/runtime/` - generic ACP proxy and MCP bridge scripts.
- `harness/opencode/` - OpenCode sidecar image.

`docs.private/` may contain local planning context. It is gitignored and must not be
treated as public documentation or release contract.

---

## 1. Project context

### About n8n-nodes-acp

- **Name:** n8n-nodes-acp.
- **Domain:** n8n community node package for running workflow items through an
  ACP-compatible agent harnesses.
- **Product shape:** one n8n agent node plus one credential. Keep it small until
  the public contract settles.
- **Compatibility target:** mirror n8n's built-in AI Agent node contract for
  input handling, output handling, and error behavior where practical. The
  configuration can differ; the workflow contract should feel drop-in. Current
  AI Agent behavior to mirror: auto prompt reads `$json.chatInput`, defined
  prompt reads `text`, plain output is `$json.output`, and `continueOnFail`
  yields per-item `{ error }`.
- **Preferred architecture:** the n8n node acts as the ACP client. Do not add a
  separate runner service unless it has a concrete job such as durable background
  sessions, auth isolation, sandboxing, or shared profile management.
- **Harnesses:** OpenCode ACP is a likely first target, but do not hard-code
  OpenCode into the node API.

### Stack

- **Language:** TypeScript on Node 20.15+.
- **Package manager:** pnpm via Corepack for `node/` only.
- **n8n package shape:** community node package in `node/` with
  `dist/credentials/...` and `dist/nodes/...` entrypoints declared in
  `node/package.json`.
- **Runtime dependency:** `n8n-workflow` is a peer dependency. Do not add n8n
  itself as a bundled dependency.

### Package map

- `node/src/credentials/AcpAgentApi.credentials.ts` - ACP endpoint credential.
- `node/src/nodes/AcpAgent/AcpAgent.node.ts` - n8n node implementation.

### Commands

```sh
make node-install
make hooks-install
make lint
make typecheck
make build
```

Run `make typecheck` before handing back code changes. Run `make build` when
package output, tsconfig, or n8n entrypoints change. Run `make hooks-install`
once per checkout to enable lefthook.

---

## 2. Invariants

- Keep the first useful version boring: one node, one credential, fresh ACP
  session per input item by default.
- Process input items sequentially in v1. Add concurrency only after a real
  workflow needs it.
- Use a hard timeout as the only required v1 runtime limit.
- Prefer n8n's native connection types for workflow ergonomics:
  `main`, and `ai_outputParser` when it works. Hide `ai_tool` until there is a
  working bridge to the ACP harness.
- Do not replace ACP with an OpenAI-compatible shim.
- Do not expose ACP or auth-bearing endpoints publicly from this package.
- Keep harness-specific setup in the harness container/process; node
  configuration should stay generic ACP transport/session configuration.
- Choose the first integration target by deployability: an ACP-enabled harness
  that can run as a container beside n8n in Kubernetes.
- Do not build dynamic tool bridging before static/session-level ACP tool
  configuration proves insufficient.
- Do not duplicate large structured-output schemas in prompts when the connected
  n8n output parser can own the schema.
- Prefer handing ACP assistant text to n8n's connected output parser over
  implementing JSON parsing/repair in this node.
- Keep dependencies intentional and minimal. Use platform/n8n/stdlib behavior
  before adding a package.
- Keep generated output out of git: `node/dist/`, `node_modules/`, coverage, local
  docs, and machine notes are ignored.
- Commit messages must not mention Claude or Claude Code. `lefthook.yml`
  enforces this through `scripts/block-claude-commit-msg.sh`.

---

## 3. Project Learnings

**Accumulated corrections. This section is for the agent to maintain.** When the
user corrects your approach, append a one-line, concrete rule here before ending the
session. If an existing line already covers the correction, tighten it instead.

- Treat ACP as the intended client protocol: prefer implementing the n8n node as
  an ACP client directly; add a runner only when it has a concrete lifecycle,
  auth, sandboxing, or shared-management responsibility.
- Before implementing node behavior, inspect the built-in n8n AI Agent node and
  mirror its input, output-parser, tool-input, and `continueOnFail` contracts
  where practical.
