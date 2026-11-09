<template>
  <span class="token-composition" role="img" :aria-label="label" :title="label">
    <span
      v-for="part in parts"
      :key="part.key"
      :style="{ width: `${total ? (part.value / total) * 100 : 0}%`, backgroundColor: part.color }"
    />
  </span>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { usePortalI18n } from '../../i18n'
import { TOKEN_SERIES_COLORS } from '../../overview/metricTones'
const props = defineProps<{
  input: number
  output: number
  cacheWrite: number
  cacheRead: number
}>()
const { pt } = usePortalI18n()
const parts = computed(() => [
  { key: 'input', value: props.input, color: TOKEN_SERIES_COLORS.input },
  { key: 'output', value: props.output, color: TOKEN_SERIES_COLORS.output },
  { key: 'cacheWrite', value: props.cacheWrite, color: TOKEN_SERIES_COLORS.cache_creation },
  { key: 'cacheRead', value: props.cacheRead, color: TOKEN_SERIES_COLORS.cache_read }
])
const total = computed(() => parts.value.reduce((sum, part) => sum + part.value, 0))
const label = computed(() =>
  parts.value
    .map((part) => `${pt(`refresh.${part.key}`)}: ${part.value.toLocaleString()}`)
    .join(' · ')
)
</script>
<style scoped>
.token-composition {
  display: flex;
  width: 100%;
  min-width: 64px;
  height: 6px;
  gap: 1px;
  overflow: hidden;
  background: var(--pp-surface-2);
}
</style>
