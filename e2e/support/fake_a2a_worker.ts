import { spawn, type ChildProcess } from "node:child_process";
import { appendFileSync, mkdirSync, readFileSync } from "node:fs";
import path from "node:path";

import type { E2ERuntimeState } from "../global_setup";

const managerGraphQL =
  process.env.BPT_MANAGER_GRAPHQL_URL || "http://localhost:8080/graphql";
const managerWorkerWs = managerGraphQL
  .replace(/^http/, "ws")
  .replace(/\/graphql$/, "/worker/ws");
const managerToken =
  process.env.BPT_MANAGER_TOKEN ||
  process.env.WORKER_TOKEN ||
  "dev-worker-token";

export type FakeA2AWorkerOptions = {
  workerId: string;
  workerName: string;
  taskId: string;
  boundProjectIds?: string[];
  expectCompletion?: boolean;
  expectInteraction?: boolean;
  expectContinuation?: boolean;
};

export type FakeA2AWorker = {
  ready: Promise<void>;
  started: Promise<void>;
  done: Promise<void>;
  requested: Promise<void>;
  completed: Promise<void>;
  firstDone: Promise<void>;
  continued: Promise<void>;
  close: () => void;
};

export function startFakeA2AWorker(
  options: FakeA2AWorkerOptions,
): FakeA2AWorker {
  const state = runtimeState();
  const workerDir = path.join(
    state.runtimeDir,
    "workers",
    options.workerId.replace(/[^A-Za-z0-9_.-]/g, "_"),
  );
  mkdirSync(workerDir, { recursive: true });
  const child = spawn(state.workerBinary, [], {
    cwd: workerDir,
    env: {
      ...process.env,
      MANAGER_WS_URL: managerWorkerWs,
      WORKER_ID: options.workerId,
      WORKER_NAME: options.workerName,
      WORKER_WORK_DIR: workerDir,
      WORKER_TOKEN: managerToken,
      WORKER_PROJECT_BINDING_MODE:
        options.boundProjectIds && options.boundProjectIds.length > 0
          ? "SPECIFIC_PROJECTS"
          : "ALL_PROJECTS",
      WORKER_BOUND_PROJECT_IDS: (options.boundProjectIds || []).join(","),
      WORKER_A2A_DB_PATH: path.join(workerDir, "a2a.db"),
      WORKER_A2A_HOST: "127.0.0.1",
      WORKER_A2A_PORT: "0",
      WORKER_TERMINAL_ENABLED: "false",
      WORKER_REVIEW_ENABLED: "false",
      CODEX_BINARY: state.codexBinary,
      CLAUDE_BINARY: state.claudeBinary,
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  if (!child.pid) {
    throw new Error(`无法启动 A2A Worker ${options.workerId}`);
  }
  appendFileSync(state.pidFile, `${child.pid}\n`);
  let output = "";
  child.stdout?.on("data", (chunk) => {
    output += String(chunk);
  });
  child.stderr?.on("data", (chunk) => {
    output += String(chunk);
  });

  const ready = waitFor(
    async () => {
      const data = await graphQL(
        `query Worker($id: ID!) {
          worker(id: $id) { status capabilities { key value } }
        }`,
        { id: options.workerId },
      );
      if (!data.worker || data.worker.status !== "ONLINE") return false;
      const capabilities = Object.fromEntries(
        data.worker.capabilities.map((item: { key: string; value: string }) => [
          item.key,
          item.value,
        ]),
      );
      return (
        capabilities.a2a_version === "1.0" &&
        capabilities.a2a_transport === "JSONRPC" &&
        Number(capabilities.a2a_port) > 0
      );
    },
    30_000,
    () => `Worker ${options.workerId} 未就绪\n${output}`,
    child,
  );
  const started = ready.then(() =>
    waitForTaskStatus(
      options.taskId,
      (status) =>
        status === "RUNNING" ||
        status === "WAITING_INPUT" ||
        status === "COMPLETED",
      child,
      output,
    ),
  );
  const done =
    options.expectCompletion === false
      ? Promise.resolve()
      : ready.then(() =>
          waitForTaskStatus(
            options.taskId,
            (status) => status === "COMPLETED",
            child,
            output,
          ),
        );
  const requested = options.expectInteraction
    ? ready.then(() =>
        waitForTaskStatus(
          options.taskId,
          (status) => status === "WAITING_INPUT",
          child,
          output,
        ),
      )
    : Promise.resolve();
  const firstDone = done;
  const continued = options.expectContinuation
    ? ready.then(() => waitForTaskRound(options.taskId, 2, child, output))
    : Promise.resolve();

  return {
    ready,
    started,
    done,
    requested,
    completed: done,
    firstDone,
    continued,
    close: () => stopChild(child),
  };
}

export function e2eGitURL(): string {
  return runtimeState().gitUrl;
}

export function fakeAgentPrompt(
  result: string,
  options: {
    approval?: boolean;
    hold?: boolean;
    claudeFile?: boolean;
    plan?: string;
    expectedEnv?: Record<string, string>;
  } = {},
): string {
  const markers = [`BPT_RESULT_B64=${Buffer.from(result).toString("base64")}`];
  if (options.approval) markers.push("BPT_REQUIRE_APPROVAL");
  if (options.hold) markers.push("BPT_HOLD");
  if (options.claudeFile) markers.push("BPT_CLAUDE_FILE_APPROVAL");
  if (options.plan) {
    markers.push(
      `BPT_CLAUDE_PLAN_B64=${Buffer.from(options.plan).toString("base64")}`,
    );
  }
  if (options.expectedEnv) {
    markers.push(
      `BPT_EXPECT_ENV_B64=${Buffer.from(
        JSON.stringify(options.expectedEnv),
      ).toString("base64")}`,
    );
  }
  return markers.join(" ");
}

function runtimeState(): E2ERuntimeState {
  const statePath = process.env.BPT_E2E_RUNTIME_STATE;
  if (!statePath) {
    throw new Error("Playwright A2A global setup 未提供运行状态");
  }
  return JSON.parse(readFileSync(statePath, "utf8")) as E2ERuntimeState;
}

async function graphQL(query: string, variables: Record<string, unknown>) {
  const response = await fetch(managerGraphQL, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      authorization: `Bearer ${managerToken}`,
    },
    body: JSON.stringify({ query, variables }),
  });
  if (!response.ok) {
    throw new Error(`GraphQL HTTP ${response.status}: ${await response.text()}`);
  }
  const body = (await response.json()) as {
    data?: Record<string, any>;
    errors?: Array<{ message: string }>;
  };
  if (body.errors?.length) {
    throw new Error(body.errors.map((item) => item.message).join("; "));
  }
  return body.data || {};
}

async function waitForTaskStatus(
  taskId: string,
  accept: (status: string) => boolean,
  child: ChildProcess,
  output: string,
) {
  await waitFor(
    async () => {
      const data = await graphQL(
        "query Task($id: ID!) { task(id: $id) { status } }",
        { id: taskId },
      );
      return Boolean(data.task && accept(data.task.status));
    },
    30_000,
    () => `Task ${taskId} 未进入目标状态\n${output}`,
    child,
  );
}

async function waitForTaskRound(
  taskId: string,
  minimumTurns: number,
  child: ChildProcess,
  output: string,
) {
  await waitFor(
    async () => {
      const data = await graphQL(
        `query A2ARounds($taskId: ID!) {
          task(id: $taskId) { status }
          taskA2AExecutions(taskId: $taskId) { turn remoteStatus }
        }`,
        { taskId },
      );
      return (
        data.task?.status === "COMPLETED" &&
        data.taskA2AExecutions?.some(
          (round: { turn: number; remoteStatus: string }) =>
            round.turn >= minimumTurns && round.remoteStatus === "COMPLETED",
        )
      );
    },
    30_000,
    () => `Task ${taskId} 未完成第 ${minimumTurns} 个 A2A turn\n${output}`,
    child,
  );
}

async function waitFor(
  check: () => Promise<boolean>,
  timeout: number,
  errorMessage: () => string,
  child?: ChildProcess,
) {
  const deadline = Date.now() + timeout;
  let lastError: unknown;
  while (Date.now() < deadline) {
    if (child && child.exitCode !== null) {
      throw new Error(`${errorMessage()}\nWorker exit code: ${child.exitCode}`);
    }
    try {
      if (await check()) return;
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(
    `${errorMessage()}${lastError ? `\nLast error: ${String(lastError)}` : ""}`,
  );
}

function stopChild(child: ChildProcess) {
  if (child.exitCode === null && child.pid && child.pid > 1) {
    child.kill("SIGTERM");
  }
}
