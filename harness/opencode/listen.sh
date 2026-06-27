#!/bin/sh
set -eu

host="${ACP_HOST:-127.0.0.1}"
port="${ACP_PORT:-8080}"
mkdir -p "${XDG_CONFIG_HOME}/opencode"
cp /etc/opencode/opencode.jsonc "${XDG_CONFIG_HOME}/opencode/opencode.jsonc"
# ponytail: raw TCP-to-stdio bridge; replace with a daemon only if ACP needs shared sessions.
exec socat "TCP-LISTEN:${port},bind=${host},reuseaddr,fork" \
	"EXEC:opencode acp --cwd /workspace"
