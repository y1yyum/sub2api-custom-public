<template>
  <section class="card p-4" data-testid="overview-quick-actions">
    <h2 class="actions-heading mb-4 text-sm font-semibold">{{ pt('refresh.quickActions') }}</h2>
    <div class="space-y-2">
      <router-link
        v-for="item in items"
        :key="item.id"
        :to="item.path"
        class="flex items-center gap-3 border border-gray-200 bg-gray-50 p-3 dark:border-dark-700 dark:bg-dark-900"
      >
        <PixelIcon :name="item.icon" size="md" /><span class="min-w-0 flex-1"
          ><span class="block text-sm font-semibold">{{ item.title }}</span
          ><span class="mt-1 block text-xs text-gray-500 dark:text-dark-400">{{
            item.status
          }}</span></span
        ><span aria-hidden="true">→</span>
      </router-link>
    </div>
    <PortalState
      v-if="failed"
      kind="error"
      compact
      class="mt-3 !p-0"
      :title="pt('refresh.summaryFailed')"
      @retry="load"
    />
  </section>
</template>
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { usageAPI, type UserDashboardStats } from '@/api/usage'
import { getCatalog } from '@/api/channelStatusCatalog'
import { useAuthStore } from '@/stores/auth'
import { isUnifiedModelChannelStatusEnabled } from '@/utils/featureFlags'
import { usePortalNavContext } from '../../shell/usePortalNav'
import { resolveDockEntries } from '../../shell/nav'
import { addDays, todayKey } from '../../overview/usageChartData'
import PixelIcon from '../common/PixelIcon.vue'
import PortalState from '../common/PortalState.vue'
import { usePortalI18n } from '../../i18n'
const props = defineProps<{ stats: UserDashboardStats | null }>()
const { pt } = usePortalI18n()
const auth = useAuthStore()
const { ctx, refreshBatchImageAccess } = usePortalNavContext()
const days = ref<number | null>(null)
const pending = ref(true)
const issues = ref<number | null>(null)
const failed = ref(false)
const dock = computed(() => resolveDockEntries(ctx.value))
const items = computed(() =>
  [
    {
      id: 'keys',
      path: '/keys',
      icon: 'key' as const,
      title: pt('refresh.createKey'),
      status: props.stats ? pt('refresh.keyCount', { count: props.stats.total_api_keys }) : '—'
    },
    {
      id: 'wallet',
      path: '/purchase',
      icon: 'creditCard' as const,
      title: pt('refresh.wallet'),
      status: pending.value
        ? pt('refresh.loading')
        : days.value == null
          ? '—'
          : pt('refresh.runway', { days: days.value })
    },
    {
      id: 'models',
      path: '/monitor',
      icon: 'cube' as const,
      title: pt('refresh.modelStatus'),
      status: pending.value
        ? pt('refresh.loading')
        : issues.value == null
          ? '—'
          : pt('refresh.issues', { count: issues.value })
    },
    {
      id: 'batchImage',
      path: '/batch-image',
      icon: 'grid' as const,
      title: pt('shell.links.batchImage'),
      status: ''
    }
  ].filter((item) =>
    item.id === 'batchImage'
      ? ctx.value.flag('flagBatchImageAccess') === true
      : dock.value.some((entry) => entry.id === item.id)
  )
)
async function load() {
  failed.value = false
  pending.value = true
  const end = todayKey()
  const tasks: Promise<void>[] = []
  if (dock.value.some((entry) => entry.id === 'wallet'))
    tasks.push(
      usageAPI
        .getDashboardTrend({
          start_date: addDays(end, -6),
          end_date: end,
          granularity: 'day',
          billing_type: 0
        })
        .then(({ trend }) => {
          const cost = trend.reduce((sum, point) => sum + point.actual_cost, 0)
          days.value =
            cost > 0 ? Math.floor((Math.max(0, auth.user?.balance ?? 0) * 7) / cost) : null
        })
    )
  if (dock.value.some((entry) => entry.id === 'models') && isUnifiedModelChannelStatusEnabled())
    tasks.push(
      getCatalog('24h').then((catalog) => {
        issues.value = catalog.status_error_code
          ? null
          : catalog.groups
              .flatMap((group) => group.models)
              .filter((row) => row.health === 'critical' || row.health === 'warning').length
      })
    )
  const results = await Promise.allSettled(tasks)
  failed.value = results.some((result) => result.status === 'rejected')
  pending.value = false
}
onMounted(() => {
  void refreshBatchImageAccess()
  void load()
})
defineExpose({ reload: load })
</script>
<style scoped>
.actions-heading { margin: -16px -16px 16px; padding: 12px 16px; border-bottom: 1px solid var(--pp-divider); }
</style>
