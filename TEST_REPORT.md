# CodeForge MCP v0.6.1 validation report

Date: 2026-06-25

## Result

The complete Go verification pipeline passed after the container cleanup.

```text
make verify
  gofmt check                         passed
  go test -count=1 ./...             passed
  go vet ./...                        passed
  go test -race -count=1 ./...       passed
  coverage run                        passed
  static CGO-disabled build           passed
```

Overall statement coverage: **74.3%**.

The repository contains **55 top-level Go tests** and advertises **32 typed MCP tools** through the official Go SDK.

## Container refactor validation

The Alpine runtime was reduced from the previous kitchen-sink image to **50 explicitly installed runtime packages**. Removed categories include:

- editors and terminal multiplexers
- shell themes and navigation helpers
- HTTPie and duplicate HTTP clients
- Kubernetes tools
- Java/Maven/Gradle
- database clients
- interactive Git frontends
- profilers and debuggers not required by the runtime
- duplicate package managers and language servers

The retained image provides:

- Go
- Rust, Cargo, rustfmt, and Clippy
- Python, pip, uv, and uvx
- Node.js, npm, and pnpm
- GCC/musl, Clang, CMake, and Ninja
- Git, Git LFS, GitHub CLI, and OpenSSH client
- Docker and Compose clients
- curl, jq, ripgrep, fd, patch, rsync, and archive utilities

Repository-specific Alpine dependencies are supplied at build time with `EXTRA_APK_PACKAGES` instead of being installed in every image.

Static checks passed:

- `docker/codeforge-stdio.sh` parses with `sh -n`.
- `docker/start-tunnel.sh` parses with `sh -n`.
- `compose.yaml` parses and contains only the `codeforge-tunnel` service.
- `compose.dind.yaml` parses and contains the tunnel plus optional nested Docker daemon.
- The obsolete interactive shell service and shell-profile/toolchain-report scripts were removed.
- The health check uses the already-installed `curl` rather than installing a second downloader.
- Persistent mounts remain for `/workspace`, `/state`, `/home/dev`, and `/var/lib/tunnel-client`.
- CodeForge still runs as the unprivileged `dev` user.
- Tunnel credentials are removed before launching the MCP child.
- No sandbox-specific Artifactory behavior is present in the product.

## Validation limitation

A complete `docker compose build` was not executed because this sandbox has no Docker or Podman daemon. Dockerfile structure, Compose YAML, shell syntax, Go tests, race tests, and the compiled binary were validated. Alpine package resolution and final image startup still require one real build on a Docker-capable machine.
