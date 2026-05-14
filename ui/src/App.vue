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
import {
  api,
  clearManagerToken,
  loadManagerBaseURL,
  loadManagerToken,
  saveManagerBaseURL,
  saveManagerToken,
} from "./api";

type PageKey = "board" | "projects" | "workers" | "events" | "settings";

const current = ref<PageKey>("board");
const ready = ref(false);
const authChecked = ref(false);
const authRequired = ref(false);
const authSubmitting = ref(false);
const authToken = ref(loadManagerToken());
const authError = ref("");
const managerReady = ref(Boolean(loadManagerBaseURL()));
const managerURL = ref(loadManagerBaseURL());
const managerSubmitting = ref(false);
const managerError = ref("");

const selectedKeys = computed(() => [current.value]);

onMounted(() => {
  document.title = "Block Play Table";
  if (managerReady.value) {
    void checkManagerAuth();
  } else {
    authChecked.value = true;
  }
});

async function checkManagerAuth(): Promise<boolean> {
  authError.value = "";
  try {
    const status = await api.fetchAuthStatus();
    authRequired.value = status.required;
    if (!status.required) {
      markReady();
      return true;
    }
    const storedToken = loadManagerToken();
    if (storedToken) {
      try {
        await api.verifyManagerToken(storedToken);
        markReady();
        return true;
      } catch {
        clearManagerToken();
        authToken.value = "";
      }
    }
    return true;
  } catch (error) {
    authError.value = error instanceof Error ? error.message : String(error);
    return false;
  } finally {
    authChecked.value = true;
  }
}

async function connectManager() {
  managerSubmitting.value = true;
  managerError.value = "";
  authError.value = "";
  ready.value = false;
  try {
    managerURL.value = saveManagerBaseURL(managerURL.value);
    managerReady.value = true;
    authChecked.value = false;
    const connected = await checkManagerAuth();
    if (!connected) {
      managerReady.value = false;
      managerError.value = authError.value || "Could not reach Manager.";
      authError.value = "";
      authChecked.value = true;
    }
  } catch (error) {
    managerReady.value = false;
    managerError.value = error instanceof Error ? error.message : String(error);
    authChecked.value = true;
  } finally {
    managerSubmitting.value = false;
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

function editManagerConnection() {
  ready.value = false;
  managerReady.value = false;
  authChecked.value = true;
  authError.value = "";
  managerError.value = "";
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
  <main v-else-if="!managerReady" class="auth-shell">
    <section class="auth-panel">
      <div class="auth-title">
        <SettingOutlined />
        <h1>Manager connection</h1>
      </div>
      <a-alert
        v-if="managerError"
        type="error"
        show-icon
        :message="managerError"
        style="margin-bottom: 12px"
      />
      <a-form layout="vertical" @submit.prevent="connectManager">
        <a-form-item label="Manager URL">
          <a-input
            v-model:value="managerURL"
            aria-label="Manager URL"
            placeholder="http://localhost:8080"
            @press-enter="connectManager"
          />
        </a-form-item>
        <a-button
          type="primary"
          block
          :loading="managerSubmitting"
          @click="connectManager"
        >
          Connect manager
        </a-button>
      </a-form>
    </section>
  </main>
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
      <a-space v-else direction="vertical" style="width: 100%">
        <a-button block @click="checkManagerAuth">Retry</a-button>
        <a-button block @click="editManagerConnection">Change manager</a-button>
      </a-space>
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
