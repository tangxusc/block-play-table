<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { message as antMessage } from "ant-design-vue";
import {
  CloseOutlined,
  CopyOutlined,
  DeleteOutlined,
  EditOutlined,
  InboxOutlined,
  PlayCircleOutlined,
  ReloadOutlined,
  RedoOutlined,
  StopOutlined,
  UserSwitchOutlined,
} from "@ant-design/icons-vue";
import { api } from "../api";
import type { BoardData, TaskDetailData, TaskGitDiffFile, TaskInteraction, TaskItem, WorkerItem } from "../models";
import {
  agentConfigInput,
  draftFromAgentConfig,
  emptyAgentConfigDraft,
  type AgentConfigDraft,
} from "../agentConfig";
import {
  dateOnly,
  interactionPayloadSummary,
  projectName,
  taskDateRange,
  workerName,
} from "../format";
import TerminalPanel from "./TerminalPanel.vue";
import AgentConfigFields from "./AgentConfigFields.vue";
import TaskA2AExecutionRounds from "./TaskA2AExecutionRounds.vue";

const props = defineProps<{
  taskId: string;
  board: BoardData;
}>();

const emit = defineEmits<{
  close: [];
  changed: [];
}>();

const detail = ref<TaskDetailData | null>(null);
const activeTab = ref("overview");
const loading = ref(false);
const busy = ref(false);
const error = ref("");
const continueMessage = ref("");
const reviewRemote = ref("origin");
const reviewBranch = ref("main");
const selectedFile = ref<TaskGitDiffFile | null>(null);
const commitOpen = ref(false);
const publishOpen = ref(false);
const assignOpen = ref(false);
const editOpen = ref(false);
const editDraft = ref<TaskItem | null>(null);
const editStartDate = ref("");
const editEndDate = ref("");
const editEmployees = ref<{ id: string; name: string }[]>([]);
const employeeName = ref("");
const assignWorkerId = ref("");
const assignAgentType = ref("");
const assignAgentConfig = ref<AgentConfigDraft>(emptyAgentConfigDraft());
const commitMessage = ref("");
const selectedPreviewWorkerId = ref("");
const previewAddress = ref("");
const previewUrl = ref("");
const previewError = ref("");
let unsubscribe: (() => void) | undefined;
let loadGeneration = 0;

const task = computed(() => detail.value?.task);
const isArchived = computed(() => task.value?.status === "ARCHIVED");
const isWaitingForInput = computed(() => task.value?.status === "WAITING_INPUT");
const canStart = computed(() => !!task.value && ["CREATED", "ASSIGNED"].includes(task.value.status));
const canAssign = computed(
  () => !!task.value && ["CREATED", "ASSIGNED"].includes(task.value.status) && assignCandidates.value.length > 0,
);
const canInterrupt = computed(() =>
  !!task.value && ["STARTING", "RUNNING", "WAITING_INPUT", "INTERRUPTING"].includes(task.value.status),
);
const diffFiles = computed(() => detail.value?.reviewDiff?.files || []);
const latestBackup = computed(() => {
  const backups = detail.value?.backups || [];
  return backups[backups.length - 1] || null;
});
const pendingInteractions = computed(() =>
  (detail.value?.interactions || []).filter((interaction) => interaction.status === "PENDING"),
);
const planMarkdown = computed(() => {
  const interactions = detail.value?.interactions || [];
  for (let index = interactions.length - 1; index >= 0; index--) {
    const plan = planFromInteraction(interactions[index]);
    if (plan) return plan;
  }
  return "";
});
const planBlocks = computed(() => parsePlanMarkdown(planMarkdown.value));
const selectedPatch = computed(() => selectedFile.value?.patch || diffFiles.value[0]?.patch || "");
const selectedHunks = computed(() => parseDiffHunks(selectedFile.value?.patch || ""));
const previewWorkers = computed(() => props.board.workers);
const assignCandidates = computed(() => {
  const current = task.value;
  if (!current) return [];
  return props.board.workers.filter((worker) => taskAssignableToWorker(worker, current));
});
const selectedAssignWorker = computed(() =>
  assignCandidates.value.find((worker) => worker.id === assignWorkerId.value),
);
const assignAgentOptions = computed(() => {
  const worker = selectedAssignWorker.value;
  const current = task.value;
  if (!worker || !current) return [];
  if (current.agentType) {
    return worker.supportedAgents.includes(current.agentType) ? [current.agentType] : [];
  }
  return worker.supportedAgents;
});
const canAssignSelection = computed(() => canAssign.value && !!assignWorkerId.value && !!assignAgentType.value);
const canSaveEditTask = computed(
  () => !!editDraft.value && editDraft.value.title.trim().length > 0 && editDraft.value.projectId.length > 0,
);
const selectedPreviewWorker = computed(() =>
  previewWorkers.value.find((worker) => worker.id === selectedPreviewWorkerId.value) || previewWorkers.value[0],
);
const canOpenPreview = computed(() => !!selectedPreviewWorker.value && previewAddress.value.trim().length > 0);

