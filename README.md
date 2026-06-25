# CodeForge MCP

CodeForge MCP is a model-neutral coding workspace runtime written in Go. ChatGPT or another MCP client performs the reasoning; CodeForge exposes typed project, persistent work-plan, file, editing, Git, and command tools.

It uses the official `github.com/modelcontextprotocol/go-sdk` and runs as a native **stdio MCP server**. Stdout is reserved for MCP JSON-RPC. Logs go to stderr. There is no HTTP listener or application authentication.

## Recommended workflow

```text
project_list
  ├─ project_select(existing project)
  └─ project_create(new project, template, Git initialization)
                    ↓
 plan_list → plan_get(existing) or plan_create(new)
                    ↓
 Inspect phase → Design phase → Implement phase → Validate phase → Review phase
       task_update(in_progress / blocked / completed + evidence)
       phase_add / task_add when new work is discovered
                    ↓
 workspace_tree / file_find / code_search / file_read
                    ↓
       file_edit / patch_apply / file_write
                    ↓
 command_run → completed inline or process_poll after promotion
                    ↓
       git_diff → final validation → plan_get → plan_update(completed)
```

Use a structured plan for non-trivial work. Plans are stored outside the repository, survive MCP reconnects, and are scoped by project ID. Phase order is enforced: later-phase tasks cannot start until earlier phases are terminal. `process_start` remains reserved for servers, watchers, interactive programs, or known long-running jobs.

## Tool surface

| Area | Tools |
|---|---|
| Projects | `project_list`, `project_create`, `project_select`, `project_info` |
| Plans | `plan_list`, `plan_create`, `plan_get`, `plan_select`, `plan_update`, `phase_add`, `phase_update`, `task_add`, `task_update` |
| Workspace | `workspace_info`, `workspace_tree`, `file_find` |
| Read/search | `file_read`, `code_search` |
| Edit | `file_write`, `file_edit`, `file_move`, `file_delete`, `patch_apply` |
| Git | `git_status`, `git_diff` |
| Commands | `command_run`, `process_start`, `process_poll`, `process_list`, `process_write_stdin`, `process_cancel`, `process_forget` |

Every tool is registered through the Go SDK's generic `mcp.AddTool`. Input and output JSON Schemas are inferred from Go structs, arguments and outputs are validated, and successful calls include MCP `structuredContent` rather than only JSON embedded in text.

### MCP tool annotations

CodeForge advertises explicit annotations for every tool:

| Profile | Tools | `readOnlyHint` | `destructiveHint` | `idempotentHint` | `openWorldHint` |
|---|---|---:|---:|---:|---:|
| Local read | project/plan/workspace/file/search/Git inspection and process polling/listing | `true` | `false` | `true` | `false` |
| Local write | project and plan mutations, file writes/edits/moves, patching, process stdin/forget | `false` | `false` | `false` | `false` |
| Destructive local | `file_delete`, `process_cancel` | `false` | `true` | `false` | `false` |
| Unrestricted command | `command_run`, `process_start` | `false` | `false` | `false` | `true` |

These annotations describe capabilities to the MCP client; they do not restrict command execution inside the container.

## Project management

`CODEFORGE_WORKSPACE_ROOT` is a directory containing projects. If it is itself a repository or contains a recognized manifest, CodeForge also exposes it as project `.` for backward compatibility.

List projects:

```json
{}
```

Create and select a complete Go project:

```json
{
  "name": "Example API",
  "directory": "example-api",
  "template": "go",
  "module": "example.com/example-api"
}
```

Defaults:

- `git_init`: `true`
- `create_readme`: `true`
- `select`: `true`
- `template`: `empty`

Available templates are `empty`, `go`, `rust`, `node`, and `python`. Template creation is transactional: if generation or `git init` fails, the newly created directory is removed.

Project IDs are direct children of the workspace root. File, Git, and command tools always operate on the active project.

## Persistent task and phase tracking

Plans are persisted in `CODEFORGE_STATE_DIR`, not in the project repository. Each plan contains ordered phases and tasks:

