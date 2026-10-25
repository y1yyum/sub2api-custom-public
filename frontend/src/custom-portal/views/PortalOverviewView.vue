<template>
  <AppLayout>
    <div class="space-y-4" data-testid="portal-overview">
      <header class="dash-rise flex flex-wrap items-start justify-between gap-3" :style="rise(0)">
        <div class="min-w-0 space-y-1">
          <h2 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ pt('pages.overview.heading') }}</h2>
          <p class="text-sm text-gray-500 dark:text-dark-400">{{ pt('pages.overview.welcome') }}</p>
          <OverviewLiveStats ref="liveStats" class="pt-1" />
        </div>
        <OverviewToolbar :refreshing="refreshing" @refresh="refreshAll" />
      </header>

      <OverviewMetricCards class="dash-rise" :style="rise(1)" />

      <div class="dash-rise grid grid-cols-1 gap-4 xl:grid-cols-3" :style="rise(2)">
        <div ref="mainChart" class="min-w-0 scroll-mt-4 xl:col-span-2" data-testid="portal-overview-main">
          <OverviewUsageChart class="h-full" />
        </div>
        <OverviewTopModels />
      </div>

      <OverviewHeatmap class="dash-rise" :style="rise(3)" @drill="scrollToMainChart" />

      <div class="dash-rise grid grid-cols-1 gap-4 lg:grid-cols-3" :style="rise(4)">
        <div class="lg:order-2 lg:col-span-1"><UserDashboardQuickActions /></div>
        <div class="lg:order-1 lg:col-span-2"><OverviewAnnouncements ref="announcements" /></div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { usePreferredReducedMotion } from '@vueuse/core'
import AppLayout from '@/components/layout/AppLayout.vue'
import UserDashboardQuickActions from '@/components/user/dashboard/UserDashboardQuickActions.vue'
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

const { pt } = usePortalI18n()
const authStore = useAuthStore()
const state = provideOverviewState()
const reducedMotion = usePreferredReducedMotion()

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
    await Promise.allSettled([refreshUser(), state.refresh(), liveStats.value?.reload(), announcements.value?.reload()])
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
.dash-rise {
  animation: dash-rise 420ms cubic-bezier(0.2, 0.7, 0.2, 1) both;
  animation-delay: calc(var(--dash-rise-index, 0) * 60ms);
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

@media (prefers-reduced-motion: reduce) {
  .dash-rise {
    animation: none;
  }
}
</style>
