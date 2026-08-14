export interface CurrentUser {
  id: string;
  trustMode: boolean;
}

export interface Employee {
  id: string;
  name: string;
}

export type SortDirection = "ASC" | "DESC";
export type BoardView = "KANBAN" | "LIST" | "CALENDAR" | "ARCHIVED";

export interface PageRequest {
  offset: number;
  limit: number;
}

export interface SortRequest {
  field: string;
  direction: SortDirection;
}

export interface PagedResult<T> {
  items: T[];
  totalCount: number;
}

export interface KeyValue {
  key: string;
  value: string;
}

export interface AgentRuntimeEnvVar {
  key: string;
  value?: string;
  valueMasked?: string;
  description?: string;
  enabled: boolean;
  sensitive: boolean;
}

export interface WorkerAgentRuntimeEnv {
  agentType: "codex" | "claude";
  vars: AgentRuntimeEnvVar[];
}

export interface ProjectItem {
  id: string;
  name: string;
  gitUrl: string;
  defaultBranch: string;
  worktreeNamePrefix: string;
  archived: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface WorkerItem {
  id: string;
  name: string;
  status: string;
  capabilities: KeyValue[];
  supportedAgents: string[];
  workDir: string;
  startupCommand?: string;
  projectBindingMode: string;
  boundProjectIds: string[];
  agentRuntimeEnv: WorkerAgentRuntimeEnv[];
  currentTaskIds: string[];
  lastHeartbeatAt?: string;
  createdAt: string;
  updatedAt: string;
}

export interface AgentExecutionConfig {
  workMode?: string;
  codex?: {
    model?: string;
    reasoningEffort?: string;
    sandboxMode?: string;
    approvalPolicy?: string;
    fullAuto?: boolean;
    bypassApprovalsAndSandbox?: boolean;
  };
  claude?: {
    model?: string;
    effort?: string;
    permissionMode?: string;
  };
}

export interface TaskItem {
  id: string;
  title: string;
  description: string;
  status: string;
  projectId: string;
  workerId?: string;
  agentType?: string;
  agentConfig: AgentExecutionConfig;
  baseBranch: string;
  worktreePath?: string;
  agentSessionId?: string;
  preCommands: string[];
  postCommands: string[];
  result?: string;
  ownerUserId: string;
  startDate: string;
  endDate: string;
  createdAt: string;
  updatedAt: string;
}

export interface BoardColumn {
  id: string;
  title: string;
  status: string;
  tasks: TaskItem[];
}

export interface BoardData {
  id: string;
  name: string;
  type: string;
  totalCount: number;
  columns: BoardColumn[];
  calendarItems: Array<{ id: string; date: string; status: string; task: TaskItem }>;
  tasks: TaskItem[];
  projects: ProjectItem[];
  workers: WorkerItem[];
}

export interface DomainEventItem {
  eventId: string;
  eventType: string;
  aggregateType: string;
  aggregateId: string;
  aggregateVersion: number;
  payload: string;
  occurredAt: string;
}

export interface SettingsData {
  workerHeartbeatTimeout: string;
  securityPolicy: string;
}

export interface TaskLogItem {
  id: string;
  stream: string;
  content: string;
  createdAt: string;
}

export interface ConversationMessage {
  id: string;
  role: string;
  content: string;
  metadata: KeyValue[];
  createdAt: string;
}

export interface TaskInteraction {
  id: string;
  taskId: string;
  kind: string;
  status: string;
  title: string;
  body: string;
  rawPayload: string;
  agentSessionId?: string;
  responseDecision?: string;
  responseMessage?: string;
  responsePayload: string;
  createdAt: string;
  updatedAt: string;
}

/** 描述一次 Manager 与 Worker 之间的 A2A 执行轮次。 */
export interface TaskA2AExecution {
  id: string;
  executionId: string;
  attempt: number;
  turn: number;
  operation: string;
  workerId: string;
  a2aTaskId: string | null;
  contextId: string | null;
  remoteStatus: string;
  lastSequence: number;
  lastSyncedAt: string | null;
  errorCode: string | null;
  errorMessage: string | null;
  retryable: boolean;
  createdAt: string;
  completedAt: string | null;
}

export interface TaskGitDiffFile {
  path: string;
  oldPath?: string;
  status: string;
  staged: boolean;
  additions: number;
  deletions: number;
  patch: string;
  truncated: boolean;
}

export interface TaskGitDiff {
  taskId: string;
  scope: string;
  baseRef?: string;
  headRef?: string;
  files: TaskGitDiffFile[];
  truncated: boolean;
  generatedAt: string;
}

export interface TaskGitStatus {
  taskId: string;
  remote: string;
  branch: string;
  currentBranch: string;
  headRef: string;
  targetRef: string;
  ahead: number;
  behind: number;
  hasStagedChanges: boolean;
  hasUnstagedChanges: boolean;
  hasUntrackedFiles: boolean;
  generatedAt: string;
}

export interface TaskGitBackup {
  id: string;
  taskId: string;
  paths: string[];
  patchPath: string;
  createdAt: string;
}

export interface TaskDetailData {
  task: TaskItem;
  a2aExecutions: TaskA2AExecution[];
  logs: TaskLogItem[];
  conversations: ConversationMessage[];
  interactions: TaskInteraction[];
  events: DomainEventItem[];
  reviewDiff?: TaskGitDiff;
  gitStatus?: TaskGitStatus;
  backups: TaskGitBackup[];
  reviewError?: string;
}

export const defaultPage = (): PageRequest => ({ offset: 0, limit: 20 });
export const defaultSort = (field = "CREATED_AT"): SortRequest => ({
  field,
  direction: "DESC",
});
