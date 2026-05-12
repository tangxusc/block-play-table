import { spawn } from "node:child_process";
import { existsSync, mkdirSync, openSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const runDir = join(root, ".local-run");
const dataDir = resolve(process.env.BPT_DATA_DIR || join(root, "data"));
const workerDir = resolve(process.env.WORKER_DATA_SOURCE || join(root, "worker-data"));
const managerAddr = process.env.MANAGER_ADDR || "127.0.0.1:8080";
const managerURL = `http://${managerAddr}`;
const uiPort = process.env.BPT_UI_PORT || "18080";
const uiURL = `http://127.0.0.1:${uiPort}`;
const uiDisplayURL = `http://localhost:${uiPort}`;
const workerToken = process.env.WORKER_TOKEN || "dev-worker-token";
const wslDistro = process.env.BPT_WSL_DISTRO || "Ubuntu-24.04";
const useWslBackend =
  process.platform === "win32" &&
  process.env.BPT_BACKEND_MODE !== "windows" &&
  commandWorks("wsl", ["-d", wslDistro, "--", "bash", "-lc", "true"]);

mkdirSync(runDir, { recursive: true });
mkdirSync(dataDir, { recursive: true });
mkdirSync(workerDir, { recursive: true });

const statePath = join(runDir, "state.json");
if (existsSync(statePath)) {
  console.log("Local stack state exists; stopping previous stack first.");
  await import("./stop-local.mjs");
}

const processes = [];

function startProcess(name, command, args, options = {}) {
  const out = openSync(join(runDir, `${name}.log`), "a");
  const child = spawn(command, args, {
    cwd: root,
    env: { ...process.env, ...options.env },
    detached: true,
    windowsHide: true,
    shell: options.shell || false,
    stdio: ["ignore", out, out],
  });
  child.unref();
  processes.push({ name, pid: child.pid });
  return child;
}

function startWslGo(name, env, packagePath) {
  const pidFileHost = join(runDir, `${name}.wsl.pid`);
  const pidFileWsl = hostToWsl(pidFileHost);
  const exports = Object.entries(env)
    .map(([key, value]) => `${key}=${shellQuote(value)}`)
    .join(" ");
  const command = [
    `cd ${shellQuote(hostToWsl(root))}`,
    `mkdir -p ${shellQuote(hostToWsl(dataDir))} ${shellQuote(hostToWsl(workerDir))}`,
    `echo $$ > ${shellQuote(pidFileWsl)}`,
    `exec env ${exports} go run ${packagePath}`,
  ].join(" && ");
  startProcess(name, "wsl", ["-d", wslDistro, "--", "bash", "-lc", command]);
  return { pidFileHost, pidFileWsl };
}

if (useWslBackend) {
  const manager = startWslGo(
    "manager",
    {
      DB_DRIVER: "sqlite",
      DB_DSN: `${hostToWsl(dataDir)}/manager.db`,
      MANAGER_HTTP_ADDR: managerAddr,
      WORKER_TOKEN: workerToken,
    },
    "./manager/cmd/manager",
  );
  await waitForHTTP(`${managerURL}/healthz`, "Manager");
  const worker = startWslGo(
    "worker",
    {
      MANAGER_WS_URL: `ws://${managerAddr}/worker/ws`,
      WORKER_ID: process.env.WORKER_ID || "worker-local",
      WORKER_NAME: process.env.WORKER_NAME || "local-worker",
      WORKER_WORK_DIR: hostToWsl(workerDir),
      WORKER_SUPPORTED_AGENTS: process.env.WORKER_SUPPORTED_AGENTS || "codex,claude",
      WORKER_TOKEN: workerToken,
    },
    "./worker/cmd/worker",
  );
  processes.push({ name: "manager-wsl", wslPidFile: manager.pidFileHost });
  processes.push({ name: "worker-wsl", wslPidFile: worker.pidFileHost });
} else {
  startProcess("manager", "go", ["run", "./manager/cmd/manager"], {
    env: {
      DB_DRIVER: "sqlite",
      DB_DSN: join(dataDir, "manager.db"),
      MANAGER_HTTP_ADDR: managerAddr,
      WORKER_TOKEN: workerToken,
    },
  });
  await waitForHTTP(`${managerURL}/healthz`, "Manager");
  startProcess("worker", "go", ["run", "./worker/cmd/worker"], {
    env: {
      MANAGER_WS_URL: `ws://${managerAddr}/worker/ws`,
      WORKER_ID: process.env.WORKER_ID || "worker-local",
      WORKER_NAME: process.env.WORKER_NAME || "local-worker",
      WORKER_WORK_DIR: workerDir,
      WORKER_SUPPORTED_AGENTS: process.env.WORKER_SUPPORTED_AGENTS || "codex,claude",
      WORKER_TOKEN: workerToken,
    },
  });
}

startProcess("ui", process.platform === "win32" ? "npm.cmd" : "npm", ["run", "dev:ui", "--", "--host", "127.0.0.1", "--port", uiPort], {
  shell: process.platform === "win32",
  env: {
    VITE_MANAGER_GRAPHQL_URL: `${managerURL}/graphql`,
    VITE_MANAGER_GRAPHQL_WS_URL: `ws://${managerAddr}/subscriptions`,
  },
});
await waitForHTTP(uiURL, "UI");

writeFileSync(
  statePath,
  JSON.stringify(
    {
      root,
      runDir,
      managerURL,
      workerToken,
      useWslBackend,
      wslDistro,
      workerDataSource: workerDir,
      workerDataWorkerPath: useWslBackend ? hostToWsl(workerDir) : workerDir,
      processes,
    },
    null,
    2,
  ),
);

console.log(`Local stack is running:
  UI:      ${uiDisplayURL}
  Manager: ${managerURL}
  Logs:    ${runDir}`);

async function waitForHTTP(url, label) {
  const deadline = Date.now() + 60_000;
  let last = "";
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url);
      if (response.ok) return;
      last = `${response.status} ${response.statusText}`;
    } catch (error) {
      last = error instanceof Error ? error.message : String(error);
    }
    await new Promise((resolveWait) => setTimeout(resolveWait, 500));
  }
  throw new Error(`${label} did not become ready at ${url}: ${last}`);
}

function commandWorks(command, args) {
  try {
    const child = spawn(command, args, { stdio: "ignore", windowsHide: true });
    return child.pid > 0;
  } catch {
    return false;
  }
}

function hostToWsl(value) {
  const resolved = resolve(value);
  const drive = resolved.slice(0, 1).toLowerCase();
  return `/mnt/${drive}${resolved.slice(2).replace(/\\/g, "/")}`;
}

function shellQuote(value) {
  return `'${String(value).replace(/'/g, `'\\''`)}'`;
}
