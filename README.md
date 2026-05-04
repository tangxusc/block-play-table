# Block Play Table

Block Play Table is a trusted-mode task orchestration prototype for AI agent work. It includes:

- Go Manager service with gqlgen GraphQL API, Worker WebSocket gateway, DDD-style domain models, domain events, and in-memory persistence.
- Go Worker service that registers with Manager, sends heartbeat messages, creates task worktrees, runs task pre/post commands, and adapts Codex/Claude non-interactive CLIs.
- Flutter Web UI for tasks, Kanban/list/calendar board views, projects, workers, and settings.
- Dockerfiles, Docker Compose, SQL schema, Go e2e tests, and coverage gates.

## Trusted Mode

GraphQL and UI authorization are intentionally disabled in this iteration. Manager and UI must run on a trusted network. Worker WebSocket and FRP tunnel connections can be protected with `WORKER_TOKEN`; when it is set on Manager, Workers must send the same token. The `/proxy/**` endpoint can reach HTTP services on Worker-local or Worker-network `host:port` targets, and the task terminal endpoint opens a real shell in the task worktree on the Worker, so expose Manager only to trusted callers. The reserved roles are `Admin`, `Developer`, and `Viewer`, but no runtime permission checks are enforced yet.

See `docs/security-trusted-mode.md`.

## Local Development

The local shell on this machine has multiple Go installations. If you hit a Go toolchain mismatch, unset stale `GOROOT` before running commands:

```bash
env -u GOROOT go test ./...
```

Standard commands:

```bash
make test-go GO=go GO_TEST_ENV='env -u GOROOT'
make coverage GO=go GO_TEST_ENV='env -u GOROOT'
make build GO=go GO_TEST_ENV='env -u GOROOT'
```

## Run Manager And Worker

```bash
DB_DRIVER=sqlite DB_DSN=./data/manager.db go run ./manager/cmd/manager
```

In another shell:

```bash
MANAGER_WS_URL=ws://localhost:8080/worker/ws \
WORKER_ID=worker-local \
WORKER_WORK_DIR=./worker-data \
go run ./worker/cmd/worker
```

Workers can connect to multiple Managers at the same time by using comma-separated URLs:

```bash
MANAGER_WS_URLS=ws://manager-a:8080/worker/ws,ws://manager-b:8080/worker/ws \
WORKER_ID=worker-local \
WORKER_NAME=local-worker \
WORKER_WORK_DIR=./worker-data \
go run ./worker/cmd/worker
```

To require Worker authentication, set the same token for Manager and Worker:

```bash
WORKER_TOKEN=dev-worker-token DB_DRIVER=sqlite DB_DSN=./data/manager.db go run ./manager/cmd/manager
WORKER_TOKEN=dev-worker-token MANAGER_WS_URL=ws://localhost:8080/worker/ws go run ./worker/cmd/worker
```

Manager endpoints:

- `GET /healthz`
- `GET /readyz`
- `POST /graphql`
- `GET /worker/ws`
- `GET /worker/frp`
- `GET /terminal/tasks/{taskID}`
- `GET /terminal/tasks/{taskID}/ws`
- `/proxy/**`
- `GET /subscriptions`

`/proxy/**` routes through the Worker FRP tunnel. Header-based requests must include `worker: <worker name>` and `worker_port: <worker port>` headers, and can optionally include `worker_host: <host>` to reach a host visible from the Worker network. If `worker_host` is omitted, Manager targets `127.0.0.1`. Manager strips the `/proxy` prefix and forwards the remaining path to `http://<worker_host>:<worker_port>` through that Worker; `worker`, `worker_host`, and `worker_port` are routing headers and are not forwarded to the target service. Worker names are unique.

Browser clients that cannot set custom headers can use `/proxy/web/<worker name>/<host>/<port>/**`. The task detail UI uses that route for its Web preview panel.

Task detail also provides a Worker terminal panel. The UI first calls `GET /terminal/tasks/{taskID}` to validate that the assigned Worker is online, the FRP tunnel is connected, and the recorded `worktreePath` still exists under the Worker `WorkDir`; then it connects to `/terminal/tasks/{taskID}/ws`. Manager proxies the WebSocket through the Worker FRP tunnel to the Worker's local terminal service. The shell starts in the task worktree only; tasks without `worktreePath`, archived tasks, or missing local worktree directories do not fall back to the Worker root directory. Worker terminal support is enabled by default and can be disabled with `WORKER_TERMINAL_ENABLED=false`; `WORKER_TERMINAL_HOST` defaults to `127.0.0.1`, and `WORKER_TERMINAL_SHELL` can override the shell binary on Unix Workers.