watch(
  () => props.taskId,
  () => {
    activeTab.value = "overview";
    previewUrl.value = "";
    previewError.value = "";
    selectedPreviewWorkerId.value = "";
    void load();
    unsubscribe?.();
    unsubscribe = api.subscribeDomainEvents(
      () => void load(false),
      { aggregateType: "Task", aggregateId: props.taskId },
      () => void load(false),
    );
  },
  { immediate: true },
);

onBeforeUnmount(() => {
  unsubscribe?.();
});

async function load(showSpinner = true) {
  const generation = ++loadGeneration;
  if (showSpinner) loading.value = true;
  error.value = "";
  try {
    const loaded = await api.fetchTaskDetail(props.taskId);
    if (generation !== loadGeneration) return;
    let loadedEmployeeName = "";
    if (loaded.task.ownerUserId) {
      const emp = await api.fetchEmployeeByID(loaded.task.ownerUserId);
      if (generation !== loadGeneration) return;
      loadedEmployeeName = emp?.name || loaded.task.ownerUserId;
    }
    detail.value = loaded;
    ensurePreviewWorker(loaded.task.workerId);
    selectedFile.value =
      loaded.reviewDiff?.files.find((file) => file.path === selectedFile.value?.path) ||
      loaded.reviewDiff?.files[0] ||
      null;
    employeeName.value = loadedEmployeeName;
  } catch (cause) {
    if (generation === loadGeneration) {
      error.value = cause instanceof Error ? cause.message : String(cause);
    }
  } finally {
    if (generation === loadGeneration) loading.value = false;
  }
}

async function run(action: () => Promise<void>) {
  busy.value = true;
  error.value = "";
  try {
    await action();
    await load(false);
    emit("changed");
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause);
  } finally {
    busy.value = false;
  }
}

function close() {
  emit("close");
}

async function approve(interaction: TaskInteraction) {
  await run(() => api.respondTaskInteraction(interaction.id, "APPROVE"));
}

async function startCurrentTask() {
  if (!task.value) return;
  await run(() => api.startTask(task.value!.id));
}

async function retryCurrentTask() {
  if (!task.value) return;
  await run(() => api.retryTask(task.value!.id));
}

async function openEditDialog() {
  if (!task.value) return;
  editDraft.value = { ...task.value };
  editStartDate.value = pickerDate(task.value.startDate);
  editEndDate.value = pickerDate(task.value.endDate);
  editEmployees.value = await api.fetchEmployees();
  editOpen.value = true;
}

async function saveEditTask() {
  if (!editDraft.value) return;
  const updatedTask = {
    ...editDraft.value,
    startDate: taskDateInput(editStartDate.value),
    endDate: taskDateInput(editEndDate.value),
  };
  await run(() => api.updateTask(updatedTask));
  editOpen.value = false;
}

function pickerDate(value?: string): string {
  return dateOnly(value);
}

function taskDateInput(value: string): string {
  return value ? `${value}T00:00:00Z` : "";
}

function openAssignDialog() {
  if (!task.value) return;
  const currentWorkerId = task.value.workerId || "";
  assignAgentConfig.value = draftFromAgentConfig(task.value.agentConfig);
  assignWorkerId.value =
    (currentWorkerId && assignCandidates.value.some((worker) => worker.id === currentWorkerId)
      ? currentWorkerId
      : "") || "";
  assignAgentType.value = "";
  assignOpen.value = true;
}

function updateAssignWorker(workerId: string) {
  assignWorkerId.value = workerId;
  assignAgentType.value = "";
}

async function assignCurrentTask() {
  if (!task.value || !assignWorkerId.value || !assignAgentType.value) return;
  await run(() =>
    api.assignWorker(
      task.value!.id,
      assignWorkerId.value,
      assignAgentType.value,
      agentConfigInput(assignAgentType.value, assignAgentConfig.value),
    ),
  );
  assignOpen.value = false;
}

async function interruptCurrentTask() {
  if (!task.value) return;
  await run(() => api.interruptTask(task.value!.id));
}

async function archiveCurrentTask() {
  if (!task.value) return;
  await run(() => api.archiveTask(task.value!.id));
}

async function deleteCurrentTask() {
  if (!task.value) return;
  await run(() => api.deleteTask(task.value!.id));
  close();
}

async function copyTaskId() {
  if (!task.value) return;
  await navigator.clipboard?.writeText(task.value.id);
  antMessage.success("Task ID copied");
}

async function sendContinuation() {
  const message = continueMessage.value.trim();
  if (!message || !task.value) return;
  continueMessage.value = "";
  await run(() => api.continueTask(task.value!.id, message));
}

async function refreshReview() {
  if (!task.value) return;
  const [gitStatus, reviewDiff] = await Promise.all([
    api.fetchTaskGitStatus(task.value.id, reviewRemote.value, reviewBranch.value),
    api.fetchTaskGitDiff(task.value.id, "UNCOMMITTED"),
  ]);
  if (detail.value) {
    detail.value = { ...detail.value, gitStatus, reviewDiff };
    selectedFile.value = reviewDiff.files[0] || null;
  }
}

