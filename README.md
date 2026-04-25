# Block Play Table

Block Play Table is a trusted-mode task orchestration prototype for AI agent work. It includes:

- Go Manager service with GraphQL-compatible HTTP API, Worker WebSocket gateway, DDD-style domain models, domain events, and in-memory persistence.
- Go Worker service that registers with Manager, sends heartbeat messages, creates task worktrees, runs setup/pre/post commands, and adapts Codex/Claude non-interactive CLIs.
- Flutter Web UI scaffold for tasks, board, projects, workers, and settings.
- Dockerfiles, Docker Compose, SQL schema, Go e2e tests, and coverage gates.

## Trusted Mode

Authentication and authorization are intentionally disabled in this iteration. Manager, UI, and Workers must run on a trusted network. The reserved roles are `Admin`, `Developer`, and `Viewer`, but no runtime permission checks are enforced yet.

See `docs/security-trusted-mode.md`.

## Local Development

The local shell on this machine has multiple Go installations. If you hit a Go toolchain mismatch, run commands with a consistent `GOROOT`:

```bash
GOROOT=/Users/tangxu/sdk/go1.16rc1 GOTOOLCHAIN=local /Users/tangxu/sdk/go1.16rc1/bin/go test ./...
```

Standard commands:

```bash
make test-go GO=/Users/tangxu/sdk/go1.16rc1/bin/go GO_TEST_ENV='GOROOT=/Users/tangxu/sdk/go1.16rc1 GOTOOLCHAIN=local'
make coverage GO=/Users/tangxu/sdk/go1.16rc1/bin/go GO_TEST_ENV='GOROOT=/Users/tangxu/sdk/go1.16rc1 GOTOOLCHAIN=local'
make build GO=/Users/tangxu/sdk/go1.16rc1/bin/go GO_TEST_ENV='GOROOT=/Users/tangxu/sdk/go1.16rc1 GOTOOLCHAIN=local'
```

## Run Manager And Worker

```bash
go run ./manager/cmd/manager
```

In another shell:

```bash
MANAGER_WS_URL=ws://localhost:8080/worker/ws \
WORKER_ID=worker-local \
WORKER_WORK_DIR=./worker-data \
go run ./worker/cmd/worker
```

Manager endpoints:

- `GET /healthz`
- `GET /readyz`
- `POST /graphql`
- `GET /worker/ws`
- `GET /subscriptions`

## Docker Compose

```bash
docker compose up --build manager worker ui
```

Then open:

- UI: `http://localhost:3000`
- Manager: `http://localhost:8080`

PostgreSQL is included as an optional profile for team-deployment wiring:

```bash
docker compose --profile postgres up --build
```

## Tests

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

The Worker expects:

- Codex: `codex exec`
- Claude: `claude -p`

The helper script checks both CLIs and starts Manager/Worker in trusted mode:

```bash
GO_BIN=/Users/tangxu/sdk/go1.16rc1/bin/go \
GOROOT=/Users/tangxu/sdk/go1.16rc1 \
GOTOOLCHAIN=local \
npm run e2e:real-agents
```

## Current Storage State

The running Manager currently uses an in-memory repository. `manager/migrations/001_init.sql` defines the SQLite/PostgreSQL-compatible schema for the next persistence adapter.
