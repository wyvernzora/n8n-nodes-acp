#!/bin/sh
set -eu

host="${ACP_HOST:-127.0.0.1}"
port="${ACP_PORT:-8080}"
mode="${CODEX_AGENT_MODE:-auto-review}"
codex_home="${CODEX_HOME:-${HOME}/.codex}"

mkdir -p "${codex_home}"
if [ ! -f "${codex_home}/config.toml" ]; then
  cp /etc/codex/config.toml "${codex_home}/config.toml"
fi
rm -rf \
  "${codex_home}/logs_2.sqlite" \
  "${codex_home}/logs_2.sqlite-wal" \
  "${codex_home}/logs_2.sqlite-shm" \
  "${codex_home}/sessions" \
  "${codex_home}/.tmp" \
  "${codex_home}/tmp" \
  "${codex_home}/shell_snapshots"
export CODEX_HOME="${codex_home}"

if [ -z "${INITIAL_AGENT_MODE:-}" ]; then
  case "${mode}" in
    auto|auto-review|agent)
      INITIAL_AGENT_MODE=agent
      CODEX_CONFIG="$(
        node -e '
          const config = process.env.CODEX_CONFIG ? JSON.parse(process.env.CODEX_CONFIG) : {};
          config.approvals_reviewer ??= "auto_review";
          process.stdout.write(JSON.stringify(config));
        '
      )"
      export CODEX_CONFIG
      ;;
    full-bypass|full|bypass|agent-full-access)
      INITIAL_AGENT_MODE=agent-full-access
      ;;
    read-only)
      INITIAL_AGENT_MODE=read-only
      ;;
    *)
      echo "unknown CODEX_AGENT_MODE: ${mode}" >&2
      exit 2
      ;;
  esac
  export INITIAL_AGENT_MODE
fi

exec codex-acp-proxy \
  --host "${host}" \
  --port "${port}" \
  --worker-command codex-acp \
  --worker-args ""
