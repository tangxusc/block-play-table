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

async function enableFlutterAccessibility(page) {
  const button = page.getByRole("button", { name: "Enable accessibility" });
  if (await button.isVisible({ timeout: 3000 }).catch(() => false)) {
    await button.evaluate((element: HTMLElement) => element.click());
    await page.waitForTimeout(500);
  }
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
  await page.getByText("Workers").click();
  const workerGroup = page.getByRole("group", { name: new RegExp(workerName) });
  await workerGroup.getByRole("button", { name: "Edit worker" }).click();
  await expect(page.getByText("Runtime environment")).toBeVisible();
  await page.getByRole("button", { name: "New env var" }).click();
  await page.getByLabel("Key").fill("BPT_E2E_AGENT_ENV");
  await page.getByLabel("Value").fill("codex-value");
  await page.getByRole("button", { name: "Save" }).last().click();
  await page.getByRole("button", { name: "Claude" }).last().click();
  await page.getByRole("button", { name: "New env var" }).click();
  await page.getByLabel("Key").fill("BPT_E2E_CLAUDE_ENV");
  await page.getByLabel("Value").fill("claude-value");
  await page.getByRole("button", { name: "Save" }).last().click();
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
    "mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id title status } }",
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
  expect(createdTask.createTask.status).toBe("ASSIGNED");

  await expect
    .poll(async () => {
      const data = await graphQL(
        request,
        "query { tasks { nodes { title status } } }",
      );
      return data.tasks.nodes.some(
        (task: { title: string; status: string }) =>
          task.title === taskTitle && task.status === "ASSIGNED",
      );
    })
    .toBeTruthy();
  const tasks = await graphQL(
    request,
    "query { tasks { nodes { id title status } } }",
  );
  const task = tasks.tasks.nodes.find(
    (item: { id: string; title: string; status: string }) =>
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
  await page.mouse.click(40, 96);
  await expect(page.locator("flutter-view")).toBeVisible();
});
