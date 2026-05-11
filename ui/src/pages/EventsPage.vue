<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from "vue";
import { ReloadOutlined } from "@ant-design/icons-vue";
import { api } from "../api";
import type { DomainEventItem, PageRequest, PagedResult, SortRequest } from "../models";
import { defaultPage, defaultSort } from "../models";
import PageHeader from "../components/PageHeader.vue";
import PaginationBar from "../components/PaginationBar.vue";
import SearchToolbar from "../components/SearchToolbar.vue";

const page = ref<PageRequest>(defaultPage());
const sort = ref<SortRequest>(defaultSort("OCCURRED_AT"));
const search = ref("");
const loading = ref(false);
const error = ref("");
const data = ref<PagedResult<DomainEventItem>>({ items: [], totalCount: 0 });
let unsubscribe: (() => void) | undefined;

watch([search, sort], () => {
  page.value = defaultPage();
  void load();
});
watch(page, () => void load());

void load();
unsubscribe = api.subscribeDomainEvents(() => void load(false));
onBeforeUnmount(() => unsubscribe?.());

async function load(showSpinner = true) {
  if (showSpinner) loading.value = true;
  error.value = "";
  try {
    data.value = await api.fetchEventsPage(page.value, search.value, sort.value);
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause);
  } finally {
    loading.value = false;
  }
}
</script>

<template>
  <div>
    <PageHeader title="Events">
      <template #actions>
        <a-button aria-label="Refresh events" @click="load()"><ReloadOutlined /> Refresh</a-button>
      </template>
    </PageHeader>
    <main class="content">
      <SearchToolbar
        :search="search"
        :sort="sort"
        :sort-options="[
          { field: 'OCCURRED_AT', label: 'Occurred' },
          { field: 'EVENT_TYPE', label: 'Event type' },
          { field: 'AGGREGATE_TYPE', label: 'Aggregate type' },
          { field: 'AGGREGATE_ID', label: 'Aggregate ID' },
        ]"
        @update:search="search = $event"
        @update:sort="sort = $event"
      />
      <a-alert v-if="error" type="error" show-icon :message="error" style="margin-bottom: 12px" />
      <a-spin :spinning="loading">
        <div class="record-list">
          <div v-for="event in data.items" :key="event.eventId" class="record-card">
            <div class="record-title">{{ event.eventType }}</div>
            <div class="record-subtitle">
              {{ event.aggregateType }} · {{ event.aggregateId }} · {{ event.occurredAt }}
            </div>
            <pre class="mono">{{ event.payload }}</pre>
          </div>
        </div>
        <PaginationBar :page="page" :total="data.totalCount" @change="page = $event" />
      </a-spin>
    </main>
  </div>
</template>
