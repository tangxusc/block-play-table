<script setup lang="ts">
import { ref } from "vue";
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

const menuOpen = ref(false);

function toggleDirection() {
  emit("update:sort", {
    ...props.sort,
    direction: props.sort.direction === "DESC" ? "ASC" : "DESC",
  });
}

function setProject(projectId: string) {
  emit("update:project", projectId);
  menuOpen.value = false;
}
</script>

<template>
  <div class="toolbar">
    <a-input-search
      :value="search"
      aria-label="Search"
      placeholder="Search"
      allow-clear
      style="width: 340px"
      @update:value="emit('update:search', String($event))"
      @search="emit('update:search', $event)"
    />
    <a-select
      :value="sort.field"
      aria-label="Sort field"
      style="width: 170px"
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
    <div v-if="projects" style="position: relative">
      <a-button
        :aria-label="`Project ${projects.find((project) => project.id === selectedProjectId)?.name || 'All projects'}`"
        @click="menuOpen = !menuOpen"
      >
        Project {{ projects.find((project) => project.id === selectedProjectId)?.name || "All projects" }}
      </a-button>
      <div v-if="menuOpen" class="project-menu" role="menu">
        <button role="menuitem" @click="setProject('')">All projects</button>
        <button
          v-for="project in projects"
          :key="project.id"
          role="menuitem"
          @click="setProject(project.id)"
        >
          {{ project.name }}
        </button>
      </div>
    </div>
  </div>
</template>
