import { expect, test } from "@playwright/test";
import WebSocket from "ws";

const managerGraphQL =
  process.env.BPT_MANAGER_GRAPHQL_URL || "http://localhost:8080/graphql";
const managerWorkerWs =
  process.env.BPT_MANAGER_WS_URL ||
  managerGraphQL.replace(/^http/, "ws").replace(/\/graphql$/, "/worker/ws");
const managerWorkerToken =
  process.env.BPT_MANAGER_WS_TOKEN ||
  process.env.WORKER_TOKEN ||
  "dev-worker-token";

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
  for (let pageAttempt = 0; pageAttempt < 10; pageAttempt++) {
    for (let scrollAttempt = 0; scrollAttempt < 20; scrollAttempt++) {
      if (await taskRow.isVisible().catch(() => false)) {
        await taskRow.click();
        return;
      }
      await page.mouse.wheel(0, 900);
      await page.waitForTimeout(150);
    }
    const nextPage = page.getByRole("button", { name: "Next page" });
    if (!(await nextPage.isEnabled().catch(() => false))) {
      break;
    }
    await nextPage.click();
    await page.waitForTimeout(300);
    await page.mouse.wheel(0, -5000);
  }
  await expect(taskRow).toBeVisible();
  await taskRow.click();
}

async function openWorkerEditor(page, workerName: string) {
  await page.getByText("Workers").click();
  await page.mouse.move(700, 520);
  const workerGroup = page.getByRole("group", { name: new RegExp(workerName) });
  for (let pageAttempt = 0; pageAttempt < 10; pageAttempt++) {
    for (let scrollAttempt = 0; scrollAttempt < 20; scrollAttempt++) {
      if (await workerGroup.isVisible().catch(() => false)) {
        await workerGroup.getByRole("button", { name: "Edit worker" }).click();
        return;
      }
      await page.mouse.wheel(0, 900);
      await page.waitForTimeout(150);
    }
    const nextPage = page.getByRole("button", { name: "Next page" });
    if (!(await nextPage.isEnabled().catch(() => false))) {
      break;
    }
    await nextPage.click();
    await page.waitForTimeout(300);
    await page.mouse.wheel(0, -5000);
  }
  await expect(workerGroup).toBeVisible();
  await workerGroup.getByRole("button", { name: "Edit worker" }).click();
}

async function fillFlutterTextField(page, input, value: string) {
  for (let attempt = 0; attempt < 3; attempt++) {
    await input.click();
    await page.waitForTimeout(100);
    await page.keyboard.press(
      process.platform === "darwin" ? "Meta+A" : "Control+A",
    );
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

function keyValuesToRecord(items: Array<{ key: string; value: string }> = []) {
  return Object.fromEntries(items.map((item) => [item.key, item.value]));
}

async function waitForTerminalWorker(request) {
  let selected: {
    id: string;
    name: string;
    status: string;
    supportedAgents: string[];
    capabilities: Array<{ key: string; value: string }>;
  } | null = null;
  await expect
    .poll(async () => {
      const data = await graphQL(
        request,
        `query Workers {
          workers {
            id name status supportedAgents capabilities { key value }
          }
        }`,
      );
      selected =
        data.workers.find(
          (worker: {
            id: string;
            name: string;
            status: string;
            supportedAgents: string[];
            capabilities: Array<{ key: string; value: string }>;
          }) => {
            const capabilities = keyValuesToRecord(worker.capabilities);
            return (
              worker.status === "ONLINE" &&
              worker.supportedAgents.includes("codex") &&
              capabilities.terminal_enabled === "true" &&
              Number(capabilities.terminal_port) > 0
            );
          },
        ) || null;
      return Boolean(selected);
    })
    .toBeTruthy();
  return selected!;
}

async function runTerminalCommand(taskId: string, marker: string) {
  const terminalURL = managerGraphQL
    .replace(/^http/, "ws")
    .replace(/\/graphql$/, `/terminal/tasks/${taskId}/ws`);
  const ws = new WebSocket(terminalURL);
  let output = "";
  await new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(() => {
      ws.close();
      reject(new Error(`timed out waiting for terminal output: ${output}`));
    }, 15000);
    ws.addEventListener(
      "open",
      () => {
        ws.send(
          JSON.stringify({
            type: "input",
            data: `pwd\necho ${marker}\nexit\n`,
          }),
        );
      },
      { once: true },
    );
    ws.addEventListener("message", (message) => {
      const event = JSON.parse(String(message.data));
      if (event.type === "output") {
        output += event.data;
      }
      if (event.type === "error") {
        clearTimeout(timeout);
        ws.close();
        reject(new Error(`terminal error: ${event.data}`));
      }
      if (event.type === "exit") {
        clearTimeout(timeout);
        ws.close();
        resolve();
      }
    });
    ws.addEventListener(
      "error",
      () => {
        clearTimeout(timeout);
        reject(new Error("terminal websocket failed"));
      },
      { once: true },
    );
  });
  expect(output).toContain(marker);
  return output;
}

