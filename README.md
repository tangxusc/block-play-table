# Block Play Table

Block Play Table is a trusted-mode task orchestration prototype for AI agent work. It includes:

- Go Manager service with gqlgen GraphQL API, Worker WebSocket gateway, DDD-style domain models, domain events, and in-memory persistence.
- Go Worker service that registers with Manager, sends heartbeat messages, creates task worktrees, runs task pre/post commands, and adapts Codex/Claude non-interactive CLIs.
- Vue 3 + Ant Design Vue UI for tasks, Kanban/list/calendar/archived board views, projects, workers, and settings.
- Dockerfiles, SQL schema, Go e2e tests, browser e2e tests, and coverage gates.

## Trusted Mode

GraphQL and UI authorization are intentionally disabled in this iteration. Manager and UI must run on a trusted network. Worker WebSocket and FRP tunnel connections can be protected with `WORKER_TOKEN`; when it is set on Manager, Workers must send the same token. The `/proxy/**` endpoint can reach HTTP services on Worker-local or Worker-network `host:port` targets, the task terminal endpoint opens a real shell in the task worktree on the Worker, and the task Review API can read diffs and stage, unstage, discard, or restore changes inside the task worktree, so expose Manager only to trusted callers. The reserved roles are `Admin`, `Developer`, and `Viewer`, but no runtime permission checks are enforced yet.

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
- `GET /terminal/workers/{workerID}`
- `GET /terminal/workers/{workerID}/ws`
- `GET /terminal/tasks/{taskID}`
- `GET /terminal/tasks/{taskID}/ws`
- `/proxy/**`
- `GET /subscriptions`

`/proxy/**` routes through the Worker FRP tunnel. Header-based requests must include `worker: <worker name>` and `worker_port: <worker port>` headers, and can optionally include `worker_host: <host>` to reach a host visible from the Worker network. If `worker_host` is omitted, Manager targets `127.0.0.1`. Manager strips the `/proxy` prefix and forwards the remaining path to `http://<worker_host>:<worker_port>` through that Worker; `worker`, `worker_host`, and `worker_port` are routing headers and are not forwarded to the target service. Worker names are unique.

Browser clients that cannot set custom headers can use `/proxy/web/<worker name>/<host>/<port>/**`. The task detail UI uses that route for its Web preview panel.

Task detail groups `Copy task ID`, lifecycle actions, archive/delete, and close controls in the dialog title as icon-only buttons. Each control exposes a tooltip on hover; the right-side floating command rail is reserved for switching between detail panels.

Task detail also provides a Worker terminal panel. The UI first calls `GET /terminal/tasks/{taskID}` to validate that the assigned Worker is online, the FRP tunnel is connected, and the recorded `worktreePath` still exists under the Worker `WorkDir`; then it connects to `/terminal/tasks/{taskID}/ws`. Manager proxies the WebSocket through the Worker FRP tunnel to the Worker's local terminal service. The shell starts in the task worktree only; tasks without `worktreePath`, archived tasks, or missing local worktree directories do not fall back to the Worker root directory. Worker terminal support is enabled by default and can be disabled with `WORKER_TERMINAL_ENABLED=false`; `WORKER_TERMINAL_HOST` defaults to `127.0.0.1`, and `WORKER_TERMINAL_SHELL` can override the shell binary on Unix Workers.

The Workers list also exposes a terminal button for each Worker. It opens a dialog terminal that starts in the Worker `WorkDir`, first calling `GET /terminal/workers/{workerID}` and then connecting to `/terminal/workers/{workerID}/ws` through the same FRP tunnel and local terminal service.

Task detail also includes a Review tab. Worker starts a local `127.0.0.1` Review HTTP service and advertises `review_enabled=true`, `review_host`, and `review_port` capabilities. Manager exposes typed GraphQL review queries/mutations and proxies each diff or Git change request to that Worker-local Review service through the existing FRP/yamux tunnel. Manager persists turn snapshots and backup metadata; full diff content is fetched live from the Worker. `discard` always creates a backup patch before modifying the worktree, and `restore` applies that backup patch back through the Worker. The Review tab exposes a guarded `Git workspace` panel with editable remote and target branch inputs, current branch/HEAD/target ref, ahead/behind, and dirty-worktree status. Git actions are grouped into `Sync` (`Fetch`, `Rebase onto target`, `Merge target into task branch`), `Changes` (`Stage`, `Unstage`, `Commit staged`, `Discard`, `Restore`), and `Publish` (`Push branch`, `Publish fast-forward`, `Publish merge commit`). Publish never force-pushes and requires a clean worktree.

