import type {
  AgentRuntimeEnvVar,
  BoardData,
  BoardView,
  DomainEventItem,
  PageRequest,
  PagedResult,
  ProjectItem,
  SettingsData,
  SortRequest,
  TaskDetailData,
  TaskGitBackup,
  TaskGitDiff,
  TaskGitStatus,
  TaskItem,
  WorkerAgentRuntimeEnv,
  WorkerItem,
} from "./models";

type CreateProjectInput = Pick<ProjectItem, "name" | "gitUrl" | "defaultBranch" | "worktreeNamePrefix">;

const managersStorageKey = "block-play-table.managers";
const legacyURLStorageKey = "block-play-table.manager-url";
const legacyTokenStorageKey = "block-play-table.manager-token";

export type ManagerEntry = {
  id: string;
  url: string;
  token: string;
  addedAt: string;
};

export type ManagersState = {
  version: 1;
  managers: ManagerEntry[];
  activeManagerId: string | null;
};

const emptyManagersState = (): ManagersState => ({
  version: 1,
  managers: [],
  activeManagerId: null,
});

function generateId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `mgr-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

export function loadManagersState(): ManagersState {
  if (typeof window === "undefined") return emptyManagersState();
  const raw = window.localStorage.getItem(managersStorageKey);
  if (raw) {
    try {
      const parsed = JSON.parse(raw) as ManagersState;
      if (parsed && Array.isArray(parsed.managers)) {
        return {
          version: 1,
          managers: parsed.managers.filter((entry) => entry && typeof entry.url === "string"),
          activeManagerId: parsed.activeManagerId ?? null,
        };
      }
    } catch {
      /* fall through to migration */
    }
  }
  const legacyURL = window.localStorage.getItem(legacyURLStorageKey) || "";
  const legacyToken = window.sessionStorage.getItem(legacyTokenStorageKey) || "";
  if (!legacyURL) {
    const empty = emptyManagersState();
    saveManagersState(empty);
    return empty;
  }
  const entry: ManagerEntry = {
    id: generateId(),
    url: legacyURL,
    token: legacyToken,
    addedAt: new Date().toISOString(),
  };
  const migrated: ManagersState = {
    version: 1,
    managers: [entry],
    activeManagerId: entry.id,
  };
  saveManagersState(migrated);
  window.localStorage.removeItem(legacyURLStorageKey);
  window.sessionStorage.removeItem(legacyTokenStorageKey);
  return migrated;
}

export function saveManagersState(state: ManagersState): void {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(managersStorageKey, JSON.stringify(state));
}

export function listManagers(): ManagerEntry[] {
  return loadManagersState().managers;
}

export function getActiveManager(): ManagerEntry | null {
  const state = loadManagersState();
  if (!state.activeManagerId) return null;
  return state.managers.find((entry) => entry.id === state.activeManagerId) ?? null;
}

export function setActiveManager(id: string | null): ManagerEntry | null {
  const state = loadManagersState();
  if (id === null) {
    state.activeManagerId = null;
    saveManagersState(state);
    return null;
  }
  const entry = state.managers.find((m) => m.id === id);
  if (!entry) {
    throw new Error("Manager not found.");
  }
  state.activeManagerId = entry.id;
  saveManagersState(state);
  api.configureManagerBaseURL(entry.url);
  return entry;
}

export async function addManager(input: { url: string; token: string }): Promise<ManagerEntry> {
  const normalizedURL = normalizeManagerBaseURL(input.url);
  const token = (input.token ?? "").trim();
  const probe = new ApiClient(normalizedURL);
  const status = await probe.fetchAuthStatus();
  if (status.required) {
    if (!token) throw new Error("Manager token is required.");
    await probe.verifyManagerToken(token);
  }
  const state = loadManagersState();
  const existing = state.managers.find((entry) => entry.url === normalizedURL);
  let entry: ManagerEntry;
  if (existing) {
    existing.token = token;
    entry = existing;
  } else {
    entry = {
      id: generateId(),
      url: normalizedURL,
      token,
      addedAt: new Date().toISOString(),
    };
    state.managers.push(entry);
  }
  state.activeManagerId = entry.id;
  saveManagersState(state);
  api.configureManagerBaseURL(entry.url);
  return entry;
}

export function removeManager(id: string): void {
  const state = loadManagersState();
  const before = state.managers.length;
  state.managers = state.managers.filter((entry) => entry.id !== id);
  if (state.managers.length === before) return;
  if (state.activeManagerId === id) {
    state.activeManagerId = null;
  }
  saveManagersState(state);
}

export function maskToken(token: string): string {
  if (!token) return "";
  if (token.length < 6) return "••••";
  return `••••${token.slice(-4)}`;
}

export function loadManagerBaseURL(): string {
  return getActiveManager()?.url ?? "";
}

export function loadManagerToken(): string {
  return getActiveManager()?.token ?? "";
}

const taskFields = `
  id title description status projectId agentType baseBranch ownerUserId
  agentConfig {
    workMode
    codex { model reasoningEffort sandboxMode approvalPolicy fullAuto bypassApprovalsAndSandbox }
    claude { model effort permissionMode }
  }
  workerId worktreePath agentSessionId preCommands postCommands result startDate endDate createdAt updatedAt
