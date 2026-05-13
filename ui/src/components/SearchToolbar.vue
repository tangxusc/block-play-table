<script setup lang="ts">
import type { ProjectItem, SortRequest } from "../models";

const props = defineProps<{
  search: string;
  sort: SortRequest;
  sortOptions: Array<{ field: string; label: string }>;
  projects?: ProjectItem[];
  selectedProjectId?: string;
}>();

const emit = defineEmits<{
  "update:search": [value: string];
  "update:sort": [value: SortRequest];
  "update:project": [value: string];
}>();

function toggleDirection() {
  emit("update:sort", {
    ...props.sort,
    direction: props.sort.direction === "DESC" ? "ASC" : "DESC",
  });
}

function popupContainer() {
  return document.body;
}
</script>

<template>
  <div class="toolbar">
    <a-input-search
      :value="search"
      aria-label="Search"
      placeholder="Search"
      allow-clear
      class="toolbar-search"
      @update:value="emit('update:search', String($event))"
      @search="emit('update:search', $event)"
    />
    <a-select
      v-if="projects"
      :value="selectedProjectId || undefined"
      aria-label="Project"
      class="toolbar-project"
      placeholder="Project All projects"
      show-search
      allow-clear
      option-filter-prop="label"
      :get-popup-container="popupContainer"
      @change="emit('update:project', String($event || ''))"
      @clear="emit('update:project', '')"
    >
      <a-select-option value="" label="All projects">All projects</a-select-option>
      <a-select-option
        v-for="project in projects"
        :key="project.id"
        :value="project.id"
        :label="project.name"
      >
        {{ project.name }}
      </a-select-option>
    </a-select>
    <a-select
      :value="sort.field"
      aria-label="Sort field"
      class="toolbar-sort"
      @change="emit('update:sort', { ...sort, field: String($event) })"
    >
      <a-select-option
        v-for="option in sortOptions"
        :key="option.field"
        :value="option.field"
      >
        {{ option.label }}
      </a-select-option>
    </a-select>
    <a-button
      :aria-label="sort.direction === 'DESC' ? 'Sort descending' : 'Sort ascending'"
      @click="toggleDirection"
    >
      {{ sort.direction === "DESC" ? "Sort descending" : "Sort ascending" }}
    </a-button>
  </div>
</template>
