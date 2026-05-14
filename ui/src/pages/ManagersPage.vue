<script setup lang="ts">
import { ref } from "vue";
import {
  CopyOutlined,
  DatabaseOutlined,
  DeleteOutlined,
  PlusOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons-vue";
import { message } from "ant-design-vue";
import {
  addManager,
  getActiveManager,
  listManagers,
  loadManagersState,
  maskToken,
  removeManager,
  setActiveManager,
  type ManagerEntry,
} from "../api";
import PageHeader from "../components/PageHeader.vue";

const props = withDefaults(
  defineProps<{ mode?: "embedded" | "standalone" }>(),
  { mode: "embedded" },
);

const emit = defineEmits<{
  (e: "activated", entry: ManagerEntry): void;
  (e: "active-cleared"): void;
}>();

const managers = ref<ManagerEntry[]>(listManagers());
const activeId = ref<string | null>(getActiveManager()?.id ?? null);
const formURL = ref("");
const formToken = ref("");
const submitting = ref(false);
const error = ref("");

function refresh() {
  const state = loadManagersState();
  managers.value = state.managers;
  activeId.value = state.activeManagerId;
}

async function handleAdd() {
  error.value = "";
  submitting.value = true;
  try {
    const entry = await addManager({ url: formURL.value, token: formToken.value });
    formURL.value = "";
    formToken.value = "";
    refresh();
    emit("activated", entry);
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause);
  } finally {
    submitting.value = false;
  }
}

function handleActivate(entry: ManagerEntry) {
  if (entry.id === activeId.value) return;
  setActiveManager(entry.id);
  if (typeof window !== "undefined") {
    window.location.reload();
  }
}

function handleDelete(entry: ManagerEntry) {
  const wasActive = entry.id === activeId.value;
  removeManager(entry.id);
  refresh();
  if (wasActive) {
    emit("active-cleared");
  }
}

async function handleCopy(token: string) {
  if (!token) return;
  try {
    if (navigator?.clipboard?.writeText) {
      await navigator.clipboard.writeText(token);
      message.success("Token copied");
    }
  } catch {
    message.error("Copy failed");
  }
}
</script>