`;

const workerFields = `
  id name status supportedAgents workDir startupCommand projectBindingMode
  boundProjectIds currentTaskIds lastHeartbeatAt createdAt updatedAt
  capabilities { key value }
  agentRuntimeEnv {
    agentType
    vars { key valueMasked description enabled sensitive }
  }
`;

const projectFields = `
  id name gitUrl defaultBranch worktreeNamePrefix archived createdAt updatedAt
`;

const eventFields = `
  eventId eventType aggregateType aggregateId aggregateVersion payload occurredAt
`;

export class ApiClient {
  managerBaseURL: string;
  endpoint: string;
  websocketEndpoint: string;

  constructor(managerBaseURL = loadManagerBaseURL()) {
    this.managerBaseURL = "";
    this.endpoint = "";
    this.websocketEndpoint = "";
    if (managerBaseURL) {
      this.configureManagerBaseURL(managerBaseURL);
    }
  }

  configureManagerBaseURL(rawURL: string): string {
    const normalized = normalizeManagerBaseURL(rawURL);
    this.managerBaseURL = normalized;
    this.endpoint = urlFromManagerBase(normalized, ["graphql"]);
    this.websocketEndpoint = urlFromManagerBase(normalized, ["subscriptions"], "websocket");
    return normalized;
  }

  async fetchAuthStatus(): Promise<{ required: boolean }> {
    const response = await fetch(urlFromManagerBase(this.ensureManagerBaseURL(), ["auth", "status"]));
    if (!response.ok) {
      throw new Error(`Auth status HTTP ${response.status}`);
    }
    return response.json();
  }

  async verifyManagerToken(token: string): Promise<void> {
    const response = await fetch(urlFromManagerBase(this.ensureManagerBaseURL(), ["auth", "verify"]), {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ token }),
    });
    if (!response.ok) {
      throw new Error("Invalid manager token");
    }
  }

  async graphQL<T>(
    query: string,
    variables: Record<string, unknown> = {},
  ): Promise<T> {
    const response = await fetch(this.ensureEndpoint(), {
      method: "POST",
      headers: { "content-type": "application/json", ...managerTokenHeaders() },
      body: JSON.stringify({ query, variables }),
    });
    if (!response.ok) {
      throw new Error(`GraphQL HTTP ${response.status}`);
    }
    const body = await response.json();
    if (body.errors?.length) {
      throw new Error(body.errors.map((item: { message: string }) => item.message).join("; "));
    }
    return body.data as T;
  }

  subscribeDomainEvents(
    onEvent: (event: DomainEventItem) => void,
    filter: Record<string, unknown> = {},
    onReset?: () => void,
  ): () => void {
    let closed = false;
    let socket: WebSocket | null = null;
    let retryTimer: number | undefined;
    const connect = () => {
      if (closed) return;
      socket = new WebSocket(urlWithManagerToken(this.ensureWebsocketEndpoint()), "graphql-transport-ws");
      const id = `sub-${Math.random().toString(36).slice(2)}`;
      socket.addEventListener("open", () => {
        socket?.send(JSON.stringify({ type: "connection_init", payload: {} }));
        socket?.send(
          JSON.stringify({
            id,
            type: "subscribe",
            payload: {
              query: `subscription Events($filter: DomainEventFilter) {
                domainEvents(filter: $filter) { ${eventFields} }
              }`,
              variables: { filter },
            },
          }),
        );
      });
      socket.addEventListener("message", (message) => {
        const data = JSON.parse(String(message.data));
        const event = data.payload?.data?.domainEvents;
        if (event) onEvent(event);
      });
      const reconnect = () => {
        if (closed) return;
        onReset?.();
        window.clearTimeout(retryTimer);
        retryTimer = window.setTimeout(connect, 3000);
      };
      socket.addEventListener("error", reconnect);
      socket.addEventListener("close", reconnect);
    };
    connect();
    return () => {
      closed = true;
      window.clearTimeout(retryTimer);
      socket?.close();
    };
  }

  async fetchBoardData(
    view: BoardView,
    page: PageRequest,
    search: string,
    sort: SortRequest,
    projectId: string,
    ownerUserId?: string,
  ): Promise<BoardData> {
    const archivedView = view === "ARCHIVED";
    const id = view === "CALENDAR" ? "calendar" : view === "LIST" || archivedView ? "list" : null;
    const [board, projects, workers] = await Promise.all([
      this.graphQL<{ board: Omit<BoardData, "projects" | "workers"> }>(
        `query Board($id: ID, $filter: TaskFilter, $sort: TaskSortInput, $page: PageInput) {
          board(id: $id, filter: $filter, sort: $sort, page: $page) {
            id name type totalCount
            columns { id title status tasks { ${taskFields} } }
            calendarItems { id date status task { ${taskFields} } }
            tasks { ${taskFields} }
          }
        }`,
        {
          id,
          filter: filterInput(
            {
              includeArchived: archivedView,
              ...(archivedView ? { status: "ARCHIVED" } : {}),
              ...(projectId ? { projectId } : {}),
              ...(ownerUserId ? { ownerUserId } : {}),
            },
            search,
          ),
          sort,
          page,
        },
      ),
      this.fetchProjects(),
      this.fetchWorkers(),
    ]);
    return { ...board.board, projects, workers };
  }

  async fetchProjects(): Promise<ProjectItem[]> {
    const data = await this.graphQL<{ projects: ProjectItem[] }>(
      `query Projects {
        projects(filter: { includeArchived: true }, sort: { field: CREATED_AT, direction: DESC }) {
          ${projectFields}
        }
      }`,
    );
    return data.projects;
  }

  async fetchProjectsPage(
    page: PageRequest,
    search: string,
    sort: SortRequest,
  ): Promise<PagedResult<ProjectItem>> {
    const data = await this.graphQL<{ projectsConnection: { nodes: ProjectItem[]; totalCount: number } }>(
      `query ProjectsPage($filter: ProjectFilter, $sort: ProjectSortInput, $page: PageInput) {
        projectsConnection(filter: $filter, sort: $sort, page: $page) {
          totalCount nodes { ${projectFields} }
        }
      }`,
      { filter: filterInput({ includeArchived: true }, search), sort, page },
    );
    return { items: data.projectsConnection.nodes, totalCount: data.projectsConnection.totalCount };
  }

  async fetchWorkers(): Promise<WorkerItem[]> {
    const data = await this.graphQL<{ workers: WorkerItem[] }>(
      `query Workers {
        workers(sort: { field: CREATED_AT, direction: DESC }) { ${workerFields} }
      }`,
    );
    return data.workers;
  }

  async fetchWorkersPage(
    page: PageRequest,
    search: string,
    sort: SortRequest,
  ): Promise<PagedResult<WorkerItem>> {
    const data = await this.graphQL<{ workersConnection: { nodes: WorkerItem[]; totalCount: number } }>(
      `query WorkersPage($filter: WorkerFilter, $sort: WorkerSortInput, $page: PageInput) {
        workersConnection(filter: $filter, sort: $sort, page: $page) {
          totalCount nodes { ${workerFields} }
        }
      }`,
      { filter: filterInput({ includeDisabled: true }, search), sort, page },
    );
    return { items: data.workersConnection.nodes, totalCount: data.workersConnection.totalCount };
  }

  async fetchEventsPage(
    page: PageRequest,
    search: string,
    sort: SortRequest,
  ): Promise<PagedResult<DomainEventItem>> {
    const data = await this.graphQL<{ domainEventsConnection: { nodes: DomainEventItem[]; totalCount: number } }>(
      `query EventsPage($filter: DomainEventFilter, $sort: DomainEventSortInput, $page: PageInput) {
        domainEventsConnection(filter: $filter, sort: $sort, page: $page) {
          totalCount nodes { ${eventFields} }
        }
      }`,
      { filter: filterInput({}, search), sort, page },
    );
    return { items: data.domainEventsConnection.nodes, totalCount: data.domainEventsConnection.totalCount };
  }

  async fetchSettings(): Promise<SettingsData> {
    const data = await this.graphQL<{ settings: SettingsData }>(
      `query Settings { settings { workerHeartbeatTimeout securityPolicy } }`,
    );
    return data.settings;
  }

  async fetchCurrentUser(): Promise<{ id: string; trustMode: boolean }> {
    const data = await this.graphQL<{ currentUser: { id: string; trustMode: boolean } }>(
      `query CurrentUser { currentUser { id trustMode } }`,
    );
    return data.currentUser;
  }

  async fetchEmployeeByID(id: string): Promise<{ id: string; name: string } | null> {
    try {
      const data = await this.graphQL<{ employeeByID: { id: string; name: string } | null }>(
        `query EmployeeByID($id: ID!) { employeeByID(id: $id) { id name } }`,
        { id },
      );
      return data.employeeByID ?? null;
    } catch {
      return null;
    }
  }

  async fetchTaskDetail(taskId: string): Promise<TaskDetailData> {
    const [taskData, logData, conversationData, interactionData, eventData] = await Promise.all([
      this.graphQL<{ task: TaskItem }>(`query Task($id: ID!) { task(id: $id) { ${taskFields} } }`, { id: taskId }),
      this.graphQL<{ taskLogs: TaskDetailData["logs"] }>(
        `query TaskLogs($taskId: ID!) { taskLogs(taskId: $taskId) { id stream content createdAt } }`,
        { taskId },
      ),
      this.graphQL<{ taskConversations: TaskDetailData["conversations"] }>(
        `query TaskConversations($taskId: ID!) {
          taskConversations(taskId: $taskId) { id role content metadata { key value } createdAt }
        }`,
        { taskId },
      ),
      this.graphQL<{ taskInteractions: TaskDetailData["interactions"] }>(
        `query TaskInteractions($taskId: ID!) {
          taskInteractions(taskId: $taskId) {
            id taskId kind status title body rawPayload agentSessionId
            responseDecision responseMessage responsePayload createdAt updatedAt
          }
        }`,
        { taskId },
      ),
      this.graphQL<{ taskEvents: DomainEventItem[] }>(
        `query TaskEvents($taskId: ID!) { taskEvents(taskId: $taskId) { ${eventFields} } }`,
        { taskId },
      ),
    ]);
    let reviewDiff: TaskGitDiff | undefined;
    let gitStatus: TaskGitStatus | undefined;
    let reviewError = "";
    try {
      [reviewDiff, gitStatus] = await Promise.all([
        this.fetchTaskGitDiff(taskId, "UNCOMMITTED"),
        this.fetchTaskGitStatus(taskId),
      ]);
    } catch (error) {
      reviewError = error instanceof Error ? error.message : String(error);
    }
    const backups = await this.graphQL<{ taskGitBackups: TaskDetailData["backups"] }>(
      `query TaskGitBackups($taskId: ID!) {
        taskGitBackups(taskId: $taskId) { id taskId paths patchPath createdAt }
      }`,
      { taskId },
    ).then((data) => data.taskGitBackups).catch(() => []);
    return {
      task: taskData.task,
      logs: logData.taskLogs,
      conversations: conversationData.taskConversations,
      interactions: interactionData.taskInteractions,
      events: eventData.taskEvents,
      reviewDiff,
      gitStatus,
      backups,
      reviewError,
    };
  }

  async fetchTaskGitDiff(taskId: string, scope: string, staged?: boolean): Promise<TaskGitDiff> {
    const data = await this.graphQL<{ taskGitDiff: TaskGitDiff }>(
      `query TaskGitDiff($taskId: ID!, $scope: TaskGitDiffScope!, $staged: Boolean) {
        taskGitDiff(taskId: $taskId, scope: $scope, staged: $staged) {
          taskId scope baseRef headRef truncated generatedAt
          files { path oldPath status staged additions deletions patch truncated }
        }
      }`,
      { taskId, scope, staged },
    );
    return data.taskGitDiff;
  }

  async fetchTaskGitStatus(taskId: string, remote = "origin", branch = "main"): Promise<TaskGitStatus> {
    const data = await this.graphQL<{ taskGitStatus: TaskGitStatus }>(
      `query TaskGitStatus($taskId: ID!, $remote: String, $branch: String) {
        taskGitStatus(taskId: $taskId, remote: $remote, branch: $branch) {
          taskId remote branch currentBranch headRef targetRef ahead behind
          hasStagedChanges hasUnstagedChanges hasUntrackedFiles generatedAt
        }
      }`,
      { taskId, remote, branch },
    );
    return data.taskGitStatus;
  }

  async createProject(input: CreateProjectInput): Promise<void> {
    await this.graphQL(
      `mutation CreateProject($input: CreateProjectInput!) { createProject(input: $input) { id } }`,
      { input: createProjectInput(input) },
    );
  }

  async updateProject(project: ProjectItem): Promise<void> {
    await this.graphQL(
      `mutation UpdateProject($input: UpdateProjectInput!) { updateProject(input: $input) { id } }`,
      {
        input: {
          id: project.id,
          name: project.name,
          gitUrl: project.gitUrl,
          defaultBranch: project.defaultBranch,
          worktreeNamePrefix: project.worktreeNamePrefix,
        },
      },
    );
  }

  async archiveProject(id: string): Promise<void> {
    await this.graphQL(`mutation ArchiveProject($id: ID!) { archiveProject(id: $id) { id } }`, { id });
  }

  async createTask(input: Record<string, unknown>): Promise<void> {
    await this.graphQL(`mutation CreateTask($input: CreateTaskInput!) { createTask(input: $input) { id } }`, { input });
  }

  async assignWorker(
    taskId: string,
    workerId: string,
    agentType?: string,
    agentConfig?: Record<string, unknown>,
  ): Promise<void> {
    await this.graphQL(
      `mutation AssignWorker($input: AssignWorkerInput!) { assignWorker(input: $input) { id } }`,
      {
        input: {
          taskId,
          workerId,
          ...(agentType ? { agentType } : {}),
          ...(agentConfig ? { agentConfig } : {}),
        },
      },
    );
  }

  async updateTask(task: TaskItem): Promise<void> {
    await this.graphQL(
      `mutation UpdateTask($input: UpdateTaskInput!) { updateTask(input: $input) { id } }`,
      {
        input: {
          id: task.id,
          title: task.title,
          description: task.description,
          projectId: task.projectId,
          agentType: task.agentType,
          baseBranch: task.baseBranch,
          preCommands: task.preCommands,
          postCommands: task.postCommands,
          startDate: task.startDate,
          endDate: task.endDate,
        },
      },
    );
  }

  async startTask(taskId: string): Promise<void> {
    await this.graphQL(`mutation StartTask($taskId: ID!) { startTask(taskId: $taskId) { id } }`, { taskId });
  }

  async interruptTask(taskId: string): Promise<void> {
    await this.graphQL(`mutation InterruptTask($taskId: ID!) { interruptTask(taskId: $taskId) { id } }`, { taskId });
  }

  async archiveTask(taskId: string): Promise<void> {
    await this.graphQL(`mutation ArchiveTask($taskId: ID!) { archiveTask(taskId: $taskId) { id } }`, { taskId });
  }

  async deleteTask(taskId: string): Promise<void> {
    await this.graphQL(`mutation DeleteTask($taskId: ID!) { deleteTask(taskId: $taskId) }`, { taskId });
  }

  async retryTask(taskId: string): Promise<void> {
    await this.graphQL(`mutation RetryTask($taskId: ID!) { retryTask(taskId: $taskId) { id } }`, { taskId });
  }

  async continueTask(taskId: string, message: string): Promise<void> {
    await this.graphQL(
      `mutation ContinueTask($input: ContinueTaskInput!) { continueTask(input: $input) { id } }`,
      { input: { taskId, message } },
    );
  }

  async respondTaskInteraction(interactionId: string, decision: string, message = ""): Promise<void> {
    await this.graphQL(
      `mutation RespondTaskInteraction($input: RespondTaskInteractionInput!) {
        respondTaskInteraction(input: $input) { id }
      }`,
      { input: { interactionId, decision, message, payload: "" } },
    );
  }

  async createWorker(input: Partial<WorkerItem>): Promise<void> {
    await this.graphQL(`mutation CreateWorker($input: CreateWorkerInput!) { createWorker(input: $input) { id } }`, {
      input,
    });
  }

  async updateWorker(worker: WorkerItem): Promise<void> {
    await this.graphQL(
      `mutation UpdateWorker($input: UpdateWorkerInput!) { updateWorker(input: $input) { id } }`,
      { input: workerInput(worker) },
    );
  }

  async enableWorker(id: string): Promise<void> {
    await this.graphQL(`mutation EnableWorker($id: ID!) { enableWorker(id: $id) { id } }`, { id });
  }

  async disableWorker(id: string): Promise<void> {
    await this.graphQL(`mutation DisableWorker($id: ID!) { disableWorker(id: $id) { id } }`, { id });
  }

  async deleteWorker(id: string): Promise<void> {
    await this.graphQL(`mutation DeleteWorker($id: ID!) { deleteWorker(id: $id) }`, { id });
  }

  async updateSettings(settings: SettingsData): Promise<void> {
    await this.graphQL(
      `mutation UpdateSettings($timeout: String!) {
        updateWorkerHeartbeatTimeout(timeout: $timeout) { id }
      }`,
      { timeout: settings.workerHeartbeatTimeout },
    );
  }

  async stageTaskGitChanges(taskId: string, paths: string[], patch?: string): Promise<void> {
    await this.taskGitChange("stageTaskGitChanges", taskId, paths, patch);
  }

  async unstageTaskGitChanges(taskId: string, paths: string[], patch?: string): Promise<void> {
    await this.taskGitChange("unstageTaskGitChanges", taskId, paths, patch);
  }

  async discardTaskGitChanges(taskId: string, paths: string[], patch?: string): Promise<TaskGitBackup | undefined> {
    const result = await this.taskGitChange("discardTaskGitChanges", taskId, paths, patch);
    return result.backup;
  }

  async restoreTaskGitBackup(taskId: string, backupId: string): Promise<void> {
    await this.graphQL(
      `mutation RestoreTaskGitBackup($input: TaskGitChangeInput!) {
        restoreTaskGitBackup(input: $input) { ok }
      }`,
      { input: { taskId, backupId } },
    );
  }

  async runTaskGitCommand(input: Record<string, unknown>): Promise<void> {
    await this.graphQL(
      `mutation RunTaskGitCommand($input: TaskGitCommandInput!) {
        runTaskGitCommand(input: $input) { ok output }
      }`,
      { input },
    );
  }

  workerTerminalCheckUrlForTask(taskId: string): string {
    return urlWithManagerToken(urlFromManagerBase(this.ensureManagerBaseURL(), ["terminal", "tasks", taskId]));
  }

  workerTerminalWebSocketUrlForTask(taskId: string): string {
    return urlWithManagerToken(urlFromManagerBase(this.ensureManagerBaseURL(), ["terminal", "tasks", taskId, "ws"], "websocket"));
  }

  workerTerminalCheckUrlForWorker(workerId: string): string {
    return urlWithManagerToken(urlFromManagerBase(this.ensureManagerBaseURL(), ["terminal", "workers", workerId]));
  }

  workerTerminalWebSocketUrlForWorker(workerId: string): string {
    return urlWithManagerToken(urlFromManagerBase(this.ensureManagerBaseURL(), ["terminal", "workers", workerId, "ws"], "websocket"));
  }

  workerWebProxyUrl(workerName: string, address: string): string {
    const normalizedWorker = workerName.trim();
    if (!normalizedWorker) throw new Error("Worker is required");
    const normalizedAddress = address.trim();
    if (!normalizedAddress) throw new Error("Address is required");
    const target = new URL(normalizedAddress.includes("://") ? normalizedAddress : `http://${normalizedAddress}`);
    if (target.protocol !== "http:") throw new Error("Only http addresses are supported");
    if (!target.hostname.trim()) throw new Error("Host is required");
    const port = target.port || "80";
    const targetPath = target.pathname.split("/").filter(Boolean);
    const url = new URL(urlFromManagerBase(this.ensureManagerBaseURL(), [
      "proxy",
      "web",
      normalizedWorker,
      target.hostname,
      port,
      ...targetPath,
    ]));
    url.search = target.search;
    url.hash = "";
    return urlWithManagerToken(url.toString());
  }

  private ensureManagerBaseURL(): string {
    if (!this.managerBaseURL) {
      throw new Error("Manager URL is required.");
    }
    return this.managerBaseURL;
  }

  private ensureEndpoint(): string {
    if (!this.endpoint) {
      throw new Error("Manager URL is required.");
    }
    return this.endpoint;
  }

  private ensureWebsocketEndpoint(): string {
    if (!this.websocketEndpoint) {
      throw new Error("Manager URL is required.");
    }
    return this.websocketEndpoint;
  }

  private async taskGitChange(
    field: string,
    taskId: string,
    paths: string[],
    patch?: string,
  ): Promise<{ ok: boolean; backup?: TaskGitBackup }> {
    const data = await this.graphQL<Record<string, { ok: boolean; backup?: TaskGitBackup }>>(
      `mutation TaskGitChange($input: TaskGitChangeInput!) {
        ${field}(input: $input) { ok backup { id taskId paths patchPath createdAt } }
      }`,
      { input: { taskId, paths, ...(patch ? { patch } : {}) } },
    );
    return data[field];
  }
}

