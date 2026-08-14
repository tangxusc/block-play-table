import { expect, test } from "@playwright/test";

import {
  e2eGitURL,
  fakeAgentPrompt,
  startFakeA2AWorker,
} from "./support/fake_a2a_worker";

const managerGraphQL =
  process.env.BPT_MANAGER_GRAPHQL_URL || "http://localhost:8080/graphql";
const managerBaseURL =
  process.env.BPT_MANAGER_URL || managerGraphQL.replace(/\/graphql$/, "");
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

function taskInColumn(page, columnTitle: string, taskTitle: string) {
  const column = page.locator(".board-column").filter({
    has: page.locator(".board-column-header", { hasText: columnTitle }),
  });
  return column.getByRole("group", { name: new RegExp(taskTitle) });
}

async function openVueApp(page) {
  await page.goto("/", { waitUntil: "domcontentloaded", timeout: 120000 });
  const state = await page
    .waitForFunction(() => {
      if (document.querySelector("#app[data-ready='true']")) return "ready";
      if (document.querySelector('input[aria-label="Manager URL"]')) return "managers-page";
      return "";
    }, null, { timeout: 120000 })
    .then((handle) => handle.jsonValue());
  if (state === "managers-page") {
    await page.getByLabel("Manager URL").fill(managerBaseURL);
    await page.getByLabel("Manager token").fill(managerAccessToken);
    await page.getByRole("button", { name: "Add manager" }).click();
  }
  await expect(page.locator("#app[data-ready='true']")).toBeVisible({ timeout: 120000 });
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
        gitUrl: e2eGitURL(),
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
        description: fakeAgentPrompt("board group running", { hold: true }),
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
        description: fakeAgentPrompt("board group completed", {
          approval: true,
        }),
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

  const workerSocket = startFakeA2AWorker({
    workerId: runningWorkerId,
    workerName: `${workerName} Running`,
    taskId: runningTask.createTask.id,
    boundProjectIds: [project.id],
    expectCompletion: false,
  });
  const doneWorkerSocket = startFakeA2AWorker({
    workerId: doneWorkerId,
    workerName: `${workerName} Done`,
    taskId: doneTask.createTask.id,
    boundProjectIds: [project.id],
    expectInteraction: true,
  });
  await workerSocket.ready;
  await doneWorkerSocket.ready;
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
        `query BoardGroupTasks($runningTaskId: ID!) {
          tasks(filter: { includeArchived: true }) { nodes { title status } }
          runningExecutions: taskA2AExecutions(taskId: $runningTaskId) {
            a2aTaskId contextId remoteStatus lastSequence
          }
        }`,
        {
          runningTaskId: runningTask.createTask.id,
        },
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
        transitionReady: tasks.some(
          (task) =>
            task.title === doneTask.createTask.title &&
            task.status === "ASSIGNED",
        ),
        complete: tasks.some(
          (task) =>
            task.title === completedBucketTask.createTask.title &&
            task.status === "ARCHIVED",
        ),
        runningExecution: data.runningExecutions[0],
      };
    }, { timeout: 30000 })
    .toMatchObject({
      pending: true,
      running: true,
      ready: true,
      transitionReady: true,
      complete: true,
      runningExecution: {
        a2aTaskId: expect.any(String),
        contextId: expect.any(String),
        remoteStatus: "WORKING",
        lastSequence: expect.any(Number),
      },
    });

  await openVueApp(page);

  const boardSearch = page.getByRole("textbox", { name: /Search/ });
  await expect(boardSearch).toBeVisible();
  await expect(page.locator(".board-column")).toHaveCount(4);
  for (const column of ["Backlog", "Ready", "In Progress", "Done"]) {
    await expect(page.locator(".board-column-header", { hasText: column })).toBeVisible();
  }

  await fillTextField(page, boardSearch, doneTask.createTask.title);
  await expect(taskInColumn(page, "Ready", doneTask.createTask.title)).toBeVisible();
  await taskInColumn(page, "Ready", doneTask.createTask.title)
    .getByRole("button")
    .click();

  const detailDialog = page.getByRole("dialog");
  const activeDetailPane = detailDialog.locator(".ant-tabs-tabpane-active");
  await expect(detailDialog).toBeVisible();
  await expect(detailDialog.getByText("ASSIGNED", { exact: true }).first()).toBeVisible();
  await detailDialog.getByRole("button", { name: "Start", exact: true }).click();
  await doneWorkerSocket.requested;
  await expect
    .poll(async () => {
      const data = await graphQL(
        request,
        `query WaitingExecution($taskId: ID!) {
          taskA2AExecutions(taskId: $taskId) { remoteStatus lastSequence }
        }`,
        { taskId: doneTask.createTask.id },
      );
      return data.taskA2AExecutions[0];
    }, { timeout: 15000 })
    .toMatchObject({
      remoteStatus: "INPUT_REQUIRED",
      lastSequence: expect.any(Number),
    });
  await expect(
    detailDialog.getByText("WAITING_INPUT", { exact: true }).first(),
  ).toBeVisible({ timeout: 15000 });
  await expect(
    detailDialog
      .getByRole("region", { name: "A2A execution rounds" })
      .getByTestId("a2a-execution-round"),
  ).toContainText("INPUT_REQUIRED");
  await detailDialog.getByRole("button", { name: "Close", exact: true }).click();
  await expect(detailDialog).toBeHidden();

  await expect(
    taskInColumn(page, "In Progress", doneTask.createTask.title),
  ).toBeVisible({ timeout: 15000 });
  await expect(taskInColumn(page, "Ready", doneTask.createTask.title)).toBeHidden();
  await taskInColumn(page, "In Progress", doneTask.createTask.title)
    .getByRole("button")
    .click();
  await expect(detailDialog.getByRole("button", { name: "Approve", exact: true })).toBeVisible();
  await detailDialog.getByRole("button", { name: "Approve", exact: true }).click();
  await doneWorkerSocket.completed;
  await expect(
    detailDialog.getByText("COMPLETED", { exact: true }).first(),
  ).toBeVisible({ timeout: 15000 });
  await expect(activeDetailPane.getByText("board group completed", { exact: true })).toBeVisible();
  await expect(
    detailDialog
      .getByRole("region", { name: "A2A execution rounds" })
      .getByTestId("a2a-execution-round"),
  ).toContainText("COMPLETED");
  await detailDialog.getByRole("button", { name: "Close", exact: true }).click();
  await expect(detailDialog).toBeHidden();
  await expect(taskInColumn(page, "Done", doneTask.createTask.title)).toBeVisible({
    timeout: 15000,
  });

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
  await expect(taskLocator(page, completedBucketTask.createTask.title)).toBeHidden();
  await expect
    .poll(async () => {
      const data = await graphQL(
        request,
        "query DeletedTask($id: ID!) { task(id: $id) { id } }",
        { id: completedBucketTask.createTask.id },
      );
      return data.task;
    }, { timeout: 15000 })
    .toBeNull();

  await page.screenshot({
    path: testInfo.outputPath("board-status-groups.png"),
    fullPage: true,
  });

  workerSocket.close();
  doneWorkerSocket.close();
});
