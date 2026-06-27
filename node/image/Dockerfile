# n8n custom node init-container image.
#
# Builds the package, then copies only package.json and dist/ into a tiny image.
# The entrypoint installs that package into the volume n8n scans via
# N8N_CUSTOM_EXTENSIONS.

FROM node:24-alpine AS build
WORKDIR /src/node
RUN corepack enable
COPY node/package.json node/pnpm-lock.yaml ./
RUN corepack pnpm install --frozen-lockfile
COPY node/tsconfig.json ./
COPY node/src/ ./src/
RUN corepack pnpm build
RUN mkdir -p /pkg && cp -r package.json dist /pkg/

FROM busybox:1.37-musl
LABEL org.opencontainers.image.source="https://github.com/wyvernzora/n8n-nodes-acp"
COPY --from=build /pkg /n8n-nodes-acp
COPY node/image/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENV N8N_ACP_NODES_TARGET=/opt/n8n/custom
ENTRYPOINT ["/entrypoint.sh"]
