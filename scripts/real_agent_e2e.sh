#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${GO_BIN:-go}"
MANAGER_ADDR="${MANAGER_ADDR:-127.0.0.1:18080}"
MANAGER_URL="http://${MANAGER_ADDR}"
WORKER_DIR="${WORKER_DIR:-${ROOT}/worker-data-real-e2e}"
WORKER_ID="${WORKER_ID:-real-agent-worker}"

command -v codex >/dev/null
command -v claude >/dev/null
command -v python3 >/dev/null

mkdir -p "${WORKER_DIR}"

DB_DRIVER=memory MANAGER_HTTP_ADDR="${MANAGER_ADDR}" "${GO_BIN}" run ./manager/cmd/manager &
MANAGER_PID=$!
trap 'kill ${MANAGER_PID} 2>/dev/null || true; kill ${WORKER_PID:-0} 2>/dev/null || true' EXIT

for _ in {1..40}; do
  if curl -fsS "${MANAGER_URL}/healthz" >/dev/null; then
    break
  fi
  sleep 0.25
done

MANAGER_WS_URL="ws://${MANAGER_ADDR}/worker/ws" WORKER_ID="${WORKER_ID}" WORKER_WORK_DIR="${WORKER_DIR}" WORKER_SUPPORTED_AGENTS="codex,claude" "${GO_BIN}" run ./worker/cmd/worker &
WORKER_PID=$!

MANAGER_URL="${MANAGER_URL}" WORKER_ID="${WORKER_ID}" WORKER_DIR="${WORKER_DIR}" python3 <<'PY'
import json
import os
import sys
import time
import urllib.error
import urllib.request

manager_url = os.environ["MANAGER_URL"]
graphql_url = manager_url + "/graphql"
worker_id = os.environ["WORKER_ID"]
codex_model = os.environ.get("REAL_AGENT_CODEX_MODEL", "").strip()
claude_model = os.environ.get("REAL_AGENT_CLAUDE_MODEL", "").strip()


