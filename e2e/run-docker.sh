#!/bin/sh
set -eu

n8n_image="${N8N_IMAGE:-n8nio/n8n:2.26.8}"
docker_cmd="${DOCKER:-docker}"
root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

run_output="$("${docker_cmd}" run --rm \
	-v "${root}/node:/opt/n8n/custom/n8n-nodes-acp:ro" \
	-v "${root}/e2e:/repo/e2e:ro" \
	-e N8N_CUSTOM_EXTENSIONS=/opt/n8n/custom \
	-e N8N_DIAGNOSTICS_ENABLED=false \
	-e N8N_ENCRYPTION_KEY=n8n-acp-e2e-fixed-key \
	--entrypoint sh \
	"${n8n_image}" \
	-lc '
		node /repo/e2e/harness/fake-acp-harness.js >/tmp/fake-acp.log 2>&1 &
		sleep 1
		n8n import:credentials --input=/repo/e2e/fixtures/acp-credentials.json --include=id,name,type,data >/tmp/import-creds.log
		n8n import:workflow --separate --input=/repo/e2e/fixtures/workflows >/tmp/import-workflows.log
		n8n execute --id=acp-e2e-text --rawOutput > /tmp/text.out
		n8n execute --id=acp-e2e-tool --rawOutput > /tmp/tool.out
		cat /tmp/text.out
		printf "\n--- tool ---\n"
		cat /tmp/tool.out
	')"

case "${run_output}" in
	*'"output": "hello, world!"'*|*'"output":"hello, world!"'*) ;;
	*)
		echo "unexpected text workflow output:" >&2
		echo "${run_output}" >&2
		exit 1
		;;
esac

case "${run_output}" in
	*'"output": "tool:tool-ok:e2e"'*|*'"output":"tool:tool-ok:e2e"'*) ;;
	*)
		echo "unexpected tool workflow output:" >&2
		echo "${run_output}" >&2
		exit 1
		;;
esac

echo "docker e2e passed"
