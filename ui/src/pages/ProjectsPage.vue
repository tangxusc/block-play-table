<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from "vue";
import { PlusOutlined, ReloadOutlined } from "@ant-design/icons-vue";
import { api } from "../api";
import type { PageRequest, PagedResult, ProjectItem, SortRequest } from "../models";
import { defaultPage, defaultSort } from "../models";
import PageHeader from "../components/PageHeader.vue";
import PaginationBar from "../components/PaginationBar.vue";
import SearchToolbar from "../components/SearchToolbar.vue";

const page = ref<PageRequest>(defaultPage());
const sort = ref<SortRequest>(defaultSort());
const search = ref("");
const loading = ref(false);
const error = ref("");
const data = ref<PagedResult<ProjectItem>>({ items: [], totalCount: 0 });
const dialogOpen = ref(false);
const draft = ref<ProjectItem | null>(null);
let unsubscribe: (() => void) | undefined;

watch([search, sort], () => {
  page.value = defaultPage();
  void load();
});
watch(page, () => void load());

void load();
unsubscribe = api.subscribeDomainEvents((event) => {
  if (event.aggregateType === "Project") void load(false);
});

onBeforeUnmount(() => unsubscribe?.());

async function load(showSpinner = true) {
  if (showSpinner) loading.value = true;
  error.value = "";
  try {
    data.value = await api.fetchProjectsPage(page.value, search.value, sort.value);
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause);
  } finally {
    loading.value = false;
  }
}

function openProject(project?: ProjectItem) {
  draft.value = project
    ? { ...project }
    : {
        id: "",
        name: "",
        gitUrl: "",
        defaultBranch: "main",
        worktreeNamePrefix: "task",
        archived: false,
        createdAt: "",
        updatedAt: "",
      };
  dialogOpen.value = true;
}

async function saveProject() {
  if (!draft.value) return;
  if (draft.value.id) await api.updateProject(draft.value);
  else await api.createProject(draft.value);
  dialogOpen.value = false;
  await load(false);
}
</script>

<template>
  <div>
    <PageHeader title="Projects">
      <template #actions>
        <a-button aria-label="Refresh projects" @click="load()"><ReloadOutlined /> Refresh</a-button>
        <a-button type="primary" @click="openProject()"><PlusOutlined /> New project</a-button>
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
          { field: 'GIT_URL', label: 'Git URL' },
          { field: 'DEFAULT_BRANCH', label: 'Default branch' },
        ]"
        @update:search="search = $event"
        @update:sort="sort = $event"
      />
      <a-alert v-if="error" type="error" show-icon :message="error" style="margin-bottom: 12px" />
      <a-spin :spinning="loading">
        <div class="record-list">
          <div
            v-for="project in data.items"
            :key="project.id"
            class="record-card"
            role="group"
            :aria-label="`${project.name} ${project.gitUrl}`"
          >
            <div class="record-header">
              <div>
                <div class="record-title">{{ project.name }}</div>
                <div class="record-subtitle">{{ project.gitUrl }}&#10;{{ project.defaultBranch }} {{ project.worktreeNamePrefix }}</div>
              </div>
              <a-space>
                <a-tag v-if="project.archived">ARCHIVED</a-tag>
                <a-button aria-label="Edit project" @click="openProject(project)">Edit project</a-button>
                <a-button
                  aria-label="Archive project"
                  :disabled="project.archived"
                  @click="api.archiveProject(project.id).then(() => load(false))"
                >
                  Archive project
                </a-button>
              </a-space>
            </div>
          </div>
        </div>
        <PaginationBar :page="page" :total="data.totalCount" @change="page = $event" />
      </a-spin>
    </main>

    <a-modal v-model:open="dialogOpen" :title="draft?.id ? 'Edit project' : 'Create project'" ok-text="Save" @ok="saveProject">
      <a-form v-if="draft" layout="vertical">
        <a-form-item label="Name"><a-input v-model:value="draft.name" aria-label="Name" /></a-form-item>
        <a-form-item label="Git URL"><a-input v-model:value="draft.gitUrl" aria-label="Git URL" /></a-form-item>
        <a-form-item label="Default branch"><a-input v-model:value="draft.defaultBranch" aria-label="Default branch" /></a-form-item>
        <a-form-item label="Worktree prefix"><a-input v-model:value="draft.worktreeNamePrefix" aria-label="Worktree prefix" /></a-form-item>
      </a-form>
    </a-modal>
  </div>
</template>
