import { flushPromises, shallowMount, type VueWrapper } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type {
  BoardData,
  DomainEventItem,
  TaskDetailData,
  TaskGitBackup,
  TaskGitDiff,
  TaskGitDiffFile,
  TaskGitStatus,
  TaskInteraction,
  TaskItem,
  WorkerItem,
} from "../models";
import TaskA2AExecutionRounds from "./TaskA2AExecutionRounds.vue";
import TaskDetailModal from "./TaskDetailModal.vue";

const mocks = vi.hoisted(() => ({
  subscribeDomainEvents: vi.fn(),
  fetchTaskDetail: vi.fn(),
  fetchEmployeeByID: vi.fn(),
  respondTaskInteraction: vi.fn(),
  startTask: vi.fn(),
  retryTask: vi.fn(),
  fetchEmployees: vi.fn(),
  updateTask: vi.fn(),
  assignWorker: vi.fn(),
  interruptTask: vi.fn(),
  archiveTask: vi.fn(),
  deleteTask: vi.fn(),
  continueTask: vi.fn(),
  fetchTaskGitStatus: vi.fn(),
  fetchTaskGitDiff: vi.fn(),
  stageTaskGitChanges: vi.fn(),
  unstageTaskGitChanges: vi.fn(),
  discardTaskGitChanges: vi.fn(),
  restoreTaskGitBackup: vi.fn(),
  runTaskGitCommand: vi.fn(),
  workerWebProxyUrl: vi.fn(),
  workerTerminalCheckUrlForTask: vi.fn((id: string) => `/terminal/tasks/${id}`),
  workerTerminalWebSocketUrlForTask: vi.fn((id: string) => `ws://terminal/tasks/${id}`),
  success: vi.fn(),
}));

vi.mock("ant-design-vue", () => ({
  message: { success: mocks.success },
}));

vi.mock("../api", () => ({ api: mocks }));

const project = {
  id: "project-1",
  name: "Project One",
  gitUrl: "repo",
  defaultBranch: "main",
  worktreeNamePrefix: "task",
  archived: false,
  createdAt: "now",
  updatedAt: "now",
};

const worker = (overrides: Partial<WorkerItem> = {}): WorkerItem => ({
  id: "worker-1",
  name: "Worker One",
  status: "ONLINE",
  capabilities: [],
  supportedAgents: ["codex", "claude"],
  workDir: "/workspace",
  projectBindingMode: "ALL_PROJECTS",
  boundProjectIds: [],
  agentRuntimeEnv: [],
  currentTaskIds: [],
  createdAt: "now",
  updatedAt: "now",
  ...overrides,
});

const workers = [
  worker(),
  worker({
    id: "worker-specific",
    name: "Specific Worker",
    supportedAgents: ["claude"],
    projectBindingMode: "SPECIFIC_PROJECTS",
    boundProjectIds: [project.id],
  }),
  worker({ id: "worker-wrong-project", projectBindingMode: "SPECIFIC_PROJECTS", boundProjectIds: ["other"] }),
  worker({ id: "worker-offline", status: "OFFLINE" }),
  worker({ id: "worker-empty", supportedAgents: [] }),
];

const board: BoardData = {
  id: "board",
  name: "Board",
  type: "KANBAN",
  totalCount: 1,
  columns: [],
  calendarItems: [],
  tasks: [],
  projects: [project],
  workers,
};

const patchText = [
  "diff --git a/main.go b/main.go",
  "index 1111111..2222222 100644",
  "--- a/main.go",
  "+++ b/main.go",
  "@@ -1 +1 @@",
  "-old",
  "+new",
  "@@ -10,0 +11 @@",
  "+second",
  "",
].join("\n");

const reviewFiles: TaskGitDiffFile[] = [
  {
    path: "main.go",
    status: "MODIFIED",
    staged: false,
    additions: 2,
    deletions: 1,
    patch: patchText,
    truncated: false,
  },
  {
    path: "README.md",
    oldPath: "README.old.md",
    status: "RENAMED",
    staged: true,
    additions: 1,
    deletions: 0,
    patch: "",
    truncated: true,
  },
];