<template>
  <main v-if="props.mode === 'standalone'" class="auth-shell">
    <section class="auth-panel managers-panel">
      <div class="auth-title">
        <DatabaseOutlined />
        <h1>Managers</h1>
      </div>
      <p class="managers-hint">
        Add a Manager endpoint to start using Block Play Table.
      </p>
      <a-alert
        v-if="error"
        type="error"
        show-icon
        role="alert"
        :message="error"
        style="margin-bottom: 12px"
      />
      <a-form layout="vertical" @submit.prevent="handleAdd">
        <a-form-item label="Manager URL">
          <a-input
            v-model:value="formURL"
            aria-label="Manager URL"
            placeholder="http://localhost:8080"
            :disabled="submitting"
            @press-enter="handleAdd"
          />
        </a-form-item>
        <a-form-item label="Manager token">
          <a-input-password
            v-model:value="formToken"
            aria-label="Manager token"
            autocomplete="current-password"
            :disabled="submitting"
            @press-enter="handleAdd"
          />
        </a-form-item>
        <a-button
          type="primary"
          block
          :loading="submitting"
          @click="handleAdd"
        >
          <PlusOutlined /> Add manager
        </a-button>
      </a-form>
      <div v-if="managers.length" class="managers-existing">
        <h2>Saved managers</h2>
        <ul>
          <li
            v-for="entry in managers"
            :key="entry.id"
            :data-testid="'manager-row'"
            :data-manager-id="entry.id"
          >
            <span class="managers-existing-url">{{ entry.url }}</span>
            <a-button size="small" @click="handleActivate(entry)">
              <ThunderboltOutlined /> Activate
            </a-button>
          </li>
        </ul>
      </div>
    </section>
  </main>
  <div v-else>
    <PageHeader title="Managers" />
    <main class="content">
      <a-alert
        v-if="error"
        type="error"
        show-icon
        role="alert"
        :message="error"
        style="margin-bottom: 12px"
      />
      <section class="detail-section">
        <h2>Add manager</h2>
        <a-form layout="vertical" style="max-width: 520px" @submit.prevent="handleAdd">
          <a-form-item label="Manager URL">
            <a-input
              v-model:value="formURL"
              aria-label="Manager URL"
              placeholder="http://localhost:8080"
              :disabled="submitting"
              @press-enter="handleAdd"
            />
          </a-form-item>
          <a-form-item label="Manager token">
            <a-input-password
              v-model:value="formToken"
              aria-label="Manager token"
              autocomplete="current-password"
              :disabled="submitting"
              @press-enter="handleAdd"
            />
          </a-form-item>
          <a-button type="primary" :loading="submitting" @click="handleAdd">
            <PlusOutlined /> Add manager
          </a-button>
        </a-form>
      </section>
      <section class="detail-section">
        <h2>Saved managers</h2>
        <a-empty
          v-if="!managers.length"
          description="No managers yet. Add one above to get started."
        />
        <table v-else class="managers-table">
          <thead>
            <tr>
              <th>URL</th>
              <th>Token</th>
              <th>Status</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="entry in managers"
              :key="entry.id"
              :data-testid="'manager-row'"
              :data-manager-id="entry.id"
            >
              <td class="managers-table-url">{{ entry.url }}</td>
              <td class="managers-table-token">
                <span class="managers-token-mask">{{ maskToken(entry.token) || "—" }}</span>
                <a-button
                  v-if="entry.token"
                  size="small"
                  type="text"
                  aria-label="Copy token"
                  @click="handleCopy(entry.token)"
                >
                  <CopyOutlined />
                </a-button>
              </td>
              <td>
                <a-tag v-if="entry.id === activeId" color="green">Active</a-tag>
                <a-tag v-else>Inactive</a-tag>
              </td>
              <td>
                <a-space>
                  <a-button
                    v-if="entry.id !== activeId"
                    size="small"
                    @click="handleActivate(entry)"
                  >
                    <ThunderboltOutlined /> Activate
                  </a-button>
                  <a-popconfirm
                    title="Delete this manager?"
                    ok-text="Yes"
                    cancel-text="No"
                    @confirm="handleDelete(entry)"
                  >
                    <a-button size="small" danger>
                      <DeleteOutlined /> Delete
                    </a-button>
                  </a-popconfirm>
                </a-space>
              </td>
            </tr>
          </tbody>
        </table>
      </section>
    </main>
  </div>
</template>

<style scoped>
.managers-panel {
  width: min(520px, 92vw);
}

.managers-hint {
  margin: 0 0 16px;
  color: var(--bpt-text-muted);
  font-size: 13px;
}

.managers-existing {
  margin-top: 20px;
  padding-top: 16px;
  border-top: 1px solid var(--bpt-border);
}

.managers-existing h2 {
  margin: 0 0 8px;
  font-size: 14px;
  color: var(--bpt-text-muted);
  font-weight: 600;
}

.managers-existing ul {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: 8px;
}

.managers-existing li {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 12px;
  background: var(--bpt-bg);
  border-radius: 8px;
}

.managers-existing-url {
  font-family: var(--bpt-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: 12px;
  word-break: break-all;
}

.managers-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}

.managers-table th,
.managers-table td {
  padding: 10px 12px;
  text-align: left;
  border-bottom: 1px solid var(--bpt-border);
  vertical-align: middle;
}

.managers-table th {
  color: var(--bpt-text-muted);
  font-weight: 600;
  background: var(--bpt-bg);
}

.managers-table-url {
  font-family: var(--bpt-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  word-break: break-all;
}

.managers-table-token {
  display: flex;
  align-items: center;
  gap: 6px;
}

.managers-token-mask {
  font-family: var(--bpt-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  letter-spacing: 1px;
}
</style>

