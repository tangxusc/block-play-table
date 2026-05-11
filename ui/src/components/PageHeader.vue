<script setup lang="ts">
defineProps<{
  title: string;
}>();
</script>

<template>
  <div class="page-header">
    <h1 class="page-title">{{ title }}</h1>
    <div class="page-actions">
      <slot name="actions" />
    </div>
  </div>
</template>
