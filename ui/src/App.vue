<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import {
  AppstoreOutlined,
  CalendarOutlined,
  FolderOpenOutlined,
  SettingOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons-vue";
import BoardPage from "./pages/BoardPage.vue";
import ProjectsPage from "./pages/ProjectsPage.vue";
import WorkersPage from "./pages/WorkersPage.vue";
import EventsPage from "./pages/EventsPage.vue";
import SettingsPage from "./pages/SettingsPage.vue";

type PageKey = "board" | "projects" | "workers" | "events" | "settings";

const current = ref<PageKey>("board");
const ready = ref(false);

const selectedKeys = computed(() => [current.value]);

onMounted(() => {
  document.title = "Block Play Table";
  document.getElementById("app")?.setAttribute("data-ready", "true");
  ready.value = true;
});
</script>

<template>
  <a-layout v-if="ready" class="bpt-shell">
    <a-layout-sider class="bpt-sider" width="220">
      <div class="bpt-brand">
        <ThunderboltOutlined />
        <span style="margin-left: 8px">Block Play Table</span>
      </div>
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
        <a-menu-item key="settings"><SettingOutlined />Settings</a-menu-item>
      </a-menu>
    </a-layout-sider>
    <a-layout class="bpt-page">
      <BoardPage v-if="current === 'board'" />
      <ProjectsPage v-else-if="current === 'projects'" />
      <WorkersPage v-else-if="current === 'workers'" />
      <EventsPage v-else-if="current === 'events'" />
      <SettingsPage v-else />
    </a-layout>
  </a-layout>
</template>
