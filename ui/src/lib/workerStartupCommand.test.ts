import { describe, expect, it } from "vitest";
import { buildWorkerCommands, managerHTTPToWS } from "./workerStartupCommand";

describe("worker startup commands", () => {
  it("converts manager URLs", () => {
    expect(managerHTTPToWS("")).toBe("");
    expect(managerHTTPToWS("https://manager.example/")).toBe("wss://manager.example/worker/ws");
    expect(managerHTTPToWS("http://localhost:8080")).toBe("ws://localhost:8080/worker/ws");
    expect(managerHTTPToWS("ws://localhost:8080")).toBe("ws://localhost:8080/worker/ws");
  });

  it("builds project-bound authenticated commands", () => {
    const commands = buildWorkerCommands({
      managerWSURL: "ws://localhost:8080/worker/ws",
      workerToken: "secret",
      workerId: "worker-1",
      workerName: "Worker One",
      workDir: "/worker-data",
      projectBindingMode: "SPECIFIC_PROJECTS",
      boundProjectIds: ["project-1", "project-2"],
    });
    expect(commands.docker).toContain("WORKER_BOUND_PROJECT_IDS=project-1,project-2");
    expect(commands.docker).toContain("WORKER_TOKEN=secret");
    expect(commands.docker).not.toContain("WORKER_SUPPORTED_AGENTS");
    expect(commands.shell).toContain("go run ./worker/cmd/worker");
  });

  it("omits optional environment variables", () => {
    const commands = buildWorkerCommands({
      managerWSURL: "ws://localhost:8080/worker/ws",
      workerToken: "",
      workerId: "worker-2",
      workerName: "Worker Two",
      workDir: "./worker-data",
      projectBindingMode: "ALL_PROJECTS",
      boundProjectIds: [],
    });
    expect(commands.shell).not.toContain("WORKER_BOUND_PROJECT_IDS");
    expect(commands.shell).not.toContain("WORKER_TOKEN");
  });
});
