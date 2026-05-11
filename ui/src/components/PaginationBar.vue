<script setup lang="ts">
import type { PageRequest } from "../models";
import { lastOffset, pageSummary } from "../format";

const props = defineProps<{
  page: PageRequest;
  total: number;
}>();

const emit = defineEmits<{
  change: [page: PageRequest];
}>();

function previous() {
  emit("change", {
    ...props.page,
    offset: Math.max(0, props.page.offset - props.page.limit),
  });
}

function next() {
  emit("change", {
    ...props.page,
    offset: Math.min(lastOffset(props.total, props.page.limit), props.page.offset + props.page.limit),
  });
}
</script>

<template>
  <div class="pagination">
    <span>{{ pageSummary(page, total) }}</span>
    <a-button
      aria-label="Previous page"
      :disabled="page.offset <= 0"
      @click="previous"
    >
      Previous page
    </a-button>
    <a-button
      aria-label="Next page"
      :disabled="page.offset + page.limit >= total"
      @click="next"
    >
      Next page
    </a-button>
  </div>
</template>
