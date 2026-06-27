#!/bin/sh
# Copy the baked-in package into the shared volume n8n scans via N8N_CUSTOM_EXTENSIONS.
set -eu

TARGET="${N8N_ACP_NODES_TARGET:-/opt/n8n/custom}"
DEST="${TARGET}/n8n-nodes-acp"

echo "n8n-acp-node: installing into ${DEST}"
mkdir -p "${DEST}"
cp -r /n8n-nodes-acp/. "${DEST}/"
chmod -R a+rX "${DEST}"
echo "n8n-acp-node: installed"
ls -la "${DEST}"
