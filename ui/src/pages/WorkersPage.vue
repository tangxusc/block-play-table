<script setup lang="ts">
import { onBeforeUnmount, reactive, ref, watch } from "vue";
import { PlusOutlined, ReloadOutlined } from "@ant-design/icons-vue";
import { api } from "../api";
import type {
  PageRequest,
  PagedResult,
  ProjectItem,
  SortRequest,
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
