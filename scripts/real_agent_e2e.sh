#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${GO_BIN:-go}"
MANAGER_ADDR="${MANAGER_ADDR:-127.0.0.1:18081}"
MANAGER_URL="http://${MANAGER_ADDR}"
RUN_DIR="$(mktemp -d "${TMPDIR:-/tmp}/bpt-real-agent-e2e.XXXXXX")"
MANAGER_BINARY="${RUN_DIR}/manager-bin"
WORKER_BINARY="${RUN_DIR}/worker-bin"
WORKER_DIR="${WORKER_DIR:-${RUN_DIR}/worker}"
GIT_FIXTURE="${RUN_DIR}/repository"
WORKER_ID="${WORKER_ID:-real-agent-worker}"
WORKER_TOKEN="${WORKER_TOKEN:-real-agent-e2e-token}"
MANAGER_PID=""
WORKER_PID=""

command -v "${CODEX_BINARY:-codex}" >/dev/null
command -v "${CLAUDE_BINARY:-claude}" >/dev/null
command -v python3 >/dev/null
command -v git >/dev/null

mkdir -p "${WORKER_DIR}" "${GIT_FIXTURE}"
git -C "${GIT_FIXTURE}" init --initial-branch=main >/dev/null
git -C "${GIT_FIXTURE}" config user.name "Block Play Table E2E"
git -C "${GIT_FIXTURE}" config user.email "e2e@block-play-table.invalid"
printf '# Real Agent E2E\n' > "${GIT_FIXTURE}/README.md"
git -C "${GIT_FIXTURE}" add README.md
git -C "${GIT_FIXTURE}" commit -m "Initialize real-agent fixture" >/dev/null
"${GO_BIN}" build -o "${MANAGER_BINARY}" ./manager/cmd/manager
"${GO_BIN}" build -o "${WORKER_BINARY}" ./worker/cmd/worker

cleanup() {
  local pid
  for pid in "${WORKER_PID}" "${MANAGER_PID}"; do
    if [[ -n "${pid}" ]] && [[ "${pid}" -gt 1 ]]; then
      kill "${pid}" 2>/dev/null || true
      wait "${pid}" 2>/dev/null || true
    fi
  done
  rm -rf "${RUN_DIR}"
}
trap cleanup EXIT

DB_DRIVER=memory MANAGER_HTTP_ADDR="${MANAGER_ADDR}" WORKER_TOKEN="${WORKER_TOKEN}" "${MANAGER_BINARY}" &
MANAGER_PID=$!

for _ in {1..40}; do
  if curl -fsS "${MANAGER_URL}/healthz" >/dev/null; then
    break
  fi
  sleep 0.25
done
curl -fsS "${MANAGER_URL}/healthz" >/dev/null

MANAGER_WS_URL="ws://${MANAGER_ADDR}/worker/ws" WORKER_ID="${WORKER_ID}" WORKER_WORK_DIR="${WORKER_DIR}" WORKER_TOKEN="${WORKER_TOKEN}" CODEX_BINARY="${CODEX_BINARY:-codex}" CLAUDE_BINARY="${CLAUDE_BINARY:-claude}" "${WORKER_BINARY}" &
WORKER_PID=$!

MANAGER_URL="${MANAGER_URL}" WORKER_ID="${WORKER_ID}" WORKER_DIR="${WORKER_DIR}" WORKER_TOKEN="${WORKER_TOKEN}" GIT_FIXTURE="${GIT_FIXTURE}" python3 <<'PY'
import json
import os
import sys
import time
import urllib.error
import urllib.request

manager_url = os.environ["MANAGER_URL"]
graphql_url = manager_url + "/graphql"
worker_id = os.environ["WORKER_ID"]
worker_token = os.environ["WORKER_TOKEN"]
git_fixture = os.environ["GIT_FIXTURE"]
codex_model = os.environ.get("REAL_AGENT_CODEX_MODEL", "").strip()
claude_model = os.environ.get("REAL_AGENT_CLAUDE_MODEL", "").strip()