## Agent CLI Run Configuration

Tasks can be created without Agent CLI parameters. Parameters are written only when assigning a Worker, including the create-task path that selects a Worker immediately. Assigned-but-not-started tasks can be assigned again to update the same task-level `agentConfig`.

Archived tasks are isolated from the active Kanban, List, and Calendar board views. The Board `Archived` view lists only archived tasks and is the only UI surface that exposes permanent task deletion.

Supported fields are typed by Agent instead of free-form JSON:

- Common `workMode`: `plan`, `implement`, or `review`; Worker turns this into a stable prompt prefix.
- Codex: `model`, `reasoningEffort`, `sandboxMode`, `approvalPolicy`, `fullAuto`, and `bypassApprovalsAndSandbox`.
- Claude: `model`, `effort`, and `permissionMode`.

Default empty config keeps the existing CLI behavior for Claude, which runs `claude -p --output-format=stream-json --verbose ...` and maps configured values to `--model`, `--effort`, and `--permission-mode`. Codex tasks run through `codex app-server --listen stdio://`; configured values are passed into the app-server turn as model, reasoning, sandbox, approval, and bypass options so live command/file/permission approvals can use the task interaction channel.

## Local UI And Full Stack

Install Node dependencies, then start the local trusted-mode stack:

```bash
npm install
make run-local
```

Then open:

- UI: `http://localhost:3000`
- Manager: `http://localhost:8080`

`make run-local` delegates to `npm run run-local`. It starts Manager, Worker, and the Vue UI in the background, writes logs to `.local-run/`, and uses SQLite at `data/manager.db`. On Windows it prefers WSL for Manager/Worker so terminal e2e can use a real Unix pty, while the UI still runs through local npm.

Stop or clean the local stack:

```bash
make stop-local
make clean-local
```

Manager storage is selected by:

- `DB_DRIVER=sqlite` with `DB_DSN=./data/manager.db`
- `DB_DRIVER=postgres` with `DB_DSN=postgres://manager:password@postgres:5432/block_play_table?sslmode=disable`
- `DB_DRIVER=memory` for temporary tests or demos only

Readiness is exposed at `GET /readyz` and checks the configured store.

## Tests

Full end-to-end test strategy, coverage matrix, release gates, and troubleshooting notes are documented in [`docs/e2e-testing.md`](docs/e2e-testing.md).

```bash
make test
make coverage
make e2e
npm run typecheck
npm run build
```

The Go coverage gate uses cross-package coverage over Manager internals, shared packages, and Worker internals and fails below 80%.

Playwright UI smoke tests are in `e2e/` and expect the local stack to be running:

```bash
npm install
make run-local
npm run e2e
make stop-local
```

## GitHub Actions

The repository defines two workflows:

- `CI` runs on pull requests, pushes to `main`, and manual dispatch. It runs `make test`, `make coverage`, `make build`, `make ui-typecheck`, `make ui-build`, and `make docker-build`. It intentionally does not run Playwright, `make run-local`, or `make stop-local`.
- `Real Agent Release Gate` runs on manual dispatch and `v*` tags. It requires a self-hosted Linux runner labeled `real-agent` with Go, Node.js, npm, bash, curl, python3, Codex CLI, and Claude CLI installed and authenticated. The workflow runs `npm run e2e:real-agents`.

When `CI` passes on `main`, it publishes Docker images to GHCR:

- `ghcr.io/<owner>/<repo>-manager`
- `ghcr.io/<owner>/<repo>-worker`
- `ghcr.io/<owner>/<repo>-ui`

Main images are tagged as `latest`, `main`, and `sha-<short-sha>`. Version tags publish after the real Agent gate passes and use `vX.Y.Z`, `X.Y.Z`, `X.Y`, and `sha-<short-sha>` tags.

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
