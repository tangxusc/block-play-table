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

async function sendWorkerEvents(page, workerId: string, taskId: string) {
  await page.evaluate(
    ({ wsUrl, workerToken, workerId, taskId }) =>
      new Promise<void>((resolve, reject) => {
        const url = new URL(wsUrl);
        url.searchParams.set("worker_id", workerId);
        if (workerToken) {
          url.searchParams.set("token", workerToken);
        }
        const ws = new WebSocket(url.toString());
        const timeout = window.setTimeout(() => {
          ws.close();
          reject(new Error("timed out sending worker events"));
        }, 5000);
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
        ws.onerror = () => {
          window.clearTimeout(timeout);
          reject(new Error("worker websocket failed"));
        };
        ws.onopen = () => {
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
          window.setTimeout(() => {
            window.clearTimeout(timeout);
            ws.close();
            resolve();
          }, 250);
        };
      }),
    { wsUrl: managerWorkerWs, workerToken: managerWorkerToken, workerId, taskId },
  );
}

test("trusted Flutter web UI covers DDD event-backed task flow", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/");
  await expect(page).toHaveTitle("Block Play Table");
  await expect(page.locator("flutter-view")).toBeVisible();
  await page.waitForTimeout(1500);

  const suffix = Date.now();
  const projectName = `E2E Project ${suffix}`;
  const taskTitle = `E2E Task ${suffix}`;
  const workerId = `worker-e2e-${suffix}`;

  await page.mouse.click(40, 208);
  await page.waitForTimeout(500);
  await page.mouse.click(190, 96);
  await page.keyboard.type(projectName);
  await page.mouse.click(500, 96);
  await page.keyboard.type("e2e-fixture");
  await page.mouse.click(986, 88);

  await expect
    .poll(async () => {
      const data = await graphQL(request, "query { projects { name } }");
      return data.projects.some(
        (project: { name: string }) => project.name === projectName,
      );
    })
    .toBeTruthy();
  const projects = await graphQL(request, "query { projects { id name } }");
  const project = projects.projects.find(
    (item: { id: string; name: string }) => item.name === projectName,
  );
  expect(project).toBeTruthy();

  await graphQL(
    request,
    "mutation RegisterWorker($input: RegisterWorkerInput!) { registerWorker(input: $input) { id status } }",
    {
      input: {
        id: workerId,
        name: "E2E Worker",
        supportedAgents: ["codex"],
        workDir: "/tmp/e2e-worker",
        projectBindingMode: "SPECIFIC_PROJECTS",
        boundProjectIds: [project.id],
      },
    },
  );

  await page.mouse.click(40, 80);
  await page.waitForTimeout(500);
  await page.mouse.click(210, 96);
  await page.keyboard.type(taskTitle);
  await page.mouse.click(922, 96);

  await expect
    .poll(async () => {
      const data = await graphQL(request, "query { tasks { title status } }");
      return data.tasks.some(
        (task: { title: string; status: string }) =>
          task.title === taskTitle && task.status === "CREATED",
      );
    })
    .toBeTruthy();
  const tasks = await graphQL(request, "query { tasks { id title status } }");
  const task = tasks.tasks.find(
    (item: { id: string; title: string; status: string }) =>
      item.title === taskTitle,
  );
  expect(task).toBeTruthy();

  await graphQL(
    request,
    "mutation StartTask($taskId: ID!) { startTask(taskId: $taskId) { id status workerId } }",
    {
      taskId: task.id,
    },
  );
  await sendWorkerEvents(page, workerId, task.id);

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
  await page.mouse.click(40, 336);
  await expect(page.locator("flutter-view")).toBeVisible();
});
