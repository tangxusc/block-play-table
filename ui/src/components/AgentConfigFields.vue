<script setup lang="ts">
import type { AgentConfigDraft } from "../agentConfig";

defineProps<{
  agentType: string;
  draft: AgentConfigDraft;
}>();

const workModeOptions = [
  { value: "", label: "Default" },
  { value: "PLAN", label: "Plan" },
  { value: "IMPLEMENT", label: "Implement" },
  { value: "REVIEW", label: "Review" },
];

const reasoningOptions = [
  { value: "", label: "Default" },
  { value: "MINIMAL", label: "Minimal" },
  { value: "LOW", label: "Low" },
  { value: "MEDIUM", label: "Medium" },
  { value: "HIGH", label: "High" },
  { value: "XHIGH", label: "XHigh" },
];

const sandboxOptions = [
  { value: "", label: "Default" },
  { value: "READ_ONLY", label: "Read only" },
  { value: "WORKSPACE_WRITE", label: "Workspace write" },
  { value: "DANGER_FULL_ACCESS", label: "Danger full access" },
];

const approvalOptions = [
  { value: "", label: "Default" },
  { value: "UNTRUSTED", label: "Untrusted" },
  { value: "ON_FAILURE", label: "On failure" },
  { value: "ON_REQUEST", label: "On request" },
  { value: "NEVER", label: "Never" },
];

const claudeEffortOptions = [
  { value: "", label: "Default" },
  { value: "LOW", label: "Low" },
  { value: "MEDIUM", label: "Medium" },
  { value: "HIGH", label: "High" },
  { value: "XHIGH", label: "XHigh" },
  { value: "MAX", label: "Max" },
];

const permissionOptions = [
  { value: "", label: "Default" },
  { value: "ACCEPT_EDITS", label: "Accept edits" },
  { value: "AUTO", label: "Auto" },
  { value: "BYPASS_PERMISSIONS", label: "Bypass permissions" },
  { value: "DEFAULT", label: "Default" },
  { value: "DONT_ASK", label: "Don't ask" },
  { value: "PLAN", label: "Plan" },
];
</script>

<template>
  <section class="detail-section">
    <h3>Agent runtime parameters</h3>
    <a-form-item label="Work mode">
      <a-select v-model:value="draft.workMode" aria-label="Work mode">
        <a-select-option v-for="option in workModeOptions" :key="option.value" :value="option.value">
          {{ option.label }}
        </a-select-option>
      </a-select>
    </a-form-item>

    <template v-if="agentType === 'codex'">
      <a-form-item label="Codex model">
        <a-input v-model:value="draft.codexModel" aria-label="Codex model" />
      </a-form-item>
      <a-form-item label="Reasoning effort">
        <a-select v-model:value="draft.codexReasoningEffort" aria-label="Reasoning effort">
          <a-select-option v-for="option in reasoningOptions" :key="option.value" :value="option.value">
            {{ option.label }}
          </a-select-option>
        </a-select>
      </a-form-item>
      <a-form-item label="Sandbox">
        <a-select v-model:value="draft.codexSandboxMode" aria-label="Sandbox">
          <a-select-option v-for="option in sandboxOptions" :key="option.value" :value="option.value">
            {{ option.label }}
          </a-select-option>
        </a-select>
      </a-form-item>
      <a-form-item label="Approval">
        <a-select v-model:value="draft.codexApprovalPolicy" aria-label="Approval">
          <a-select-option v-for="option in approvalOptions" :key="option.value" :value="option.value">
            {{ option.label }}
          </a-select-option>
        </a-select>
      </a-form-item>
      <a-space direction="vertical">
        <a-checkbox v-model:checked="draft.codexFullAuto" aria-label="Full auto">Full auto</a-checkbox>
        <a-checkbox
          v-model:checked="draft.codexBypassApprovalsAndSandbox"
          aria-label="Bypass approvals and sandbox"
        >
          Bypass approvals and sandbox
        </a-checkbox>
      </a-space>
    </template>

    <template v-else-if="agentType === 'claude'">
      <a-form-item label="Claude model">
        <a-input v-model:value="draft.claudeModel" aria-label="Claude model" />
      </a-form-item>
      <a-form-item label="Effort">
        <a-select v-model:value="draft.claudeEffort" aria-label="Effort">
          <a-select-option v-for="option in claudeEffortOptions" :key="option.value" :value="option.value">
            {{ option.label }}
          </a-select-option>
        </a-select>
      </a-form-item>
      <a-form-item label="Permission mode">
        <a-select v-model:value="draft.claudePermissionMode" aria-label="Permission mode">
          <a-select-option v-for="option in permissionOptions" :key="option.value" :value="option.value">
            {{ option.label }}
          </a-select-option>
        </a-select>
      </a-form-item>
    </template>
  </section>
</template>
