import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  ApiClient,
  addManager,
  getActiveManager,
  listManagers,
  loadManagerBaseURL,
  loadManagerToken,
  loadManagersState,
  maskToken,
  normalizeManagerBaseURL,
  removeManager,
  saveManagersState,
  setActiveManager,
  type ManagersState,
} from "./api";
import type {
  BoardData,
  ProjectItem,
  TaskA2AExecution,
  TaskGitBackup,
  TaskGitDiff,
  TaskGitStatus,
  TaskItem,
  WorkerItem,
} from "./models";

function response(data: unknown, ok = true, status = 200) {
  return {
    ok,
    status,
    json: vi.fn().mockResolvedValue(data),
  } as unknown as Response;
}

const task = {
  id: "task-1",
  title: "Task",
  description: "Description",
  status: "RUNNING",
  projectId: "project-1",
  agentConfig: {},
  baseBranch: "main",
  preCommands: [],
  postCommands: [],
  ownerUserId: "",
  startDate: "2026-08-10T00:00:00Z",
  endDate: "2026-08-10T00:00:00Z",
  createdAt: "2026-08-10T00:00:00Z",
  updatedAt: "2026-08-10T00:00:00Z",
} as TaskItem;

const project = {
  id: "project-1",
  name: "Project",
  gitUrl: "repo",
  defaultBranch: "main",
  worktreeNamePrefix: "project",
  archived: false,
  createdAt: "2026-08-10T00:00:00Z",
  updatedAt: "2026-08-10T00:00:00Z",
} as ProjectItem;

const worker = {
  id: "worker-1",
  name: "Worker",
  status: "ONLINE",
  capabilities: [],
  supportedAgents: ["codex"],
  workDir: "/tmp/worker",
  startupCommand: "",
  projectBindingMode: "ALL_PROJECTS",
  boundProjectIds: [],
  agentRuntimeEnv: [
    {
      agentType: "codex",
      vars: [
        { key: "VISIBLE", value: "plain", enabled: true, sensitive: false },
        { key: "SECRET", valueMasked: "***", enabled: true, sensitive: true },
        { key: "MASKED", valueMasked: "shown", enabled: true, sensitive: false },
      ],
    },
  ],
  currentTaskIds: [],
  createdAt: "2026-08-10T00:00:00Z",
  updatedAt: "2026-08-10T00:00:00Z",
} as WorkerItem;

const board: Omit<BoardData, "projects" | "workers"> = {
  id: "board",
  name: "Board",
  type: "KANBAN",
  totalCount: 1,
  columns: [],
  calendarItems: [],
  tasks: [task],
};

const a2aExecution = {
  id: "round-1",
  executionId: "execution-1",
  attempt: 1,
  turn: 1,
  operation: "START",
  workerId: "worker-1",
  a2aTaskId: "a2a-task-1",
  contextId: "context-1",
  remoteStatus: "WORKING",
  lastSequence: 2,
  lastSyncedAt: "2026-08-10T00:00:00Z",
  errorCode: null,
  errorMessage: null,
  retryable: false,
  createdAt: "2026-08-10T00:00:00Z",
  completedAt: null,
} as TaskA2AExecution;

beforeEach(() => {
  vi.restoreAllMocks();
});

describe("manager configuration", () => {
  it("loads empty, stored and migrated manager states", () => {
    expect(loadManagersState()).toEqual({ version: 1, managers: [], activeManagerId: null });
    window.localStorage.setItem("block-play-table.managers", JSON.stringify({
      version: 1,
      managers: [null, { id: "manager-1", url: "http://manager", token: "token", addedAt: "now" }],
      activeManagerId: "manager-1",
    }));
    expect(listManagers()).toHaveLength(1);
    expect(getActiveManager()?.id).toBe("manager-1");
    expect(loadManagerBaseURL()).toBe("http://manager");
    expect(loadManagerToken()).toBe("token");

    window.localStorage.setItem("block-play-table.managers", "{");
    window.localStorage.setItem("block-play-table.manager-url", "http://legacy");
    window.sessionStorage.setItem("block-play-table.manager-token", "legacy-token");
    const migrated = loadManagersState();
    expect(migrated.managers[0].url).toBe("http://legacy");
    expect(migrated.activeManagerId).toBe(migrated.managers[0].id);
    expect(window.localStorage.getItem("block-play-table.manager-url")).toBeNull();
  });

  it("sets, removes and masks managers", () => {
    const state: ManagersState = {
      version: 1,
      managers: [{ id: "manager-1", url: "http://manager", token: "123456789", addedAt: "now" }],
      activeManagerId: null,
    };
    saveManagersState(state);
    expect(getActiveManager()).toBeNull();
    expect(setActiveManager("manager-1")?.url).toBe("http://manager");
    expect(setActiveManager(null)).toBeNull();
    expect(() => setActiveManager("missing")).toThrow("Manager not found");
    removeManager("missing");
    expect(listManagers()).toHaveLength(1);
    setActiveManager("manager-1");
    removeManager("manager-1");
    expect(loadManagersState().activeManagerId).toBeNull();
    expect(maskToken("")).toBe("");
    expect(maskToken("12345")).toBe("••••");
    expect(maskToken("123456789")).toBe("••••6789");
  });

  it("adds public, protected and existing managers", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response({ required: false }))
      .mockResolvedValueOnce(response({ required: true }))
      .mockResolvedValueOnce(response({}, true))
      .mockResolvedValueOnce(response({ required: false }));
    vi.stubGlobal("fetch", fetchMock);
    const first = await addManager({ url: "manager.local/", token: "" });
    expect(first.url).toBe("http://manager.local");
    const protectedManager = await addManager({ url: "https://secure.local", token: " secret " });
    expect(protectedManager.token).toBe("secret");
    const updated = await addManager({ url: "manager.local", token: "updated" });
    expect(updated.id).toBe(first.id);
    expect(updated.token).toBe("updated");
  });

  it("rejects a missing protected-manager token", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response({ required: true })));
    await expect(addManager({ url: "http://secure.local", token: "" })).rejects.toThrow("Manager token is required");
  });
});