function filterInput(base: Record<string, unknown>, search: string): Record<string, unknown> {
  const trimmed = search.trim();
  return trimmed ? { ...base, search: trimmed } : base;
}

function createProjectInput(project: CreateProjectInput): Record<string, unknown> {
  return {
    name: project.name,
    gitUrl: project.gitUrl,
    defaultBranch: project.defaultBranch,
    worktreeNamePrefix: project.worktreeNamePrefix,
  };
}

function workerInput(worker: WorkerItem): Record<string, unknown> {
  return {
    id: worker.id,
    name: worker.name,
    supportedAgents: worker.supportedAgents,
    workDir: worker.workDir,
    startupCommand: worker.startupCommand || "",
    projectBindingMode: worker.projectBindingMode,
    boundProjectIds: worker.boundProjectIds,
    capabilities: worker.capabilities,
    agentRuntimeEnv: normalizeRuntimeEnv(worker.agentRuntimeEnv),
  };
}

function normalizeRuntimeEnv(groups: WorkerAgentRuntimeEnv[]): WorkerAgentRuntimeEnv[] {
  return groups.map((group) => ({
    agentType: group.agentType,
    vars: group.vars.map((item: AgentRuntimeEnvVar) => ({
      key: item.key,
      value: item.value ?? (item.sensitive ? "" : item.valueMasked ?? ""),
      description: item.description ?? "",
      enabled: item.enabled,
      sensitive: item.sensitive,
    })),
  }));
}