async function stageSelectedFile() {
  const file = selectedFile.value;
  if (!task.value || !file) return;
  await run(() => api.stageTaskGitChanges(task.value!.id, [file.path]));
}

async function unstageSelectedFile() {
  const file = selectedFile.value;
  if (!task.value || !file) return;
  await run(() => api.unstageTaskGitChanges(task.value!.id, [file.path]));
}

async function discardSelectedFile() {
  const file = selectedFile.value;
  if (!task.value || !file) return;
  await run(async () => {
    const backup = await api.discardTaskGitChanges(task.value!.id, [file.path]);
    if (backup) antMessage.success(`Backup created: ${backup.id}`);
  });
}

async function restoreBackup(backupId: string) {
  if (!task.value || !backupId) return;
  await run(() => api.restoreTaskGitBackup(task.value!.id, backupId));
}

async function stageHunk(patch: string) {
  const file = selectedFile.value;
  if (!task.value || !file || !patch.trim()) return;
  await run(() => api.stageTaskGitChanges(task.value!.id, [file.path], patch));
}

async function unstageHunk(patch: string) {
  const file = selectedFile.value;
  if (!task.value || !file || !patch.trim()) return;
  await run(() => api.unstageTaskGitChanges(task.value!.id, [file.path], patch));
}

async function discardHunk(patch: string) {
  const file = selectedFile.value;
  if (!task.value || !file || !patch.trim()) return;
  await run(async () => {
    const backup = await api.discardTaskGitChanges(task.value!.id, [file.path], patch);
    if (backup) antMessage.success(`Backup created: ${backup.id}`);
  });
}

async function gitCommand(command: string, extra: Record<string, unknown> = {}) {
  if (!task.value) return;
  await run(async () => {
    await api.runTaskGitCommand({
      taskId: task.value!.id,
      command,
      remote: reviewRemote.value,
      branch: reviewBranch.value,
      ...extra,
    });
    await refreshReview();
  });
}

async function commit() {
  const message = commitMessage.value.trim();
  if (!message) return;
  commitOpen.value = false;
  commitMessage.value = "";
  await gitCommand("COMMIT", { message });
}

async function publish(strategy: string) {
  publishOpen.value = false;
  await gitCommand("PUBLISH", { publishStrategy: strategy });
}

function ensurePreviewWorker(taskWorkerId?: string) {
  if (selectedPreviewWorkerId.value && previewWorkers.value.some((worker) => worker.id === selectedPreviewWorkerId.value)) {
    return;
  }
  selectedPreviewWorkerId.value =
    (taskWorkerId && previewWorkers.value.some((worker) => worker.id === taskWorkerId) ? taskWorkerId : "") ||
    previewWorkers.value[0]?.id ||
    "";
}

function taskAssignableToWorker(worker: WorkerItem, current: TaskItem): boolean {
  const supportsAgent = !current.agentType || worker.supportedAgents.includes(current.agentType);
  return (
    worker.status === "ONLINE" &&
    worker.supportedAgents.length > 0 &&
    supportsAgent &&
    workerAllowsProject(worker, current.projectId)
  );
}

function workerAllowsProject(worker: WorkerItem, projectId: string): boolean {
  return worker.projectBindingMode === "ALL_PROJECTS" || worker.boundProjectIds.includes(projectId);
}

function openWebPreview() {
  const worker = selectedPreviewWorker.value;
  if (!worker) {
    previewUrl.value = "";
    previewError.value = "Worker is required";
    return;
  }
  try {
    previewUrl.value = api.workerWebProxyUrl(worker.name, previewAddress.value);
    previewError.value = "";
  } catch (cause) {
    previewUrl.value = "";
    previewError.value = cause instanceof Error ? cause.message : String(cause);
  }
}

type MarkdownInline = {
  type: "text" | "code" | "strong";
  text: string;
};

type MarkdownBlock = {
  type: "heading" | "paragraph" | "list" | "code";
  level?: number;
  ordered?: boolean;
  inline?: MarkdownInline[];
  items?: MarkdownInline[][];
  text?: string;
  language?: string;
};

function planFromInteraction(interaction: TaskInteraction): string {
  const rawPayload = interaction.rawPayload?.trim();
  if (!rawPayload) return "";
  try {
    const parsed = JSON.parse(rawPayload);
    return planFromValue(parsed);
  } catch {
    return "";
  }
}

function planFromValue(value: unknown): string {
  if (!value || typeof value !== "object") return "";
  if (Array.isArray(value)) {
    for (const item of value) {
      const plan = planFromValue(item);
      if (plan) return plan;
    }
    return "";
  }
  const record = value as Record<string, unknown>;
  if (typeof record.plan === "string" && record.plan.trim()) {
    return record.plan.trim();
  }
  for (const key of ["tool_input", "toolInput", "input", "payload"]) {
    const plan = planFromValue(record[key]);
    if (plan) return plan;
  }
  return "";
}

