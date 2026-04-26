# Block Play Table

Block Play Table is a trusted-mode task orchestration prototype for AI agent work. It includes:

- Go Manager service with gqlgen GraphQL API, Worker WebSocket gateway, DDD-style domain models, domain events, and in-memory persistence.
- Go Worker service that registers with Manager, sends heartbeat messages, creates task worktrees, runs task pre/post commands, and adapts Codex/Claude non-interactive CLIs.
- Flutter Web UI scaffold for tasks, board, projects, workers, and settings.
- Dockerfiles, Docker Compose, SQL schema, Go e2e tests, and coverage gates.

## Trusted Mode

GraphQL and UI authorization are intentionally disabled in this iteration. Manager and UI must run on a trusted network. Worker WebSocket connections can be protected with `WORKER_TOKEN`; when it is set on Manager, Workers must send the same token. The reserved roles are `Admin`, `Developer`, and `Viewer`, but no runtime permission checks are enforced yet.

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
- `GET /subscriptions`

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
make test-go
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

- Codex: `codex exec`
- Claude: `claude -p`

The helper script checks both CLIs and starts Manager/Worker in trusted mode. After it starts, create and verify one fixed `agentType=codex` task and one fixed `agentType=claude` task through UI, GraphQL, or an automated API flow. See [`docs/e2e-testing.md`](docs/e2e-testing.md) for the full acceptance criteria.

```bash
GO_BIN=/Users/tangxu/sdk/go1.16rc1/bin/go \
GOROOT=/Users/tangxu/sdk/go1.16rc1 \
GOTOOLCHAIN=local \
npm run e2e:real-agents
```

## Current Storage State

The running Manager now supports SQLite and PostgreSQL through the shared repository interface. `manager/migrations/001_init.sql` is embedded and applied at startup. In-memory storage is still available with `DB_DRIVER=memory` for tests and short-lived demos.
