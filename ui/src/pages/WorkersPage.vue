<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from "vue";
import { PlusOutlined, ReloadOutlined } from "@ant-design/icons-vue";
import { api } from "../api";
import type {
  AgentRuntimeEnvVar,
  PageRequest,
  PagedResult,
  ProjectItem,
  SortRequest,
  WorkerAgentRuntimeEnv,
  WorkerItem,
} from "../models";
import { defaultPage, defaultSort } from "../models";
import PageHeader from "../components/PageHeader.vue";
import PaginationBar from "../components/PaginationBar.vue";
import SearchToolbar from "../components/SearchToolbar.vue";
import TerminalPanel from "../components/TerminalPanel.vue";
import WorkerStartupCommandDialog from "../components/WorkerStartupCommandDialog.vue";

interface WorkerFormDraft {
  workerId: string;
  workerName: string;
  workDir: string;
  supportedAgents: string[];
  projectBindingMode: "ALL_PROJECTS" | "SPECIFIC_PROJECTS";
  boundProjectIds: string[];
}

function newDraft(): WorkerFormDraft {
  return {
    workerId: "worker-local",
    workerName: "local-worker",
    workDir: "./worker-data",
    supportedAgents: ["codex"],
    projectBindingMode: "ALL_PROJECTS",
    boundProjectIds: [],
  };
}

const pageReq = ref<PageRequest>(defaultPage());
const sort = ref<SortRequest>(defaultSort());
const search = ref("");
const loading = ref(false);
const error = ref("");
const data = ref<PagedResult<WorkerItem>>({ items: [], totalCount: 0 });
const projects = ref<ProjectItem[]>([]);
const formDialogOpen = ref(false);
const commandDialogOpen = ref(false);
const draft = reactive<WorkerFormDraft>(newDraft());
const terminalWorker = ref<WorkerItem | null>(null);
let unsubscribe: (() => void) | undefined;


watch([search, sort], () => {
  pageReq.value = defaultPage();
  void load();
});
watch(pageReq, () => void load());

void load();
unsubscribe = api.subscribeDomainEvents((event) => {
  if (event.aggregateType === "Worker" || event.aggregateType === "Project") void load(false);
});

onBeforeUnmount(() => unsubscribe?.());

async function load(showSpinner = true) {
  if (showSpinner) loading.value = true;
  error.value = "";
  try {
    const [workerPage, projectItems] = await Promise.all([
      api.fetchWorkersPage(pageReq.value, search.value, sort.value),
      api.fetchProjects(),
    ]);
    data.value = workerPage;
    projects.value = projectItems;
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause);
  } finally {
    loading.value = false;
  }
}

function openNewWorkerForm() {
  Object.assign(draft, newDraft());
  formDialogOpen.value = true;
}

function generateCommand() {
  formDialogOpen.value = false;
  commandDialogOpen.value = true;
}

// --- Edit worker ---
const editDialogOpen = ref(false);
const editDraft = ref<WorkerItem | null>(null);
const activeAgent = ref<"codex" | "claude">("codex");
const envDialogOpen = ref(false);
const envDraft = ref<AgentRuntimeEnvVar>({ key: "", value: "", description: "", enabled: true, sensitive: true });
const editingEnvKey = ref("");

const activeEnvVars = computed(() => {
  if (!editDraft.value) return [];
  return envGroup(editDraft.value.agentRuntimeEnv, activeAgent.value).vars;
});

const envColumns = [
  { title: "Key", dataIndex: "key", key: "key" },
  { title: "Value", key: "value" },
  { title: "Description", dataIndex: "description", key: "description" },
  { title: "Status", key: "status" },
  { title: "Actions", key: "actions", width: 160 },
];

function openEditWorker(worker: WorkerItem) {
  editDraft.value = cloneWorker(worker);
  activeAgent.value = "codex";
  editDialogOpen.value = true;
}

async function saveEditWorker() {
  if (!editDraft.value) return;
  await api.updateWorker(editDraft.value);
  editDialogOpen.value = false;
  await load(false);
}

function openEnvDialog() {
  editingEnvKey.value = "";
  envDraft.value = { key: "", value: "", description: "", enabled: true, sensitive: true };
  envDialogOpen.value = true;
}

function editEnvVar(item: AgentRuntimeEnvVar) {
  editingEnvKey.value = item.key;
  envDraft.value = {
    key: item.key,
    value: item.sensitive ? "" : item.value ?? item.valueMasked ?? "",
    valueMasked: item.valueMasked,
    description: item.description ?? "",
    enabled: item.enabled,
    sensitive: item.sensitive,
  };
  envDialogOpen.value = true;
}

