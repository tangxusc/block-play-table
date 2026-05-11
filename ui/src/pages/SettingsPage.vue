<script setup lang="ts">
import { onBeforeUnmount, ref } from "vue";
import { ReloadOutlined, SaveOutlined } from "@ant-design/icons-vue";
import { api } from "../api";
import type { SettingsData } from "../models";
import PageHeader from "../components/PageHeader.vue";

const loading = ref(false);
const error = ref("");
const settings = ref<SettingsData>({ workerHeartbeatTimeout: "90s", securityPolicy: "" });
let unsubscribe: (() => void) | undefined;

void load();
unsubscribe = api.subscribeDomainEvents((event) => {
  if (event.aggregateType === "Settings") void load(false);
});
onBeforeUnmount(() => unsubscribe?.());

async function load(showSpinner = true) {
  if (showSpinner) loading.value = true;
  error.value = "";
  try {
    settings.value = await api.fetchSettings();
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause);
  } finally {
    loading.value = false;
  }
}

async function save() {
  await api.updateSettings(settings.value);
  await load(false);
}
</script>

<template>
  <div>
    <PageHeader title="Settings">
      <template #actions>
        <a-button aria-label="Refresh settings" @click="load()"><ReloadOutlined /> Refresh</a-button>
        <a-button type="primary" @click="save"><SaveOutlined /> Save settings</a-button>
      </template>
    </PageHeader>
    <main class="content">
      <a-alert v-if="error" type="error" show-icon :message="error" style="margin-bottom: 12px" />
      <a-spin :spinning="loading">
        <section class="detail-section">
          <h2>Trusted mode</h2>
          <p>
            Authentication and authorization are intentionally disabled in this build.
            Deploy Manager, UI, and Workers only on a trusted network.
          </p>
          <a-form layout="vertical" style="max-width: 360px">
            <a-form-item label="Worker heartbeat timeout">
              <a-input v-model:value="settings.workerHeartbeatTimeout" aria-label="Worker heartbeat timeout" />
            </a-form-item>
          </a-form>
        </section>
      </a-spin>
    </main>
  </div>
</template>