export function normalizeManagerBaseURL(rawURL: string): string {
  const trimmed = rawURL.trim();
  if (!trimmed) throw new Error("Manager URL is required.");
  const withProtocol = /^[a-z][a-z\d+.-]*:\/\//i.test(trimmed) ? trimmed : `http://${trimmed}`;
  const url = new URL(withProtocol);
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new Error("Manager URL must start with http:// or https://.");
  }
  url.pathname = url.pathname.replace(/\/+$/, "");
  url.search = "";
  url.hash = "";
  const normalized = url.toString();
  return normalized.endsWith("/") ? normalized.slice(0, -1) : normalized;
}

function urlFromManagerBase(baseURL: string, segments: string[], mode: "http" | "websocket" = "http"): string {
  const url = new URL(baseURL);
  if (mode === "websocket") {
    url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  }
  const basePath = url.pathname.replace(/\/+$/, "");
  url.pathname = [basePath, ...segments.map(encodeURIComponent)].filter(Boolean).join("/");
  if (!url.pathname.startsWith("/")) {
    url.pathname = `/${url.pathname}`;
  }
  url.search = "";
  url.hash = "";
  return url.toString();
}

function managerTokenHeaders(): Record<string, string> {
  const token = loadManagerToken();
  return token ? { authorization: `Bearer ${token}` } : {};
}

function urlWithManagerToken(rawURL: string): string {
  const token = loadManagerToken();
  if (!token) return rawURL;
  const url = new URL(rawURL);
  url.searchParams.set("token", token);
  return url.toString();
}

export const api = new ApiClient();
