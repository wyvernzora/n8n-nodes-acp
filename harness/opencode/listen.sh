#!/bin/sh
set -eu

host="${ACP_HOST:-127.0.0.1}"
port="${ACP_PORT:-8080}"
mkdir -p "${XDG_CONFIG_HOME}/opencode"
cp /etc/opencode/opencode.jsonc "${XDG_CONFIG_HOME}/opencode/opencode.jsonc"
export ACP_HOST="${host}"
export ACP_PORT="${port}"
export OPENCODE_CWD="${OPENCODE_CWD:-/workspace}"
exec opencode-acp-proxy
