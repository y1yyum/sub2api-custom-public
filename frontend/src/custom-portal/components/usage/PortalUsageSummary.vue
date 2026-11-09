<template>
  <section class="usage-summary card" :aria-busy="loading" data-testid="usage-summary">
    <div class="grid grid-cols-2 lg:grid-cols-5">
      <div>
        <p class="text-xs text-gray-500 dark:text-dark-400">{{ pt('refresh.requests') }}</p>
        <p class="mt-1 font-mono text-xl">{{ stats?.total_requests.toLocaleString() ?? '—' }}</p>
      </div>
      <div>
        <p class="text-xs text-gray-500 dark:text-dark-400">{{ pt('refresh.tokens') }}</p>
        <p class="mt-1 font-mono text-xl">{{ stats?.total_tokens.toLocaleString() ?? '—' }}</p>
        <TokenComposition
          v-if="stats"
          class="mt-2"
          :input="stats.total_input_tokens"
          :output="stats.total_output_tokens"
          :cache-write="stats.total_cache_creation_tokens"
          :cache-read="stats.total_cache_read_tokens"
        />
      </div>
      <div>
        <p class="text-xs text-gray-500 dark:text-dark-400">{{ pt('refresh.cost') }}</p>
        <p class="mt-1 font-mono text-xl">
          {{ stats ? `$${stats.total_actual_cost.toFixed(4)}` : '—' }}
        </p>
      </div>
      <div>
        <p class="text-xs text-gray-500 dark:text-dark-400">{{ pt('refresh.latency') }}</p>
        <p class="mt-1 font-mono text-xl">
          {{ stats ? `${Math.round(stats.average_duration_ms)} ms` : '—' }}
        </p>
      </div>
      <div>
        <p class="text-xs text-gray-500 dark:text-dark-400">{{ pt('refresh.cache') }}</p>
        <p class="mt-1 font-mono text-xl">{{ cacheRate }}</p>
      </div>
    </div>
    <PortalState
      v-if="failed"
      kind="error"
      compact
      class="border-t border-gray-200 dark:border-dark-700"
      :title="pt('refresh.summaryFailed')"
      @retry="$emit('retry')"
    />
  </section>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import type { UsageStatsResponse } from '@/types'
import { usePortalI18n } from '../../i18n'
import TokenComposition from '../common/TokenComposition.vue'
import PortalState from '../common/PortalState.vue'
const props = defineProps<{ stats: UsageStatsResponse | null; loading: boolean; failed: boolean }>()
defineEmits<{ retry: [] }>()
const { pt } = usePortalI18n()
const cacheRate = computed(() => {
  const stats = props.stats
  if (!stats) return '—'
  const prompt =
    stats.total_input_tokens + stats.total_cache_read_tokens + stats.total_cache_creation_tokens
  return prompt > 0 ? `${((stats.total_cache_read_tokens / prompt) * 100).toFixed(1)}%` : '—'
})
</script>
<style scoped>
.usage-summary > .grid > div { padding: 16px; border-left: 1px solid var(--pp-divider); }
.usage-summary > .grid > div:first-child { border-left: 0; }
.usage-summary .font-mono { margin-top: 8px; font: 400 24px/28px var(--pp-font-pixel); font-synthesis: none; }
.usage-summary > .grid > div > p:first-child { font-size: 13px; }
@media (max-width: 1023px) {
  .usage-summary > .grid > div:nth-child(odd) { border-left: 0; }
  .usage-summary > .grid > div:nth-child(n+3) { border-top: 1px solid var(--pp-divider); }
}
</style>
