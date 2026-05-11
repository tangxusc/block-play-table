<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { message as antMessage } from "ant-design-vue";
import { api } from "../api";
import type { BoardData, TaskDetailData, TaskGitDiffFile, TaskInteraction, TaskItem, WorkerItem } from "../models";
import {
  agentConfigInput,
  draftFromAgentConfig,
  emptyAgentConfigDraft,
  type AgentConfigDraft,
} from "../agentConfig";
import {
  interactionPayloadSummary,
  projectName,
  taskDateRange,
  workerName,
} from "../format";
import TerminalPanel from "./TerminalPanel.vue";
import AgentConfigFields from "./AgentConfigFields.vue";

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
const assignWorkerId = ref("");
const assignAgentType = ref("");
const assignAgentConfig = ref<AgentConfigDraft>(emptyAgentConfigDraft());
const commitMessage = ref("");
const selectedPreviewWorkerId = ref("");
const previewAddress = ref("");
const previewUrl = ref("");
const previewError = ref("");
let unsubscribe: (() => void) | undefined;
let pollTimer: number | undefined;

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
    window.clearInterval(pollTimer);
    pollTimer = window.setInterval(() => void load(false), 5000);
  },
  { immediate: true },
);

onBeforeUnmount(() => {
  unsubscribe?.();
  window.clearInterval(pollTimer);
});

async function load(showSpinner = true) {
  if (showSpinner) loading.value = true;
  error.value = "";
  try {
    const loaded = await api.fetchTaskDetail(props.taskId);
    detail.value = loaded;
    ensurePreviewWorker(loaded.task.workerId);
    selectedFile.value =
      loaded.reviewDiff?.files.find((file) => file.path === selectedFile.value?.path) ||
      loaded.reviewDiff?.files[0] ||
      null;
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause);
  } finally {
    loading.value = false;
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
    @cancel="close"
  >
    <template #title>
      <div class="task-detail-titlebar">
        <span class="task-detail-title">{{ task?.title || "Task detail" }}</span>
        <a-space v-if="task" wrap>
          <a-button aria-label="Copy task ID" @click="copyTaskId">Copy task ID</a-button>
          <a-button
            v-if="!isArchived"
            aria-label="Assign"
            :disabled="!canAssign || isWaitingForInput"
            :loading="busy"
            @click="openAssignDialog"
          >
            Assign
          </a-button>
          <a-button
            v-if="!isArchived"
            aria-label="Start"
            type="primary"
            :disabled="!canStart || isWaitingForInput"
            :loading="busy"
            @click="startCurrentTask"
          >
            Start
          </a-button>
          <a-button
            v-if="!isArchived"
            aria-label="Retry"
            :disabled="isWaitingForInput"
            :loading="busy"
            @click="retryCurrentTask"
          >
            Retry
          </a-button>
          <a-button
            v-if="!isArchived && canInterrupt"
            aria-label="Interrupt"
            :loading="busy"
            @click="interruptCurrentTask"
          >
            Interrupt
          </a-button>
          <a-button v-if="!isArchived" aria-label="Archive" :loading="busy" @click="archiveCurrentTask">
            Archive
          </a-button>
          <a-button v-else danger aria-label="Delete" :loading="busy" @click="deleteCurrentTask">
            Delete
          </a-button>
        </a-space>
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
