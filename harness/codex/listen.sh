#!/bin/sh
set -eu

host="${ACP_HOST:-127.0.0.1}"
port="${ACP_PORT:-8080}"

exec codex-acp-proxy \
  --host "${host}" \
  --port "${port}" \
  --worker-command codex-acp \
  --worker-args ""
