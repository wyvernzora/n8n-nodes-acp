#!/bin/sh
set -eu

cluster="${KIND_CLUSTER:-n8n-acp-e2e}"
namespace="${E2E_NAMESPACE:-n8n-acp-e2e}"
n8n_image="${N8N_IMAGE:-n8nio/n8n:2.26.8}"
fake_harness_image="${FAKE_HARNESS_IMAGE:-${n8n_image}}"

kubectl_cmd="${KUBECTL:-kubectl}"
kind_cmd="${KIND:-kind}"
docker_cmd="${DOCKER:-docker}"

if ! "${kind_cmd}" get clusters | grep -qx "${cluster}"; then
	"${kind_cmd}" create cluster --name "${cluster}"
fi

"${docker_cmd}" build -f node/image/Dockerfile -t n8n-acp-node:e2e .
"${kind_cmd}" load docker-image --name "${cluster}" n8n-acp-node:e2e
if [ "${E2E_KIND_LOAD_UPSTREAM_IMAGES:-0}" = "1" ]; then
	"${kind_cmd}" load docker-image --name "${cluster}" "${n8n_image}"
	if [ "${fake_harness_image}" != "${n8n_image}" ]; then
		"${kind_cmd}" load docker-image --name "${cluster}" "${fake_harness_image}"
	fi
fi

"${kubectl_cmd}" apply -f e2e/k8s/n8n-acp-e2e.yaml
"${kubectl_cmd}" -n "${namespace}" create configmap fake-acp-harness \
	--from-file=fake-acp-harness.js=e2e/harness/fake-acp-harness.js \
	--dry-run=client -o yaml | "${kubectl_cmd}" apply -f -
"${kubectl_cmd}" -n "${namespace}" set image deployment/n8n-acp-e2e n8n="${n8n_image}"
"${kubectl_cmd}" -n "${namespace}" set image deployment/n8n-acp-e2e fake-acp-harness="${fake_harness_image}"
"${kubectl_cmd}" -n "${namespace}" rollout restart deployment/n8n-acp-e2e
"${kubectl_cmd}" -n "${namespace}" rollout status deployment/n8n-acp-e2e --timeout=180s

"${kubectl_cmd}" -n "${namespace}" wait --for=condition=Ready pod -l app=n8n-acp-e2e --timeout=180s
pod="$("${kubectl_cmd}" -n "${namespace}" get pod -l app=n8n-acp-e2e --field-selector=status.phase=Running --sort-by=.metadata.creationTimestamp -o name | tail -n 1 | sed 's#^pod/##')"
"${kubectl_cmd}" -n "${namespace}" exec "${pod}" -c n8n -- test -f /opt/n8n/custom/n8n-nodes-acp/package.json

"${kubectl_cmd}" -n "${namespace}" cp e2e/fixtures/acp-credentials.json "${pod}:/tmp/acp-credentials.json" -c n8n
"${kubectl_cmd}" -n "${namespace}" cp e2e/fixtures/workflows "${pod}:/tmp/acp-workflows" -c n8n
"${kubectl_cmd}" -n "${namespace}" exec "${pod}" -c n8n -- n8n import:credentials --input=/tmp/acp-credentials.json --include=id,name,type,data
"${kubectl_cmd}" -n "${namespace}" exec "${pod}" -c n8n -- n8n import:workflow --separate --input=/tmp/acp-workflows

text_output="$("${kubectl_cmd}" -n "${namespace}" exec "${pod}" -c n8n -- n8n execute --id=acp-e2e-text --rawOutput)"
case "${text_output}" in
	*'"output": "hello, world!"'*|*'"output":"hello, world!"'*) ;;
	*)
		echo "unexpected text workflow output:" >&2
		echo "${text_output}" >&2
		exit 1
		;;
esac

tool_output="$("${kubectl_cmd}" -n "${namespace}" exec "${pod}" -c n8n -- n8n execute --id=acp-e2e-tool --rawOutput)"
case "${tool_output}" in
	*'"output": "tool:tool-ok:e2e"'*|*'"output":"tool:tool-ok:e2e"'*) ;;
	*)
		echo "unexpected tool workflow output:" >&2
		echo "${tool_output}" >&2
		exit 1
		;;
esac

cat <<EOF
n8n ACP e2e passed.

Cluster: ${cluster}
Namespace: ${namespace}
Pod: ${pod}
EOF
