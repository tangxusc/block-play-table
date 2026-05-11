import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";
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
const managerAccessToken =
  process.env.BPT_MANAGER_TOKEN ||
  process.env.WORKER_TOKEN ||
  "dev-worker-token";

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
  await expect(page.locator("#app[data-ready='true']")).toBeVisible({
    timeout: 120000,
  });
}

function withManagerAccessToken(rawURL: string) {
  const url = new URL(rawURL);
  url.searchParams.set("token", managerAccessToken);
  return url.toString();
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

async function selectTaskDetailTab(page, name: string) {
  await page.getByRole("tab", { name, exact: true }).click();
}

async function expectIconOnlyTitleAction(page, name: string) {
  const button = page.getByRole("button", { name, exact: true }).first();
  await expect(button).toBeVisible();
  await expect(button).toHaveText(/^\s*$/);
  await button.hover();
  await expect(page.getByRole("tooltip", { name, exact: true })).toBeVisible();
  await page.mouse.move(0, 0);
}

async function expectDetailCardsDoNotOverflow(page) {
  await expect
    .poll(async () =>
      page.getByRole("dialog").evaluate((dialog: HTMLElement) => {
        const dialogRight = dialog.getBoundingClientRect().right;
        return Array.from(
          dialog.querySelectorAll<HTMLElement>(
            ".record-card, .record-title, .record-subtitle",
          ),
        )
          .filter((element) => element.offsetParent !== null)
          .filter((element) => {
            const rect = element.getBoundingClientRect();
            return (
              element.scrollWidth > element.clientWidth + 1 ||
              rect.right > dialogRight + 1
            );
          })
          .map((element) => ({
            className: element.className,
            scrollWidth: element.scrollWidth,
            clientWidth: element.clientWidth,
            text: element.textContent?.slice(0, 80),
          }));
      }),
    )
    .toEqual([]);
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

async function fillTextField(page, input, value: string) {
  for (let attempt = 0; attempt < 3; attempt++) {
    await input.click();
    await page.waitForTimeout(100);
    await input.fill(value);
    await page.waitForTimeout(100);
    if ((await input.inputValue()) === value) {
      return;
    }
  }
  await expect(input).toHaveValue(value);
}

async function selectFieldOption(page, field, optionName: string) {
  await field.click();
  const roleOption = page.getByRole("option", { name: optionName });
  if (await roleOption.isVisible({ timeout: 1000 }).catch(() => false)) {
    await roleOption.click();
    return;
  }
  await page.getByText(optionName, { exact: true }).last().click();
}

async function addWorkerEnvVar(page, key: string, value: string) {
  await page.getByRole("button", { name: "New env var" }).click();
  await expect(page.getByText("Create env var")).toBeVisible();
  const keyInput = page.getByLabel("Key").last();
  await fillTextField(page, keyInput, key);
  const valueInput = page.getByLabel("Value").last();
  await fillTextField(page, valueInput, value);
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
    workDir: string;
    supportedAgents: string[];
    capabilities: Array<{ key: string; value: string }>;
  } | null = null;
  await expect
    .poll(async () => {
      const data = await graphQL(
        request,
        `query Workers {
          workers {
            id name status workDir supportedAgents capabilities { key value }
          }
        }`,
      );
      selected =
        data.workers.find(
          (worker: {
            id: string;
            name: string;
            status: string;
            workDir: string;
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
    }, { timeout: 15000 })
    .toBeTruthy();
  return selected!;
}

async function waitForReviewWorker(request) {
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
              capabilities.review_enabled === "true" &&
              Number(capabilities.review_port) > 0
            );
          },
        ) || null;
      return Boolean(selected);
    })
    .toBeTruthy();
  return selected!;
}

function runGit(cwd: string, args: string[]) {
  return execFileSync("git", args, {
    cwd,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  }).trim();
}