def graphql(query, variables=None):
    body = json.dumps({"query": query, "variables": variables or {}}).encode()
    request = urllib.request.Request(
        graphql_url,
        data=body,
        headers={"content-type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(request, timeout=30) as response:
        decoded = json.loads(response.read().decode())
    if decoded.get("errors"):
        raise RuntimeError(decoded["errors"])
    return decoded["data"]


def wait_for_worker():
    deadline = time.time() + 30
    while time.time() < deadline:
        try:
            data = graphql(
                "query Worker($id: ID!) { worker(id: $id) { id status supportedAgents currentTaskIds } }",
                {"id": worker_id},
            )
            worker = data.get("worker")
            if worker and worker["status"] == "ONLINE":
                return worker
        except Exception:
            pass
        time.sleep(0.5)
    raise TimeoutError("worker did not come online")


def wait_for_completed(task_id, expected_text, previous_session_id=None):
    deadline = time.time() + 600
    last_task = None
    while time.time() < deadline:
        data = graphql(
            "query Task($id: ID!) { task(id: $id) { id status result agentSessionId worktreePath } }",
            {"id": task_id},
        )
        task = data["task"]
        last_task = task
        if task["status"] == "FAILED":
            raise RuntimeError(f"task {task_id} failed: {task}")
        if task["status"] == "COMPLETED" and task.get("agentSessionId"):
            result = task.get("result") or ""
            if expected_text not in result:
                conversations = graphql(
                    "query TaskConversations($taskId: ID!) { taskConversations(taskId: $taskId) { role content } }",
                    {"taskId": task_id},
                )["taskConversations"]
                joined = "\n".join(item["content"] for item in conversations)
                if expected_text not in joined:
                    raise RuntimeError(
                        f"task {task_id} completed without expected text {expected_text!r}: result={result!r} conversations={joined!r}"
                    )
            if previous_session_id and task["agentSessionId"] != previous_session_id:
                raise RuntimeError(
                    f"task {task_id} session changed from {previous_session_id} to {task['agentSessionId']}"
                )
            return task
        time.sleep(1)
    raise TimeoutError(f"task {task_id} did not complete, last={last_task}")


def wait_for_idle_worker():
    deadline = time.time() + 60
    while time.time() < deadline:
        worker = graphql(
            "query Worker($id: ID!) { worker(id: $id) { id status currentTaskIds } }",
            {"id": worker_id},
        )["worker"]
        if worker["status"] == "ONLINE" and not worker.get("currentTaskIds"):
            return
        time.sleep(0.5)
    raise TimeoutError("worker did not become idle")


def verify_artifacts(task_id, expected_text):
    data = graphql(
        """
        query Verify($taskId: ID!) {
          taskLogs(taskId: $taskId) { stream content }
          taskConversations(taskId: $taskId) { role content }
          taskEvents(taskId: $taskId) { eventType }
        }
        """,
        {"taskId": task_id},
    )
    if not data["taskLogs"]:
        raise RuntimeError(f"task {task_id} has no logs")
    if not data["taskConversations"]:
        raise RuntimeError(f"task {task_id} has no conversations")
    if expected_text not in "\n".join(item["content"] for item in data["taskConversations"]):
        raise RuntimeError(f"task {task_id} conversations do not include {expected_text!r}")
    event_types = {item["eventType"] for item in data["taskEvents"]}
    required = {"TaskCreated", "TaskStartRequested", "TaskCompleted", "TaskContinueRequested"}
    missing = sorted(required - event_types)
    if missing:
        raise RuntimeError(f"task {task_id} missing events {missing}, got {sorted(event_types)}")


def agent_config(agent):
    if agent == "codex":
        config = {
            "workMode": "IMPLEMENT",
            "codex": {
                "reasoningEffort": "LOW",
                "sandboxMode": "WORKSPACE_WRITE",
                "approvalPolicy": "NEVER",
            },
        }
        if codex_model:
            config["codex"]["model"] = codex_model
        return config
    if agent == "claude":
        config = {
            "workMode": "IMPLEMENT",
            "claude": {
                "effort": "LOW",
                "permissionMode": "DEFAULT",
            },
        }
        if claude_model:
            config["claude"]["model"] = claude_model
        return config
    raise ValueError(agent)


def verify_agent_config(task_id, agent):
    data = graphql(
        """
        query TaskConfig($id: ID!) {
          task(id: $id) {
            agentConfig {
              workMode
              codex { model reasoningEffort sandboxMode approvalPolicy }
              claude { model effort permissionMode }
            }
          }
        }
        """,
        {"id": task_id},
    )
    config = data["task"]["agentConfig"]
    if config["workMode"] != "IMPLEMENT":
        raise RuntimeError(f"task {task_id} workMode = {config['workMode']!r}")
    if agent == "codex":
        codex = config["codex"]
        if codex["reasoningEffort"] != "LOW" or codex["sandboxMode"] != "WORKSPACE_WRITE" or codex["approvalPolicy"] != "NEVER":
            raise RuntimeError(f"task {task_id} codex config = {codex}")
        if codex_model and codex.get("model") != codex_model:
            raise RuntimeError(f"task {task_id} codex model = {codex.get('model')!r}")
    if agent == "claude":
        claude = config["claude"]
        if claude["effort"] != "LOW" or claude["permissionMode"] != "DEFAULT":
            raise RuntimeError(f"task {task_id} claude config = {claude}")
        if claude_model and claude.get("model") != claude_model:
            raise RuntimeError(f"task {task_id} claude model = {claude.get('model')!r}")


def run_agent(project_id, agent):
    suffix = f"{agent}-{int(time.time() * 1000)}"
    first = f"BPT_{agent.upper()}_FIRST_{suffix}"
    second = f"BPT_{agent.upper()}_SECOND_{suffix}"
    task = graphql(
        "mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id status } }",
        {
            "input": {
                "title": f"Real agent {agent}",
                "description": f"Return exactly this token and no other text: {first}",
                "projectId": project_id,
                "workerId": worker_id,
                "agentType": agent,
                "agentConfig": agent_config(agent),
                "baseBranch": "main",
            }
        },
    )["createTask"]
    verify_agent_config(task["id"], agent)
    graphql("mutation StartTask($taskId: ID!) { startTask(taskId: $taskId) { id } }", {"taskId": task["id"]})
    completed = wait_for_completed(task["id"], first)
    if not completed.get("worktreePath") or not os.path.isdir(completed["worktreePath"]):
        raise RuntimeError(f"task {task['id']} worktree is missing: {completed}")
    session_id = completed["agentSessionId"]
    wait_for_idle_worker()
    graphql(
        "mutation ContinueTask($input: ContinueTaskInput!) { continueTask(input: $input) { id } }",
        {"input": {"taskId": task["id"], "message": f"Return exactly this token and no other text: {second}"}},
    )
    wait_for_completed(task["id"], second, previous_session_id=session_id)
    verify_artifacts(task["id"], second)
    wait_for_idle_worker()
    print(f"{agent} real-agent e2e passed for task {task['id']} with session {session_id}")


wait_for_worker()
project = graphql(
    "mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }",
    {
        "input": {
            "name": "Real Agent E2E",
            "gitUrl": "real-agent-fixture",
            "defaultBranch": "main",
            "worktreeNamePrefix": "real-agent-e2e",
        }
    },
)["createProject"]
run_agent(project["id"], "codex")
run_agent(project["id"], "claude")
PY