describe("ApiClient transport", () => {
  it("normalizes endpoints and validates URLs", () => {
    expect(normalizeManagerBaseURL(" manager.local/path///?x=1#hash ")).toBe("http://manager.local/path");
    expect(normalizeManagerBaseURL("https://manager.local/")).toBe("https://manager.local");
    expect(() => normalizeManagerBaseURL(" ")).toThrow("Manager URL is required");
    expect(() => normalizeManagerBaseURL("ftp://manager.local")).toThrow("Manager URL must start");
    const client = new ApiClient("https://manager.local/base");
    expect(client.endpoint).toBe("https://manager.local/base/graphql");
    expect(client.websocketEndpoint).toBe("wss://manager.local/base/subscriptions");
  });

  it("handles auth and GraphQL HTTP outcomes", async () => {
    const client = new ApiClient("http://manager.local");
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response({ required: true }))
      .mockResolvedValueOnce(response({}, false, 503))
      .mockResolvedValueOnce(response({}, false, 500))
      .mockResolvedValueOnce(response({ errors: [{ message: "one" }, { message: "two" }] }))
      .mockResolvedValueOnce(response({ data: { value: 1 } }));
    vi.stubGlobal("fetch", fetchMock);
    await expect(client.fetchAuthStatus()).resolves.toEqual({ required: true });
    await expect(client.verifyManagerToken("bad")).rejects.toThrow("Invalid manager token");
    await expect(client.graphQL("query Broken")).rejects.toThrow("GraphQL HTTP 500");
    await expect(client.graphQL("query Errors")).rejects.toThrow("one; two");
    await expect(client.graphQL("query Good")).resolves.toEqual({ value: 1 });
  });

  it("rejects requests before a manager is configured", async () => {
    const client = new ApiClient("");
    await expect(client.fetchAuthStatus()).rejects.toThrow("Manager URL is required");
    await expect(client.graphQL("query Test")).rejects.toThrow("Manager URL is required");
    expect(() => client.workerTerminalCheckUrlForTask("task-1")).toThrow("Manager URL is required");
    expect(() => client.subscribeDomainEvents(() => undefined)).toThrow("Manager URL is required");
  });
});

