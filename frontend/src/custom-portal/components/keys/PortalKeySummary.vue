<template>
  <section
    class="key-summary card grid grid-cols-2 lg:grid-cols-4"
    :aria-busy="loading"
    data-testid="key-summary"
  >
    <div>
      <p class="text-xs text-gray-500 dark:text-dark-400">{{ pt('refresh.total') }}</p>
      <p class="mt-1 font-mono text-2xl">{{ stats?.total_api_keys ?? '—' }}</p>
    </div>
    <div>
      <p class="text-xs text-gray-500 dark:text-dark-400">{{ pt('refresh.enabled') }}</p>
      <p class="mt-1 font-mono text-2xl">{{ stats?.active_api_keys ?? '—' }}</p>
    </div>
    <div>
      <p class="text-xs text-gray-500 dark:text-dark-400">{{ pt('refresh.monthSpend') }}</p>
      <p class="mt-1 font-mono text-2xl">
        {{ monthCost == null ? '—' : `$${monthCost.toFixed(2)}` }}
      </p>
    </div>
    <div>
      <p class="text-xs text-gray-500 dark:text-dark-400">
        {{ pt('refresh.expiring') }} · {{ pt('refresh.pageScope') }}
      </p>
      <p class="mt-1 flex items-center gap-3 font-mono text-2xl">
        {{ expiring.length }}
        <button
          v-if="expiring.length"
          type="button"
          class="text-xs underline"
          @click="$emit('renew', expiring[0])"
        >
          {{ pt('refresh.renew') }}
        </button>
      </p>
    </div>
    <PortalState
      v-if="failed"
      kind="error"
      compact
      class="col-span-full"
      :title="pt('refresh.summaryFailed')"
      @retry="load"
    />
  </section>
</template>
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { usageAPI, type UserDashboardStats } from '@/api/usage'
import type { ApiKey } from '@/types'
import { usePortalI18n } from '../../i18n'
import PortalState from '../common/PortalState.vue'
import { todayKey } from '../../overview/usageChartData'
const props = defineProps<{ keys: ApiKey[] }>()
defineEmits<{ renew: [key: ApiKey] }>()
const { pt } = usePortalI18n()
const stats = ref<UserDashboardStats | null>(null)
const monthCost = ref<number | null>(null)
const failed = ref(false)
const loading = ref(false)
const expiring = computed(() =>
  props.keys.filter(
    (key) =>
      key.expires_at &&
      Date.parse(key.expires_at) > Date.now() &&
      Date.parse(key.expires_at) <= Date.now() + 7 * 86400000
  )
)
let sequence = 0
async function load() {
  const token = ++sequence
  loading.value = true
  const end = todayKey()
  const results = await Promise.allSettled([
    usageAPI.getDashboardStats(),
    usageAPI.getStats({ start_date: `${end.slice(0, 7)}-01`, end_date: end })
  ])
  if (sequence !== token) return
  stats.value = results[0].status === 'fulfilled' ? results[0].value : null
  monthCost.value = results[1].status === 'fulfilled' ? results[1].value.total_actual_cost : null
  failed.value = results.some((result) => result.status === 'rejected')
  loading.value = false
}
watch(
  () => props.keys.map((key) => `${key.id}:${key.status}`).join(','),
  (_next, previous) => {
    if (previous) void load()
  }
)
onMounted(load)
defineExpose({ reload: load })
</script>
<style scoped>
.key-summary > div { padding: 16px; border-left: 1px solid var(--pp-divider); }
.key-summary > div:first-child { border-left: 0; }
.key-summary > div > p:last-child { margin-top: 8px; font: 400 24px/28px var(--pp-font-pixel); font-synthesis: none; }
.key-summary > div > p:first-child { font-size: 13px; }
.key-summary > [role='alert'] { padding: 12px 16px; border-top: 1px solid var(--pp-divider); }
@media (max-width: 1023px) {
  .key-summary > div:nth-child(3) { border-left: 0; }
  .key-summary > div:nth-child(n+3) { border-top: 1px solid var(--pp-divider); }
}
</style>
