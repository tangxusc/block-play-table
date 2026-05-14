export interface WorkerStartupCommandInput {
  managerWSURL: string;
  workerToken: string;
  workerId: string;
  workerName: string;
  workDir: string;
  supportedAgents: string[];
  projectBindingMode: string;
  boundProjectIds: string[];
}

export interface WorkerStartupCommands {
  docker: string;
  shell: string;
}

const dockerImage = "ghcr.io/tangxusc/block-play-table-worker:latest";

export function managerHTTPToWS(httpURL: string): string {
  const trimmed = (httpURL || "").trim().replace(/\/+$/, "");
  if (!trimmed) return "";
  let wsURL = trimmed;
  if (wsURL.startsWith("https://")) {
    wsURL = "wss://" + wsURL.slice("https://".length);
  } else if (wsURL.startsWith("http://")) {
    wsURL = "ws://" + wsURL.slice("http://".length);
  }
  return wsURL + "/worker/ws";
}

function envEntries(input: WorkerStartupCommandInput): Array<[string, string]> {
  const entries: Array<[string, string]> = [];
  entries.push(["MANAGER_WS_URL", input.managerWSURL]);
  entries.push(["WORKER_ID", input.workerId]);
  entries.push(["WORKER_NAME", input.workerName]);
  entries.push(["WORKER_WORK_DIR", input.workDir]);
  entries.push(["WORKER_SUPPORTED_AGENTS", input.supportedAgents.join(",")]);
  entries.push(["WORKER_PROJECT_BINDING_MODE", input.projectBindingMode]);
  if (
    input.projectBindingMode === "SPECIFIC_PROJECTS" &&
    input.boundProjectIds.length > 0
  ) {
    entries.push(["WORKER_BOUND_PROJECT_IDS", input.boundProjectIds.join(",")]);
  }
  if (input.workerToken) {
    entries.push(["WORKER_TOKEN", input.workerToken]);
  }
  return entries;
}

export function buildWorkerCommands(
  input: WorkerStartupCommandInput,
): WorkerStartupCommands {
  const entries = envEntries(input);
  const dockerArgs = entries.map(([k, v]) => `  -e ${k}=${v} \\`);
  const docker = [
    `docker run --rm --name bpt-worker-${input.workerId} \\`,
    `  -v bpt-worker-data:${input.workDir} \\`,
    ...dockerArgs,
    `  ${dockerImage}`,
  ].join("\n");

  const shellEnv = entries.map(([k, v]) => `${k}=${v} \\`);
  const shell = [...shellEnv, "go run ./worker/cmd/worker"].join("\n");

  return { docker, shell };
}