const reviewDiff: TaskGitDiff = {
  taskId: "task-1",
  scope: "UNCOMMITTED",
  baseRef: "main",
  headRef: "task/task-1",
  files: reviewFiles,
  truncated: false,
  generatedAt: "now",
};

const gitStatus: TaskGitStatus = {
  taskId: "task-1",
  remote: "origin",
  branch: "main",
  currentBranch: "task/task-1",
  headRef: "head",
  targetRef: "target",
  ahead: 2,
  behind: 1,
  hasStagedChanges: true,
  hasUnstagedChanges: true,
  hasUntrackedFiles: true,
  generatedAt: "now",
};

const backup: TaskGitBackup = {
  id: "backup-1",
  taskId: "task-1",
  paths: ["main.go"],
  patchPath: "/backups/one.patch",
  createdAt: "now",
};

const planMarkdown = [
  "# Delivery **plan**",
  "Use `A2A` safely.",
  "",
  "1. First **step**",
  "2. Second `step`",
  "",
  "- Verify",
  "- Publish",
  "",
  "```go",
  "fmt.Println(\"done\")",
  "```",
].join("\n");

function task(status = "RUNNING", overrides: Partial<TaskItem> = {}): TaskItem {
  return {
    id: "task-1",
    title: "A2A task",
    description: "Track the remote execution",
    status,
    projectId: project.id,
    workerId: worker().id,
    agentType: "codex",
    agentConfig: { workMode: "IMPLEMENT", codex: { model: "gpt", reasoningEffort: "HIGH" } },
    baseBranch: "main",
    worktreePath: "/workspace/task-1",
    agentSessionId: "session-1",
    preCommands: [],
    postCommands: [],
    result: "Completed output",
    ownerUserId: "user-1",
    startDate: "2026-08-10T00:00:00Z",
    endDate: "2026-08-11T00:00:00Z",
    createdAt: "now",
    updatedAt: "now",
    ...overrides,
  };
}

function interaction(overrides: Partial<TaskInteraction> = {}): TaskInteraction {
  return {
    id: "interaction-1",
    taskId: "task-1",
    kind: "APPROVAL",
    status: "PENDING",
    title: "Approve changes",
    body: "Apply the generated patch?",
    rawPayload: JSON.stringify({ payload: { tool_input: { plan: planMarkdown } } }),
    responsePayload: "",
    createdAt: "now",
    updatedAt: "now",
    ...overrides,
  };
}

function makeDetail(status = "RUNNING", overrides: Partial<TaskDetailData> = {}): TaskDetailData {
  return {
    task: task(status),
    a2aExecutions: [
      {
        id: "round-1",
        executionId: "execution-1",
        attempt: 1,
        turn: 2,
        operation: "MESSAGE",
        workerId: "worker-1",
        a2aTaskId: "remote-task-1",
        contextId: "context-1",
        remoteStatus: "WORKING",
        lastSequence: 3,
        lastSyncedAt: "now",
        errorCode: null,
        errorMessage: null,
        retryable: false,
        createdAt: "now",
        completedAt: null,
      },
    ],
    logs: [{ id: "log-1", stream: "stdout", content: "running", createdAt: "now" }],
    conversations: [{ id: "message-1", role: "assistant", content: "working", metadata: [], createdAt: "now" }],
    interactions: [interaction({ id: "completed", status: "RESPONDED", rawPayload: "invalid" }), interaction()],
    events: [{
      eventId: "event-1",
      eventType: "TaskUpdated",
      aggregateType: "Task",
      aggregateId: "task-1",
      aggregateVersion: 2,
      payload: "{}",
      occurredAt: "now",
    }],
    reviewDiff,
    gitStatus,
    backups: [backup],
    reviewError: "Review warning",
    ...overrides,
  };
}

