FROM golang:1.24-bookworm AS runtime-build

WORKDIR /src/harness/runtime
COPY harness/runtime/go.mod ./
COPY harness/runtime/cmd ./cmd
COPY harness/runtime/internal ./internal
COPY harness/runtime/pkg ./pkg
RUN CGO_ENABLED=0 go build -trimpath -o /out/opencode-acp-proxy ./cmd/acp-proxy

FROM debian:bookworm-slim

ARG OPENCODE_VERSION=1.17.9
ARG TARGETARCH

RUN set -eux; \
	apt-get update; \
	apt-get install -y --no-install-recommends ca-certificates curl ripgrep tar; \
	rm -rf /var/lib/apt/lists/*; \
	case "${TARGETARCH}" in \
		amd64) opencode_arch="x64" ;; \
		arm64) opencode_arch="arm64" ;; \
		*) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
	esac; \
	curl -fsSL "https://github.com/anomalyco/opencode/releases/download/v${OPENCODE_VERSION}/opencode-linux-${opencode_arch}.tar.gz" -o /tmp/opencode.tar.gz; \
	tar -xzf /tmp/opencode.tar.gz -C /usr/local/bin opencode; \
	chmod 0755 /usr/local/bin/opencode; \
	rm /tmp/opencode.tar.gz; \
	useradd --create-home --uid 10001 --shell /usr/sbin/nologin opencode; \
	mkdir -p /workspace /home/opencode/.local/share /home/opencode/.local/state /tmp/opencode /etc/opencode; \
	chown -R opencode:opencode /workspace /home/opencode /tmp/opencode

COPY harness/opencode/opencode.jsonc /etc/opencode/opencode.jsonc
COPY harness/opencode/listen.sh /usr/local/bin/opencode-acp-listen
COPY --from=runtime-build /out/opencode-acp-proxy /usr/local/bin/opencode-acp-proxy
RUN chmod 0755 /usr/local/bin/opencode-acp-listen /usr/local/bin/opencode-acp-proxy

USER 10001:10001
WORKDIR /workspace

ENV HOME=/home/opencode
ENV XDG_CONFIG_HOME=/tmp/opencode/config
ENV XDG_DATA_HOME=/home/opencode/.local/share
ENV XDG_STATE_HOME=/home/opencode/.local/state
ENV TMPDIR=/tmp/opencode

EXPOSE 8080

ENTRYPOINT ["opencode-acp-listen"]
