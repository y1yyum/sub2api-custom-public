<template>
  <AppLayout>
    <div class="space-y-4" data-testid="portal-overview">
      <header class="overview-header dash-rise relative z-20" :style="rise(0)">
        <div class="overview-heading min-w-0">
          <h2 class="overview-title text-gray-900 dark:text-white">{{ pt('pages.overview.heading') }}</h2>
          <p class="overview-description mt-2 text-sm text-gray-500 dark:text-dark-400">{{ pt('pages.overview.welcome') }}</p>
        </div>
        <OverviewLiveStats ref="liveStats" @loaded="dashboardStats = $event" />
        <OverviewToolbar :refreshing="refreshing" @refresh="refreshAll" />
      </header>

      <PortalFilterChips :items="filterChips" @remove="removeFilter" />

      <OverviewMetricCards class="dash-rise" :style="rise(1)" />

      <div class="dash-rise grid grid-cols-1 gap-4 xl:grid-cols-3" :style="rise(2)">
        <div ref="mainChart" class="min-w-0 scroll-mt-4 xl:col-span-2" data-testid="portal-overview-main">
          <OverviewUsageChart class="h-full" />
        </div>
        <OverviewTopModels />
      </div>

      <OverviewHeatmap class="dash-rise overview-deferred" :style="rise(3)" @drill="scrollToMainChart" />

      <div class="dash-rise overview-deferred grid grid-cols-1 gap-4 lg:grid-cols-3" :style="rise(4)">
        <div class="lg:order-2 lg:col-span-1"><UserDashboardQuickActions ref="quickActions" :stats="dashboardStats" /></div>
        <div class="lg:order-1 lg:col-span-2"><OverviewAnnouncements ref="announcements" /></div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { usePreferredReducedMotion } from '@vueuse/core'
import AppLayout from '@/components/layout/AppLayout.vue'
import UserDashboardQuickActions from '../components/overview/OverviewQuickActions.vue'
import type { UserDashboardStats } from '@/api/usage'
import { useAuthStore } from '@/stores/auth'
import OverviewAnnouncements from '../components/overview/OverviewAnnouncements.vue'
import OverviewHeatmap from '../components/overview/OverviewHeatmap.vue'
import OverviewLiveStats from '../components/overview/OverviewLiveStats.vue'
import OverviewMetricCards from '../components/overview/OverviewMetricCards.vue'
import OverviewToolbar from '../components/overview/OverviewToolbar.vue'
import OverviewTopModels from '../components/overview/OverviewTopModels.vue'
import OverviewUsageChart from '../components/overview/OverviewUsageChart.vue'
import { usePortalI18n } from '../i18n'
import { provideOverviewState } from '../overview/usageChartState'
import type { OverviewFilters } from '../overview/usageChartState'
import PortalFilterChips from '../components/common/PortalFilterChips.vue'

const { pt } = usePortalI18n()
const authStore = useAuthStore()
const state = provideOverviewState()
const dashboardStats = ref<UserDashboardStats | null>(null)
const filterChips = computed(() => Object.entries(state.filters.value).map(([key, value]) => ({ key, label: `${pt(`pages.overview.toolbar.${{ apiKeyId: 'apiKey', groupId: 'group', requestType: 'requestType', model: 'model' }[key]}`)}: ${value}` })))
function removeFilter(key: string) { state.setFilters({ [key as keyof OverviewFilters]: undefined }) }
const reducedMotion = usePreferredReducedMotion()

const quickActions = ref<InstanceType<typeof UserDashboardQuickActions> | null>(null)
const liveStats = ref<InstanceType<typeof OverviewLiveStats> | null>(null)
const announcements = ref<InstanceType<typeof OverviewAnnouncements> | null>(null)
const mainChart = ref<HTMLElement | null>(null)
const refreshing = ref(false)

const rise = (index: number) => ({ '--dash-rise-index': index })

function scrollToMainChart() {
  mainChart.value?.scrollIntoView({ behavior: reducedMotion.value === 'reduce' ? 'auto' : 'smooth', block: 'start' })
}

async function refreshUser() {
  try {
    await authStore.refreshUser()
  } catch (error) {
    console.warn('Failed to refresh user:', error)
  }
}

/** Waits for every section so the refresh control spins until the slowest one settles. */
async function refreshAll() {
  refreshing.value = true
  try {
    await Promise.allSettled([refreshUser(), state.refresh(), liveStats.value?.reload(), announcements.value?.reload(), quickActions.value?.reload()])
  } finally {
    refreshing.value = false
  }
}

onMounted(() => {
  void state.refresh()
  void refreshUser()
})
</script>

<style scoped>
/* Pixel title on the 12px grid, underlined by the square-spiral band. */
.overview-title {
  width: fit-content;
  font-family: var(--pp-font-pixel);
  font-size: 24px;
  font-synthesis: none;
  font-weight: 400;
  line-height: 28px;
}
.overview-header {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: 16px;
  padding-bottom: 4px;
}
.overview-description {
  padding-bottom: 20px;
  position: relative;
}
.overview-description::after {
  content: '';
  position: absolute;
  bottom: 0;
  left: 0;
  width: 96px;
  height: 8px;
  background: var(--pp-huiwen) left bottom / 8px 8px repeat-x;
}
.overview-header > :last-child { margin-left: auto; }
@media (min-width: 1280px) {
  .overview-header { column-gap: 24px; }
  .overview-heading { max-width: 255px; }
  .overview-header > :last-child { max-width: 460px; }
}
@media (max-width: 767px) {
  .overview-header > :last-child { margin-left: 0; width: 100%; }
}

.dash-rise {
  animation: dash-rise 240ms steps(3, end) both;
  animation-delay: calc(var(--dash-rise-index, 0) * 40ms);
}

@keyframes dash-rise {
  from {
    opacity: 0;
    transform: translateY(8px);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

.overview-deferred {
  content-visibility: auto;
  contain-intrinsic-size: auto none auto 240px;
}

@media (prefers-reduced-motion: reduce) {
  .dash-rise {
    animation: none;
    transform: none;
  }
}
</style>