describe("ApiClient queries and mutations", () => {
  function mockClient() {
    const client = new ApiClient("http://manager.local");
    const backup = { id: "backup-1" } as TaskGitBackup;
    const diff = { taskId: "task-1", files: [] } as unknown as TaskGitDiff;
    const status = { taskId: "task-1", remote: "origin", branch: "main" } as TaskGitStatus;
    const graphQL = vi.spyOn(client, "graphQL").mockImplementation(async (query: string) => {
      if (query.includes("query Board(")) return { board };
      if (query.includes("query Projects {")) return { projects: [project] };
      if (query.includes("query ProjectsPage")) return { projectsConnection: { nodes: [project], totalCount: 1 } };
      if (query.includes("query Workers {")) return { workers: [worker] };
      if (query.includes("query WorkersPage")) return { workersConnection: { nodes: [worker], totalCount: 1 } };
      if (query.includes("query EventsPage")) return { domainEventsConnection: { nodes: [], totalCount: 0 } };
      if (query.includes("query Settings")) return { settings: { workerHeartbeatTimeout: "30s", securityPolicy: "trusted" } };
      if (query.includes("query CurrentUser")) return { currentUser: { id: "user-1", trustMode: true } };
      if (query.includes("query EmployeeByID")) return { employeeByID: { id: "user-1", name: "User" } };
      if (query.includes("query Employees")) return { employees: [{ id: "user-1", name: "User" }] };
      if (query.includes("query Task($id")) return { task };
      if (query.includes("query TaskA2AExecutions")) return { taskA2AExecutions: [a2aExecution] };
      if (query.includes("query TaskLogs")) return { taskLogs: [] };
      if (query.includes("query TaskConversations")) return { taskConversations: [] };
      if (query.includes("query TaskInteractions")) return { taskInteractions: [] };
      if (query.includes("query TaskEvents")) return { taskEvents: [] };
      if (query.includes("query TaskGitDiff")) return { taskGitDiff: diff };
      if (query.includes("query TaskGitStatus")) return { taskGitStatus: status };
      if (query.includes("query TaskGitBackups")) return { taskGitBackups: [backup] };
      if (query.includes("mutation TaskGitChange")) {
        const field = query.match(/\n\s+(\w+)\(input/)?.[1] || "unknown";
        return { [field]: { ok: true, backup } };
      }
      return {};
    });
    return { client, graphQL, backup, diff, status };
  }

  it("loads boards, lists and settings", async () => {
    const { client, graphQL } = mockClient();
    await expect(client.fetchBoardData("KANBAN", { offset: 0, limit: 20 }, " query ", { field: "CREATED_AT", direction: "DESC" }, "project-1", "owner-1"))
      .resolves.toMatchObject({ id: "board", projects: [project], workers: [worker] });
    await client.fetchBoardData("CALENDAR", { offset: 0, limit: 20 }, "", { field: "CREATED_AT", direction: "DESC" }, "");
    await client.fetchBoardData("LIST", { offset: 0, limit: 20 }, "", { field: "CREATED_AT", direction: "DESC" }, "");
    await client.fetchBoardData("ARCHIVED", { offset: 0, limit: 20 }, "", { field: "CREATED_AT", direction: "DESC" }, "");
    await expect(client.fetchProjectsPage({ offset: 0, limit: 20 }, "find", { field: "NAME", direction: "ASC" }))
      .resolves.toEqual({ items: [project], totalCount: 1 });
    await expect(client.fetchWorkersPage({ offset: 0, limit: 20 }, "", { field: "NAME", direction: "ASC" }))
      .resolves.toEqual({ items: [worker], totalCount: 1 });
    await expect(client.fetchEventsPage({ offset: 0, limit: 20 }, "", { field: "OCCURRED_AT", direction: "DESC" }))
      .resolves.toEqual({ items: [], totalCount: 0 });
    await expect(client.fetchSettings()).resolves.toEqual({ workerHeartbeatTimeout: "30s", securityPolicy: "trusted" });
    await expect(client.fetchCurrentUser()).resolves.toEqual({ id: "user-1", trustMode: true });
    expect(graphQL).toHaveBeenCalled();
  });

  it("loads employees with failure fallbacks", async () => {
    const { client } = mockClient();
    await expect(client.fetchEmployeeByID("user-1")).resolves.toEqual({ id: "user-1", name: "User" });
    await expect(client.fetchEmployees()).resolves.toEqual([{ id: "user-1", name: "User" }]);
    vi.spyOn(client, "graphQL").mockRejectedValue(new Error("offline"));
    await expect(client.fetchEmployeeByID("missing")).resolves.toBeNull();
    await expect(client.fetchEmployees()).resolves.toEqual([]);
  });

  it("loads task detail including A2A rounds", async () => {
    const { client, graphQL } = mockClient();
    const detail = await client.fetchTaskDetail("task-1");
    expect(detail.task).toBe(task);
    expect(detail.a2aExecutions).toEqual([a2aExecution]);
    expect(detail.reviewDiff?.taskId).toBe("task-1");
    expect(detail.backups).toHaveLength(1);
    expect(detail.reviewError).toBe("");
    const taskCall = graphQL.mock.calls.findIndex(([query]) => query.includes("query Task($id"));
    const roundCall = graphQL.mock.calls.findIndex(([query]) => query.includes("query TaskA2AExecutions"));
    expect(taskCall).toBeGreaterThanOrEqual(0);
    expect(roundCall).toBeGreaterThan(taskCall);
    expect(graphQL.mock.invocationCallOrder[roundCall]).toBeGreaterThan(
      graphQL.mock.invocationCallOrder[taskCall],
    );
  });

  it("keeps task detail when review and backups fail", async () => {
    const { client, graphQL } = mockClient();
    vi.spyOn(client, "fetchTaskGitDiff").mockRejectedValue("review unavailable");
    const original = graphQL.getMockImplementation();
    graphQL.mockImplementation(async (query: string, variables) => {
      if (query.includes("query TaskGitBackups")) throw new Error("no backups");
      return original?.(query, variables);
    });
    const detail = await client.fetchTaskDetail("task-1");
    expect(detail.reviewError).toBe("review unavailable");
    expect(detail.backups).toEqual([]);
  });

  it("executes lifecycle, settings and Git mutations", async () => {
    const { client, graphQL, backup } = mockClient();
    await client.createProject(project);
    await client.updateProject(project);
    await client.archiveProject(project.id);
    await client.createTask({ title: "Task" });
    await client.assignWorker(task.id, worker.id);
    await client.assignWorker(task.id, worker.id, "codex", { workMode: "IMPLEMENT" });
    await client.updateTask(task);
    await client.updateTask({ ...task, ownerUserId: "user-1" });
    await client.startTask(task.id);
    await client.interruptTask(task.id);
    await client.archiveTask(task.id);
    await client.deleteTask(task.id);
    await client.retryTask(task.id);
    await client.continueTask(task.id, "continue");
    await client.respondTaskInteraction("interaction-1", "APPROVE");
    await client.respondTaskInteraction("interaction-1", "DENY", "reason");
    await client.createWorker(worker);
    await client.updateWorker(worker);
    await client.enableWorker(worker.id);
    await client.disableWorker(worker.id);
    await client.deleteWorker(worker.id);
    await client.updateSettings({ workerHeartbeatTimeout: "45s", securityPolicy: "trusted" });
    await client.stageTaskGitChanges(task.id, ["main.go"]);
    await client.unstageTaskGitChanges(task.id, ["main.go"], "patch");
    await expect(client.discardTaskGitChanges(task.id, ["main.go"])).resolves.toBe(backup);
    await client.restoreTaskGitBackup(task.id, backup.id);
    await client.runTaskGitCommand({ taskId: task.id, command: "FETCH" });
    expect(graphQL.mock.calls.length).toBeGreaterThan(20);
  });

  it("builds terminal and web proxy URLs", () => {
    const { client } = mockClient();
    expect(client.workerTerminalCheckUrlForTask("task id")).toBe("http://manager.local/terminal/tasks/task%20id");
    expect(client.workerTerminalWebSocketUrlForTask("task-1")).toBe("ws://manager.local/terminal/tasks/task-1/ws");
    expect(client.workerTerminalCheckUrlForWorker("worker-1")).toBe("http://manager.local/terminal/workers/worker-1");
    expect(client.workerTerminalWebSocketUrlForWorker("worker-1")).toBe("ws://manager.local/terminal/workers/worker-1/ws");
    expect(client.workerWebProxyUrl(" worker ", "localhost/path?q=1#hash")).toBe("http://manager.local/proxy/web/worker/localhost/80/path?q=1");
    expect(() => client.workerWebProxyUrl("", "localhost")).toThrow("Worker is required");
    expect(() => client.workerWebProxyUrl("worker", "")).toThrow("Address is required");
    expect(() => client.workerWebProxyUrl("worker", "https://localhost")).toThrow("Only http addresses");
  });
});

describe("domain event subscriptions", () => {
  it("subscribes, reconnects and closes", () => {
    vi.useFakeTimers();
    const sockets: FakeWebSocket[] = [];
    class FakeWebSocket {
      listeners = new Map<string, Array<(event: any) => void>>();
      sent: string[] = [];
      closed = false;

      constructor(public url: string, public protocol: string) {
        sockets.push(this);
      }

      addEventListener(type: string, listener: (event: any) => void): void {
        const listeners = this.listeners.get(type) || [];
        listeners.push(listener);
        this.listeners.set(type, listeners);
      }

      send(value: string): void {
        this.sent.push(value);
      }

      close(): void {
        this.closed = true;
      }

      emit(type: string, event: any = {}): void {
        for (const listener of this.listeners.get(type) || []) listener(event);
      }
    }
    vi.stubGlobal("WebSocket", FakeWebSocket);
    const client = new ApiClient("http://manager.local");
    const onEvent = vi.fn();
    const onReset = vi.fn();
    const unsubscribe = client.subscribeDomainEvents(onEvent, { aggregateType: "Task" }, onReset);
    sockets[0].emit("open");
    expect(sockets[0].sent).toHaveLength(2);
    sockets[0].emit("message", { data: JSON.stringify({ payload: { data: { domainEvents: { eventId: "event-1" } } } }) });
    sockets[0].emit("message", { data: JSON.stringify({ payload: {} }) });
    expect(onEvent).toHaveBeenCalledWith({ eventId: "event-1" });
    sockets[0].emit("error");
    expect(onReset).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(3000);
    expect(sockets).toHaveLength(2);
    unsubscribe();
    expect(sockets[1].closed).toBe(true);
    sockets[1].emit("close");
    vi.advanceTimersByTime(3000);
    expect(sockets).toHaveLength(2);
    vi.useRealTimers();
  });
});