function saveEnvVar() {
  if (!editDraft.value || !envDraft.value.key.trim()) return;
  const group = envGroup(editDraft.value.agentRuntimeEnv, activeAgent.value);
  const next = {
    ...envDraft.value,
    key: envDraft.value.key.trim(),
    description: envDraft.value.description?.trim() || "",
  };
  if (!next.value?.trim()) delete next.value;
  group.vars = [
    ...group.vars.filter((item) => item.key !== editingEnvKey.value && item.key !== next.key),
    next,
  ];
  envDialogOpen.value = false;
}

function removeEnvVar(item: AgentRuntimeEnvVar) {
  if (!editDraft.value) return;
  const group = envGroup(editDraft.value.agentRuntimeEnv, activeAgent.value);
  group.vars = group.vars.filter((existing) => existing.key !== item.key);
}

function cloneWorker(worker: WorkerItem): WorkerItem {
  const cloned: WorkerItem = JSON.parse(JSON.stringify(worker));
  for (const agent of ["codex", "claude"] as const) envGroup(cloned.agentRuntimeEnv, agent);
  cloned.supportedAgents = [...worker.supportedAgents];
  cloned.boundProjectIds = [...worker.boundProjectIds];
  return cloned;
}

function envGroup(groups: WorkerAgentRuntimeEnv[], agentType: "codex" | "claude"): WorkerAgentRuntimeEnv {
  let group = groups.find((item) => item.agentType === agentType);
  if (!group) {
    group = { agentType, vars: [] };
    groups.push(group);
  }
  return group;
}
</script>

