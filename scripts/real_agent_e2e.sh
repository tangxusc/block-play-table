#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${GO_BIN:-go}"
MANAGER_ADDR="${MANAGER_ADDR:-127.0.0.1:18080}"
MANAGER_URL="http://${MANAGER_ADDR}"
WORKER_DIR="${WORKER_DIR:-${ROOT}/worker-data-real-e2e}"

command -v codex >/dev/null
command -v claude >/dev/null

mkdir -p "${WORKER_DIR}"

MANAGER_HTTP_ADDR="${MANAGER_ADDR}" "${GO_BIN}" run ./manager/cmd/manager &
MANAGER_PID=$!
trap 'kill ${MANAGER_PID} 2>/dev/null || true; kill ${WORKER_PID:-0} 2>/dev/null || true' EXIT

for _ in {1..40}; do
  if curl -fsS "${MANAGER_URL}/healthz" >/dev/null; then
    break
  fi
  sleep 0.25
done

MANAGER_WS_URL="ws://${MANAGER_ADDR}/worker/ws" WORKER_ID="real-agent-worker" WORKER_WORK_DIR="${WORKER_DIR}" "${GO_BIN}" run ./worker/cmd/worker &
WORKER_PID=$!

echo "Manager and Worker are running for real-agent e2e."
echo "Create two tasks through the UI or GraphQL: one with agentType=codex and one with agentType=claude."
echo "This script verifies CLIs are present and starts the trusted-mode stack; it leaves task creation to Playwright/API flows."
