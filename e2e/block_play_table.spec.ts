import { expect, test } from "@playwright/test";
import WebSocket from "ws";

const managerGraphQL =
  process.env.BPT_MANAGER_GRAPHQL_URL || "http://localhost:8080/graphql";
const managerWorkerWs =
  process.env.BPT_MANAGER_WS_URL ||
  managerGraphQL.replace(/^http/, "ws").replace(/\/graphql$/, "/worker/ws");
const managerWorkerToken =
  process.env.BPT_MANAGER_WS_TOKEN || process.env.WORKER_TOKEN || "dev-worker-token";

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

async function enableFlutterAccessibility(page) {
  const button = page.getByRole("button", { name: "Enable accessibility" });
  if (await button.isVisible({ timeout: 3000 }).catch(() => false)) {
    await button.evaluate((element: HTMLElement) => element.click());
    await page.waitForTimeout(500);
  }
}

async function openTaskFromList(page, taskTitle: string) {
  await page.getByRole("button", { name: "List" }).click();
  await page.mouse.move(700, 520);
  const taskRow = page.getByRole("button", { name: new RegExp(taskTitle) });
  for (let attempt = 0; attempt < 20; attempt++) {
    if (await taskRow.isVisible().catch(() => false)) {
      break;
    }
    await page.mouse.wheel(0, 900);
    await page.waitForTimeout(150);
  }
  await expect(taskRow).toBeVisible();
  await taskRow.click();
}

async function openWorkerEditor(page, workerName: string) {
  await page.getByText("Workers").click();
  await page.mouse.move(700, 520);
  const workerGroup = page.getByRole("group", { name: new RegExp(workerName) });
  for (let attempt = 0; attempt < 20; attempt++) {
    if (await workerGroup.isVisible().catch(() => false)) {
      break;
    }
    await page.mouse.wheel(0, 900);
    await page.waitForTimeout(150);
  }
  await expect(workerGroup).toBeVisible();
  await workerGroup.getByRole("button", { name: "Edit worker" }).click();
}

async function fillFlutterTextField(page, input, value: string) {
  for (let attempt = 0; attempt < 3; attempt++) {
    await input.click();
    await page.waitForTimeout(100);
    await page.keyboard.press(process.platform === "darwin" ? "Meta+A" : "Control+A");
    await page.keyboard.press("Backspace");
    await page.waitForTimeout(100);
    await input.type(value, { delay: 20 });
    await page.waitForTimeout(100);
    if ((await input.inputValue()) === value) {
      return;
    }
  }
  await expect(input).toHaveValue(value);
}

async function addWorkerEnvVar(page, key: string, value: string) {
  await page.getByRole("button", { name: "New env var" }).click();
  await expect(page.getByText("Create env var")).toBeVisible();
  const keyInput = page.getByLabel("Key").last();
  await fillFlutterTextField(page, keyInput, key);
  const valueInput = page.getByLabel("Value").last();
  await fillFlutterTextField(page, valueInput, value);
  await page.getByLabel("Description").last().click();
  await page.getByRole("button", { name: "Save" }).last().click();
  await expect(page.getByText("Create env var")).toBeHidden();
}

function connectWorkerEvents(
  workerId: string,
  taskId: string,
  expectedEnv: Record<string, string> = {},
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
    ws.addEventListener("error", () => reject(new Error("worker websocket failed")), { once: true });
  });
  const done = new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(() => {
      ws.close();
      reject(new Error("timed out waiting for TASK_START"));
    }, 5000);
    ws.addEventListener("message", (message) => {
      const envelope = JSON.parse(String(message.data));
      if (envelope.type !== "TASK_START") {
        return;
      }
      const runtimeEnv = envelope.payload?.agentRuntimeEnv || [];
      for (const [key, value] of Object.entries(expectedEnv)) {
        expect(
          runtimeEnv.some(
            (item: { key: string; value: string }) =>
              item.key === key && item.value === value,
          ),
        ).toBeTruthy();
      }
      expect(
        runtimeEnv.some(
          (item: { key: string }) => item.key === "BPT_E2E_CLAUDE_ENV",
        ),
      ).toBeFalsy();
      send(`accepted-${taskId}`, "TASK_ACCEPTED");
      send(`started-${taskId}`, "TASK_STARTED", {
        taskId,
        content: "/tmp/e2e-worktree",
      });
      send(`log-${taskId}`, "TASK_LOG", {
        taskId,
        stream: "stdout",
        content: "hello from e2e worker",
      });
      send(`conversation-${taskId}`, "TASK_CONVERSATION", {
        taskId,
        content: "agent response from e2e",
        metadata: { role: "assistant" },
      });
      send(`result-${taskId}`, "TASK_RESULT", {
        taskId,
        result: "e2e completed",
      });
      send(`completed-${taskId}`, "TASK_COMPLETED", {
        taskId,
        result: "e2e completed",
      });
      setTimeout(() => {
        clearTimeout(timeout);
        ws.close();
        resolve();
      }, 250);
    });
    ws.addEventListener("error", () => {
      clearTimeout(timeout);
      reject(new Error("worker websocket failed"));
    }, { once: true });
  });
  return { ready, done };
}

