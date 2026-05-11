import { expect, test } from "@playwright/test";
import WebSocket from "ws";

const managerGraphQL =
  process.env.BPT_MANAGER_GRAPHQL_URL || "http://localhost:8080/graphql";
const managerWorkerWs =
  process.env.BPT_MANAGER_WS_URL ||
  managerGraphQL.replace(/^http/, "ws").replace(/\/graphql$/, "/worker/ws");
const managerWorkerToken =
  process.env.BPT_MANAGER_WS_TOKEN || process.env.WORKER_TOKEN || "dev-worker-token";
const managerAccessToken =
  process.env.BPT_MANAGER_TOKEN || process.env.WORKER_TOKEN || "dev-worker-token";

async function graphQL(
  request,
  query: string,
  variables: Record<string, unknown> = {},
) {
  const response = await request.post(managerGraphQL, {
    data: { query, variables },
    headers: {
      "content-type": "application/json",
      authorization: `Bearer ${managerAccessToken}`,
    },
  });
  expect(response.ok()).toBeTruthy();
  const body = await response.json();
  expect(body.errors).toBeFalsy();
  return body.data;
}

async function fillTextField(page, input, value: string) {
  for (let attempt = 0; attempt < 3; attempt++) {
    await input.click();
    await page.waitForTimeout(100);
    if (value.length > 0) {
      await input.fill(value);
    } else {
      await input.fill("");
    }
    await page.waitForTimeout(300);
    const current = await input.inputValue().catch(() => value);
    if (current === value) {
      return;
    }
  }
}

function taskLocator(page, title: string) {
  const name = new RegExp(title);
  return page.getByRole("group", { name });
}

async function openVueApp(page) {
  await page.goto("/", { waitUntil: "domcontentloaded", timeout: 120000 });
  const authState = await page
    .waitForFunction(() => {
      if (document.querySelector("#app[data-ready='true']")) return "ready";
      if (document.querySelector('input[aria-label="Manager token"]')) return "token";
      return "";
    }, null, { timeout: 120000 })
    .then((handle) => handle.jsonValue());
  const tokenInput = page.getByLabel("Manager token");
  if (authState === "token") {
    await tokenInput.fill(managerAccessToken);
    await page.getByRole("button", { name: "Unlock manager" }).click();
  }
  await expect(page.locator("#app[data-ready='true']")).toBeVisible({ timeout: 30000 });
}