describe("TaskDetailModal", () => {
  let currentDetail: TaskDetailData;
  let subscriber: () => void;
  let reset: () => void;
  let unsubscribe: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    currentDetail = makeDetail();
    unsubscribe = vi.fn();
    mocks.fetchTaskDetail.mockImplementation(async () => currentDetail);
    mocks.fetchEmployeeByID.mockResolvedValue({ id: "user-1", name: "User One" });
    mocks.fetchEmployees.mockResolvedValue([{ id: "user-1", name: "User One" }]);
    mocks.subscribeDomainEvents.mockImplementation((callback: () => void, _filter: unknown, onReset: () => void) => {
      subscriber = callback;
      reset = onReset;
      return unsubscribe;
    });
    mocks.fetchTaskGitStatus.mockResolvedValue(gitStatus);
    mocks.fetchTaskGitDiff.mockResolvedValue(reviewDiff);
    mocks.discardTaskGitChanges.mockResolvedValue(backup);
    mocks.workerWebProxyUrl.mockImplementation((workerName: string, address: string) => `http://manager/proxy/${workerName}/${address}`);
    for (const key of [
      "respondTaskInteraction",
      "startTask",
      "retryTask",
      "updateTask",
      "assignWorker",
      "interruptTask",
      "archiveTask",
      "deleteTask",
      "continueTask",
      "stageTaskGitChanges",
      "unstageTaskGitChanges",
      "restoreTaskGitBackup",
      "runTaskGitCommand",
    ] as const) {
      mocks[key].mockResolvedValue(undefined);
    }
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
  });

  async function mountDetail(): Promise<{ wrapper: VueWrapper; vm: any }> {
    const wrapper = shallowMount(TaskDetailModal, {
      props: { taskId: "task-1", board },
      global: {
        stubs: {
          AModal: { name: "AModal", template: '<section><slot name="title" /><slot /></section>' },
          ATabs: { name: "ATabs", template: "<div><slot /></div>" },
          ATabPane: { name: "ATabPane", template: "<section><slot /></section>" },
          ATooltip: { name: "ATooltip", template: "<span><slot /></span>" },
          APopconfirm: { name: "APopconfirm", template: "<span><slot /></span>" },
        },
      },
    });
    await flushPromises();
    return { wrapper, vm: wrapper.vm as any };
  }

  it("loads all task detail channels and refreshes from A2A domain events", async () => {
    const { wrapper, vm } = await mountDetail();
    expect(vm.task.title).toBe("A2A task");
    expect(vm.employeeName).toBe("User One");
    expect(vm.pendingInteractions).toHaveLength(1);
    expect(vm.planBlocks.map((block: { type: string }) => block.type)).toEqual(["heading", "paragraph", "list", "list", "code"]);
    expect(vm.diffFiles).toHaveLength(2);
    expect(vm.selectedFile.path).toBe("main.go");
    expect(vm.selectedHunks).toHaveLength(2);
    expect(vm.latestBackup.id).toBe("backup-1");
    expect(vm.canInterrupt).toBe(true);
    expect(vm.canStart).toBe(false);
    expect(wrapper.text()).toContain("Completed output");
    expect(wrapper.text()).toContain("working");
    expect(wrapper.text()).toContain("running");
    expect(wrapper.text()).toContain("TaskUpdated");
    expect(wrapper.findComponent(TaskA2AExecutionRounds).props("executions")).toHaveLength(1);

    subscriber();
    reset();
    await flushPromises();
    expect(mocks.fetchTaskDetail.mock.calls.length).toBeGreaterThanOrEqual(3);
    await wrapper.setProps({ taskId: "task-2" });
    await flushPromises();
    expect(unsubscribe).toHaveBeenCalled();
    wrapper.unmount();
    expect(unsubscribe).toHaveBeenCalledTimes(2);
  });

  it("ignores an older detail response that completes after a newer event refresh", async () => {
    let resolveFirst: ((detail: TaskDetailData) => void) | undefined;
    const staleDetail = makeDetail("RUNNING");
    const freshDetail = makeDetail("WAITING_INPUT", {
      a2aExecutions: [
        { ...staleDetail.a2aExecutions[0], remoteStatus: "INPUT_REQUIRED", lastSequence: 4 },
      ],
    });
    mocks.fetchTaskDetail
      .mockImplementationOnce(
        () => new Promise<TaskDetailData>((resolve) => { resolveFirst = resolve; }),
      )
      .mockResolvedValueOnce(freshDetail);

    const { wrapper, vm } = await mountDetail();
    subscriber();
    await flushPromises();
    resolveFirst?.(staleDetail);
    await flushPromises();

    expect(vm.task.status).toBe("WAITING_INPUT");
    expect(wrapper.findComponent(TaskA2AExecutionRounds).props("executions")[0].remoteStatus)
      .toBe("INPUT_REQUIRED");
  });

  it("executes lifecycle, approval, continuation and close actions", async () => {
    const { wrapper, vm } = await mountDetail();
    await vm.approve(vm.pendingInteractions[0]);
    expect(mocks.respondTaskInteraction).toHaveBeenCalledWith("interaction-1", "APPROVE");
    expect(wrapper.emitted("changed")?.length).toBeGreaterThan(0);
    await vm.startCurrentTask();
    await vm.retryCurrentTask();
    await vm.interruptCurrentTask();
    await vm.archiveCurrentTask();
    expect(mocks.startTask).toHaveBeenCalledWith("task-1");
    expect(mocks.retryTask).toHaveBeenCalledWith("task-1");
    expect(mocks.interruptTask).toHaveBeenCalledWith("task-1");
    expect(mocks.archiveTask).toHaveBeenCalledWith("task-1");

    await vm.copyTaskId();
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith("task-1");
    expect(mocks.success).toHaveBeenCalledWith("Task ID copied");
    vm.continueMessage = "   ";
    await vm.sendContinuation();
    expect(mocks.continueTask).not.toHaveBeenCalled();
    vm.continueMessage = " continue work ";
    await vm.sendContinuation();
    expect(mocks.continueTask).toHaveBeenCalledWith("task-1", "continue work");
    expect(vm.continueMessage).toBe("");

    await vm.deleteCurrentTask();
    expect(mocks.deleteTask).toHaveBeenCalledWith("task-1");
    expect(wrapper.emitted("close")).toEqual([[]]);
    vm.close();
    expect(wrapper.emitted("close")).toHaveLength(2);
  });

  it("reports action failures and clears busy state", async () => {
    const { wrapper, vm } = await mountDetail();
    mocks.startTask.mockRejectedValueOnce(new Error("start failed"));
    await vm.startCurrentTask();
    expect(vm.error).toBe("start failed");
    expect(vm.busy).toBe(false);
    mocks.retryTask.mockRejectedValueOnce("retry offline");
    await vm.retryCurrentTask();
    expect(vm.error).toBe("retry offline");
    wrapper.unmount();
  });

  it("edits and assigns tasks across Worker compatibility branches", async () => {
    const { wrapper, vm } = await mountDetail();
    await vm.openEditDialog();
    expect(vm.editDraft).not.toBe(vm.task);
    expect(vm.editStartDate).toBe("2026-08-10");
    expect(vm.editEmployees).toHaveLength(1);
    vm.editDraft.title = "Updated title";
    vm.editStartDate = "";
    vm.editEndDate = "2026-08-20";
    await vm.saveEditTask();
    expect(mocks.updateTask).toHaveBeenCalledWith(expect.objectContaining({
      title: "Updated title",
      startDate: "",
      endDate: "2026-08-20T00:00:00Z",
    }));
    expect(vm.editOpen).toBe(false);
    expect(vm.pickerDate(undefined)).toBe("");
    expect(vm.taskDateInput("")).toBe("");

    vm.openAssignDialog();
    expect(vm.assignOpen).toBe(true);
    expect(vm.assignWorkerId).toBe("worker-1");
    expect(vm.assignAgentOptions).toEqual(["codex"]);
    vm.updateAssignWorker("worker-specific");
    expect(vm.assignAgentType).toBe("");
    await vm.assignCurrentTask();
    expect(mocks.assignWorker).not.toHaveBeenCalled();
    vm.assignAgentType = "claude";
    await vm.assignCurrentTask();
    expect(mocks.assignWorker).toHaveBeenCalledWith("task-1", "worker-specific", "claude", expect.any(Object));
    expect(vm.assignOpen).toBe(false);

    expect(vm.taskAssignableToWorker(workers[0], vm.task)).toBe(true);
    expect(vm.taskAssignableToWorker(workers[1], vm.task)).toBe(false);
    expect(vm.taskAssignableToWorker(workers[2], vm.task)).toBe(false);
    expect(vm.taskAssignableToWorker(workers[3], vm.task)).toBe(false);
    expect(vm.taskAssignableToWorker(workers[4], vm.task)).toBe(false);
    expect(vm.workerAllowsProject(workers[0], "other")).toBe(true);
    expect(vm.workerAllowsProject(workers[2], project.id)).toBe(false);

    vm.detail.task.agentType = undefined;
    await wrapper.vm.$nextTick();
    vm.openAssignDialog();
    vm.updateAssignWorker("worker-1");
    expect(vm.assignAgentOptions).toEqual(["codex", "claude"]);
    wrapper.unmount();
  });

  it("stages, discards, restores and publishes Git changes", async () => {
    const { wrapper, vm } = await mountDetail();
    await vm.refreshReview();
    expect(mocks.fetchTaskGitStatus).toHaveBeenCalledWith("task-1", "origin", "main");
    expect(mocks.fetchTaskGitDiff).toHaveBeenCalledWith("task-1", "UNCOMMITTED");
    await vm.stageSelectedFile();
    await vm.unstageSelectedFile();
    await vm.discardSelectedFile();
    expect(mocks.stageTaskGitChanges).toHaveBeenCalledWith("task-1", ["main.go"]);
    expect(mocks.unstageTaskGitChanges).toHaveBeenCalledWith("task-1", ["main.go"]);
    expect(mocks.success).toHaveBeenCalledWith("Backup created: backup-1");
    await vm.restoreBackup("backup-1");
    expect(mocks.restoreTaskGitBackup).toHaveBeenCalledWith("task-1", "backup-1");

    const firstHunk = vm.selectedHunks[0].patch;
    await vm.stageHunk(firstHunk);
    await vm.unstageHunk(firstHunk);
    await vm.discardHunk(firstHunk);
    expect(mocks.stageTaskGitChanges).toHaveBeenCalledWith("task-1", ["main.go"], firstHunk);
    expect(mocks.unstageTaskGitChanges).toHaveBeenCalledWith("task-1", ["main.go"], firstHunk);

    await vm.gitCommand("FETCH");
    expect(mocks.runTaskGitCommand).toHaveBeenCalledWith(expect.objectContaining({ taskId: "task-1", command: "FETCH" }));
    vm.commitMessage = "   ";
    await vm.commit();
    const beforeCommit = mocks.runTaskGitCommand.mock.calls.length;
    vm.commitMessage = " commit message ";
    await vm.commit();
    expect(mocks.runTaskGitCommand.mock.calls.length).toBe(beforeCommit + 1);
    expect(mocks.runTaskGitCommand).toHaveBeenLastCalledWith(expect.objectContaining({ command: "COMMIT", message: "commit message" }));
    await vm.publish("MERGE_COMMIT");
    expect(mocks.runTaskGitCommand).toHaveBeenLastCalledWith(expect.objectContaining({ command: "PUBLISH", publishStrategy: "MERGE_COMMIT" }));

    mocks.discardTaskGitChanges.mockResolvedValueOnce(null);
    await vm.discardSelectedFile();
    mocks.discardTaskGitChanges.mockResolvedValueOnce(null);
    await vm.discardHunk(firstHunk);
    wrapper.unmount();
  });

  it("guards Git operations when task, file, backup or patch is absent", async () => {
    const { wrapper, vm } = await mountDetail();
    vm.selectedFile = null;
    await vm.stageSelectedFile();
    await vm.unstageSelectedFile();
    await vm.discardSelectedFile();
    await vm.stageHunk("");
    await vm.unstageHunk("   ");
    await vm.discardHunk("");
    await vm.restoreBackup("");
    const stageCalls = mocks.stageTaskGitChanges.mock.calls.length;
    vm.detail = null;
    await vm.gitCommand("FETCH");
    await vm.refreshReview();
    await vm.startCurrentTask();
    await vm.retryCurrentTask();
    await vm.interruptCurrentTask();
    await vm.archiveCurrentTask();
    await vm.deleteCurrentTask();
    await vm.copyTaskId();
    await vm.openEditDialog();
    await vm.saveEditTask();
    vm.openAssignDialog();
    await vm.assignCurrentTask();
    expect(mocks.stageTaskGitChanges.mock.calls.length).toBe(stageCalls);
    wrapper.unmount();
  });

  it("selects Web preview Workers and reports URL failures", async () => {
    const { wrapper, vm } = await mountDetail();
    expect(vm.selectedPreviewWorker.id).toBe("worker-1");
    expect(vm.canOpenPreview).toBe(false);
    vm.previewAddress = "localhost:3000";
    vm.openWebPreview();
    expect(vm.previewUrl).toBe("http://manager/proxy/Worker One/localhost:3000");
    expect(vm.previewError).toBe("");

    mocks.workerWebProxyUrl.mockImplementationOnce(() => { throw new Error("invalid address"); });
    vm.openWebPreview();
    expect(vm.previewError).toBe("invalid address");
    mocks.workerWebProxyUrl.mockImplementationOnce(() => { throw "proxy offline"; });
    vm.openWebPreview();
    expect(vm.previewError).toBe("proxy offline");

    vm.selectedPreviewWorkerId = "missing";
    vm.ensurePreviewWorker("worker-specific");
    expect(vm.selectedPreviewWorkerId).toBe("worker-specific");
    vm.selectedPreviewWorkerId = "";
    vm.ensurePreviewWorker("missing");
    expect(vm.selectedPreviewWorkerId).toBe("worker-1");

    await wrapper.setProps({ board: { ...board, workers: [] } });
    vm.selectedPreviewWorkerId = "";
    vm.openWebPreview();
    expect(vm.previewError).toBe("Worker is required");
    wrapper.unmount();
  });

  it("parses nested plans, Markdown and Git hunks", async () => {
    const { wrapper, vm } = await mountDetail();
    expect(vm.planFromInteraction(interaction({ rawPayload: "" }))).toBe("");
    expect(vm.planFromInteraction(interaction({ rawPayload: "{" }))).toBe("");
    expect(vm.planFromValue(null)).toBe("");
    expect(vm.planFromValue("plan")).toBe("");
    expect(vm.planFromValue([{ input: { plan: "  nested plan  " } }])).toBe("nested plan");
    expect(vm.planFromValue([null, {}])).toBe("");
    expect(vm.planFromValue({ plan: " " })).toBe("");
    expect(vm.planFromValue({ toolInput: { plan: "tool plan" } })).toBe("tool plan");
    expect(vm.planFromValue({ payload: { input: { plan: "payload plan" } } })).toBe("payload plan");

    const blocks = vm.parsePlanMarkdown("\r\n### H **bold** `code`\r\n\r\n1. one\r\n- two\r\n```\r\nunclosed");
    expect(blocks.map((block: { type: string }) => block.type)).toEqual(["heading", "list", "list", "code"]);
    expect(vm.parsePlanMarkdown("first line\nsecond line")[0].inline[0].text).toBe("first line second line");
    expect(vm.parseListLine("  1. ordered ")).toEqual({ ordered: true, text: "ordered" });
    expect(vm.parseListLine(" + unordered ")).toEqual({ ordered: false, text: "unordered" });
    expect(vm.parseListLine("plain")).toBeNull();
    expect(vm.startsMarkdownBlock("```ts")).toBe(true);
    expect(vm.startsMarkdownBlock("# heading")).toBe(true);
    expect(vm.startsMarkdownBlock("plain")).toBe(false);
    expect(vm.parseMarkdownInline("text `code` and **strong**").map((part: { type: string }) => part.type)).toEqual(["text", "code", "text", "strong"]);
    expect(vm.parseMarkdownInline("` open ** strong")).toEqual([
      { type: "text", text: "` open " },
      { type: "text", text: "** strong" },
    ]);
    expect(vm.nextTokenIndex("abc", "`", 0)).toBe(3);
    expect(vm.headingTag()).toBe("h3");
    expect(vm.headingTag(6)).toBe("h6");

    expect(vm.parseDiffHunks("")).toEqual([]);
    expect(vm.parseDiffHunks("diff --git a/a b/a\n--- a/a\n+++ b/a")).toEqual([]);
    const hunks = vm.parseDiffHunks([
      "diff --git a/a b/a",
      "index 1..2",
      "new file mode 100644",
      "deleted file mode 100644",
      "similarity index 90%",
      "rename from old",
      "rename to new",
      "--- a/a",
      "+++ b/a",
      "@@ -0,0 +1 @@",
      "+one",
    ].join("\n"));
    expect(hunks).toHaveLength(1);
    expect(hunks[0].patch).toContain("rename to new");
    wrapper.unmount();
  });

  it("handles empty owner, missing review data and load failure shapes", async () => {
    currentDetail = makeDetail("CREATED", {
      task: task("CREATED", { ownerUserId: "", workerId: undefined, description: "", result: undefined }),
      interactions: [],
      logs: [],
      conversations: [],
      events: [],
      reviewDiff: undefined,
      gitStatus: undefined,
      backups: [],
      reviewError: "",
    });
    const { wrapper, vm } = await mountDetail();
    expect(vm.employeeName).toBe("");
    expect(vm.selectedFile).toBeNull();
    expect(vm.latestBackup).toBeNull();
    expect(vm.canStart).toBe(true);
    expect(vm.canInterrupt).toBe(false);
    expect(wrapper.text()).toContain("No result yet");
    expect(wrapper.findAllComponents({ name: "AEmpty" }).length).toBeGreaterThan(0);

    mocks.fetchTaskDetail.mockRejectedValueOnce(new Error("detail unavailable"));
    await vm.load();
    expect(vm.error).toBe("detail unavailable");
    mocks.fetchTaskDetail.mockRejectedValueOnce("offline");
    await vm.load(false);
    expect(vm.error).toBe("offline");
    wrapper.unmount();
  });

  it("renders archived and waiting task states", async () => {
    currentDetail = makeDetail("ARCHIVED");
    const archived = await mountDetail();
    expect(archived.vm.isArchived).toBe(true);
    expect(archived.wrapper.findAllComponents({ name: "AButton" }).some((button) => button.attributes("aria-label") === "Delete")).toBe(true);
    archived.wrapper.unmount();

    currentDetail = makeDetail("WAITING_INPUT");
    const waiting = await mountDetail();
    expect(waiting.vm.isWaitingForInput).toBe(true);
    expect(waiting.vm.canInterrupt).toBe(true);
    waiting.wrapper.unmount();
  });
});
