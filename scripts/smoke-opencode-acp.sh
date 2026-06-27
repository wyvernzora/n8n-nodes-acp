#!/bin/sh
set -eu

host="${1:-127.0.0.1}"
port="${2:-8080}"

nc -vz -w 5 "$host" "$port"
