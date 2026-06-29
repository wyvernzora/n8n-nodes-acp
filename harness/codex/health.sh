#!/bin/sh
set -eu

path="${CODEX_HEALTH_PATH:-${CODEX_HOME:-${HOME}/.codex}}"
max_used="${CODEX_HEALTH_MAX_USED_PERCENT:-90}"

used="$(
  df -P "${path}" | awk 'NR == 2 {
    gsub(/%/, "", $5)
    print $5
  }'
)"

if [ -z "${used}" ]; then
  echo "could not read disk usage for ${path}" >&2
  exit 1
fi

if [ "${used}" -gt "${max_used}" ]; then
  echo "${path} disk use is ${used}%, max ${max_used}%" >&2
  exit 1
fi