def graphql(query, variables=None):
    body = json.dumps({"query": query, "variables": variables or {}}).encode()
    request = urllib.request.Request(
        graphql_url,
        data=body,
        headers={"content-type": "application/json", "authorization": f"Bearer {worker_token}"},
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


def wait_for_completed(task_id, expected_text, expected_turn=1, previous_session_id=None):
    deadline = time.time() + 600
    last = None
    while time.time() < deadline:
        data = graphql(
            """
            query TaskCompletion($id: ID!) {
              task(id: $id) { id status result agentSessionId worktreePath }
              taskA2AExecutions(taskId: $id) { turn remoteStatus }
            }
            """,
            {"id": task_id},
        )
        task = data["task"]
        rounds = data["taskA2AExecutions"]
        last = {"task": task, "rounds": rounds}
        if task["status"] == "FAILED":
            raise RuntimeError(f"task {task_id} failed: {last}")
        target_round = next((item for item in rounds if item["turn"] == expected_turn), None)
        if (
            task["status"] == "COMPLETED"
            and task.get("agentSessionId")
            and target_round
            and target_round["remoteStatus"] == "COMPLETED"
        ):
            result = task.get("result") or ""
            if expected_text not in result:
                conversations = graphql(
                    "query TaskConversations($taskId: ID!) { taskConversations(taskId: $taskId) { role content } }",
                    {"taskId": task_id},
                )["taskConversations"]
                joined = "\n".join(
                    item["content"] for item in conversations if item["role"] == "assistant"
                )
                if expected_text not in joined:
                    raise RuntimeError(
                        f"task {task_id} completed without expected assistant text {expected_text!r}: result={result!r} conversations={joined!r}"
                    )
            if previous_session_id and task["agentSessionId"] != previous_session_id:
                raise RuntimeError(
                    f"task {task_id} session changed from {previous_session_id} to {task['agentSessionId']}"
                )
            return task
        time.sleep(1)
    raise TimeoutError(f"task {task_id} turn {expected_turn} did not complete, last={last}")


def wait_for_interaction(task_id, agent):
    deadline = time.time() + 600
    last = None
    while time.time() < deadline:
        data = graphql(
            """
            query PendingInteraction($taskId: ID!) {
              task(id: $taskId) { id status agentSessionId }
              taskInteractions(taskId: $taskId, status: PENDING) {
                id kind title agentSessionId
              }
              taskA2AExecutions(taskId: $taskId) {
                executionId turn operation a2aTaskId contextId remoteStatus lastSequence
              }
            }
            """,
            {"taskId": task_id},
        )
        last = data
        task = data["task"]
        if task["status"] == "FAILED":
            raise RuntimeError(f"{agent} task {task_id} failed before interaction: {data}")
        if task["status"] == "COMPLETED":
            artifacts = graphql(
                """
                query CompletedWithoutInteraction($taskId: ID!) {
                  task(id: $taskId) { result }
                  taskLogs(taskId: $taskId) { stream content }
                  taskConversations(taskId: $taskId) { role content }
                }
                """,
                {"taskId": task_id},
            )
            raise RuntimeError(
                f"{agent} task {task_id} completed without requesting interaction: {artifacts}"
            )
        interactions = data["taskInteractions"]
        executions = data["taskA2AExecutions"]
        if task["status"] == "WAITING_INPUT" and len(interactions) == 1 and len(executions) == 1:
            interaction = interactions[0]
            execution = executions[0]
            session_id = interaction.get("agentSessionId") or task.get("agentSessionId")
            if not session_id:
                raise RuntimeError(f"{agent} interaction has no agent session: {data}")
            if interaction["kind"] not in {"COMMAND_APPROVAL", "FILE_APPROVAL", "PERMISSION_APPROVAL"}:
                raise RuntimeError(f"{agent} unexpected interaction kind: {interaction}")
            if execution["turn"] != 1 or execution["operation"] != "START":
                raise RuntimeError(f"{agent} initial A2A round is invalid: {execution}")
            if not execution.get("a2aTaskId") or not execution.get("contextId") or execution["lastSequence"] < 1:
                raise RuntimeError(f"{agent} initial A2A identity is incomplete: {execution}")
            if execution["remoteStatus"] not in {"INPUT_REQUIRED", "AUTH_REQUIRED"}:
                raise RuntimeError(f"{agent} interaction round status is {execution['remoteStatus']!r}")
            return interaction, session_id, execution
        time.sleep(1)
    raise TimeoutError(f"{agent} task {task_id} did not request interaction, last={last}")


def approve_interaction(interaction_id):
    return graphql(
        """
        mutation ApproveInteraction($input: RespondTaskInteractionInput!) {
          respondTaskInteraction(input: $input) { id status responseDecision }
        }
        """,
        {"input": {"interactionId": interaction_id, "decision": "APPROVE"}},
    )["respondTaskInteraction"]


def verify_interaction_and_rounds(task_id, initial, expect_continue):
    data = graphql(
        """
        query VerifyA2A($taskId: ID!) {
          taskInteractions(taskId: $taskId) { id status responseDecision }
          taskA2AExecutions(taskId: $taskId) {
            executionId turn operation a2aTaskId contextId remoteStatus lastSequence
          }
        }
        """,
        {"taskId": task_id},
    )
    answered = data["taskInteractions"]
    if len(answered) != 1 or answered[0]["status"] != "ANSWERED" or answered[0]["responseDecision"] != "APPROVE":
        raise RuntimeError(f"task {task_id} interaction was not approved: {answered}")
    executions = data["taskA2AExecutions"]
    expected_count = 2 if expect_continue else 1
    if len(executions) != expected_count:
        raise RuntimeError(f"task {task_id} A2A round count = {len(executions)}, expected {expected_count}: {executions}")
    first = executions[0]
    if (
        first["executionId"] != initial["executionId"]
        or first["a2aTaskId"] != initial["a2aTaskId"]
        or first["contextId"] != initial["contextId"]
        or first["remoteStatus"] != "COMPLETED"
        or first["lastSequence"] <= initial["lastSequence"]
    ):
        raise RuntimeError(f"task {task_id} initial A2A round changed unexpectedly: before={initial}, after={first}")
    if expect_continue:
        second = executions[1]
        if (
            second["executionId"] != first["executionId"]
            or second["turn"] != 2
            or second["operation"] != "CONTINUE"
            or not second.get("a2aTaskId")
            or second["a2aTaskId"] == first["a2aTaskId"]
            or second["contextId"] != first["contextId"]
            or second["remoteStatus"] != "COMPLETED"
            or second["lastSequence"] <= first["lastSequence"]
        ):
            raise RuntimeError(f"task {task_id} continue A2A round is invalid: {executions}")
    return executions


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
    required = {
        "TaskCreated",
        "TaskStartRequested",
        "TaskInteractionRequested",
        "TaskResumed",
        "TaskCompleted",
        "TaskContinueRequested",
    }
    missing = sorted(required - event_types)
    if missing:
        raise RuntimeError(f"task {task_id} missing events {missing}, got {sorted(event_types)}")


def agent_config(agent):
    if agent == "codex":
        config = {
            "workMode": "IMPLEMENT",
            "codex": {
                "reasoningEffort": "LOW",
                "sandboxMode": "READ_ONLY",
                "approvalPolicy": "ON_REQUEST",
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
        if codex["reasoningEffort"] != "LOW" or codex["sandboxMode"] != "READ_ONLY" or codex["approvalPolicy"] != "ON_REQUEST":
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
    approval_file = f"{agent}-approval-{first}.txt"
    command = f"touch {approval_file}"
    if agent == "codex":
        instructions = (
            f"This is an automated approval-flow verification. You MUST call the shell tool and execute "
            f"exactly this command with sandbox_permissions=require_escalated and a short justification: {command}. "
            f"Do not simulate the command, do not use another file-writing tool, and do not reply before the "
            f"approval response and successful shell exit. Then reply exactly with this token: {first}"
        )
    else:
        instructions = (
            f"Use the shell tool to execute exactly this command: {command}. "
            f"Do not create the file by another method. After the approved command succeeds, "
            f"reply exactly with this token and no other text: {first}"
        )
    task = graphql(
        "mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id status } }",
        {
            "input": {
                "title": f"Real agent {agent}",
                "description": instructions,
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
    interaction, interaction_session_id, initial_execution = wait_for_interaction(task["id"], agent)
    approved = approve_interaction(interaction["id"])
    if approved["status"] != "ANSWERED" or approved["responseDecision"] != "APPROVE":
        raise RuntimeError(f"{agent} interaction approval was not persisted: {approved}")
    completed = wait_for_completed(task["id"], first, previous_session_id=interaction_session_id)
    if not completed.get("worktreePath") or not os.path.isdir(completed["worktreePath"]):
        raise RuntimeError(f"task {task['id']} worktree is missing: {completed}")
    approval_path = os.path.join(completed["worktreePath"], approval_file)
    if not os.path.isfile(approval_path):
        raise RuntimeError(f"{agent} approved command did not create {approval_path}")
    session_id = completed["agentSessionId"]
    worktree_path = completed["worktreePath"]
    verify_interaction_and_rounds(task["id"], initial_execution, expect_continue=False)
    wait_for_idle_worker()
    graphql(
        "mutation ContinueTask($input: ContinueTaskInput!) { continueTask(input: $input) { id } }",
        {"input": {"taskId": task["id"], "message": f"Return exactly this token and no other text: {second}"}},
    )
    continued = wait_for_completed(task["id"], second, expected_turn=2, previous_session_id=session_id)
    if continued.get("worktreePath") != worktree_path:
        raise RuntimeError(
            f"{agent} continue changed worktree from {worktree_path!r} to {continued.get('worktreePath')!r}"
        )
    if not os.path.isdir(worktree_path) or not os.path.isfile(approval_path):
        raise RuntimeError(
            f"{agent} continue did not preserve worktree or approved file: {worktree_path}"
        )
    verify_interaction_and_rounds(task["id"], initial_execution, expect_continue=True)
    verify_artifacts(task["id"], second)
    wait_for_idle_worker()
    print(f"{agent} real-agent e2e passed for task {task['id']} with session {session_id}")


wait_for_worker()
project = graphql(
    "mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }",
    {
        "input": {
            "name": "Real Agent E2E",
            "gitUrl": git_fixture,
            "defaultBranch": "main",
            "worktreeNamePrefix": "real-agent-e2e",
        }
    },
)["createProject"]
run_agent(project["id"], "codex")
run_agent(project["id"], "claude")
PY
