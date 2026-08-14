<script setup lang="ts">
import type { TaskA2AExecution } from "../models";

defineProps<{
  executions: TaskA2AExecution[];
}>();

function shortIdentifier(value: string | null): string {
  if (!value) return "-";
  if (value.length <= 20) return value;
  return `${value.slice(0, 10)}...${value.slice(-7)}`;
}

function compactTimestamp(value: string | null): string {
  if (!value) return "-";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return new Intl.DateTimeFormat("en-US", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(parsed);
}

function executionError(execution: TaskA2AExecution): string {
  const diagnostic = [execution.errorCode, execution.errorMessage].filter(Boolean).join(": ");
  if (!diagnostic) return "-";
  return execution.retryable ? `${diagnostic} (retryable)` : diagnostic;
}
</script>

<template>
  <section class="detail-section a2a-rounds" role="region" aria-label="A2A execution rounds">
    <div class="a2a-rounds-header">
      <h3>A2A execution rounds</h3>
      <a-tag>{{ executions.length }}</a-tag>
    </div>
    <a-empty v-if="executions.length === 0" description="No A2A execution rounds" />
    <div v-else class="a2a-rounds-table-wrap">
      <table class="a2a-rounds-table">
        <thead>
          <tr>
            <th scope="col">Attempt / turn</th>
            <th scope="col">Operation</th>
            <th scope="col">Remote status</th>
            <th scope="col">A2A task / context</th>
            <th scope="col">Sequence</th>
            <th scope="col">Last sync</th>
            <th scope="col">Error</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="execution in executions" :key="execution.id" data-testid="a2a-execution-round">
            <td><strong>#{{ execution.attempt }}</strong> / {{ execution.turn }}</td>
            <td>{{ execution.operation }}</td>
            <td><a-tag>{{ execution.remoteStatus }}</a-tag></td>
            <td class="mono a2a-round-identifiers">
              <span :title="execution.a2aTaskId || ''">{{ shortIdentifier(execution.a2aTaskId) }}</span>
              <span :title="execution.contextId || ''">{{ shortIdentifier(execution.contextId) }}</span>
            </td>
            <td>{{ execution.lastSequence }}</td>
            <td>
              <time :datetime="execution.lastSyncedAt || undefined">
                {{ compactTimestamp(execution.lastSyncedAt) }}
              </time>
            </td>
            <td :class="{ 'a2a-round-error': execution.errorCode || execution.errorMessage }">
              {{ executionError(execution) }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