## Agent CLI Run Configuration

Tasks can be created without Agent CLI parameters. Parameters are written only when assigning a Worker, including the create-task path that selects a Worker immediately. Assigned-but-not-started tasks can be assigned again to update the same task-level `agentConfig`.

Supported fields are typed by Agent instead of free-form JSON:

- Common `workMode`: `plan`, `implement`, or `review`; Worker turns this into a stable prompt prefix.
- Codex: `model`, `reasoningEffort`, `sandboxMode`, `approvalPolicy`, `fullAuto`, and `bypassApprovalsAndSandbox`.
- Claude: `model`, `effort`, and `permissionMode`.

Default empty config keeps the existing CLI behavior for Claude, which runs `claude -p --output-format=stream-json --verbose ...` and maps configured values to `--model`, `--effort`, and `--permission-mode`. Codex tasks run through `codex app-server --listen stdio://`; configured values are passed into the app-server turn as model, reasoning, sandbox, approval, and bypass options so live command/file/permission approvals can use the task interaction channel.

## Docker Compose

Local SQLite mode:

```bash
docker compose up --build manager worker ui
```

Then open:

- UI: `http://localhost:3000`
- Manager: `http://localhost:8080`

Team PostgreSQL mode:

```bash
cp .env.example .env
# edit WORKER_TOKEN, POSTGRES_PASSWORD, and WORKER_SSH_DIR
docker compose -f docker-compose.team.yml up --build
```

For local `git@github.com:...` project URLs, the Worker image includes `openssh-client`.
`make run-local` passes `WORKER_SSH_DIR=$(HOME)/.ssh` and Docker Desktop's
`WORKER_SSH_AUTH_SOCK=/run/host-services/ssh-auth.sock` into the Worker container.
The GitHub SSH key must be usable non-interactively by either the mounted SSH
directory or the forwarded ssh-agent.

Manager storage is selected by:

- `DB_DRIVER=sqlite` with `DB_DSN=/data/manager.db`
- `DB_DRIVER=postgres` with `DB_DSN=postgres://manager:password@postgres:5432/block_play_table?sslmode=disable`
- `DB_DRIVER=memory` for temporary tests or demos only

Readiness is exposed at `GET /readyz` and checks the configured store.

## Tests

Full end-to-end test strategy, coverage matrix, release gates, and troubleshooting notes are documented in [`docs/e2e-testing.md`](docs/e2e-testing.md).

```bash
make test
make coverage
make e2e
```

The Go coverage gate uses cross-package coverage over Manager internals, shared packages, and Worker internals and fails below 80%.

Flutter is expected to run through Docker because Flutter is not installed locally:

```bash
make flutter-test
```

Playwright UI smoke tests are in `e2e/`:

```bash
npm install
npm run e2e
```

## Real Agent E2E

Real Agent E2E is a release gate and must cover both Codex and Claude. The Worker expects:

- Codex: `codex app-server --listen stdio://`
- Claude: `claude -p`

Codex tasks use the app-server JSON-RPC protocol so command, file, permission, and user-input requests can be surfaced in the task detail UI. Claude tasks inspect stream-json `permission_denials` and surface the same `TaskInteraction` workflow; after approval the Worker resumes the same Claude session with the derived `--allowedTools` value. The Manager persists pending `TaskInteraction` records, moves the task to `WAITING_INPUT`, and sends the user's approve/deny/answer decision back to the same Worker before the task resumes.

The helper script checks both CLIs and starts Manager/Worker in trusted mode. After it starts, create and verify one fixed `agentType=codex` task and one fixed `agentType=claude` task through UI, GraphQL, or an automated API flow. See [`docs/e2e-testing.md`](docs/e2e-testing.md) for the full acceptance criteria.
By default the script passes non-empty `agentConfig` without model names; set `REAL_AGENT_CODEX_MODEL` or `REAL_AGENT_CLAUDE_MODEL` to verify explicit model CLI arguments in your environment.

```bash
GO_BIN=/Users/tangxu/sdk/go1.16rc1/bin/go \
GOROOT=/Users/tangxu/sdk/go1.16rc1 \
GOTOOLCHAIN=local \
npm run e2e:real-agents
```

## Current Storage State

The running Manager now supports SQLite and PostgreSQL through the shared repository interface. `manager/migrations/001_init.sql` is embedded and applied at startup. In-memory storage is still available with `DB_DRIVER=memory` for tests and short-lived demos.
