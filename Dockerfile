# syntax=docker/dockerfile:1.7

FROM golang:alpine AS codeforge-build
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

FROM ghcr.io/astral-sh/uv AS uv-bin

FROM oven/bun:alpine AS bun-bin

FROM golang:alpine AS runtime

LABEL org.opencontainers.image.title="CodeForge Alpine coding-agent runtime" \
      org.opencontainers.image.description="Lean polyglot coding runtime with CodeForge MCP (streamable HTTP)" \
      org.opencontainers.image.source="https://github.com/divyam234/codeforge-mcp"

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
      zstd

COPY --from=uv-bin /uv /uvx /usr/local/bin/
COPY --from=bun-bin /usr/local/bin/bun /usr/local/bin/bun
COPY --from=codeforge-build /out/codeforge-mcp /usr/local/bin/codeforge-mcp
COPY docker/entrypoint.sh /usr/local/bin/codeforge-entrypoint

RUN set -eux; \
    addgroup -S -g 1000 dev; \
    adduser -S -D -H -u 1000 -G dev -s /bin/bash dev; \
    git lfs install --system; \
    mkdir -p \
      /workspace \
      /state \
      /home/dev/.cache \
      /home/dev/.config \
      /home/dev/.local/bin \
      /home/dev/.local/share \
      /home/dev/go/bin; \
    chown -R dev:dev /workspace /state /home/dev; \
    chmod 0755 /usr/local/bin/codeforge-mcp /usr/local/bin/codeforge-entrypoint; \
    echo 'export PATH="/home/dev/.local/bin:/home/dev/go/bin:/usr/local/go/bin:/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin"' \
      > /etc/profile.d/codeforge.sh

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
    PUID=1000 \
    PGID=1000 \
    UV_CACHE_DIR=/home/dev/.cache/uv \
    UV_LINK_MODE=copy \
    PYTHONUNBUFFERED=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_CACHE_DIR=/home/dev/.cache/pip \
    BUN_INSTALL_CACHE_DIR=/home/dev/.cache/bun \
    GOMODCACHE=/home/dev/go/pkg/mod \
    GOCACHE=/home/dev/.cache/go-build \
    CARGO_HOME=/home/dev/.cargo \
    CARGO_NET_GIT_FETCH_WITH_CLI=true \
    NPM_CONFIG_CACHE=/home/dev/.cache/npm \
    NPM_CONFIG_UPDATE_NOTIFIER=false \
    PNPM_HOME=/home/dev/.local/share/pnpm

WORKDIR /workspace
EXPOSE 9000
VOLUME ["/workspace", "/state", "/home/dev"]

USER root
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/codeforge-entrypoint"]
CMD ["/usr/local/bin/codeforge-mcp"]
