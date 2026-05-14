<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import {
  AppstoreOutlined,
  CalendarOutlined,
  DatabaseOutlined,
  FolderOpenOutlined,
  SettingOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons-vue";
import BoardPage from "./pages/BoardPage.vue";
import ProjectsPage from "./pages/ProjectsPage.vue";
import WorkersPage from "./pages/WorkersPage.vue";
import EventsPage from "./pages/EventsPage.vue";
import SettingsPage from "./pages/SettingsPage.vue";
import ManagersPage from "./pages/ManagersPage.vue";
import { getActiveManager, type ManagerEntry } from "./api";

type PageKey = "board" | "projects" | "workers" | "events" | "settings" | "managers";

const current = ref<PageKey>("board");
const ready = ref(false);
const activeManager = ref<ManagerEntry | null>(getActiveManager());

const selectedKeys = computed(() => [current.value]);

onMounted(() => {
  document.title = "Block Play Table";
  if (activeManager.value) {
    markReady();
  }
});

function markReady() {
  document.getElementById("app")?.setAttribute("data-ready", "true");
  ready.value = true;
}

function onManagerActivated(entry: ManagerEntry) {
  activeManager.value = entry;
  markReady();
}

function onActiveCleared() {
  activeManager.value = null;
  ready.value = false;
  document.getElementById("app")?.removeAttribute("data-ready");
  current.value = "board";
}
</script>

<template>
  <ManagersPage
    v-if="!activeManager"
    mode="standalone"
    @activated="onManagerActivated"
  />
  <a-layout v-else class="bpt-shell">
    <a-layout-sider class="bpt-sider" width="220">
      <div class="bpt-brand">
        <ThunderboltOutlined />
        <span>Block Play Table</span>
      </div>
      <div class="bpt-brand-meta" :title="activeManager.url">{{ activeManager.url }}</div>
      <a-menu
        theme="dark"
        mode="inline"
        :selected-keys="selectedKeys"
        @click="current = $event.key as PageKey"
      >
        <a-menu-item key="board"><AppstoreOutlined />Board</a-menu-item>
        <a-menu-item key="projects"><FolderOpenOutlined />Projects</a-menu-item>
        <a-menu-item key="workers"><ThunderboltOutlined />Workers</a-menu-item>
        <a-menu-item key="events"><CalendarOutlined />Events</a-menu-item>
        <a-menu-item key="managers"><DatabaseOutlined />Managers</a-menu-item>
        <a-menu-item key="settings"><SettingOutlined />Settings</a-menu-item>
      </a-menu>
    </a-layout-sider>
    <a-layout class="bpt-page">
      <BoardPage v-if="current === 'board'" />
      <ProjectsPage v-else-if="current === 'projects'" />
      <WorkersPage v-else-if="current === 'workers'" />
      <EventsPage v-else-if="current === 'events'" />
      <ManagersPage
        v-else-if="current === 'managers'"
        mode="embedded"
        @active-cleared="onActiveCleared"
      />
      <SettingsPage v-else />
    </a-layout>
  </a-layout>
</template>