function connectWorkerUntilStarted(
  workerId: string,
  taskId: string,
  options: { complete?: boolean } = {},
) {
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
      if (options.complete) {
        send(`completed-${taskId}`, "TASK_COMPLETED", {
          taskId,
          result: "board group completed",
        });
      }
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

test("board scrum groups task statuses into four visual columns", async ({
  page,
  request,
}, testInfo) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  const suffix = Date.now();
  const projectName = `Board Groups Project ${suffix}`;
  const assignedWorkerId = `worker-board-groups-assigned-${suffix}`;
  const runningWorkerId = `worker-board-groups-running-${suffix}`;
  const doneWorkerId = `worker-board-groups-done-${suffix}`;
  const workerName = `Board Groups Worker ${suffix}`;

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
        id: assignedWorkerId,
        name: `${workerName} Assigned`,
        supportedAgents: ["codex"],
        workDir: "/tmp/e2e-board-groups-worker",
        projectBindingMode: "SPECIFIC_PROJECTS",
        boundProjectIds: [project.id],
      },
    },
  );
  await graphQL(
    request,
    "mutation RegisterWorker($input: RegisterWorkerInput!) { registerWorker(input: $input) { id status } }",
    {
      input: {
        id: runningWorkerId,
        name: `${workerName} Running`,
        supportedAgents: ["codex"],
        workDir: "/tmp/e2e-board-groups-running-worker",
        projectBindingMode: "SPECIFIC_PROJECTS",
        boundProjectIds: [project.id],
      },
    },
  );
  await graphQL(
    request,
    "mutation RegisterWorker($input: RegisterWorkerInput!) { registerWorker(input: $input) { id status } }",
    {
      input: {
        id: doneWorkerId,
        name: `${workerName} Done`,
        supportedAgents: ["codex"],
        workDir: "/tmp/e2e-board-groups-done-worker",
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
      },
    },
  );
  expect(pendingTask.createTask.status).toBe("CREATED");

  const assignedTask = await graphQL(
    request,
    "mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id title status } }",
    {
      input: {
        title: `Board Groups Ready ${suffix}`,
        projectId: project.id,
        workerId: assignedWorkerId,
        agentType: "codex",
        baseBranch: "main",
      },
    },
  );
  expect(assignedTask.createTask.status).toBe("ASSIGNED");

  const runningTask = await graphQL(
    request,
    "mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id title status } }",
    {
      input: {
        title: `Board Groups Running ${suffix}`,
        projectId: project.id,
        workerId: runningWorkerId,
        agentType: "codex",
        baseBranch: "main",
      },
    },
  );

  const doneTask = await graphQL(
    request,
    "mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id title status } }",
    {
      input: {
        title: `Board Groups Done ${suffix}`,
        projectId: project.id,
        workerId: doneWorkerId,
        agentType: "codex",
        baseBranch: "main",
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
      },
    },
  );
  await graphQL(
    request,
    "mutation ArchiveTask($taskId: ID!) { archiveTask(taskId: $taskId) { id status } }",
    { taskId: completedBucketTask.createTask.id },
  );

  const workerSocket = connectWorkerUntilStarted(
    runningWorkerId,
    runningTask.createTask.id,
  );
  const doneWorkerSocket = connectWorkerUntilStarted(
    doneWorkerId,
    doneTask.createTask.id,
    { complete: true },
  );
  await workerSocket.ready;
  await doneWorkerSocket.ready;
  await graphQL(
    request,
    "mutation StartTask($taskId: ID!) { startTask(taskId: $taskId) { id status workerId } }",
    { taskId: runningTask.createTask.id },
  );
  await graphQL(
    request,
    "mutation StartTask($taskId: ID!) { startTask(taskId: $taskId) { id status workerId } }",
    { taskId: doneTask.createTask.id },
  );
  await workerSocket.started;
  await doneWorkerSocket.started;

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
        ready: tasks.some(
          (task) =>
            task.title === assignedTask.createTask.title &&
            task.status === "ASSIGNED",
        ),
        done: tasks.some(
          (task) =>
            task.title === doneTask.createTask.title &&
            task.status === "COMPLETED",
        ),
        complete: tasks.some(
          (task) =>
            task.title === completedBucketTask.createTask.title &&
            task.status === "ARCHIVED",
        ),
      };
    })
    .toEqual({ pending: true, running: true, ready: true, done: true, complete: true });

  await openVueApp(page);

  const boardSearch = page.getByRole("textbox", { name: /Search/ });
  await expect(boardSearch).toBeVisible();
  await expect(page.locator(".board-column")).toHaveCount(4);
  for (const column of ["Backlog", "Ready", "In Progress", "Done"]) {
    await expect(page.locator(".board-column-header", { hasText: column })).toBeVisible();
  }
  await fillTextField(page, boardSearch, pendingTask.createTask.title);
  await expect(taskLocator(page, pendingTask.createTask.title)).toBeVisible();

  await fillTextField(page, boardSearch, assignedTask.createTask.title);
  await expect(taskLocator(page, assignedTask.createTask.title)).toBeVisible();

  await fillTextField(page, boardSearch, runningTask.createTask.title);
  await expect(taskLocator(page, runningTask.createTask.title)).toBeVisible();

  await fillTextField(page, boardSearch, doneTask.createTask.title);
  await expect(taskLocator(page, doneTask.createTask.title)).toBeVisible();

  await fillTextField(
    page,
    boardSearch,
    completedBucketTask.createTask.title,
  );
  await expect(taskLocator(page, completedBucketTask.createTask.title)).toBeHidden();

  await page.getByRole("button", { name: "List" }).click();
  const listSearch = page.getByRole("textbox", { name: /Search/ });
  await fillTextField(page, listSearch, pendingTask.createTask.title);
  await expect(taskLocator(page, pendingTask.createTask.title)).toBeVisible();
  await fillTextField(
    page,
    listSearch,
    completedBucketTask.createTask.title,
  );
  await expect(taskLocator(page, completedBucketTask.createTask.title)).toBeHidden();

  await page.getByRole("button", { name: "Calendar" }).click();
  const calendarSearch = page.getByRole("textbox", { name: /Search/ });
  await fillTextField(
    page,
    calendarSearch,
    completedBucketTask.createTask.title,
  );
  await expect(taskLocator(page, completedBucketTask.createTask.title)).toBeHidden();

  await page.getByRole("button", { name: "Archived" }).click();
  const archivedSearch = page.getByRole("textbox", { name: /Search/ });
  await expect(archivedSearch).toBeVisible();
  await fillTextField(
    page,
    archivedSearch,
    completedBucketTask.createTask.title,
  );
  await expect(taskLocator(page, completedBucketTask.createTask.title)).toBeVisible();
  await expect(taskLocator(page, pendingTask.createTask.title)).toBeHidden();

  await page.getByRole("button", { name: "Delete task" }).click();
  await page.getByRole("button", { name: "Delete" }).click();
  await expect(taskLocator(page, completedBucketTask.createTask.title)).toBeHidden();
  await expect
    .poll(async () => {
      const data = await graphQL(
        request,
        "query DeletedTask($id: ID!) { task(id: $id) { id } }",
        { id: completedBucketTask.createTask.id },
      );
      return data.task;
    })
    .toBeNull();

  await page.screenshot({
    path: testInfo.outputPath("board-status-groups.png"),
    fullPage: true,
  });

  workerSocket.close();
  doneWorkerSocket.close();
});