function connectWorkerForContinuation(workerId: string, taskId: string) {
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
    ws.addEventListener("error", () => reject(new Error("worker websocket failed")), { once: true });
  });
  const firstDone = new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("timed out waiting for TASK_START")), 5000);
    ws.addEventListener("message", (message) => {
      const envelope = JSON.parse(String(message.data));
      if (envelope.type !== "TASK_START") {
        return;
      }
      send(`accepted-first-${taskId}`, "TASK_ACCEPTED");
      send(`started-first-${taskId}`, "TASK_STARTED", {
        taskId,
        content: "/tmp/e2e-continuation-worktree",
      });
      send(`conversation-first-${taskId}`, "TASK_CONVERSATION", {
        taskId,
        content: "first agent response",
        agentSessionId: "session-1",
        metadata: { role: "assistant" },
      });
      send(`completed-first-${taskId}`, "TASK_COMPLETED", {
        taskId,
        result: "first result",
        agentSessionId: "session-1",
      });
      clearTimeout(timeout);
      resolve();
    });
  });
  const continued = new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("timed out waiting for TASK_CONTINUE")), 15000);
    ws.addEventListener("message", (message) => {
      const envelope = JSON.parse(String(message.data));
      if (envelope.type !== "TASK_CONTINUE") {
        return;
      }
      expect(envelope.payload.agentSessionId).toBe("session-1");
      expect(envelope.payload.message).toBe("follow up from ui");
      expect(envelope.payload.worktreePath).toBe("/tmp/e2e-continuation-worktree");
      send(`accepted-continue-${taskId}`, "TASK_ACCEPTED");
      send(`started-continue-${taskId}`, "TASK_STARTED", {
        taskId,
        content: "/tmp/e2e-continuation-worktree",
        agentSessionId: "session-1",
      });
      send(`conversation-continue-${taskId}`, "TASK_CONVERSATION", {
        taskId,
        content: "second agent response",
        agentSessionId: "session-1",
        metadata: { role: "assistant" },
      });
      send(`completed-continue-${taskId}`, "TASK_COMPLETED", {
        taskId,
        result: "second result",
        agentSessionId: "session-1",
      });
      clearTimeout(timeout);
      setTimeout(() => {
        ws.close();
        resolve();
      }, 250);
    });
  });
  return { ready, firstDone, continued, close: () => ws.close() };
}