function parsePlanMarkdown(markdown: string): MarkdownBlock[] {
  const lines = markdown.replace(/\r\n/g, "\n").split("\n");
  const blocks: MarkdownBlock[] = [];
  let index = 0;
  while (index < lines.length) {
    const line = lines[index];
    if (!line.trim()) {
      index++;
      continue;
    }

    const fence = line.match(/^```(\S*)\s*$/);
    if (fence) {
      index++;
      const codeLines: string[] = [];
      while (index < lines.length && !lines[index].startsWith("```")) {
        codeLines.push(lines[index]);
        index++;
      }
      if (index < lines.length) index++;
      blocks.push({ type: "code", language: fence[1] || "", text: codeLines.join("\n") });
      continue;
    }

    const heading = line.match(/^(#{1,6})\s+(.+?)\s*#*\s*$/);
    if (heading) {
      blocks.push({
        type: "heading",
        level: heading[1].length,
        inline: parseMarkdownInline(heading[2].trim()),
      });
      index++;
      continue;
    }

    const listMatch = parseListLine(line);
    if (listMatch) {
      const ordered = listMatch.ordered;
      const items: MarkdownInline[][] = [];
      while (index < lines.length) {
        const item = parseListLine(lines[index]);
        if (!item || item.ordered !== ordered) break;
        items.push(parseMarkdownInline(item.text));
        index++;
      }
      blocks.push({ type: "list", ordered, items });
      continue;
    }

    const paragraph: string[] = [line.trim()];
    index++;
    while (index < lines.length && lines[index].trim() && !startsMarkdownBlock(lines[index])) {
      paragraph.push(lines[index].trim());
      index++;
    }
    blocks.push({ type: "paragraph", inline: parseMarkdownInline(paragraph.join(" ")) });
  }
  return blocks;
}

function parseListLine(line: string): { ordered: boolean; text: string } | null {
  const ordered = line.match(/^\s*\d+\.\s+(.+)$/);
  if (ordered) return { ordered: true, text: ordered[1].trim() };
  const unordered = line.match(/^\s*[-*+]\s+(.+)$/);
  if (unordered) return { ordered: false, text: unordered[1].trim() };
  return null;
}

function startsMarkdownBlock(line: string): boolean {
  return /^```/.test(line) || /^(#{1,6})\s+/.test(line) || parseListLine(line) !== null;
}

function parseMarkdownInline(text: string): MarkdownInline[] {
  const parts: MarkdownInline[] = [];
  let index = 0;
  while (index < text.length) {
    if (text[index] === "`") {
      const end = text.indexOf("`", index + 1);
      if (end > index + 1) {
        parts.push({ type: "code", text: text.slice(index + 1, end) });
        index = end + 1;
        continue;
      }
    }
    if (text.startsWith("**", index)) {
      const end = text.indexOf("**", index + 2);
      if (end > index + 2) {
        parts.push({ type: "strong", text: text.slice(index + 2, end) });
        index = end + 2;
        continue;
      }
    }
    const nextCode = nextTokenIndex(text, "`", index + 1);
    const nextStrong = nextTokenIndex(text, "**", index + 1);
    const next = Math.min(nextCode, nextStrong);
    parts.push({ type: "text", text: text.slice(index, next) });
    index = next;
  }
  return parts.filter((part) => part.text.length > 0);
}

function nextTokenIndex(text: string, token: string, from: number): number {
  const index = text.indexOf(token, from);
  return index === -1 ? text.length : index;
}

function headingTag(level = 1): string {
  return `h${Math.min(Math.max(level + 2, 3), 6)}`;
}

type DiffHunk = {
  id: string;
  header: string;
  patch: string;
  lines: string[];
};

function parseDiffHunks(patch: string): DiffHunk[] {
  if (!patch.trim()) return [];
  const lines = patch.split(/\r?\n/);
  const fileHeader: string[] = [];
  const hunks: DiffHunk[] = [];
  let current: string[] = [];
  const pushCurrent = () => {
    if (current.length === 0) return;
    const patchLines = [...fileHeader, ...current].filter((line) => line.length > 0);
    hunks.push({
      id: `${hunks.length}-${current[0]}`,
      header: current[0],
      patch: patchLines.join("\n") + "\n",
      lines: [...current],
    });
    current = [];
  };
  for (const line of lines) {
    if (line.startsWith("@@ ")) {
      pushCurrent();
      current = [line];
      continue;
    }
    if (current.length > 0) {
      current.push(line);
      continue;
    }
    if (
      line.startsWith("diff --git ") ||
      line.startsWith("index ") ||
      line.startsWith("new file mode ") ||
      line.startsWith("deleted file mode ") ||
      line.startsWith("similarity index ") ||
      line.startsWith("rename from ") ||
      line.startsWith("rename to ") ||
      line.startsWith("--- ") ||
      line.startsWith("+++ ")
    ) {
      fileHeader.push(line);
    }
  }
  pushCurrent();
  return hunks;
}
</script>

