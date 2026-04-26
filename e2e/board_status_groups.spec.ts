import { expect, test } from "@playwright/test";

const managerGraphQL =
  process.env.BPT_MANAGER_GRAPHQL_URL || "http://localhost:8080/graphql";
const managerWorkerWs =
  process.env.BPT_MANAGER_WS_URL ||
  managerGraphQL.replace(/^http/, "ws").replace(/\/graphql$/, "/worker/ws");
const managerWorkerToken = process.env.BPT_MANAGER_WS_TOKEN || "";

async function graphQL(
  request,
  query: string,
  variables: Record<string, unknown> = {},
) {
  const response = await request.post(managerGraphQL, {
    data: { query, variables },
    headers: { "content-type": "application/json" },
  });
  expect(response.ok()).toBeTruthy();
  const body = await response.json();
  expect(body.errors).toBeFalsy();
  return body.data;
}

function connectWorkerUntilStarted(workerId: string, taskId: string) {
  const url = new URL(managerWorkerWs);
  url.searchParams.set("worker_id", workerId);
  if (managerWorkerToken) {
    url.searchParams.set("token", managerWorkerToken);
  }

  const ws = new WebSocket(url.toString());
  const now = () => new Date().toISOString();
  const send = (
    messageId: string,
    type: string,
    payload: Record<string, unknown> = {},
  ) => {
    ws.send(
      JSON.stringify({
        messageId,
        type,
        workerId,
        taskId,
        timestamp: now(),
        payload,
      }),
    );
  };

  const ready = new Promise<void>((resolve, reject) => {
    ws.addEventListener("open", () => resolve(), { once: true });
    ws.addEventListener(
      "error",
      () => reject(new Error("worker websocket failed")),
      { once: true },
    );
  });
  const started = new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(() => {
      ws.close();
      reject(new Error("timed out waiting for TASK_START"));
    }, 5000);
    ws.addEventListener("message", (message) => {
      const envelope = JSON.parse(String(message.data));
      if (envelope.type !== "TASK_START") {
        return;
      }
      send(`accepted-${taskId}`, "TASK_ACCEPTED");
      send(`started-${taskId}`, "TASK_STARTED", {
        taskId,
        content: "/tmp/e2e-running-worktree",
      });
      clearTimeout(timeout);
      resolve();
    });
    ws.addEventListener(
      "error",
      () => {
        clearTimeout(timeout);
        reject(new Error("worker websocket failed"));
      },
      { once: true },
    );
  });

  return {
    ready,
    started,
    close: () => ws.close(),
  };
}

test("board kanban groups task statuses into three visual columns", async ({
  page,
  request,
}, testInfo) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  const suffix = Date.now();
  const projectName = `Board Groups Project ${suffix}`;
  const workerId = `worker-board-groups-${suffix}`;

  const createdProject = await graphQL(
    request,
    "mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id name } }",
    {
      input: {
        name: projectName,
        gitUrl: "e2e-fixture",
        defaultBranch: "main",
        worktreeNamePrefix: "board-groups",
      },
    },
  );
  const project = createdProject.createProject;
  expect(project).toBeTruthy();

  await graphQL(
    request,
    "mutation RegisterWorker($input: RegisterWorkerInput!) { registerWorker(input: $input) { id status } }",
    {
      input: {
        id: workerId,
        name: "Board Groups Worker",
        supportedAgents: ["codex"],
        workDir: "/tmp/e2e-board-groups-worker",
        projectBindingMode: "SPECIFIC_PROJECTS",
        boundProjectIds: [project.id],
      },
    },
  );

  const pendingTask = await graphQL(
    request,
    "mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id title status } }",
    {
      input: {
        title: `Board Groups Pending ${suffix}`,
        projectId: project.id,
        agentType: "codex",
        baseBranch: "main",
        targetBranch: `task/board-groups-pending-${suffix}`,
      },
    },
  );
  expect(pendingTask.createTask.status).toBe("CREATED");

  const runningTask = await graphQL(
    request,
    "mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id title status } }",
    {
      input: {
        title: `Board Groups Running ${suffix}`,
        projectId: project.id,
        agentType: "codex",
        baseBranch: "main",
        targetBranch: `task/board-groups-running-${suffix}`,
      },
    },
  );

  const completedBucketTask = await graphQL(
    request,
    "mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id title status } }",
    {
      input: {
        title: `Board Groups Complete ${suffix}`,
        projectId: project.id,
        agentType: "codex",
        baseBranch: "main",
        targetBranch: `task/board-groups-complete-${suffix}`,
      },
    },
  );
  await graphQL(
    request,
    "mutation ArchiveTask($taskId: ID!) { archiveTask(taskId: $taskId) { id status } }",
    { taskId: completedBucketTask.createTask.id },
  );

  const workerSocket = connectWorkerUntilStarted(
    workerId,
    runningTask.createTask.id,
  );
  await workerSocket.ready;
  await graphQL(
    request,
    "mutation StartTask($taskId: ID!) { startTask(taskId: $taskId) { id status workerId } }",
    { taskId: runningTask.createTask.id },
  );
  await workerSocket.started;

  await expect
    .poll(async () => {
      const data = await graphQL(
        request,
        "query BoardGroupTasks { tasks(filter: { includeArchived: true }) { nodes { title status } } }",
      );
      const tasks = data.tasks.nodes as Array<{ title: string; status: string }>;
      return {
        pending: tasks.some(
          (task) =>
            task.title === pendingTask.createTask.title &&
            task.status === "CREATED",
        ),
        running: tasks.some(
          (task) =>
            task.title === runningTask.createTask.title &&
            task.status === "RUNNING",
        ),
        complete: tasks.some(
          (task) =>
            task.title === completedBucketTask.createTask.title &&
            task.status === "ARCHIVED",
        ),
      };
    })
    .toEqual({ pending: true, running: true, complete: true });

  await page.goto("/");
  await expect(page.locator("flutter-view")).toBeVisible();
  await page.waitForTimeout(2000);

  await page.screenshot({
    path: testInfo.outputPath("board-status-groups.png"),
    fullPage: true,
  });

  workerSocket.close();
});
