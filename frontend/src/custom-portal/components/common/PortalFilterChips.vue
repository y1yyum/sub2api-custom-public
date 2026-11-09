<template>
  <div v-if="items.length" class="flex flex-wrap gap-2" aria-live="polite">
    <button
      v-for="item in items"
      :key="item.key"
      type="button"
      class="portal-filter-chip"
      :aria-label="pt('refresh.clear', { name: item.label })"
      @click="$emit('remove', item.key)"
    >
      {{ item.label }} <span aria-hidden="true">×</span>
    </button>
  </div>
</template>
<script setup lang="ts">
import { usePortalI18n } from '../../i18n'
defineProps<{ items: { key: string; label: string }[] }>()
defineEmits<{ remove: [key: string] }>()
const { pt } = usePortalI18n()
</script>
<style scoped>
.portal-filter-chip {
  display: inline-flex;
  align-items: center;
  gap: 12px;
  padding: 4px 8px;
  border: 1px solid var(--pp-control);
  background: var(--pp-surface-2);
  font-size: 12px;
}
.portal-filter-chip:focus-visible {
  outline: 2px solid var(--pp-ink);
  outline-offset: 2px;
}
</style>