test("trusted Flutter web UI covers DDD event-backed task flow", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/");
  await expect(page).toHaveTitle("Block Play Table");
  await expect(page.locator("flutter-view")).toBeVisible({ timeout: 30000 });
  await page.waitForTimeout(1500);
  await enableFlutterAccessibility(page);

  const suffix = Date.now();
  const projectName = `E2E Project ${suffix}`;
  const taskTitle = `E2E Task ${suffix}`;
  const workerId = `worker-e2e-${suffix}`;
  const workerName = `E2E Worker ${suffix}`;
  const taskStartDate = "2026-05-01T00:00:00Z";
  const taskEndDate = "2026-05-03T00:00:00Z";

  const createdProject = await graphQL(
    request,
    "mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id name } }",
    {
      input: {
        name: projectName,
        gitUrl: "e2e-fixture",
        defaultBranch: "main",
        worktreeNamePrefix: "e2e",
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
        name: workerName,
        supportedAgents: ["codex", "claude"],
        workDir: "/tmp/e2e-worker",
        projectBindingMode: "SPECIFIC_PROJECTS",
        boundProjectIds: [project.id],
      },
    },
  );

  await page.reload();
  await page.waitForTimeout(1500);
  await enableFlutterAccessibility(page);
  await openWorkerEditor(page, workerName);
  await expect(page.getByText("Runtime environment")).toBeVisible();
  await addWorkerEnvVar(page, "BPT_E2E_AGENT_ENV", "codex-value");
  await page.getByRole("button", { name: "Claude" }).last().click();
  await addWorkerEnvVar(page, "BPT_E2E_CLAUDE_ENV", "claude-value");
  await page.getByRole("button", { name: "Save" }).last().click();

  await expect
    .poll(async () => {
      const data = await graphQL(
        request,
        "query Worker($id: ID!) { worker(id: $id) { agentRuntimeEnv { agentType vars { key valueMasked } } } }",
        { id: workerId },
      );
      return Object.fromEntries(
        data.worker.agentRuntimeEnv.map(
          (group: { agentType: string; vars: Array<{ key: string; valueMasked: string }> }) => [
            group.agentType,
            group.vars,
          ],
        ),
      );
    })
    .toEqual({
      codex: [{ key: "BPT_E2E_AGENT_ENV", valueMasked: "********" }],
      claude: [{ key: "BPT_E2E_CLAUDE_ENV", valueMasked: "********" }],
    });

  const createdTask = await graphQL(
    request,
    "mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id title status startDate endDate } }",
    {
      input: {
        title: taskTitle,
        projectId: project.id,
        workerId,
        agentType: "codex",
        baseBranch: "main",
        startDate: taskStartDate,
        endDate: taskEndDate,
      },
    },
  );
  expect(createdTask.createTask.status).toBe("ASSIGNED");
  expect(createdTask.createTask.startDate).toBe(taskStartDate);
  expect(createdTask.createTask.endDate).toBe(taskEndDate);

  await expect
    .poll(async () => {
      const data = await graphQL(
        request,
        "query { tasks { nodes { title status startDate endDate } } }",
      );
      return data.tasks.nodes.some(
        (task: { title: string; status: string; startDate: string; endDate: string }) =>
          task.title === taskTitle &&
          task.status === "ASSIGNED" &&
          task.startDate === taskStartDate &&
          task.endDate === taskEndDate,
      );
    })
    .toBeTruthy();
  const tasks = await graphQL(
    request,
    "query { tasks { nodes { id title status startDate endDate } } }",
  );
  const task = tasks.tasks.nodes.find(
    (item: { id: string; title: string; status: string; startDate: string; endDate: string }) =>
      item.title === taskTitle,
  );
  expect(task).toBeTruthy();

  const workerSocket = connectWorkerEvents(workerId, task.id, {
    BPT_E2E_AGENT_ENV: "codex-value",
  });
  await workerSocket.ready;
  await graphQL(
    request,
    "mutation StartTask($taskId: ID!) { startTask(taskId: $taskId) { id status workerId } }",
    {
      taskId: task.id,
    },
  );
  await workerSocket.done;

  await expect
    .poll(async () => {
      const [taskData, logData, conversationData, eventData] =
        await Promise.all([
          graphQL(
            request,
            "query Task($id: ID!) { task(id: $id) { status result } }",
            { id: task.id },
          ),
          graphQL(
            request,
            "query TaskLogs($taskId: ID!) { taskLogs(taskId: $taskId) { content } }",
            { taskId: task.id },
          ),
          graphQL(
            request,
            "query TaskConversations($taskId: ID!) { taskConversations(taskId: $taskId) { content } }",
            {
              taskId: task.id,
            },
          ),
          graphQL(
            request,
            "query TaskEvents($taskId: ID!) { taskEvents(taskId: $taskId) { eventType } }",
            { taskId: task.id },
          ),
        ]);
      const eventTypes = eventData.taskEvents.map(
        (event: { eventType: string }) => event.eventType,
      );
      return (
        taskData.task.status === "COMPLETED" &&
        taskData.task.result === "e2e completed" &&
        logData.taskLogs.some((log: { content: string }) =>
          log.content.includes("hello from e2e worker"),
        ) &&
        conversationData.taskConversations.some(
          (message: { content: string }) =>
            message.content.includes("agent response from e2e"),
        ) &&
        eventTypes.includes("TaskCreated") &&
        eventTypes.includes("TaskStartRequested") &&
        eventTypes.includes("TaskCompleted")
      );
    })
    .toBeTruthy();

  await page.reload();
  await page.waitForTimeout(1500);
  await enableFlutterAccessibility(page);
  await openTaskFromList(page, taskTitle);
  await expect(page.getByText("2026-05-01 - 2026-05-03")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator("flutter-view")).toBeVisible();
});