function connectWorkerEvents(
  workerId: string,
  taskId: string,
  expectedEnv: Record<string, string> = {},
  expectedAgentConfig: Record<string, unknown> = {},
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
      if (Object.keys(expectedAgentConfig).length > 0) {
        expect(envelope.payload?.task?.agentConfig).toMatchObject(
          expectedAgentConfig,
        );
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
    ws.addEventListener(
      "error",
      () => {
        clearTimeout(timeout);
        reject(new Error("worker websocket failed"));
      },
      { once: true },
    );
  });
  return { ready, done };
}

function connectWorkerForContinuation(
  workerId: string,
  taskId: string,
  expectedAgentConfig: Record<string, unknown> = {},
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
  const firstDone = new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(
      () => reject(new Error("timed out waiting for TASK_START")),
      5000,
    );
    ws.addEventListener("message", (message) => {
      const envelope = JSON.parse(String(message.data));
      if (envelope.type !== "TASK_START") {
        return;
      }
      if (Object.keys(expectedAgentConfig).length > 0) {
        expect(envelope.payload?.task?.agentConfig).toMatchObject(
          expectedAgentConfig,
        );
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
    const timeout = setTimeout(
      () => reject(new Error("timed out waiting for TASK_CONTINUE")),
      15000,
    );
    ws.addEventListener("message", (message) => {
      const envelope = JSON.parse(String(message.data));
      if (envelope.type !== "TASK_CONTINUE") {
        return;
      }
      if (Object.keys(expectedAgentConfig).length > 0) {
        expect(envelope.payload?.task?.agentConfig).toMatchObject(
          expectedAgentConfig,
        );
      }
      expect(envelope.payload.agentSessionId).toBe("session-1");
      expect(envelope.payload.message).toBe("follow up from ui");
      expect(envelope.payload.worktreePath).toBe(
        "/tmp/e2e-continuation-worktree",
      );
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

function connectWorkerForInteraction(
  workerId: string,
  taskId: string,
  options: {
    kind?: string;
    title?: string;
    body?: string;
    rawPayload?: string;
    agentSessionId?: string;
    result?: string;
    log?: string;
    conversation?: string;
  } = {},
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
  const interactionKind = options.kind || "COMMAND_APPROVAL";
  const interactionTitle = options.title || "Approve command";
  const interactionBody = options.body || "Run make test before completing";
  const interactionRawPayload =
    options.rawPayload ||
    JSON.stringify({
      reason: "Need to run verification",
      cwd: "/tmp/e2e-interaction-worktree",
      command: "make test",
    });
  const agentSessionId = options.agentSessionId || "codex-thread-e2e";
  const resultText = options.result || "interaction e2e completed";
  const logText = options.log || "approved command executed";
  const conversationText =
    options.conversation || "interaction approved and task completed";
  const ready = new Promise<void>((resolve, reject) => {
    ws.addEventListener("open", () => resolve(), { once: true });
    ws.addEventListener(
      "error",
      () => reject(new Error("worker websocket failed")),
      { once: true },
    );
  });
  const requested = new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(
      () => reject(new Error("timed out waiting for interaction request")),
      8000,
    );
    ws.addEventListener("message", (message) => {
      const envelope = JSON.parse(String(message.data));
      if (envelope.type !== "TASK_START") {
        return;
      }
      send(`accepted-interaction-${taskId}`, "TASK_ACCEPTED");
      send(`started-interaction-${taskId}`, "TASK_STARTED", {
        taskId,
        content: "/tmp/e2e-interaction-worktree",
      });
      send(`interaction-request-${taskId}`, "TASK_INTERACTION_REQUEST", {
        interactionId: `interaction-${taskId}`,
        taskId,
        kind: interactionKind,
        title: interactionTitle,
        body: interactionBody,
        rawPayload: interactionRawPayload,
        agentSessionId,
      });
      clearTimeout(timeout);
      resolve();
    });
  });
  const completed = new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(
      () => reject(new Error("timed out waiting for interaction response")),
      15000,
    );
    ws.addEventListener("message", (message) => {
      const envelope = JSON.parse(String(message.data));
      if (envelope.type !== "TASK_INTERACTION_RESPONSE") {
        return;
      }
      expect(envelope.payload.interactionId).toBe(`interaction-${taskId}`);
      expect(envelope.payload.decision).toBe("APPROVE");
      send(`interaction-resolved-${taskId}`, "TASK_INTERACTION_RESOLVED", {
        interactionId: `interaction-${taskId}`,
        taskId,
      });
      send(`interaction-log-${taskId}`, "TASK_LOG", {
        taskId,
        stream: "stdout",
        content: logText,
      });
      send(`interaction-conversation-${taskId}`, "TASK_CONVERSATION", {
        taskId,
        content: conversationText,
        agentSessionId,
        metadata: { role: "assistant" },
      });
      send(`interaction-result-${taskId}`, "TASK_RESULT", {
        taskId,
        result: resultText,
        agentSessionId,
      });
      send(`interaction-completed-${taskId}`, "TASK_COMPLETED", {
        taskId,
        result: resultText,
        agentSessionId,
      });
      clearTimeout(timeout);
      setTimeout(() => {
        ws.close();
        resolve();
      }, 250);
    });
  });
  return { ready, requested, completed };
}

test("trusted Flutter web UI paginates board projects workers and events", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  const suffix = Date.now();

  const createdProject = await graphQL(
    request,
    "mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }",
    {
      input: {
        name: `Pagination Base Project ${suffix}`,
        gitUrl: "pagination-fixture",
        defaultBranch: "main",
        worktreeNamePrefix: "pagination",
      },
    },
  );
  let boardProjectId = createdProject.createProject.id;
  let projectName = `Pagination Base Project ${suffix}`;

  const otherProject = await graphQL(
    request,
    "mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id name } }",
    {
      input: {
        name: `Pagination Other Project ${suffix}`,
        gitUrl: "pagination-other-fixture",
        defaultBranch: "main",
        worktreeNamePrefix: "pagination-other",
      },
    },
  );
  for (let index = 0; index < 3; index++) {
    await graphQL(
      request,
      "mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id } }",
      {
        input: {
          title: `Project Filter Other Task ${suffix}-${index}`,
          projectId: otherProject.createProject.id,
          agentType: "codex",
        },
      },
    );
  }

  for (let index = 0; index < 21; index++) {
    const paginationProject = await graphQL(
      request,
      "mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }",
      {
        input: {
          name: `Pagination Project ${suffix}-${index}`,
          gitUrl: `pagination-project-${index}`,
          defaultBranch: "main",
          worktreeNamePrefix: `pagination-project-${index}`,
        },
      },
    );
    if (index === 20) {
      boardProjectId = paginationProject.createProject.id;
      projectName = `Pagination Project ${suffix}-${index}`;
    }
    await graphQL(
      request,
      "mutation RegisterWorker($input: RegisterWorkerInput!) { registerWorker(input: $input) { id } }",
      {
        input: {
          id: `worker-pagination-${suffix}-${index}`,
          name: `Pagination Worker ${suffix}-${index}`,
          supportedAgents: ["codex"],
          workDir: `/tmp/e2e-pagination-worker-${index}`,
          projectBindingMode: "ALL_PROJECTS",
        },
      },
    );
  }

  for (let index = 0; index < 21; index++) {
    await graphQL(
      request,
      "mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id } }",
      {
        input: {
          title: `Pagination Task ${suffix}-${index}`,
          projectId: boardProjectId,
          agentType: "codex",
        },
      },
    );
  }

  await page.goto("/");
  await expect(page.locator("flutter-view")).toBeVisible({ timeout: 30000 });
  await page.waitForTimeout(1500);
  await enableFlutterAccessibility(page);

  await expect(page.getByText(/Showing 1-20 of \d+/)).toBeVisible();
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Task ${suffix}-20`),
    }),
  ).toBeVisible();
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Task ${suffix}-0`),
    }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "Next page" }).click();
  await expect(page.getByText(/Showing 21-\d+ of \d+/)).toBeVisible();
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Task ${suffix}-0`),
    }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Previous page" }).click();
  await expect(page.getByText(/Showing 1-20 of \d+/)).toBeVisible();
  const boardSearch = page.getByRole("textbox", { name: /Search/ });
  await fillFlutterTextField(page, boardSearch, `Pagination Task ${suffix}`);
  await expect(page.getByText(/Showing 1-20 of 21/)).toBeVisible();
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Task ${suffix}-20`),
    }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Sort descending" }).click();
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Task ${suffix}-0`),
    }),
  ).toBeVisible();
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Task ${suffix}-20`),
    }),
  ).toHaveCount(0);
  await fillFlutterTextField(
    page,
    boardSearch,
    `Pagination Task ${suffix}-20`,
  );
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Task ${suffix}-20`),
    }),
  ).toBeVisible();
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Task ${suffix}-0`),
    }),
  ).toHaveCount(0);

  await fillFlutterTextField(page, boardSearch, "");
  await page.getByRole("button", { name: /Project All projects/ }).click();
  await page.getByRole("menuitem", { name: projectName }).click();
  await expect(page.getByText(/Showing 1-20 of 21/)).toBeVisible();
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Task ${suffix}-0`),
    }),
  ).toBeVisible();
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Project Filter Other Task ${suffix}-0`),
    }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "Next page" }).click();
  await expect(page.getByText(/Showing 21-21 of 21/)).toBeVisible();
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Task ${suffix}-20`),
    }),
  ).toBeVisible();
  await fillFlutterTextField(page, boardSearch, `Pagination Task ${suffix}`);
  await page.getByRole("button", { name: "Sort ascending" }).click();
  await expect(page.getByText(/Showing 1-20 of 21/)).toBeVisible();
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Task ${suffix}-20`),
    }),
  ).toBeVisible();
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Project Filter Other Task ${suffix}-0`),
    }),
  ).toHaveCount(0);

  await page.getByText("Projects").click();
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Project ${suffix}-20`),
    }),
  ).toBeVisible();
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Project ${suffix}-0`),
    }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "Next page" }).click();
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Project ${suffix}-0`),
    }),
  ).toBeVisible();
  const projectsSearch = page.getByRole("textbox", { name: /Search/ });
  await fillFlutterTextField(
    page,
    projectsSearch,
    `Pagination Project ${suffix}-20`,
  );
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Project ${suffix}-20`),
    }),
  ).toBeVisible();
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Project ${suffix}-0`),
    }),
  ).toHaveCount(0);

  await page.getByText("Workers").click();
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Worker ${suffix}-20`),
    }),
  ).toBeVisible();
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Worker ${suffix}-0`),
    }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "Next page" }).click();
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Worker ${suffix}-0`),
    }),
  ).toBeVisible();
  const workersSearch = page.getByRole("textbox", { name: /Search/ });
  await fillFlutterTextField(
    page,
    workersSearch,
    `Pagination Worker ${suffix}-20`,
  );
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Worker ${suffix}-20`),
    }),
  ).toBeVisible();
  await expect(
    page.getByRole("group", {
      name: new RegExp(`Pagination Worker ${suffix}-0`),
    }),
  ).toHaveCount(0);

  await page.getByText("Events").click();
  await expect(page.getByText(/Showing 1-20 of \d+/)).toBeVisible();
  await page.getByRole("button", { name: "Next page" }).click();
  await expect(page.getByText(/Showing 21-40 of \d+/)).toBeVisible();
  await page.getByRole("button", { name: "Previous page" }).click();
  await expect(page.getByText(/Showing 1-20 of \d+/)).toBeVisible();
  const eventsSearch = page.getByRole("textbox", { name: /Search/ });
  await fillFlutterTextField(
    page,
    eventsSearch,
    `Pagination Project ${suffix}-20`,
  );
  await expect(
    page.getByText(new RegExp(`Pagination Project ${suffix}-20`)),
  ).toBeVisible();
  await expect(
    page.getByText(new RegExp(`Pagination Project ${suffix}-0`)),
  ).toHaveCount(0);
});

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
  const expectedCodexPayloadConfig = {
    workMode: "implement",
    codex: {
      model: "gpt-5.4",
      reasoningEffort: "high",
      sandboxMode: "workspace-write",
      approvalPolicy: "never",
      fullAuto: true,
    },
  };

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
          (group: {
            agentType: string;
            vars: Array<{ key: string; valueMasked: string }>;
          }) => [group.agentType, group.vars],
        ),
      );
    })
    .toEqual({
      codex: [{ key: "BPT_E2E_AGENT_ENV", valueMasked: "********" }],
      claude: [{ key: "BPT_E2E_CLAUDE_ENV", valueMasked: "********" }],
    });

  const createdTask = await graphQL(
    request,
    `mutation CreateTask($input: CreateTaskInput!) {
      createTask(input: $input) {
        id
        title
        status
        startDate
        endDate
        agentConfig {
          workMode
          codex { model reasoningEffort sandboxMode approvalPolicy fullAuto }
        }
      }
    }`,
    {
      input: {
        title: taskTitle,
        projectId: project.id,
        workerId,
        agentType: "codex",
        agentConfig: {
          workMode: "IMPLEMENT",
          codex: {
            model: "gpt-5.4",
            reasoningEffort: "HIGH",
            sandboxMode: "WORKSPACE_WRITE",
            approvalPolicy: "NEVER",
            fullAuto: true,
          },
        },
        baseBranch: "main",
        startDate: taskStartDate,
        endDate: taskEndDate,
      },
    },
  );
  expect(createdTask.createTask.status).toBe("ASSIGNED");
  expect(createdTask.createTask.startDate).toBe(taskStartDate);
  expect(createdTask.createTask.endDate).toBe(taskEndDate);
  expect(createdTask.createTask.agentConfig).toMatchObject({
    workMode: "IMPLEMENT",
    codex: {
      model: "gpt-5.4",
      reasoningEffort: "HIGH",
      sandboxMode: "WORKSPACE_WRITE",
      approvalPolicy: "NEVER",
      fullAuto: true,
    },
  });

  await expect
    .poll(async () => {
      const data = await graphQL(
        request,
        "query { tasks { nodes { title status startDate endDate } } }",
      );
      return data.tasks.nodes.some(
        (task: {
          title: string;
          status: string;
          startDate: string;
          endDate: string;
        }) =>
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
    (item: {
      id: string;
      title: string;
      status: string;
      startDate: string;
      endDate: string;
    }) => item.title === taskTitle,
  );
  expect(task).toBeTruthy();

  const workerSocket = connectWorkerEvents(
    workerId,
    task.id,
    {
      BPT_E2E_AGENT_ENV: "codex-value",
    },
    expectedCodexPayloadConfig,
  );
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
  await page.getByRole("button", { name: "Calendar" }).click();
  const calendarSearch = page.getByRole("textbox", { name: /Search/ });
  await expect(calendarSearch).toBeVisible();
  await fillFlutterTextField(page, calendarSearch, taskTitle);
  await expect(page.getByText("May 2026")).toBeVisible();
  await expect(
    page.getByRole("button", { name: new RegExp(taskTitle) }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Week", exact: true }).click();
  await expect(page.getByText("Apr 27, 2026 - May 3, 2026")).toBeVisible();
  await expect(
    page.getByRole("button", { name: new RegExp(taskTitle) }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Day", exact: true }).click();
  await expect(page.getByText("May 1, 2026")).toBeVisible();
  await expect(page.getByText("2026-05-01 - 2026-05-03")).toBeVisible();
  await page.getByRole("button", { name: "Year", exact: true }).click();
  await expect(page.getByText("2026", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("button", { name: new RegExp(taskTitle) }),
  ).toBeVisible();
  await openTaskFromList(page, taskTitle);
  await expect(page.getByText(projectName, { exact: true })).toBeVisible();
  await expect(page.getByText(project.id, { exact: true })).toHaveCount(0);
  await expect(page.getByText("2026-05-01 - 2026-05-03")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator("flutter-view")).toBeVisible();
});

test("task detail terminal runs commands in task worktree", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/");
  await expect(page.locator("flutter-view")).toBeVisible({ timeout: 30000 });
  await page.waitForTimeout(1500);
  await enableFlutterAccessibility(page);

  const suffix = Date.now();
  const marker = `BPT_TERMINAL_E2E_${suffix}`;
  const worker = await waitForTerminalWorker(request);
  const project = (
    await graphQL(
      request,
      "mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }",
      {
        input: {
          name: `Terminal Project ${suffix}`,
          gitUrl: "terminal-e2e-fixture",
          defaultBranch: "main",
          worktreeNamePrefix: `terminal-e2e-${suffix}`,
        },
      },
    )
  ).createProject;
  const task = (
    await graphQL(
      request,
      "mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id title } }",
      {
        input: {
          title: `Terminal Task ${suffix}`,
          projectId: project.id,
          workerId: worker.id,
          agentType: "codex",
          baseBranch: "main",
        },
      },
    )
  ).createTask;
  await graphQL(
    request,
    "mutation StartTask($taskId: ID!) { startTask(taskId: $taskId) { id status } }",
    { taskId: task.id },
  );
  let worktreePath = "";
  await expect
    .poll(async () => {
      const data = await graphQL(
        request,
        "query Task($id: ID!) { task(id: $id) { worktreePath } }",
        { id: task.id },
      );
      worktreePath = data.task.worktreePath || "";
      return worktreePath !== "";
    })
    .toBeTruthy();

  const output = await runTerminalCommand(task.id, marker);
  expect(output).toContain(worktreePath);

  await page.reload();
  await page.waitForTimeout(1500);
  await enableFlutterAccessibility(page);
  const terminalSocketURLs: string[] = [];
  page.on("websocket", (socket) => {
    terminalSocketURLs.push(socket.url());
  });
  await openTaskFromList(page, task.title);
  await page.getByRole("button", { name: "Terminal" }).click();
  await expect(page.getByText("Terminal unavailable")).toHaveCount(0);
  await page.getByRole("button", { name: "Connect worker terminal" }).click();
  await expect(page.getByText("Connected")).toBeVisible();
  await expect
    .poll(() =>
      terminalSocketURLs.some((url) =>
        url.includes(`/terminal/tasks/${task.id}/ws`),
      ),
    )
    .toBeTruthy();
});

test("task detail approves a live agent interaction and refreshes results", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/");
  await expect(page.locator("flutter-view")).toBeVisible({ timeout: 30000 });
  await page.waitForTimeout(1500);
  await enableFlutterAccessibility(page);

  const suffix = Date.now();
  const projectName = `E2E Interaction Project ${suffix}`;
  const taskTitle = `E2E Interaction Task ${suffix}`;
  const workerId = `worker-e2e-interaction-${suffix}`;

  const createdProject = await graphQL(
    request,
    "mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }",
    {
      input: {
        name: projectName,
        gitUrl: "e2e-fixture",
        defaultBranch: "main",
        worktreeNamePrefix: "e2e-interaction",
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
        name: `E2E Interaction Worker ${suffix}`,
        supportedAgents: ["codex"],
        workDir: "/tmp/e2e-interaction-worker",
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
  const workerSocket = connectWorkerForInteraction(workerId, taskId);
  await workerSocket.ready;
  await graphQL(
    request,
    "mutation StartTask($taskId: ID!) { startTask(taskId: $taskId) { id status } }",
    { taskId },
  );
  await workerSocket.requested;
  await expect
    .poll(async () => {
      const data = await graphQL(
        request,
        `query TaskInteraction($taskId: ID!) {
          task(id: $taskId) { status }
          taskInteractions(taskId: $taskId, status: PENDING) { id title kind }
        }`,
        { taskId },
      );
      return {
        status: data.task.status,
        interactions: data.taskInteractions,
      };
    })
    .toMatchObject({
      status: "WAITING_INPUT",
      interactions: [
        expect.objectContaining({
          title: "Approve command",
          kind: "COMMAND_APPROVAL",
        }),
      ],
    });

  await page.reload();
  await page.waitForTimeout(1500);
  await enableFlutterAccessibility(page);
  await openTaskFromList(page, taskTitle);
  await expect(
    page.getByRole("textbox", { name: /Approve command/ }),
  ).toBeVisible();
  await expect(page.getByRole("textbox", { name: /make test/ })).toBeVisible();
  await page.getByRole("button", { name: "Approve", exact: true }).click();

  await workerSocket.completed;
  await expect
    .poll(async () => {
      const [taskData, logData, conversationData, interactionData, eventData] =
        await Promise.all([
          graphQL(
            request,
            "query Task($id: ID!) { task(id: $id) { status result agentSessionId } }",
            { id: taskId },
          ),
          graphQL(
            request,
            "query TaskLogs($taskId: ID!) { taskLogs(taskId: $taskId) { content } }",
            { taskId },
          ),
          graphQL(
            request,
            "query TaskConversations($taskId: ID!) { taskConversations(taskId: $taskId) { content } }",
            { taskId },
          ),
          graphQL(
            request,
            "query TaskInteractions($taskId: ID!) { taskInteractions(taskId: $taskId) { status responseDecision } }",
            { taskId },
          ),
          graphQL(
            request,
            "query TaskEvents($taskId: ID!) { taskEvents(taskId: $taskId) { eventType } }",
            { taskId },
          ),
        ]);
      return {
        task: taskData.task,
        hasLog: logData.taskLogs.some((log: { content: string }) =>
          log.content.includes("approved command executed"),
        ),
        hasConversation: conversationData.taskConversations.some(
          (message: { content: string }) =>
            message.content.includes("interaction approved"),
        ),
        interactions: interactionData.taskInteractions,
        eventTypes: eventData.taskEvents.map(
          (event: { eventType: string }) => event.eventType,
        ),
      };
    })
    .toMatchObject({
      task: {
        status: "COMPLETED",
        result: "interaction e2e completed",
        agentSessionId: "codex-thread-e2e",
      },
      hasLog: true,
      hasConversation: true,
      interactions: [
        expect.objectContaining({
          status: "ANSWERED",
          responseDecision: "APPROVE",
        }),
      ],
      eventTypes: expect.arrayContaining([
        "TaskInteractionRequested",
        "TaskResumed",
        "TaskCompleted",
      ]),
    });
});

test("claude task detail waits for permission interaction before completion", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/");
  await expect(page.locator("flutter-view")).toBeVisible({ timeout: 30000 });
  await page.waitForTimeout(1500);
  await enableFlutterAccessibility(page);

  const suffix = Date.now();
  const taskTitle = `E2E Claude Interaction Task ${suffix}`;
  const workerId = `worker-e2e-claude-interaction-${suffix}`;
  const project = (
    await graphQL(
      request,
      "mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }",
      {
        input: {
          name: `E2E Claude Interaction Project ${suffix}`,
          gitUrl: "e2e-fixture",
          defaultBranch: "main",
          worktreeNamePrefix: "e2e-claude-interaction",
        },
      },
    )
  ).createProject;
  await graphQL(
    request,
    "mutation RegisterWorker($input: RegisterWorkerInput!) { registerWorker(input: $input) { id status } }",
    {
      input: {
        id: workerId,
        name: `E2E Claude Interaction Worker ${suffix}`,
        supportedAgents: ["claude"],
        workDir: "/tmp/e2e-claude-interaction-worker",
        projectBindingMode: "ALL_PROJECTS",
      },
    },
  );
  const taskId = (
    await graphQL(
      request,
      "mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id } }",
      {
        input: {
          title: taskTitle,
          projectId: project.id,
          workerId,
          agentType: "claude",
          baseBranch: "main",
        },
      },
    )
  ).createTask.id;
  const workerSocket = connectWorkerForInteraction(workerId, taskId, {
    kind: "FILE_APPROVAL",
    title: "Approve Claude file change",
    body: "Claude needs permission to edit README.md",
    rawPayload: JSON.stringify({
      tool_name: "Edit",
      tool_use_id: "toolu_e2e",
      tool_input: {
        file_path: "/tmp/e2e-claude-interaction-worktree/README.md",
        old_string: "old",
        new_string: "new",
      },
    }),
    agentSessionId: "claude-session-e2e",
    result: "claude interaction e2e completed",
    log: "approved claude edit executed",
    conversation: "claude interaction approved",
  });
  await workerSocket.ready;
  await graphQL(
    request,
    "mutation StartTask($taskId: ID!) { startTask(taskId: $taskId) { id status } }",
    { taskId },
  );
  await workerSocket.requested;

  await expect
    .poll(async () => {
      const data = await graphQL(
        request,
        `query TaskInteraction($taskId: ID!) {
          task(id: $taskId) { status agentType }
          taskInteractions(taskId: $taskId, status: PENDING) { title kind rawPayload }
        }`,
        { taskId },
      );
      return {
        task: data.task,
        interactions: data.taskInteractions,
      };
    })
    .toMatchObject({
      task: { status: "WAITING_INPUT", agentType: "claude" },
      interactions: [
        expect.objectContaining({
          title: "Approve Claude file change",
          kind: "FILE_APPROVAL",
        }),
      ],
    });

  await page.reload();
  await page.waitForTimeout(1500);
  await enableFlutterAccessibility(page);
  await openTaskFromList(page, taskTitle);
  await expect(
    page.getByRole("textbox", { name: /Approve Claude file change/ }),
  ).toBeVisible();
  await expect(page.getByRole("textbox", { name: /README.md/ })).toBeVisible();
  await expect(page.getByRole("textbox", { name: /Tool: Edit/ })).toBeVisible();
  await page.getByRole("button", { name: "Approve", exact: true }).click();

  await workerSocket.completed;
  await expect
    .poll(async () => {
      const data = await graphQL(
        request,
        `query Verify($id: ID!, $taskId: ID!) {
          task(id: $id) { status result agentSessionId }
          taskInteractions(taskId: $taskId) { status responseDecision }
        }`,
        { id: taskId, taskId },
      );
      return data;
    })
    .toMatchObject({
      task: {
        status: "COMPLETED",
        result: "claude interaction e2e completed",
        agentSessionId: "claude-session-e2e",
      },
      taskInteractions: [
        expect.objectContaining({
          status: "ANSWERED",
          responseDecision: "APPROVE",
        }),
      ],
    });
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
  const expectedContinuationConfig = {
    workMode: "review",
    codex: {
      model: "gpt-5.4-mini",
      reasoningEffort: "medium",
      approvalPolicy: "on-request",
    },
  };

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
        agentConfig: {
          workMode: "REVIEW",
          codex: {
            model: "gpt-5.4-mini",
            reasoningEffort: "MEDIUM",
            approvalPolicy: "ON_REQUEST",
          },
        },
        baseBranch: "main",
      },
    },
  );
  const taskId = createdTask.createTask.id;

  const workerSocket = connectWorkerForContinuation(
    workerId,
    taskId,
    expectedContinuationConfig,
  );
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
  await expect(
    page.getByRole("button", { name: /^Conversation$/ }),
  ).toBeVisible();
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
        expect.objectContaining({
          role: "assistant",
          content: "first agent response",
        }),
        expect.objectContaining({ role: "user", content: "follow up from ui" }),
        expect.objectContaining({
          role: "assistant",
          content: "second agent response",
        }),
      ]),
    });
});
