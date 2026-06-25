# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26
ARG ALPINE_VERSION=3.22
ARG UV_VERSION=0.11.24
ARG TUNNEL_CLIENT_VERSION=v0.0.9--context-conduit-topaz

FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS codeforge-build
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go test ./... && \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' \
      -o /out/codeforge-mcp ./cmd/codeforge-mcp

FROM ghcr.io/astral-sh/uv:${UV_VERSION} AS uv-bin

FROM alpine:${ALPINE_VERSION} AS tunnel-bin
ARG TARGETARCH
ARG TUNNEL_CLIENT_VERSION
RUN apk add --no-cache ca-certificates curl unzip
RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64|arm64) ;; \
      *) echo "unsupported tunnel-client architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    asset="tunnel-client-${TUNNEL_CLIENT_VERSION}-linux-${TARGETARCH}.zip"; \
    base="https://persistent.oaistatic.com/tunnel-client/${TUNNEL_CLIENT_VERSION}"; \
    curl -fL --retry 5 --retry-all-errors -o "/tmp/${asset}" "${base}/${asset}"; \
    curl -fL --retry 5 --retry-all-errors -o /tmp/SHA256SUMS.txt "${base}/SHA256SUMS.txt"; \
    grep " ${asset}$" /tmp/SHA256SUMS.txt | (cd /tmp && sha256sum -c -); \
    mkdir -p /tmp/tunnel; \
    unzip -q "/tmp/${asset}" -d /tmp/tunnel; \
    binary="$(find /tmp/tunnel -type f -name tunnel-client | head -n 1)"; \
    test -n "${binary}"; \
    install -m 0755 "${binary}" /out-tunnel-client

FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS runtime
ARG DEV_UID=1000
ARG DEV_GID=1000
ARG EXTRA_APK_PACKAGES=""

LABEL org.opencontainers.image.title="CodeForge Alpine coding-agent runtime" \
      org.opencontainers.image.description="Lean polyglot coding runtime with CodeForge MCP and OpenAI Secure MCP Tunnel" \
      org.opencontainers.image.source="https://github.com/killercrock/codeforge-mcp"

# Keep the base focused on tools a coding agent commonly invokes. Add project-
# specific system dependencies at image build time with EXTRA_APK_PACKAGES.
RUN set -eux; \
    apk add --no-cache \
      bash \
      build-base \
      ca-certificates \
      cargo \
      clang \
      cmake \
      coreutils \
      curl \
      diffutils \
      docker-cli \
      docker-cli-compose \
      fd \
      file \
      findutils \
      gawk \
      gcompat \
      git \
      git-lfs \
      github-cli \
      grep \
      jq \
      linux-headers \
      ninja \
      nodejs \
      npm \
      openssh-client \
      openssl \
      openssl-dev \
      patch \
      pkgconf \
      pnpm \
      procps \
      py3-pip \
      python3 \
      python3-dev \
      ripgrep \
      rsync \
      rust \
      rust-clippy \
      rustfmt \
      sed \
      su-exec \
      tar \
      tini \
      tzdata \
      unzip \
      xz \
      zip \
      zlib-dev \
      zstd; \
    if [ -n "${EXTRA_APK_PACKAGES}" ]; then \
      # shellcheck disable=SC2086
      apk add --no-cache ${EXTRA_APK_PACKAGES}; \
    fi

COPY --from=uv-bin /uv /uvx /usr/local/bin/
COPY --from=tunnel-bin /out-tunnel-client /usr/local/bin/tunnel-client
COPY --from=codeforge-build /out/codeforge-mcp /usr/local/bin/codeforge-mcp
COPY docker/codeforge-stdio.sh /usr/local/bin/codeforge-stdio
COPY docker/start-tunnel.sh /usr/local/bin/start-codeforge-tunnel

RUN set -eux; \
    addgroup -g "${DEV_GID}" dev; \
    adduser -D -u "${DEV_UID}" -G dev -s /bin/bash dev; \
    git lfs install --system; \
    mkdir -p \
      /workspace \
      /state \
      /var/lib/tunnel-client \
      /run/tunnel-client \
      /home/dev/.cache \
      /home/dev/.config \
      /home/dev/.local/bin \
      /home/dev/.local/share \
      /home/dev/go/bin; \
    chown -R dev:dev /workspace /state /home/dev; \
    chmod 0755 \
      /usr/local/bin/codeforge-mcp \
      /usr/local/bin/codeforge-stdio \
      /usr/local/bin/start-codeforge-tunnel \
      /usr/local/bin/tunnel-client

ENV HOME=/home/dev \
    XDG_CACHE_HOME=/home/dev/.cache \
    XDG_CONFIG_HOME=/home/dev/.config \
    XDG_DATA_HOME=/home/dev/.local/share \
    XDG_STATE_HOME=/home/dev/.local/state \
    USER=dev \
    LOGNAME=dev \
    SHELL=/bin/bash \
    PATH=/home/dev/.local/bin:/home/dev/go/bin:/usr/local/go/bin:/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin \
    PAGER=cat \
    GIT_PAGER=cat \
    GH_PAGER=cat \
    GIT_TERMINAL_PROMPT=0 \
    CODEFORGE_WORKSPACE_ROOT=/workspace \
    CODEFORGE_STATE_DIR=/state \
    CODEFORGE_COMMAND_POLICY=unrestricted \
    CODEFORGE_SHELL=/bin/bash \
    CODEFORGE_ALLOW_DELETE=true \
    CODEFORGE_FOREGROUND_YIELD_MS=10000 \
    UV_CACHE_DIR=/home/dev/.cache/uv \
    UV_LINK_MODE=copy \
    PYTHONUNBUFFERED=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_CACHE_DIR=/home/dev/.cache/pip \
    GOMODCACHE=/home/dev/go/pkg/mod \
    GOCACHE=/home/dev/.cache/go-build \
    CARGO_HOME=/home/dev/.cargo \
    CARGO_NET_GIT_FETCH_WITH_CLI=true \
    NPM_CONFIG_CACHE=/home/dev/.cache/npm \
    NPM_CONFIG_UPDATE_NOTIFIER=false \
    PNPM_HOME=/home/dev/.local/share/pnpm \
    TUNNEL_CLIENT_PROFILE_DIR=/var/lib/tunnel-client/profiles \
    TUNNEL_CLIENT_CONFIG_DIR=/var/lib/tunnel-client/config

WORKDIR /workspace
EXPOSE 8080
VOLUME ["/workspace", "/state", "/home/dev", "/var/lib/tunnel-client"]

# tunnel-client supervises the stdio child. codeforge-stdio removes tunnel
# credentials and drops the MCP process to the unprivileged dev account.
USER root
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["/usr/local/bin/start-codeforge-tunnel"]