```json
{
  "title": "Add OAuth callback handling",
  "objective": "Implement and verify native OAuth callback processing",
  "phases": [
    {
      "key": "inspect",
      "title": "Inspect",
      "tasks": [
        {
          "key": "audit",
          "title": "Trace the current auth flow",
          "priority": "high",
          "acceptance_criteria": ["Relevant handlers, callers, and tests identified"]
        }
      ]
    },
    {
      "key": "implement",
      "title": "Implement",
      "tasks": [
        {
          "key": "callback",
          "title": "Implement callback validation",
          "depends_on": ["audit"],
          "acceptance_criteria": ["Valid callbacks succeed", "Invalid state is rejected"]
        }
      ]
    },
    {
      "key": "validate",
      "title": "Validate",
      "tasks": [
        {
          "key": "tests",
          "title": "Run regression and full tests",
          "depends_on": ["callback"]
        }
      ]
    }
  ]
}
```

Task statuses are `pending`, `in_progress`, `blocked`, `completed`, `skipped`, and `cancelled`. Plan statuses are `planned`, `in_progress`, `blocked`, `completed`, and `cancelled`.

`task_update` records progress notes, concrete blockers, and verification evidence. Completing tasks automatically advances phase and plan state. A blocked task or phase requires a blocker message. Completed work can be reopened by adding a new task or phase. `plan_get` returns progress counts, ready tasks, and blocked tasks so the model can choose the next valid action.

The default state directory is derived from the workspace root under the user's state directory. Set `CODEFORGE_STATE_DIR` explicitly in containers and mount it if plans must survive container replacement.

## Structured command execution

### Normal commands

Use `command_run`:

```json
{
  "argv": ["go", "test", "./..."],
  "cwd": ".",
  "yield_time_ms": 10000,
  "timeout_seconds": 600
}
```

When the command finishes during the foreground window, the result is returned directly:

```json
{
  "mode": "completed",
  "status": "succeeded",
  "output": "...",
  "exit_code": 0,
  "duration_ms": 842
}
```

When it remains active, the same command is automatically retained as a process session:

```json
{
  "mode": "background",
  "status": "running",
  "session_id": "proc_...",
  "output": "partial output",
  "next_cursor": 124
}
```

Continue with `process_poll`, reusing `next_cursor`. This avoids turning `pwd`, `git status`, formatting, and quick tests into unnecessary background sessions while still handling unexpectedly long commands safely.

### Explicit background processes

Use `process_start` for development servers, file watchers, interactive tools, or known long builds. Sessions support incremental output cursors, bounded retained output, stdin, cancellation, timeouts, listing, and cleanup.

## Hash-anchored editing

`file_read` defaults to `hashline` mode and returns a SHA-256 file snapshot plus addressable lines:

```text
18:35c1d9a63065e8a0|func oldName() error {
19:e3b0c44298fc1c14|
20:7e6a6737e16f6f53|    return nil
21:d10b36aa74a59bcf|}
```

Use those anchors with `file_edit`:

```json
{
  "path": "internal/service/service.go",
  "snapshot": "<snapshot from file_read>",
  "edits": [
    {
      "mode": "replace",
      "start_line": 18,
      "start_hash": "35c1d9a63065e8a0",
      "end_line": 21,
      "end_hash": "d10b36aa74a59bcf",
      "replacement": "func newName() error {\n    return nil\n}"
    }
  ]
}
```

Edits fail safely when the snapshot or line hashes are stale, ranges overlap, output becomes invalid UTF-8, or paths escape the project. Writes are atomic and preserve file permissions, line-ending style, and final-newline state.

## Configuration

```bash
export CODEFORGE_WORKSPACE_ROOT="$HOME/src"
export CODEFORGE_ACTIVE_PROJECT="my-project" # optional
export CODEFORGE_STATE_DIR="$HOME/.local/state/codeforge-mcp/my-workspace"
export CODEFORGE_COMMAND_POLICY=unrestricted
export CODEFORGE_FOREGROUND_YIELD_MS=10000
```

Important settings:

```text
CODEFORGE_WORKSPACE_ROOT
CODEFORGE_ACTIVE_PROJECT
CODEFORGE_STATE_DIR
CODEFORGE_COMMAND_POLICY
CODEFORGE_ALLOWED_COMMANDS
CODEFORGE_SHELL
CODEFORGE_ALLOW_DELETE
CODEFORGE_MAX_FILE_BYTES
CODEFORGE_MAX_SEARCH_RESULTS
CODEFORGE_MAX_TREE_ENTRIES
CODEFORGE_MAX_CONCURRENT_PROCESSES
CODEFORGE_MAX_PROCESS_OUTPUT_BYTES
CODEFORGE_PROCESS_TIMEOUT_SECONDS
CODEFORGE_FOREGROUND_YIELD_MS
```

