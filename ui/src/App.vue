<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import {
  AppstoreOutlined,
  CalendarOutlined,
  FolderOpenOutlined,
  LockOutlined,
  SettingOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons-vue";
import BoardPage from "./pages/BoardPage.vue";
import ProjectsPage from "./pages/ProjectsPage.vue";
import WorkersPage from "./pages/WorkersPage.vue";
import EventsPage from "./pages/EventsPage.vue";
import SettingsPage from "./pages/SettingsPage.vue";
import { api, clearManagerToken, loadManagerToken, saveManagerToken } from "./api";

type PageKey = "board" | "projects" | "workers" | "events" | "settings";

const current = ref<PageKey>("board");
const ready = ref(false);
const authChecked = ref(false);
const authRequired = ref(false);
const authSubmitting = ref(false);
const authToken = ref(loadManagerToken());
const authError = ref("");

const selectedKeys = computed(() => [current.value]);

onMounted(() => {
  document.title = "Block Play Table";
  void checkManagerAuth();
});

async function checkManagerAuth() {
  authError.value = "";
  try {
    const status = await api.fetchAuthStatus();
    authRequired.value = status.required;
    if (!status.required) {
      markReady();
      return;
    }
    const storedToken = loadManagerToken();
    if (storedToken) {
      try {
        await api.verifyManagerToken(storedToken);
        markReady();
        return;
      } catch {
        clearManagerToken();
        authToken.value = "";
      }
    }
  } catch (error) {
    authError.value = error instanceof Error ? error.message : String(error);
  } finally {
    authChecked.value = true;
  }
}

async function unlockManager() {
  const token = authToken.value.trim();
  if (!token) {
    authError.value = "Manager token is required.";
    return;
  }
  authSubmitting.value = true;
  authError.value = "";
  try {
    await api.verifyManagerToken(token);
    saveManagerToken(token);
    markReady();
  } catch (error) {
    authError.value = error instanceof Error ? error.message : String(error);
  } finally {
    authSubmitting.value = false;
  }
}

function markReady() {
  document.getElementById("app")?.setAttribute("data-ready", "true");
  ready.value = true;
}
</script>

<template>
  <div v-if="!authChecked" class="auth-shell">
    <a-spin />
  </div>
  <main v-else-if="!ready" class="auth-shell">
    <section class="auth-panel">
      <div class="auth-title">
        <LockOutlined />
        <h1>Manager access</h1>
      </div>
      <a-alert
        v-if="authError"
        type="error"
        show-icon
        :message="authError"
        style="margin-bottom: 12px"
      />
      <a-form v-if="authRequired" layout="vertical" @submit.prevent="unlockManager">
        <a-form-item label="Manager token">
          <a-input-password
            v-model:value="authToken"
            aria-label="Manager token"
            autocomplete="current-password"
            @press-enter="unlockManager"
          />
        </a-form-item>
        <a-button
          type="primary"
          block
          :loading="authSubmitting"
          @click="unlockManager"
        >
          Unlock manager
        </a-button>
      </a-form>
      <a-button v-else block @click="checkManagerAuth">Retry</a-button>
    </section>
  </main>
  <a-layout v-if="ready" class="bpt-shell">
    <a-layout-sider class="bpt-sider" width="220">
      <div class="bpt-brand">
        <ThunderboltOutlined />
        <span>Block Play Table</span>
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
