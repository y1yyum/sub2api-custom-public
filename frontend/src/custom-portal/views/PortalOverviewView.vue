<template>
  <AppLayout>
    <div class="space-y-6" data-testid="portal-overview">
      <div v-if="loading" class="flex items-center justify-center py-12"><LoadingSpinner /></div>
      <UserDashboardStats
        v-else-if="stats"
        :stats="stats"
        :balance="user?.balance || 0"
        :is-simple="authStore.isSimpleMode"
        :platform-quotas="platformQuotas"
      />

      <section class="card space-y-4 p-4" :aria-label="pt('pages.overview.chartTitle')" data-testid="portal-overview-chart">
        <div class="flex flex-wrap items-center gap-3">
          <h2 class="mr-auto text-sm font-semibold text-gray-900 dark:text-white">{{ pt('pages.overview.chartTitle') }}</h2>
          <div class="flex items-center gap-2">
            <span class="text-sm text-gray-600 dark:text-dark-300">{{ pt('pages.overview.range') }}</span>
            <DateRangePicker v-model:start-date="startDate" v-model:end-date="endDate" @change="loadCharts" />
          </div>
          <div v-if="dimension === 'trend'" class="flex items-center gap-2">
            <span class="text-sm text-gray-600 dark:text-dark-300">{{ pt('pages.overview.granularity') }}</span>
            <div class="w-28">
              <Select v-model="granularity" :options="granularityOptions" @change="loadCharts" />
            </div>
          </div>
          <button type="button" class="btn btn-secondary" :disabled="chartsLoading" @click="refreshAll">
            {{ pt('pages.overview.refresh') }}
          </button>
        </div>

        <div
          class="inline-flex flex-wrap gap-1 rounded-xl bg-gray-100 p-1 dark:bg-dark-800"
          role="radiogroup"
          :aria-label="pt('pages.overview.dimension')"
        >
          <button
            v-for="option in dimensions"
            :key="option"
            type="button"
            role="radio"
            class="rounded-lg px-3 py-1.5 text-sm font-medium transition-colors"
            :class="
              option === dimension
                ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
                : 'text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white'
            "
            :aria-checked="option === dimension"
            :data-testid="`portal-overview-dimension-${option}`"
            @click="dimension = option"
          >
            {{ pt(`pages.overview.dimensions.${option}`) }}
          </button>
        </div>

        <TokenUsageTrend v-if="dimension === 'trend'" :trend-data="trendData" :loading="chartsLoading" />
        <ModelDistributionChart
          v-else-if="dimension === 'model'"
          v-model:metric="modelMetric"
          :model-stats="modelStats"
          :loading="chartsLoading"
          :show-source-toggle="false"
          :show-metric-toggle="true"
          :enable-breakdown="false"
          :show-account-cost="false"
          :start-date="startDate"
          :end-date="endDate"
        />
        <GroupDistributionChart
          v-else-if="dimension === 'group'"
          v-model:metric="groupMetric"
          :group-stats="groupStats"
          :loading="chartsLoading"
          :show-metric-toggle="true"
          :enable-breakdown="false"
          :show-account-cost="false"
          :start-date="startDate"
          :end-date="endDate"
        />
        <EndpointDistributionChart
          v-else
          v-model:metric="endpointMetric"
          source="inbound"
          :endpoint-stats="endpointStats"
          :upstream-endpoint-stats="[]"
          :endpoint-path-stats="[]"
          :loading="chartsLoading"
          :show-source-toggle="false"
          :show-metric-toggle="true"
          :enable-breakdown="false"
          :title="pt('pages.overview.endpointTitle')"
          :start-date="startDate"
          :end-date="endDate"
        />
      </section>

      <div class="grid grid-cols-1 gap-6" :class="{ 'lg:grid-cols-3': quotaRows.length }">
        <div :class="{ 'lg:col-span-2': quotaRows.length }">
          <UserDashboardRecentUsage :data="recentUsage" :loading="recentLoading" />
        </div>
        <section v-if="quotaRows.length" class="card p-4" data-testid="portal-overview-quota">
          <div class="mb-4 flex items-center justify-between gap-2">
            <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ pt('pages.overview.quotaTitle') }}</h2>
            <router-link v-if="!authStore.isSimpleMode" :to="manageSubscriptionsLink" class="text-xs font-medium text-primary-600 hover:underline dark:text-primary-400">
              {{ pt('pages.overview.quotaManage') }}
            </router-link>
          </div>
          <ul class="space-y-4">
            <li v-for="row in quotaRows" :key="row.id" class="space-y-2">
              <div class="truncate text-sm font-medium text-gray-900 dark:text-white">{{ row.name }}</div>
              <p v-if="!row.periods.length" class="text-xs text-emerald-600 dark:text-emerald-400">
                {{ pt('pages.overview.quotaUnlimited') }}
              </p>
              <div v-for="period in row.periods" :key="period.key" class="space-y-1">
                <div class="flex justify-between text-xs text-gray-500 dark:text-dark-400">
                  <span>{{ pt(`pages.overview.${period.key}`) }}</span>
                  <span>{{ formatMoney(period.used) }} / {{ formatMoney(period.limit) }}</span>
                </div>
                <div class="h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700">
                  <div class="h-full rounded-full" :class="barClass(period.ratio)" :style="{ width: `${Math.min(period.ratio, 1) * 100}%` }"></div>
                </div>
              </div>
            </li>
          </ul>
        </section>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Select from '@/components/common/Select.vue'