function runWorkerGit(cwd: string, args: string[]) {
  if (
    process.env.BPT_WORKER_EXEC === "wsl" ||
    (process.platform === "win32" && cwd.startsWith("/"))
  ) {
    return execFileSync("wsl", [
      "-d",
      process.env.BPT_WSL_DISTRO || "Ubuntu-24.04",
      "--",
      "git",
      "-C",
      cwd,
      ...args,
    ], {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();
  }
  return execFileSync("git", ["-C", cwd, ...args], {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  }).trim();
}

function workerPathFromHostPath(hostPath: string) {
  const configured = process.env.BPT_WORKER_DATA_WORKER_PATH;
  if (configured) {
    const workerRoot = configured.replace(/\/+$/, "");
    const hostRoot = path.resolve(
      process.env.BPT_WORKER_DATA_SOURCE ||
        process.env.WORKER_DATA_SOURCE ||
        "worker-data",
    );
    const relative = path.relative(hostRoot, hostPath).replace(/\\/g, "/");
    return relative ? `${workerRoot}/${relative}` : workerRoot;
  }
  const resolved = path.resolve(hostPath);
  if (process.platform === "win32") {
    const drive = resolved.slice(0, 1).toLowerCase();
    return `/mnt/${drive}${resolved.slice(2).replace(/\\/g, "/")}`;
  }
  return resolved;
}

function hostPathFromWorkerPath(workerPath: string, hostRoot: string, workerRoot: string) {
  const normalizedWorkerRoot = workerRoot.replace(/\/+$/, "");
  if (workerPath.startsWith(normalizedWorkerRoot)) {
    const relative = workerPath.slice(normalizedWorkerRoot.length).replace(/^\/+/, "");
    return path.join(hostRoot, ...relative.split("/").filter(Boolean));
  }
  const wslMatch = workerPath.match(/^\/mnt\/([a-z])\/(.*)$/i);
  if (wslMatch && process.platform === "win32") {
    return `${wslMatch[1].toUpperCase()}:\\${wslMatch[2].replace(/\//g, "\\")}`;
  }
  return workerPath;
}

function createReviewGitFixture(suffix: number) {
  const workerDataHost = path.resolve(
    process.env.BPT_WORKER_DATA_SOURCE ||
      process.env.WORKER_DATA_SOURCE ||
      "worker-data",
  );
  mkdirSync(workerDataHost, { recursive: true });
  const fixtureName = `review-git-${suffix}`;
  const fixtureHost = path.join(workerDataHost, fixtureName);
  const remoteHost = path.join(fixtureHost, "remote.git");
  const seedHost = path.join(fixtureHost, "seed");
  const workerDataWorker = workerPathFromHostPath(workerDataHost);
  const remoteWorker = `${workerDataWorker}/${fixtureName}/remote.git`;
  mkdirSync(seedHost, { recursive: true });

  runGit(fixtureHost, ["init", "--bare", remoteHost]);
  runGit(seedHost, ["init", "-b", "main"]);
  runGit(seedHost, ["config", "user.email", "bpt@example.test"]);
  runGit(seedHost, ["config", "user.name", "Block Play Table"]);
  writeFileSync(path.join(seedHost, "tracked.txt"), "base\n");
  runGit(seedHost, ["add", "tracked.txt"]);
  runGit(seedHost, ["commit", "-m", "base"]);
  runGit(seedHost, ["remote", "add", "origin", remoteHost]);
  runGit(seedHost, ["push", "origin", "main"]);

  return {
    remoteHost,
    seedHost,
    gitUrl: `file://${remoteWorker}`,
    hostPathForWorkerPath: (workerPath: string) =>
      hostPathFromWorkerPath(workerPath, workerDataHost, workerDataWorker),
  };
}

async function runTerminalCommand(
  taskId: string,
  marker: string,
  expectedPath: string,
) {
  const terminalURL = managerGraphQL
    .replace(/^http/, "ws")
    .replace(/\/graphql$/, `/terminal/tasks/${taskId}/ws`);
  const ws = new WebSocket(withManagerAccessToken(terminalURL));
  let output = "";
  let exitSent = false;
  await new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(() => {
      ws.close();
      reject(new Error(`timed out waiting for terminal output: ${output}`));
    }, 15000);
    ws.addEventListener("open", () => {
      ws.send(
        JSON.stringify({
          type: "input",
          data: `pwd\n`,
        }),
      );
    });
    ws.addEventListener("message", (message) => {
      const event = JSON.parse(String(message.data));
      if (event.type === "output") {
        output += event.data;
        if (!exitSent && output.includes(expectedPath)) {
          exitSent = true;
          ws.send(
            JSON.stringify({
              type: "input",
              data: `echo ${marker}\nexit\n`,
            }),
          );
        }
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

async function runWorkerTerminalCommand(
  workerId: string,
  marker: string,
  expectedPath: string,
) {
  const terminalURL = managerGraphQL
    .replace(/^http/, "ws")
    .replace(/\/graphql$/, `/terminal/workers/${workerId}/ws`);
  const ws = new WebSocket(withManagerAccessToken(terminalURL));
  let output = "";
  let exitSent = false;
  await new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(() => {
      ws.close();
      reject(new Error(`timed out waiting for terminal output: ${output}`));
    }, 15000);
    ws.addEventListener("open", () => {
      ws.send(
        JSON.stringify({
          type: "input",
          data: `pwd\n`,
        }),
      );
    });
    ws.addEventListener("message", (message) => {
      const event = JSON.parse(String(message.data));
      if (event.type === "output") {
        output += event.data;
        if (!exitSent && output.includes(expectedPath)) {
          exitSent = true;
          ws.send(
            JSON.stringify({
              type: "input",
              data: `echo ${marker}\nexit\n`,
            }),
          );
        }
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

async function openWorkerTerminal(page, workerName: string) {
  await page.getByText("Workers").click();
  await fillTextField(
    page,
    page.getByRole("textbox", { name: /Search/ }),
    workerName,
  );
  await page.mouse.move(700, 520);
  const workerGroup = page.getByRole("group", { name: new RegExp(workerName) });
  for (let pageAttempt = 0; pageAttempt < 10; pageAttempt++) {
    for (let scrollAttempt = 0; scrollAttempt < 20; scrollAttempt++) {
      if (await workerGroup.isVisible().catch(() => false)) {
        await workerGroup.getByRole("button", { name: "Open worker terminal" }).click();
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
  await workerGroup.getByRole("button", { name: "Open worker terminal" }).click();
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
        content: `hello from e2e worker ${"x".repeat(240)}`,
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

function connectWorkerConversationOnly(
  workerId: string,
  taskId: string,
  conversation: string,
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
      send(`accepted-conversation-${taskId}`, "TASK_ACCEPTED");
      send(`started-conversation-${taskId}`, "TASK_STARTED", {
        taskId,
        content: "/tmp/e2e-conversation-worktree",
      });
      send(`conversation-only-${taskId}`, "TASK_CONVERSATION", {
        taskId,
        content: conversation,
        metadata: { role: "assistant" },
      });
      send(`result-conversation-${taskId}`, "TASK_RESULT", {
        taskId,
        result: conversation,
      });
      send(`completed-conversation-${taskId}`, "TASK_COMPLETED", {
        taskId,
        result: conversation,
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

test("trusted Vue web UI paginates board projects workers and events", async ({
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

  await openVueApp(page);

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
  await fillTextField(page, boardSearch, `Pagination Task ${suffix}`);
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
  await fillTextField(
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

  await fillTextField(page, boardSearch, "");
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
  await fillTextField(page, boardSearch, `Pagination Task ${suffix}`);
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
  await fillTextField(
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

  const uiProjectName = `UI Created Project ${suffix}`;
  await fillTextField(page, projectsSearch, "");
  await page.getByRole("button", { name: "New project" }).click();
  const createProjectDialog = page.getByRole("dialog", {
    name: "Create project",
  });
  await fillTextField(page, createProjectDialog.getByLabel("Name"), uiProjectName);
  await fillTextField(
    page,
    createProjectDialog.getByLabel("Git URL"),
    "git@github.com:tangxusc/block-play-table-test-repo.git",
  );
  await fillTextField(page, createProjectDialog.getByLabel("Default branch"), "main");
  await fillTextField(page, createProjectDialog.getByLabel("Worktree prefix"), "task");
  await createProjectDialog.getByRole("button", { name: "Save" }).click();
  await expect(createProjectDialog).toBeHidden();
  await expect(
    page.getByRole("group", { name: new RegExp(uiProjectName) }),
  ).toBeVisible({ timeout: 15000 });

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
  await fillTextField(
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
  await fillTextField(
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

test("task create and detail assignment expose worker controls", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await openVueApp(page);

  const suffix = Date.now();
  const worker = await waitForTerminalWorker(request);
  const projectName = `UI Assign Project ${suffix}`;
  const createdProject = await graphQL(
    request,
    "mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id name } }",
    {
      input: {
        name: projectName,
        gitUrl: "assign-ui-fixture",
        defaultBranch: "main",
        worktreeNamePrefix: "assign-ui",
      },
    },
  );
  const project = createdProject.createProject;

  const unassignedViaUiTitle = `UI Unassigned Task ${suffix}`;
  await openVueApp(page);
  await page.getByRole("button", { name: "New task" }).click();
  let createTaskDialog = page.getByRole("dialog", { name: "Create task" });
  await expect(createTaskDialog.getByLabel("Worker")).toBeVisible();
  await fillTextField(page, createTaskDialog.getByLabel("Title"), unassignedViaUiTitle);
  await selectFieldOption(page, createTaskDialog.getByLabel("Project"), projectName);
  await expect(createTaskDialog.getByLabel("Agent")).toHaveCount(0);
  await expect(createTaskDialog.getByText("Agent runtime parameters")).toHaveCount(0);
  await createTaskDialog.getByRole("button", { name: "Save" }).click();
  await expect(createTaskDialog).toBeHidden();
  await expect
    .poll(async () => {
      const data = await graphQL(
        request,
        `query Tasks($filter: TaskFilter) {
          tasks(filter: $filter) { nodes { title status workerId agentType } }
        }`,
        { filter: { search: unassignedViaUiTitle } },
      );
      return data.tasks.nodes[0];
    }, { timeout: 15000 })
    .toMatchObject({
      title: unassignedViaUiTitle,
      status: "CREATED",
      workerId: null,
      agentType: null,
    });

  const createdViaUiTitle = `UI Worker Selected Task ${suffix}`;
  await openVueApp(page);
  await page.getByRole("button", { name: "New task" }).click();
  createTaskDialog = page.getByRole("dialog", { name: "Create task" });
  await expect(createTaskDialog.getByLabel("Worker")).toBeVisible();
  await expect(createTaskDialog.getByLabel("Agent")).toHaveCount(0);
  await expect(createTaskDialog.getByText("Agent runtime parameters")).toHaveCount(0);
  await fillTextField(page, createTaskDialog.getByLabel("Title"), createdViaUiTitle);
  await selectFieldOption(page, createTaskDialog.getByLabel("Project"), projectName);
  await selectFieldOption(page, createTaskDialog.getByLabel("Worker"), worker.name);
  await expect(createTaskDialog.getByLabel("Agent")).toBeVisible();
  await selectFieldOption(page, createTaskDialog.getByLabel("Agent"), "Codex");
  await expect(createTaskDialog.getByText("Agent runtime parameters")).toBeVisible();
  await selectFieldOption(page, createTaskDialog.getByLabel("Work mode"), "Implement");
  await fillTextField(page, createTaskDialog.getByLabel("Codex model"), "gpt-5.4");
  await selectFieldOption(page, createTaskDialog.getByLabel("Reasoning effort"), "High");
  await selectFieldOption(page, createTaskDialog.getByLabel("Sandbox", { exact: true }), "Workspace write");
  await selectFieldOption(page, createTaskDialog.getByLabel("Approval", { exact: true }), "Never");
  await createTaskDialog.getByLabel("Full auto").check();
  await createTaskDialog.getByRole("button", { name: "Save" }).click();
  await expect(createTaskDialog).toBeHidden();

  await expect
    .poll(async () => {
      const data = await graphQL(
        request,
        `query Tasks($filter: TaskFilter) {
          tasks(filter: $filter) {
            nodes {
              title status workerId agentType
              agentConfig {
                workMode
                codex { model reasoningEffort sandboxMode approvalPolicy fullAuto }
              }
            }
          }
        }`,
        { filter: { search: createdViaUiTitle } },
      );
      return data.tasks.nodes[0];
    }, { timeout: 15000 })
    .toMatchObject({
      title: createdViaUiTitle,
      status: "ASSIGNED",
      workerId: worker.id,
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
    });

  const unassignedTitle = `UI Assign Later Task ${suffix}`;
  const createdTask = await graphQL(
    request,
    "mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id title } }",
    {
      input: {
        title: unassignedTitle,
        projectId: project.id,
        baseBranch: "main",
      },
    },
  );

  await openVueApp(page);
  await openTaskFromList(page, createdTask.createTask.title);
  for (const actionName of ["Copy task ID", "Assign", "Start", "Retry", "Archive", "Close"]) {
    await expectIconOnlyTitleAction(page, actionName);
  }
  await page.getByRole("button", { name: "Assign", exact: true }).click();
  const assignDialog = page.getByRole("dialog", { name: "Assign worker" });
  await expect(assignDialog.getByLabel("Worker")).toBeVisible();
  await expect(assignDialog.getByLabel("Agent")).toHaveCount(0);
  await expect(assignDialog.getByText("Agent runtime parameters")).toHaveCount(0);
  await selectFieldOption(page, assignDialog.getByLabel("Worker"), worker.name);
  await expect(assignDialog.getByLabel("Agent")).toBeVisible();
  await selectFieldOption(page, assignDialog.getByLabel("Agent"), "Codex");
  await expect(assignDialog.getByText("Agent runtime parameters")).toBeVisible();
  await selectFieldOption(page, assignDialog.getByLabel("Work mode"), "Review");
  await fillTextField(page, assignDialog.getByLabel("Codex model"), "gpt-5.4-mini");
  await selectFieldOption(page, assignDialog.getByLabel("Reasoning effort"), "Medium");
  await selectFieldOption(page, assignDialog.getByLabel("Approval", { exact: true }), "On request");
  await assignDialog.getByRole("button", { name: "Assign", exact: true }).click();
  await expect(assignDialog).toBeHidden();
  await expect
    .poll(async () => {
      const data = await graphQL(
        request,
        `query Task($id: ID!) {
          task(id: $id) {
            status workerId agentType
            agentConfig {
              workMode
              codex { model reasoningEffort approvalPolicy }
            }
          }
        }`,
        { id: createdTask.createTask.id },
      );
      return data.task;
    }, { timeout: 15000 })
    .toMatchObject({
      status: "ASSIGNED",
      workerId: worker.id,
      agentType: "codex",
      agentConfig: {
        workMode: "REVIEW",
        codex: {
          model: "gpt-5.4-mini",
          reasoningEffort: "MEDIUM",
          approvalPolicy: "ON_REQUEST",
        },
      },
    });
});

test("trusted Vue web UI covers DDD event-backed task flow", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await openVueApp(page);
  await expect(page).toHaveTitle("Block Play Table");

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
        agentRuntimeEnv: [
          {
            agentType: "codex",
            vars: [
              {
                key: "BPT_EXISTING_PUBLIC_ENV",
                value: "keep-me",
                description: "existing public value",
                enabled: true,
                sensitive: false,
              },
            ],
          },
        ],
      },
    },
  );

  await openVueApp(page);
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
      codex: [
        { key: "BPT_E2E_AGENT_ENV", valueMasked: "********" },
        { key: "BPT_EXISTING_PUBLIC_ENV", valueMasked: "keep-me" },
      ],
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
    }, { timeout: 15000 })
    .toBeTruthy();

  await openVueApp(page);
  await page.getByRole("button", { name: "Calendar" }).click();
  const calendarSearch = page.getByRole("textbox", { name: /Search/ });
  await expect(calendarSearch).toBeVisible();
  await fillTextField(page, calendarSearch, taskTitle);
  await expect(page.getByText("May 2026")).toBeVisible();
  await expect(page.locator(".calendar-grid.calendar-month-grid")).toBeVisible();
  await expect(page.locator(".calendar-grid.calendar-month-grid .calendar-cell")).toHaveCount(35);
  await expect(
    page.getByRole("button", { name: new RegExp(taskTitle) }).first(),
  ).toBeVisible();
  await page.getByRole("button", { name: "Week", exact: true }).click();
  await expect(page.getByText("Apr 27, 2026 - May 3, 2026")).toBeVisible();
  await expect(page.locator(".calendar-grid.calendar-week-grid")).toBeVisible();
  await expect(page.locator(".calendar-grid.calendar-week-grid .calendar-cell")).toHaveCount(7);
  await expect(
    page.getByRole("button", { name: new RegExp(taskTitle) }).first(),
  ).toBeVisible();
  await page.getByRole("button", { name: "Day", exact: true }).click();
  await expect(page.getByText("May 1, 2026").first()).toBeVisible();
  await expect(page.locator(".calendar-day-view")).toBeVisible();
  await expect(page.getByText("2026-05-01 - 2026-05-03")).toBeVisible();
  await page.getByRole("button", { name: "Year", exact: true }).click();
  await expect(page.getByText("2026", { exact: true })).toBeVisible();
  await expect(page.locator(".calendar-year-grid")).toBeVisible();
  await expect(
    page.getByRole("button", { name: new RegExp(taskTitle) }).first(),
  ).toBeVisible();
  await openTaskFromList(page, taskTitle);
  for (const tabName of ["Conversation", "Logs", "Review", "Web preview", "Terminal"]) {
    await expect(page.getByRole("tab", { name: tabName, exact: true })).toHaveCount(1);
    await expect(page.getByRole("button", { name: tabName, exact: true })).toHaveCount(0);
  }
  await expectIconOnlyTitleAction(page, "Copy task ID");
  await expect(page.getByRole("button", { name: "Start", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Retry", exact: true })).toBeVisible();
  await selectTaskDetailTab(page, "Web preview");
  await fillTextField(page, page.getByLabel("Worker web address"), "localhost:4173/preview");
  await page.getByRole("button", { name: "Open worker web preview" }).click();
  await expect(page.locator('[data-testid="task-detail-worker-web-preview"]')).toBeVisible();
  await expect(page.getByText(/\/proxy\/web\//)).toBeVisible();
  await expect(page.getByText(projectName, { exact: true })).toBeVisible();
  await expect(page.getByText(project.id, { exact: true })).toHaveCount(0);
  await expect(page.getByText("2026-05-01 - 2026-05-03")).toBeVisible();
  await selectTaskDetailTab(page, "Logs");
  await expect(page.getByText(/hello from e2e worker/)).toBeVisible();
  await expectDetailCardsDoNotOverflow(page);
  await selectTaskDetailTab(page, "Domain events");
  await expect(page.getByText(/TaskLogAppended/)).toBeVisible();
  await expectDetailCardsDoNotOverflow(page);
  await page.keyboard.press("Escape");
  await expect(page.locator("#app")).toBeVisible();
});

test("task detail logs show conversation-only agent output", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  const suffix = Date.now();
  const projectName = `Conversation Log Project ${suffix}`;
  const taskTitle = `Conversation Log Task ${suffix}`;
  const workerId = `worker-conversation-log-${suffix}`;
  const workerName = `Conversation Log Worker ${suffix}`;
  const conversation = `assistant conversation-only log ${suffix}`;

  const project = (
    await graphQL(
      request,
      "mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id name } }",
      {
        input: {
          name: projectName,
          gitUrl: "conversation-log-fixture",
          defaultBranch: "main",
          worktreeNamePrefix: "conversation-log",
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
        name: workerName,
        supportedAgents: ["codex"],
        workDir: "/tmp/e2e-conversation-log-worker",
        projectBindingMode: "SPECIFIC_PROJECTS",
        boundProjectIds: [project.id],
      },
    },
  );
  const task = (
    await graphQL(
      request,
      "mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id title } }",
      {
        input: {
          title: taskTitle,
          projectId: project.id,
          workerId,
          agentType: "codex",
          baseBranch: "main",
        },
      },
    )
  ).createTask;

  const workerSocket = connectWorkerConversationOnly(workerId, task.id, conversation);
  await workerSocket.ready;
  await graphQL(
    request,
    "mutation StartTask($taskId: ID!) { startTask(taskId: $taskId) { id status } }",
    { taskId: task.id },
  );
  await workerSocket.done;

  await expect
    .poll(async () => {
      const data = await graphQL(
        request,
        "query TaskLogs($taskId: ID!) { taskLogs(taskId: $taskId) { stream content } }",
        { taskId: task.id },
      );
      return data.taskLogs;
    }, { timeout: 15000 })
    .toEqual([{ stream: "assistant", content: conversation }]);

  await openVueApp(page);
  await openTaskFromList(page, taskTitle);
  await selectTaskDetailTab(page, "Logs");
  await expect(page.getByLabel("Logs").getByText(conversation)).toBeVisible();
  await expectDetailCardsDoNotOverflow(page);
});

test("task detail terminal runs commands in task worktree", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await openVueApp(page);

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

  const output = await runTerminalCommand(task.id, marker, worktreePath);
  expect(output).toContain(worktreePath);

  await openVueApp(page);
  const terminalSocketURLs: string[] = [];
  page.on("websocket", (socket) => {
    terminalSocketURLs.push(socket.url());
  });
  await openTaskFromList(page, task.title);
  await selectTaskDetailTab(page, "Terminal");
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

test("worker list terminal button opens a worker shell in the worker workdir", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await openVueApp(page);

  const suffix = Date.now();
  const marker = `BPT_WORKER_TERMINAL_E2E_${suffix}`;
  const worker = await waitForTerminalWorker(request);
  const workerTerminalSocketURLs: string[] = [];
  page.on("websocket", (socket) => {
    workerTerminalSocketURLs.push(socket.url());
  });

  await openWorkerTerminal(page, worker.name);
  const terminalDialog = page.getByRole("alertdialog");
  await expect(
    terminalDialog.getByText("Worker terminal", { exact: true }),
  ).toBeVisible();
  await terminalDialog
    .getByRole("button", { name: "Connect worker terminal" })
    .click();
  await expect(terminalDialog.getByText("Connected")).toBeVisible();
  await expect
    .poll(() =>
      workerTerminalSocketURLs.some((url) =>
        url.includes(`/terminal/workers/${worker.id}/ws`),
      ),
    )
    .toBeTruthy();

  const output = await runWorkerTerminalCommand(
    worker.id,
    marker,
    worker.workDir,
  );
  expect(output).toContain(worker.workDir);
});

test("task detail review git workflow commits and publishes staged changes", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await openVueApp(page);

  const suffix = Date.now();
  const fixture = createReviewGitFixture(suffix);
  const worker = await waitForReviewWorker(request);
  const project = (
    await graphQL(
      request,
      "mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }",
      {
        input: {
          name: `Review Git Project ${suffix}`,
          gitUrl: fixture.gitUrl,
          defaultBranch: "main",
          worktreeNamePrefix: `review-git-${suffix}`,
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
          title: `Review Git Task ${suffix}`,
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
      return worktreePath;
    })
    .not.toBe("");

  const worktreeHost = fixture.hostPathForWorkerPath(worktreePath);
  await expect
    .poll(() => existsSync(path.join(worktreeHost, "tracked.txt")))
    .toBeTruthy();
  runWorkerGit(worktreePath, ["config", "user.email", "bpt@example.test"]);
  runWorkerGit(worktreePath, ["config", "user.name", "Block Play Table"]);
  writeFileSync(path.join(worktreeHost, "tracked.txt"), "base\npublished\n");

  await openVueApp(page);
  await openTaskFromList(page, task.title);
  await selectTaskDetailTab(page, "Review");
  await page.getByLabel("Remote").fill("origin");
  await page.getByLabel("Target branch").fill("main");
  await expect(page.getByRole("button", { name: /tracked\.txt/ })).toBeVisible({
    timeout: 15000,
  });
  await page.getByRole("button", { name: "Stage", exact: true }).click();
  await expect
    .poll(() => runWorkerGit(worktreePath, ["diff", "--cached", "--name-only"]))
    .toBe("tracked.txt");
  await page.getByRole("button", { name: "Unstage", exact: true }).click();
  await expect
    .poll(() => runWorkerGit(worktreePath, ["diff", "--cached", "--name-only"]))
    .toBe("");
  await page.getByRole("button", { name: "Stage", exact: true }).click();
  await expect
    .poll(() => runWorkerGit(worktreePath, ["diff", "--cached", "--name-only"]))
    .toBe("tracked.txt");
  await page.getByRole("button", { name: "Fetch" }).click();
  await page.getByRole("button", { name: "Commit staged" }).click();
  await fillTextField(
    page,
    page.getByLabel("Commit message"),
    "review git publish",
  );
  await page.getByRole("button", { name: "Commit", exact: true }).click();
  await expect
    .poll(() => runWorkerGit(worktreePath, ["log", "-1", "--pretty=%s"]))
    .toBe("review git publish");

  await page.getByRole("button", { name: "Publish fast-forward" }).click();
  await page.getByRole("button", { name: "Publish", exact: true }).click();
  await expect
    .poll(() =>
      runGit(fixture.remoteHost, ["log", "-1", "--pretty=%s", "main"]),
    )
    .toBe("review git publish");
  expect(runGit(fixture.remoteHost, ["show", "main:tracked.txt"])).toContain(
    "published",
  );
});

test("task detail review git workflow fetches and rebases after remote main advances", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await openVueApp(page);

  const suffix = Date.now();
  const fixture = createReviewGitFixture(suffix);
  const worker = await waitForReviewWorker(request);
  const project = (
    await graphQL(
      request,
      "mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }",
      {
        input: {
          name: `Review Git Sync Project ${suffix}`,
          gitUrl: fixture.gitUrl,
          defaultBranch: "main",
          worktreeNamePrefix: `review-git-sync-${suffix}`,
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
          title: `Review Git Sync Task ${suffix}`,
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
      return worktreePath;
    })
    .not.toBe("");

  const worktreeHost = fixture.hostPathForWorkerPath(worktreePath);
  await expect
    .poll(() => existsSync(path.join(worktreeHost, "tracked.txt")))
    .toBeTruthy();

  runGit(fixture.seedHost, ["config", "user.email", "bpt@example.test"]);
  runGit(fixture.seedHost, ["config", "user.name", "Block Play Table"]);
  writeFileSync(path.join(fixture.seedHost, "tracked.txt"), "base\nremote\n");
  runGit(fixture.seedHost, ["add", "tracked.txt"]);
  runGit(fixture.seedHost, ["commit", "-m", "remote main update"]);
  runGit(fixture.seedHost, ["push", "origin", "main"]);

  await openVueApp(page);
  await openTaskFromList(page, task.title);
  await selectTaskDetailTab(page, "Review");
  await page.getByLabel("Remote").fill("origin");
  await page.getByLabel("Target branch").fill("main");

  await page.getByRole("button", { name: "Fetch" }).click();
  await expect(page.getByText("behind 1")).toBeVisible({ timeout: 15000 });

  await page.getByRole("button", { name: "Rebase onto target" }).click();
  await expect
    .poll(() => runWorkerGit(worktreePath, ["log", "-1", "--pretty=%s"]))
    .toBe("remote main update");
  expect(runWorkerGit(worktreePath, ["show", "HEAD:tracked.txt"])).toContain(
    "remote",
  );
});

test("task detail approves a live agent interaction and refreshes results", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await openVueApp(page);

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
    }, { timeout: 15000 })
    .toMatchObject({
      status: "WAITING_INPUT",
      interactions: [
        expect.objectContaining({
          title: "Approve command",
          kind: "COMMAND_APPROVAL",
        }),
      ],
    });

  await openVueApp(page);
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
    }, { timeout: 15000 })
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
  await openVueApp(page);

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

  await openVueApp(page);
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
    }, { timeout: 15000 })
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
  await openVueApp(page);

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

  await openVueApp(page);
  await openTaskFromList(page, taskTitle);
  await expect(
    page.getByRole("tab", { name: /^Conversation$/ }),
  ).toBeVisible();
  await selectTaskDetailTab(page, "Conversation");
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
