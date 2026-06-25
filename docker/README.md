# CodeForge lean Alpine container

The image is intended for an MCP coding agent, not for interactive terminal use.
It includes only the common toolchains and utilities that the agent is likely to
invoke directly:

- Go
- Rust, Cargo, rustfmt, and Clippy
- Python, pip, uv, and uvx
- Node.js, npm, and pnpm
- GCC/musl, Clang, CMake, and Ninja
- Git, Git LFS, GitHub CLI, and OpenSSH client
- Docker and Compose clients
- curl, jq, ripgrep, fd, patch, rsync, and archive utilities

It deliberately excludes editors, terminal multiplexers, shell themes, database
clients, Kubernetes tools, Java, HTTPie, and other interactive conveniences.
Project-specific Alpine packages can be added without editing the Dockerfile:

```sh
EXTRA_APK_PACKAGES="protobuf sqlite-dev" docker compose build
```

## Start

```sh
cp .env.docker.example .env
mkdir -p workspace
sed -i "s/^HOST_UID=.*/HOST_UID=$(id -u)/" .env
sed -i "s/^HOST_GID=.*/HOST_GID=$(id -g)/" .env
# Set CONTROL_PLANE_TUNNEL_ID and CONTROL_PLANE_API_KEY in .env.
docker compose build
docker compose up -d
docker compose logs -f codeforge-tunnel
```

Persistent data:

- `/workspace`: host repositories
- `/state`: CodeForge plans and task state
- `/home/dev`: Git/GitHub credentials and language caches
- `/var/lib/tunnel-client`: tunnel state

The Docker CLI is installed without mounting the host Docker socket. Use
`compose.dind.yaml` when the agent must build containers with an isolated nested
daemon:

```sh
docker compose -f compose.yaml -f compose.dind.yaml up -d
```
