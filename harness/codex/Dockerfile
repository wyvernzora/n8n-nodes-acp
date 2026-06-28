FROM golang:1.24-bookworm AS runtime-build

WORKDIR /src/harness/runtime
COPY harness/runtime/go.mod ./
COPY harness/runtime/cmd ./cmd
COPY harness/runtime/internal ./internal
COPY harness/runtime/pkg ./pkg
RUN CGO_ENABLED=0 go build -trimpath -o /out/codex-acp-proxy ./cmd/acp-proxy

FROM node:24-bookworm-slim AS codex-build

ARG CODEX_ACP_VERSION=1.0.1
ARG CODEX_VERSION=0.142.3

RUN set -eux; \
	npm install -g "@openai/codex@${CODEX_VERSION}" "@agentclientprotocol/codex-acp@${CODEX_ACP_VERSION}"

FROM debian:bookworm-slim

RUN set -eux; \
	apt-get update; \
	apt-get install -y --no-install-recommends ca-certificates; \
	rm -rf /var/lib/apt/lists/*; \
	useradd --create-home --uid 10001 --shell /usr/sbin/nologin codex; \
	mkdir -p /workspace /home/codex/.codex /tmp/codex; \
	chown -R codex:codex /workspace /home/codex /tmp/codex

COPY --from=codex-build /usr/local/bin/node /usr/local/bin/node
COPY --from=codex-build /usr/local/lib/node_modules /usr/local/lib/node_modules
COPY harness/codex/codex /usr/local/bin/codex
COPY harness/codex/codex-acp /usr/local/bin/codex-acp
COPY harness/codex/listen.sh /usr/local/bin/codex-acp-listen
COPY --from=runtime-build /out/codex-acp-proxy /usr/local/bin/codex-acp-proxy
RUN chmod 0755 /usr/local/bin/codex /usr/local/bin/codex-acp /usr/local/bin/codex-acp-listen /usr/local/bin/codex-acp-proxy

USER 10001:10001
WORKDIR /workspace

ENV HOME=/home/codex
ENV TMPDIR=/tmp/codex
ENV NO_BROWSER=1

EXPOSE 8080

ENTRYPOINT ["codex-acp-listen"]