<template>
  <a-modal
    open
    width="1120px"
    :footer="null"
    :closable="false"
    @cancel="close"
  >
    <template #title>
      <div class="task-detail-titlebar">
        <span class="task-detail-title">{{ task?.title || "Task detail" }}</span>
        <div class="task-detail-title-actions">
          <template v-if="task">
            <a-tooltip title="Copy task ID">
              <span class="task-title-action-wrapper">
                <a-button
                  class="task-title-action-button"
                  shape="circle"
                  aria-label="Copy task ID"
                  @click="copyTaskId"
                >
                  <CopyOutlined />
                </a-button>
              </span>
            </a-tooltip>
            <a-tooltip v-if="!isArchived" title="Edit">
              <span class="task-title-action-wrapper">
                <a-button
                  class="task-title-action-button"
                  shape="circle"
                  aria-label="Edit task"
                  :disabled="busy"
                  @click="openEditDialog"
                >
                  <EditOutlined />
                </a-button>
              </span>
            </a-tooltip>
            <a-tooltip v-if="!isArchived" title="Assign">
              <span class="task-title-action-wrapper">
                <a-button
                  class="task-title-action-button"
                  shape="circle"
                  aria-label="Assign"
                  :disabled="!canAssign || isWaitingForInput"
                  :loading="busy"
                  @click="openAssignDialog"
                >
                  <UserSwitchOutlined />
                </a-button>
              </span>
            </a-tooltip>
            <a-tooltip v-if="!isArchived" title="Start">
              <span class="task-title-action-wrapper">
                <a-button
                  class="task-title-action-button"
                  shape="circle"
                  aria-label="Start"
                  type="primary"
                  :disabled="!canStart || isWaitingForInput"
                  :loading="busy"
                  @click="startCurrentTask"
                >
                  <PlayCircleOutlined />
                </a-button>
              </span>
            </a-tooltip>
            <a-tooltip v-if="!isArchived" title="Retry">
              <span class="task-title-action-wrapper">
                <a-button
                  class="task-title-action-button"
                  shape="circle"
                  aria-label="Retry"
                  :disabled="isWaitingForInput"
                  :loading="busy"
                  @click="retryCurrentTask"
                >
                  <RedoOutlined />
                </a-button>
              </span>
            </a-tooltip>
            <a-tooltip v-if="!isArchived && canInterrupt" title="Interrupt">
              <span class="task-title-action-wrapper">
                <a-button
                  class="task-title-action-button"
                  shape="circle"
                  aria-label="Interrupt"
                  :loading="busy"
                  @click="interruptCurrentTask"
                >
                  <StopOutlined />
                </a-button>
              </span>
            </a-tooltip>
            <a-tooltip v-if="!isArchived" title="Archive">
              <span class="task-title-action-wrapper">
                <a-button
                  class="task-title-action-button"
                  shape="circle"
                  aria-label="Archive"
                  :loading="busy"
                  @click="archiveCurrentTask"
                >
                  <InboxOutlined />
                </a-button>
              </span>
            </a-tooltip>
            <a-tooltip v-else title="Delete">
              <span class="task-title-action-wrapper">
                <a-button
                  class="task-title-action-button"
                  shape="circle"
                  danger
                  aria-label="Delete"
                  :loading="busy"
                  @click="deleteCurrentTask"
                >
                  <DeleteOutlined />
                </a-button>
              </span>
            </a-tooltip>
          </template>
          <a-tooltip title="Refresh task">
            <span class="task-title-action-wrapper">
              <a-button
                class="task-title-action-button"
                shape="circle"
                aria-label="Refresh task"
                :loading="loading"
                @click="load()"
              >
                <ReloadOutlined />
              </a-button>
            </span>
          </a-tooltip>
          <a-tooltip title="Close">
            <span class="task-title-action-wrapper">
              <a-button
                class="task-title-action-button"
                shape="circle"
                aria-label="Close"
                @click="close"
              >
                <CloseOutlined />
              </a-button>
            </span>
          </a-tooltip>
        </div>
      </div>
    </template>
    <a-spin :spinning="loading">
      <a-alert v-if="error" type="error" show-icon :message="error" style="margin-bottom: 12px" />
      <template v-if="task">
        <a-space style="margin-bottom: 12px; flex-wrap: wrap">
          <a-tag>{{ task.status }}</a-tag>
          <span>{{ projectName(board.projects, task.projectId) }}</span>
          <span>{{ workerName(board.workers, task.workerId) }}</span>
          <span>{{ taskDateRange(task) }}</span>
        </a-space>

        <a-tabs v-model:active-key="activeTab">
          <a-tab-pane key="overview" tab="Overview">
            <div class="detail-grid">
              <section class="detail-section">
                <h3>Task</h3>
                <p>{{ task.description || "No description" }}</p>
                <p><strong>Employee:</strong> {{ employeeName || "Unassigned" }}</p>
                <p><strong>Agent:</strong> {{ task.agentType || "None" }}</p>
                <p><strong>Base branch:</strong> {{ task.baseBranch }}</p>
                <p v-if="task.worktreePath"><strong>Worktree:</strong> <span class="mono">{{ task.worktreePath }}</span></p>
                <p v-if="task.agentSessionId"><strong>Session:</strong> {{ task.agentSessionId }}</p>
              </section>
              <section class="detail-section">
                <h3>Result</h3>
                <p class="mono">{{ task.result || "No result yet" }}</p>
              </section>
            </div>
            <TaskA2AExecutionRounds :executions="detail?.a2aExecutions || []" />
            <section
              v-if="planBlocks.length"
              class="detail-section plan-section"
              role="region"
              aria-label="Plan"
            >
              <h3>Plan</h3>
              <div class="markdown-body">
                <template v-for="(block, blockIndex) in planBlocks" :key="blockIndex">
                  <component
                    :is="headingTag(block.level)"
                    v-if="block.type === 'heading'"
                    class="markdown-heading"
                  >
                    <template v-for="(part, partIndex) in block.inline || []" :key="partIndex">
                      <code v-if="part.type === 'code'">{{ part.text }}</code>
                      <strong v-else-if="part.type === 'strong'">{{ part.text }}</strong>
                      <template v-else>{{ part.text }}</template>
                    </template>
                  </component>
                  <p v-else-if="block.type === 'paragraph'">
                    <template v-for="(part, partIndex) in block.inline || []" :key="partIndex">
                      <code v-if="part.type === 'code'">{{ part.text }}</code>
                      <strong v-else-if="part.type === 'strong'">{{ part.text }}</strong>
                      <template v-else>{{ part.text }}</template>
                    </template>
                  </p>
                  <ol v-else-if="block.type === 'list' && block.ordered" class="markdown-list">
                    <li v-for="(item, itemIndex) in block.items || []" :key="itemIndex">
                      <template v-for="(part, partIndex) in item" :key="partIndex">
                        <code v-if="part.type === 'code'">{{ part.text }}</code>
                        <strong v-else-if="part.type === 'strong'">{{ part.text }}</strong>
                        <template v-else>{{ part.text }}</template>
                      </template>
                    </li>
                  </ol>
                  <ul v-else-if="block.type === 'list'" class="markdown-list">
                    <li v-for="(item, itemIndex) in block.items || []" :key="itemIndex">
                      <template v-for="(part, partIndex) in item" :key="partIndex">
                        <code v-if="part.type === 'code'">{{ part.text }}</code>
                        <strong v-else-if="part.type === 'strong'">{{ part.text }}</strong>
                        <template v-else>{{ part.text }}</template>
                      </template>
                    </li>
                  </ul>
                  <pre v-else-if="block.type === 'code'" class="markdown-code"><code>{{ block.text }}</code></pre>
                </template>
              </div>
            </section>
            <div v-for="interaction in pendingInteractions" :key="interaction.id" class="interaction-box">
              <textarea :aria-label="interaction.title" readonly :value="interaction.body" />
              <textarea :aria-label="interactionPayloadSummary(interaction)" readonly :value="interactionPayloadSummary(interaction)" />
              <a-space>
                <a-button type="primary" :loading="busy" @click="approve(interaction)">Approve</a-button>
                <a-button :loading="busy" @click="run(() => api.respondTaskInteraction(interaction.id, 'DENY'))">Deny</a-button>
              </a-space>
            </div>
          </a-tab-pane>

          <a-tab-pane key="conversation" tab="Conversation">
            <div class="record-list">
              <div v-for="message in detail?.conversations || []" :key="message.id" class="record-card">
                <div class="record-title">{{ message.role }}</div>
                <div class="record-subtitle mono">{{ message.content }}</div>
              </div>
            </div>
            <a-textarea
              v-model:value="continueMessage"
              aria-label="Continue conversation"
              placeholder="Continue conversation"
              style="margin-top: 12px"
              :rows="3"
            />
            <a-button
              type="primary"
              aria-label="Send continuation"
              :loading="busy"
              style="margin-top: 8px"
              @click="sendContinuation"
            >
              Send continuation
            </a-button>
          </a-tab-pane>

          <a-tab-pane key="logs" tab="Logs">
            <div class="record-list">
              <a-empty v-if="(detail?.logs || []).length === 0" description="No logs" />
              <div v-for="log in detail?.logs || []" :key="log.id" class="record-card">
                <div class="record-title">{{ log.stream }}</div>
                <div class="record-subtitle mono">{{ log.content }}</div>
              </div>
            </div>
          </a-tab-pane>

          <a-tab-pane key="review" tab="Review">
            <a-alert
              v-if="detail?.reviewError"
              type="warning"
              show-icon
              :message="detail.reviewError"
              style="margin-bottom: 12px"
            />
            <section class="detail-section" style="margin-bottom: 12px">
              <h3>Git workspace</h3>
              <a-space style="flex-wrap: wrap">
                <a-input v-model:value="reviewRemote" aria-label="Remote" style="width: 160px" />
                <a-input v-model:value="reviewBranch" aria-label="Target branch" style="width: 180px" />
                <a-tag v-if="detail?.gitStatus">ahead {{ detail.gitStatus.ahead }}</a-tag>
                <a-tag v-if="detail?.gitStatus">behind {{ detail.gitStatus.behind }}</a-tag>
                <a-tag v-if="detail?.gitStatus?.hasStagedChanges" color="green">staged changes</a-tag>
                <a-tag v-if="detail?.gitStatus?.hasUnstagedChanges" color="gold">unstaged changes</a-tag>
                <a-tag v-if="detail?.gitStatus?.hasUntrackedFiles" color="blue">untracked files</a-tag>
                <a-button @click="gitCommand('FETCH')">Fetch</a-button>
                <a-button @click="gitCommand('REBASE')">Rebase onto target</a-button>
                <a-button @click="gitCommand('MERGE_BASE')">Merge target into task branch</a-button>
                <a-button :disabled="!selectedFile" :loading="busy" @click="stageSelectedFile">Stage</a-button>
                <a-button :disabled="!selectedFile" :loading="busy" @click="unstageSelectedFile">Unstage</a-button>
                <a-button @click="commitOpen = true">Commit staged</a-button>
                <a-popconfirm
                  title="Discard selected file changes? A backup patch will be created."
                  ok-text="Discard"
                  cancel-text="Cancel"
                  @confirm="discardSelectedFile"
                >
                  <a-button danger :disabled="!selectedFile" :loading="busy">Discard</a-button>
                </a-popconfirm>
                <a-button
                  :disabled="!latestBackup"
                  :loading="busy"
                  @click="latestBackup && restoreBackup(latestBackup.id)"
                >
                  {{ latestBackup ? `Restore ${latestBackup.id}` : "Restore" }}
                </a-button>
                <a-button @click="publishOpen = true">Publish fast-forward</a-button>
                <a-button @click="publish('MERGE_COMMIT')">Publish merge commit</a-button>
              </a-space>
              <div v-if="commitOpen" style="margin-top: 10px">
                <a-space>
                  <a-input v-model:value="commitMessage" aria-label="Commit message" style="width: 320px" />
                  <a-button type="primary" @click="commit">Commit</a-button>
                </a-space>
              </div>
              <div v-if="publishOpen" style="margin-top: 10px">
                <a-space>
                  <span>Publish the committed task branch to {{ reviewRemote }}/{{ reviewBranch }} using a fast-forward push?</span>
                  <a-button type="primary" @click="publish('FAST_FORWARD')">Publish</a-button>
                </a-space>
              </div>
            </section>
            <section v-if="selectedFile" class="detail-section review-selected-file">
              <div class="review-selected-header">
                <div>
                  <h3>Selected change</h3>
                  <p class="mono">{{ selectedFile.path }}</p>
                </div>
                <a-space wrap>
                  <a-tag :color="selectedFile.staged ? 'green' : 'gold'">
                    {{ selectedFile.staged ? "STAGED" : "UNSTAGED" }}
                  </a-tag>
                  <a-tag>{{ selectedFile.status }}</a-tag>
                  <a-tag color="green">+{{ selectedFile.additions }}</a-tag>
                  <a-tag color="red">-{{ selectedFile.deletions }}</a-tag>
                </a-space>
              </div>
            </section>
            <section v-if="detail?.backups?.length" class="detail-section review-backups">
              <h3>Backups</h3>
              <div v-for="backup in detail.backups" :key="backup.id" class="review-backup-row">
                <span class="mono">{{ backup.id }} {{ backup.paths.join(", ") }}</span>
                <a-button size="small" :loading="busy" @click="restoreBackup(backup.id)">Restore backup</a-button>
              </div>
            </section>
            <div class="review-layout">
              <div class="review-files">
                <a-empty v-if="diffFiles.length === 0" description="No changes" />
                <a-button
                  v-for="file in diffFiles"
                  :key="file.path"
                  block
                  class="review-file-button"
                  :class="{ 'review-file-button-active': selectedFile?.path === file.path }"
                  @click="selectedFile = file"
                >
                  <span class="review-file-name">{{ file.path }}</span>
                  <span class="review-file-meta">
                    {{ file.staged ? "STAGED" : "UNSTAGED" }} {{ file.status }} +{{ file.additions }} -{{ file.deletions }}
                  </span>
                </a-button>
              </div>
              <div class="review-diff-pane">
                <pre class="review-diff">{{ selectedPatch || "No diff" }}</pre>
                <div v-if="selectedHunks.length" class="review-hunk-list">
                  <div v-for="hunk in selectedHunks" :key="hunk.id" class="review-hunk">
                    <div class="review-hunk-toolbar">
                      <span class="mono">{{ hunk.header }}</span>
                      <a-space>
                        <a-button size="small" :loading="busy" @click="stageHunk(hunk.patch)">Stage hunk</a-button>
                        <a-button size="small" :loading="busy" @click="unstageHunk(hunk.patch)">Unstage hunk</a-button>
                        <a-button size="small" danger :loading="busy" @click="discardHunk(hunk.patch)">
                          Discard hunk
                        </a-button>
                      </a-space>
                    </div>
                    <pre class="review-hunk-body">{{ hunk.lines.join("\n") }}</pre>
                  </div>
                </div>
              </div>
            </div>
          </a-tab-pane>

          <a-tab-pane key="web" tab="Web preview">
            <section class="detail-section">
              <a-space wrap>
                <a-select
                  v-model:value="selectedPreviewWorkerId"
                  aria-label="Worker"
                  style="width: 220px"
                  @change="previewUrl = ''; previewError = ''"
                >
                  <a-select-option v-for="worker in previewWorkers" :key="worker.id" :value="worker.id">
                    {{ worker.name }}
                  </a-select-option>
                </a-select>
                <a-input
                  v-model:value="previewAddress"
                  aria-label="Worker web address"
                  placeholder="localhost:3000"
                  style="width: 360px"
                  @press-enter="openWebPreview"
                />
                <a-button
                  type="primary"
                  aria-label="Open worker web preview"
                  :disabled="!canOpenPreview"
                  @click="openWebPreview"
                >
                  Open worker web preview
                </a-button>
              </a-space>
              <a-alert
                v-if="previewError"
                type="error"
                show-icon
                :message="previewError"
                style="margin-top: 12px"
              />
              <template v-if="previewUrl">
                <p class="mono" style="margin-top: 12px">{{ previewUrl }}</p>
                <iframe
                  data-testid="task-detail-worker-web-preview"
                  class="worker-web-preview"
                  :src="previewUrl"
                  title="Worker web preview"
                  allow="clipboard-read; clipboard-write"
                />
              </template>
            </section>
          </a-tab-pane>

          <a-tab-pane key="terminal" tab="Terminal">
            <TerminalPanel
              :check-url="api.workerTerminalCheckUrlForTask(task.id)"
              :socket-url="api.workerTerminalWebSocketUrlForTask(task.id)"
            />
          </a-tab-pane>

          <a-tab-pane key="events" tab="Domain events">
            <div class="record-list">
              <div v-for="event in detail?.events || []" :key="event.eventId" class="record-card">
                <div class="record-title">{{ event.eventType }}</div>
                <div class="record-subtitle mono">{{ event.payload }}</div>
              </div>
            </div>
          </a-tab-pane>
        </a-tabs>
      </template>
    </a-spin>
  </a-modal>

  <a-modal
    v-model:open="editOpen"
    title="Edit task"
    ok-text="Save"
    :ok-button-props="{ disabled: !canSaveEditTask, loading: busy }"
    @ok="saveEditTask"
  >
    <a-form v-if="editDraft" layout="vertical">
      <a-form-item label="Title">
        <a-input v-model:value="editDraft.title" aria-label="Edit title" />
      </a-form-item>
      <a-form-item label="Description">
        <a-textarea v-model:value="editDraft.description" aria-label="Edit description" />
      </a-form-item>
      <a-form-item label="Project">
        <a-select v-model:value="editDraft.projectId" aria-label="Edit project">
          <a-select-option v-for="project in board.projects" :key="project.id" :value="project.id">
            {{ project.name }}
          </a-select-option>
        </a-select>
      </a-form-item>
      <a-form-item label="Employee">
        <a-select
          v-model:value="editDraft.ownerUserId"
          aria-label="Edit employee"
          show-search
          allow-clear
          option-filter-prop="label"
          placeholder="Select employee"
        >
          <a-select-option v-for="emp in editEmployees" :key="emp.id" :value="emp.id" :label="emp.name">
            {{ emp.name }}
          </a-select-option>
        </a-select>
      </a-form-item>
      <a-form-item label="Base branch">
        <a-input v-model:value="editDraft.baseBranch" aria-label="Edit base branch" />
      </a-form-item>
      <a-form-item label="Start date">
        <a-date-picker
          v-model:value="editStartDate"
          value-format="YYYY-MM-DD"
          placeholder="Edit start date"
          style="width: 100%"
        />
      </a-form-item>
      <a-form-item label="End date">
        <a-date-picker
          v-model:value="editEndDate"
          value-format="YYYY-MM-DD"
          placeholder="Edit end date"
          style="width: 100%"
        />
      </a-form-item>
    </a-form>
  </a-modal>

  <a-modal
    v-model:open="assignOpen"
    title="Assign worker"
    ok-text="Assign"
    :ok-button-props="{ disabled: !canAssignSelection, loading: busy }"
    @ok="assignCurrentTask"
  >
    <a-form layout="vertical">
      <a-form-item label="Worker">
        <a-select :value="assignWorkerId" aria-label="Worker" @change="updateAssignWorker">
          <a-select-option v-for="worker in assignCandidates" :key="worker.id" :value="worker.id">
            {{ worker.name }}
          </a-select-option>
        </a-select>
      </a-form-item>
      <a-form-item v-if="assignWorkerId" label="Agent">
        <a-select v-model:value="assignAgentType" aria-label="Agent">
          <a-select-option v-for="agent in assignAgentOptions" :key="agent" :value="agent">
            {{ agent === "codex" ? "Codex" : agent === "claude" ? "Claude" : agent }}
          </a-select-option>
        </a-select>
      </a-form-item>
      <AgentConfigFields
        v-if="assignWorkerId && assignAgentType"
        :agent-type="assignAgentType"
        :draft="assignAgentConfig"
      />
    </a-form>
  </a-modal>
</template>