import TokenUsageTrend from '@/components/charts/TokenUsageTrend.vue'
import ModelDistributionChart from '@/components/charts/ModelDistributionChart.vue'
import GroupDistributionChart from '@/components/charts/GroupDistributionChart.vue'
import EndpointDistributionChart from '@/components/charts/EndpointDistributionChart.vue'
import UserDashboardStats from '@/components/user/dashboard/UserDashboardStats.vue'
import UserDashboardRecentUsage from '@/components/user/dashboard/UserDashboardRecentUsage.vue'
import { usageAPI, type UserDashboardStats as UserStats } from '@/api/usage'
import { getMyPlatformQuotas } from '@/api/user'
import { useAuthStore } from '@/stores/auth'
import { useSubscriptionStore } from '@/stores'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import { formatDateLocalInput } from '@/utils/format'
import type { EndpointStat, GroupStat, ModelStat, PlatformQuotaItem, TrendDataPoint, UsageLog, UserSubscription } from '@/types'
import { usePortalI18n } from '../i18n'
import { WALLET_FOCUS_SUBSCRIPTIONS, WALLET_PATH } from '../pageRoutes'
import { buildQuotaRows } from './overviewQuota'

type Dimension = 'trend' | 'model' | 'group' | 'endpoint'
type DistributionMetric = 'tokens' | 'actual_cost'

const dimensions: Dimension[] = ['trend', 'model', 'group', 'endpoint']

const { pt } = usePortalI18n()
const authStore = useAuthStore()
const subscriptionStore = useSubscriptionStore()
const user = computed(() => authStore.user)

const stats = ref<UserStats | null>(null)
const platformQuotas = ref<PlatformQuotaItem[] | null>(null)
const loading = ref(false)

// Same default window as the upstream dashboard: the last seven days by day.
const startDate = ref(formatDateLocalInput(new Date(Date.now() - 6 * 86400000)))
const endDate = ref(formatDateLocalInput(new Date()))
const granularity = ref<'day' | 'hour'>('day')
const granularityOptions = computed(() => [
  { value: 'day', label: pt('pages.overview.day') },
  { value: 'hour', label: pt('pages.overview.hour') }
])
const dimension = ref<Dimension>('trend')
const modelMetric = ref<DistributionMetric>('tokens')
const groupMetric = ref<DistributionMetric>('tokens')
const endpointMetric = ref<DistributionMetric>('tokens')

const chartsLoading = ref(false)
const trendData = ref<TrendDataPoint[]>([])
const groupStats = ref<GroupStat[]>([])
const modelStats = ref<ModelStat[]>([])
const endpointStats = ref<EndpointStat[]>([])
let chartSeq = 0

const recentUsage = ref<UsageLog[]>([])
const recentLoading = ref(false)

const subscriptionsEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.subscription))
const quotaRows = computed(() =>
  subscriptionsEnabled.value ? buildQuotaRows(subscriptionStore.activeSubscriptions as UserSubscription[]) : []
)
const manageSubscriptionsLink = { path: WALLET_PATH, query: { focus: WALLET_FOCUS_SUBSCRIPTIONS } }

const formatMoney = (value: number) => `$${value.toFixed(2)}`
function barClass(ratio: number): string {
  if (ratio >= 0.9) return 'bg-red-500'
  if (ratio >= 0.7) return 'bg-amber-500'
  return 'bg-emerald-500'
}

async function loadStats() {
  loading.value = true
  try {
    await authStore.refreshUser()
    stats.value = await usageAPI.getDashboardStats()
  } catch (error) {
    console.error('Failed to load dashboard stats:', error)
  } finally {
    loading.value = false
  }
}

async function loadPlatformQuotas() {
  try {
    const data = await getMyPlatformQuotas()
    platformQuotas.value = data.platform_quotas ?? []
  } catch (error) {
    console.warn('Failed to load platform quotas:', error)
    platformQuotas.value = []
  }
}

// One window feeds every dimension, so switching dimensions never refetches.
async function loadCharts() {
  const seq = ++chartSeq
  const range = { start_date: startDate.value, end_date: endDate.value }
  chartsLoading.value = true
  const [snapshot, models, usage] = await Promise.allSettled([
    usageAPI.getDashboardSnapshotV2({
      ...range,
      granularity: granularity.value,
      include_trend: true,
      include_model_stats: false,
      include_group_stats: true
    }),
    usageAPI.getDashboardModels({ ...range, model_source: 'requested' }),
    usageAPI.getStats(range)
  ])
  if (seq !== chartSeq) return
  trendData.value = snapshot.status === 'fulfilled' ? snapshot.value.trend || [] : []
  groupStats.value = snapshot.status === 'fulfilled' ? snapshot.value.groups || [] : []
  modelStats.value = models.status === 'fulfilled' ? models.value.models || [] : []
  endpointStats.value = usage.status === 'fulfilled' ? usage.value.endpoints || [] : []
  for (const result of [snapshot, models, usage]) {
    if (result.status === 'rejected') console.error('Failed to load overview charts:', result.reason)
  }
  chartsLoading.value = false
}

async function loadRecent() {
  recentLoading.value = true
  try {
    const res = await usageAPI.getByDateRange(startDate.value, endDate.value)
    recentUsage.value = res.items.slice(0, 5)
  } catch (error) {
    console.error('Failed to load recent usage:', error)
  } finally {
    recentLoading.value = false
  }
}

function refreshAll() {
  void loadStats()
  void loadPlatformQuotas()
  void loadCharts()
  void loadRecent()
  if (subscriptionsEnabled.value) {
    subscriptionStore.fetchActiveSubscriptions().catch((error: unknown) => {
      console.warn('Failed to load subscriptions:', error)
    })
  }
}

onMounted(refreshAll)
</script>