test("task detail continues a completed task with the same agent session", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/");
  await expect(page.locator("flutter-view")).toBeVisible({ timeout: 30000 });
  await page.waitForTimeout(1500);
  await enableFlutterAccessibility(page);

  const suffix = Date.now();
  const projectName = `E2E Continue Project ${suffix}`;
  const taskTitle = `E2E Continue Task ${suffix}`;
  const workerId = `worker-e2e-continue-${suffix}`;

  const createdProject = await graphQL(
    request,
    "mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }",
    {
      input: {
        name: projectName,
        gitUrl: "e2e-fixture",
        defaultBranch: "main",
        worktreeNamePrefix: "e2e-continue",
      },
    },
  );
  const project = createdProject.createProject;

  await graphQL(
    request,
    "mutation RegisterWorker($input: RegisterWorkerInput!) { registerWorker(input: $input) { id status } }",
    {
      input: {
        id: workerId,
        name: `E2E Continue Worker ${suffix}`,
        supportedAgents: ["codex"],
        workDir: "/tmp/e2e-continuation-worker",
        projectBindingMode: "ALL_PROJECTS",
      },
    },
  );

  const createdTask = await graphQL(
    request,
    "mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id } }",
    {
      input: {
        title: taskTitle,
        projectId: project.id,
        workerId,
        agentType: "codex",
        baseBranch: "main",
      },
    },
  );
  const taskId = createdTask.createTask.id;

  const workerSocket = connectWorkerForContinuation(workerId, taskId);
  await workerSocket.ready;
  await graphQL(
    request,
    "mutation StartTask($taskId: ID!) { startTask(taskId: $taskId) { id status } }",
    { taskId },
  );
  await workerSocket.firstDone;

  await expect
    .poll(async () => {
      const data = await graphQL(
        request,
        "query Task($id: ID!) { task(id: $id) { status result agentSessionId } }",
        { id: taskId },
      );
      return data.task;
    })
    .toMatchObject({
      status: "COMPLETED",
      result: "first result",
      agentSessionId: "session-1",
    });

  await page.reload();
  await page.waitForTimeout(1500);
  await enableFlutterAccessibility(page);
  await openTaskFromList(page, taskTitle);
  await expect(page.getByRole("tab", { name: "Conversation" })).toBeVisible();
  await page.getByLabel("Continue conversation").click();
  await page.waitForTimeout(100);
  await page.keyboard.type("follow up from ui");
  await page.getByRole("button", { name: /Send continuation/ }).click();

  await workerSocket.continued;
  await expect
    .poll(async () => {
      const [taskData, conversationData] = await Promise.all([
        graphQL(
          request,
          "query Task($id: ID!) { task(id: $id) { status result agentSessionId } }",
          { id: taskId },
        ),
        graphQL(
          request,
          "query TaskConversations($taskId: ID!) { taskConversations(taskId: $taskId) { role content } }",
          { taskId },
        ),
      ]);
      return {
        task: taskData.task,
        messages: conversationData.taskConversations,
      };
    })
    .toMatchObject({
      task: {
        status: "COMPLETED",
        result: "second result",
        agentSessionId: "session-1",
      },
      messages: expect.arrayContaining([
        expect.objectContaining({ role: "assistant", content: "first agent response" }),
        expect.objectContaining({ role: "user", content: "follow up from ui" }),
        expect.objectContaining({ role: "assistant", content: "second agent response" }),
      ]),
    });
});