CodeForge does not translate or hardcode any package-registry configuration. Builds and launched commands inherit the ordinary process environment. Configure Go with standard variables such as `GOPROXY`, `GOPRIVATE`, `GONOSUMDB`, and `GOAUTH` outside the application when required.

## Build and test

```bash
make verify
```

Or directly:

```bash
go test ./...
go vet ./...
go build ./cmd/codeforge-mcp
```

## Run as stdio MCP

```bash
export CODEFORGE_WORKSPACE_ROOT="$HOME/src"
./bin/codeforge-mcp
```

Generic client configuration:

```json
{
  "mcpServers": {
    "codeforge": {
      "command": "/absolute/path/to/codeforge-mcp",
      "env": {
        "CODEFORGE_WORKSPACE_ROOT": "/absolute/path/to/projects",
        "CODEFORGE_STATE_DIR": "/absolute/path/to/codeforge-state",
        "CODEFORGE_COMMAND_POLICY": "unrestricted"
      }
    }
  }
}
```

For OpenAI Secure MCP Tunnel, configure the tunnel to launch the binary as its stdio MCP command. The tunnel is the connectivity boundary; CodeForge intentionally contains no application authentication.

## Lean Alpine coding-agent container

The container is designed for ChatGPT-driven coding rather than interactive
terminal use. It bundles CodeForge, OpenAI Secure MCP Tunnel, and a focused
polyglot toolchain:

- Go
- Rust, Cargo, rustfmt, and Clippy
- Python, pip, uv, and uvx
- Node.js, npm, and pnpm
- GCC/musl, Clang, CMake, and Ninja
- Git, Git LFS, GitHub CLI, and OpenSSH client
- Docker and Compose clients
- curl, jq, ripgrep, fd, patch, rsync, and archive utilities

It deliberately omits editors, terminal multiplexers, shell themes, HTTPie,
Java, database clients, Kubernetes utilities, and other interactive extras. Add
repository-specific system dependencies at build time with
`EXTRA_APK_PACKAGES` instead of bloating every image.

The tunnel process starts as root only to supervise the stdio child.
`codeforge-stdio` removes the tunnel API key and launches CodeForge as the
unprivileged `dev` user.

### Start

```bash
cp .env.docker.example .env
mkdir -p workspace
sed -i "s/^HOST_UID=.*/HOST_UID=$(id -u)/" .env
sed -i "s/^HOST_GID=.*/HOST_GID=$(id -g)/" .env
# Set CONTROL_PLANE_TUNNEL_ID and CONTROL_PLANE_API_KEY in .env.
docker compose build
docker compose up -d
docker compose logs -f codeforge-tunnel
```

The local tunnel dashboard is available only on loopback at
`http://127.0.0.1:8080/ui` by default. Once `/readyz` is healthy, connect
ChatGPT to the same tunnel ID.

### Persistent state

| Container path | Compose source | Purpose |
|---|---|---|
| `/workspace` | `${PROJECTS_DIR}` bind mount | Repositories and generated projects |
| `/state` | `codeforge-state` volume | Plans, phases, tasks, and selected project state |
| `/home/dev` | `codeforge-home` volume | Git/GitHub credentials and Go/Cargo/uv/npm caches |
| `/var/lib/tunnel-client` | `codeforge-tunnel-state` volume | Tunnel profiles and operator state |
| `/tmp` | tmpfs | Disposable build and temporary files |

### Add repository-specific packages

Set a space-separated package list in `.env`:

```dotenv
EXTRA_APK_PACKAGES=protobuf sqlite-dev libffi-dev
```

Then rebuild:

```bash
docker compose build --no-cache
```

### Optional isolated Docker daemon

The image includes Docker and Compose clients but does not mount the host Docker
socket. Use the optional Docker-in-Docker override when the agent must build
containers:

```bash
docker compose -f compose.yaml -f compose.dind.yaml up -d
```

The nested daemon persists layers in `codeforge-docker-data` and is not
published to the host. It requires privileged mode, but it avoids granting the
agent direct control over the host Docker daemon.

## Trust boundary

CodeForge validates file paths and confines file tools to the selected project.
The unrestricted shell is not an operating-system sandbox; it can access
anything available to the unprivileged `dev` account inside the container. Do
not mount the host root, Docker socket, SSH private keys, or unrelated
credentials unless that access is intentional.
