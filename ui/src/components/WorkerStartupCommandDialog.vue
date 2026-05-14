<script setup lang="ts">
import { computed, ref } from "vue";
import { loadManagerBaseURL, loadManagerToken } from "../api";
import {
  buildWorkerCommands,
  managerHTTPToWS,
} from "../lib/workerStartupCommand";

interface WorkerFormInput {
  workerId: string;
  workerName: string;
  workDir: string;
  supportedAgents: string[];
  projectBindingMode: string;
  boundProjectIds: string[];
}

const props = defineProps<{
  open: boolean;
  workerInput: WorkerFormInput;
}>();

const emit = defineEmits<{
  (event: "update:open", value: boolean): void;
}>();

const activeTab = ref<"docker" | "shell">("docker");
const copiedDocker = ref(false);
const copiedShell = ref(false);

const managerHTTPURL = computed(() => loadManagerBaseURL());
const managerWSURL = computed(() => managerHTTPToWS(managerHTTPURL.value));
const workerToken = computed(() => loadManagerToken());

const commands = computed(() =>
  buildWorkerCommands({
    managerWSURL: managerWSURL.value,
    workerToken: workerToken.value,
    workerId: props.workerInput.workerId,
    workerName: props.workerInput.workerName,
    workDir: props.workerInput.workDir,
    supportedAgents: props.workerInput.supportedAgents,
    projectBindingMode: props.workerInput.projectBindingMode,
    boundProjectIds: props.workerInput.boundProjectIds,
  }),
);

const managerConfigured = computed(() => managerWSURL.value !== "");

async function copyToClipboard(text: string, target: "docker" | "shell") {
  try {
    await navigator.clipboard.writeText(text);
    if (target === "docker") {
      copiedDocker.value = true;
      setTimeout(() => (copiedDocker.value = false), 2000);
    } else {
      copiedShell.value = true;
      setTimeout(() => (copiedShell.value = false), 2000);
    }
  } catch {
    // ignore clipboard failure (e.g. permissions); the <pre> is selectable.
  }
}

function close() {
  emit("update:open", false);
}
</script>

<template>
  <a-modal
    :open="props.open"
    title="Worker startup command"
    width="860px"
    :footer="null"
    @update:open="(value: boolean) => emit('update:open', value)"
  >
    <a-alert
      v-if="!managerConfigured"
      type="warning"
      show-icon
      message="Manager URL is not configured"
      description="Open Settings and set the Manager URL before generating a startup command."
      style="margin-bottom: 12px"
    />
    <a-tabs v-model:active-key="activeTab">
      <a-tab-pane key="docker" tab="Docker">
        <div class="command-block">
          <a-button
            :aria-label="'Copy Docker command'"
            :disabled="!managerConfigured"
            @click="copyToClipboard(commands.docker, 'docker')"
          >
            {{ copiedDocker ? "Copied" : "Copy Docker command" }}
          </a-button>
          <pre class="command-pre">{{ commands.docker }}</pre>
        </div>
      </a-tab-pane>
      <a-tab-pane key="shell" tab="Command line">
        <div class="command-block">
          <a-button
            :aria-label="'Copy command line'"
            :disabled="!managerConfigured"
            @click="copyToClipboard(commands.shell, 'shell')"
          >
            {{ copiedShell ? "Copied" : "Copy command line" }}
          </a-button>
          <pre class="command-pre">{{ commands.shell }}</pre>
        </div>
      </a-tab-pane>
    </a-tabs>
    <div class="dialog-footer">
      <a-button @click="close">Done</a-button>
    </div>
  </a-modal>
</template>

<style scoped>
.command-block {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.command-pre {
  background: #1f1f1f;
  color: #f5f5f5;
  padding: 12px;
  border-radius: 4px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12px;
  line-height: 1.6;
  white-space: pre;
  overflow-x: auto;
  margin: 0;
}
.dialog-footer {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}
</style>