<template>
  <div>
    <PageHeader title="Workers">
      <template #actions>
        <a-button aria-label="Refresh workers" @click="load()"><ReloadOutlined /> Refresh</a-button>
        <a-button type="primary" @click="openNewWorkerForm()"><PlusOutlined /> New worker</a-button>
      </template>
    </PageHeader>
    <main class="content">
      <SearchToolbar
        :search="search"
        :sort="sort"
        :sort-options="[
          { field: 'CREATED_AT', label: 'Created' },
          { field: 'UPDATED_AT', label: 'Updated' },
          { field: 'NAME', label: 'Name' },
          { field: 'STATUS', label: 'Status' },
          { field: 'LAST_HEARTBEAT_AT', label: 'Last heartbeat' },
        ]"
        @update:search="search = $event"
        @update:sort="sort = $event"
      />
      <a-alert v-if="error" type="error" show-icon :message="error" style="margin-bottom: 12px" />
      <a-spin :spinning="loading">
        <div class="record-list">
          <div
            v-for="worker in data.items"
            :key="worker.id"
            class="record-card"
            role="group"
            :aria-label="`${worker.name} ${worker.status}`"
          >
            <div class="record-header">
              <div>
                <div class="record-title">{{ worker.name }}</div>
                <div class="record-subtitle">{{ worker.workDir }}&#10;{{ worker.supportedAgents.join(", ") }} {{ worker.projectBindingMode }}</div>
              </div>
              <a-space wrap>
                <a-tag>{{ worker.status }}</a-tag>
                <span>{{ worker.currentTaskIds.length ? `${worker.currentTaskIds.length} running` : "No running tasks" }}</span>
                <a-button aria-label="Open worker terminal" @click="terminalWorker = worker">Open worker terminal</a-button>
                <a-button aria-label="Edit worker" @click="openEditWorker(worker)">Edit worker</a-button>
                <a-button aria-label="Enable worker" @click="api.enableWorker(worker.id).then(() => load(false))">Enable worker</a-button>
                <a-button aria-label="Disable worker" @click="api.disableWorker(worker.id).then(() => load(false))">Disable worker</a-button>
              </a-space>
            </div>
          </div>
        </div>
        <PaginationBar :page="pageReq" :total="data.totalCount" @change="pageReq = $event" />
      </a-spin>
    </main>

    <a-modal
      v-model:open="formDialogOpen"
      title="Generate worker startup command"
      ok-text="Generate command"
      width="860px"
      @ok="generateCommand"
    >
      <a-form layout="vertical">
        <a-form-item label="Worker ID"><a-input v-model:value="draft.workerId" aria-label="Worker ID" /></a-form-item>
        <a-form-item label="Name"><a-input v-model:value="draft.workerName" aria-label="Name" /></a-form-item>
        <a-form-item label="Work directory"><a-input v-model:value="draft.workDir" aria-label="Work directory" /></a-form-item>
        <a-form-item label="Supported agents">
          <a-checkbox-group v-model:value="draft.supportedAgents" :options="['codex', 'claude']" />
        </a-form-item>
        <a-form-item label="Project binding">
          <a-radio-group v-model:value="draft.projectBindingMode">
            <a-radio-button value="ALL_PROJECTS">All projects</a-radio-button>
            <a-radio-button value="SPECIFIC_PROJECTS">Specific projects</a-radio-button>
          </a-radio-group>
        </a-form-item>
        <a-form-item v-if="draft.projectBindingMode === 'SPECIFIC_PROJECTS'" label="Bound projects">
          <a-select v-model:value="draft.boundProjectIds" mode="multiple" aria-label="Bound projects">
            <a-select-option v-for="project in projects" :key="project.id" :value="project.id">
              {{ project.name }}
            </a-select-option>
          </a-select>
        </a-form-item>
      </a-form>
    </a-modal>

    <WorkerStartupCommandDialog
      v-model:open="commandDialogOpen"
      :worker-input="draft"
    />

    <a-modal v-model:open="editDialogOpen" title="Edit worker" ok-text="Save" width="860px" @ok="saveEditWorker">
      <a-form v-if="editDraft" layout="vertical">
        <a-form-item label="Name"><a-input v-model:value="editDraft.name" aria-label="Name" /></a-form-item>
        <a-form-item label="Work directory"><a-input v-model:value="editDraft.workDir" aria-label="Work directory" /></a-form-item>
        <a-form-item label="Supported agents">
          <a-checkbox-group v-model:value="editDraft.supportedAgents" :options="['codex', 'claude']" />
        </a-form-item>
        <a-form-item label="Project binding">
          <a-radio-group v-model:value="editDraft.projectBindingMode">
            <a-radio-button value="ALL_PROJECTS">All projects</a-radio-button>
            <a-radio-button value="SPECIFIC_PROJECTS">Specific projects</a-radio-button>
          </a-radio-group>
        </a-form-item>
        <a-form-item v-if="editDraft.projectBindingMode === 'SPECIFIC_PROJECTS'" label="Bound projects">
          <a-select v-model:value="editDraft.boundProjectIds" mode="multiple" aria-label="Bound projects">
            <a-select-option v-for="project in projects" :key="project.id" :value="project.id">
              {{ project.name }}
            </a-select-option>
          </a-select>
        </a-form-item>
        <!-- Runtime environment section -->
        <a-divider>Runtime environment</a-divider>
        <a-tabs v-model:activeKey="activeAgent" style="margin-bottom: 12px">
          <a-tab-pane key="codex" tab="Codex" />
          <a-tab-pane key="claude" tab="Claude" />
        </a-tabs>
        <a-button style="margin-bottom: 12px" @click="openEnvDialog">New env var</a-button>
        <a-table :data-source="activeEnvVars" :columns="envColumns" row-key="key" size="small" :pagination="false">
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'value'">{{ record.sensitive ? '********' : (record.value || record.valueMasked || '') }}</template>
            <template v-if="column.key === 'status'">
              <a-tag :color="record.enabled ? 'green' : 'default'">{{ record.enabled ? 'enabled' : 'disabled' }}</a-tag>
              <a-tag v-if="record.sensitive" color="orange">sensitive</a-tag>
            </template>
            <template v-if="column.key === 'actions'">
              <a-space>
                <a-button size="small" @click="editEnvVar(record)">Edit</a-button>
                <a-button size="small" danger @click="removeEnvVar(record)">Remove</a-button>
              </a-space>
            </template>
          </template>
        </a-table>
      </a-form>
    </a-modal>

    <a-modal
      v-model:open="envDialogOpen"
      :title="editingEnvKey ? 'Edit env var' : 'Create env var'"
      ok-text="Save"
      @ok="saveEnvVar"
    >
      <a-form layout="vertical">
        <a-form-item label="Key"><a-input v-model:value="envDraft.key" aria-label="Key" /></a-form-item>
        <a-form-item label="Value">
          <a-input-password
            v-if="envDraft.sensitive"
            v-model:value="envDraft.value"
            aria-label="Value"
            :placeholder="editingEnvKey ? 'Leave blank to keep current value' : ''"
          />
          <a-input v-else v-model:value="envDraft.value" aria-label="Value" />
        </a-form-item>
        <a-form-item label="Description"><a-input v-model:value="envDraft.description" aria-label="Description" /></a-form-item>
        <a-form-item>
          <a-checkbox v-model:checked="envDraft.enabled">Enabled</a-checkbox>
          <a-checkbox v-model:checked="envDraft.sensitive" style="margin-left: 12px">Sensitive</a-checkbox>
        </a-form-item>
      </a-form>
    </a-modal>

    <a-modal :open="!!terminalWorker" :footer="null" width="960px" @cancel="terminalWorker = null">
      <div v-if="terminalWorker" role="alertdialog" aria-modal="true">
        <h2>Worker terminal</h2>
        <TerminalPanel
          :check-url="api.workerTerminalCheckUrlForWorker(terminalWorker.id)"
          :socket-url="api.workerTerminalWebSocketUrlForWorker(terminalWorker.id)"
        />
      </div>
    </a-modal>
  </div>
</template>
