<template>
  <div class="min-h-screen bg-gray-50 text-gray-900 selection:bg-primary-500/20 selection:text-primary-800 dark:bg-dark-950 dark:text-gray-100 dark:selection:text-primary-200">
    <!-- Console Navigation -->
    <ConsoleNav @open-create-key="showKeyModal = true" />

    <main class="mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:px-8 space-y-6">
      <!-- 1. Developer Onboarding Card -->
      <DeveloperOnboardingCard
        :balance="userBalance"
        :key-count="keyCount"
        :has-keys="keyCount > 0"
        @open-create-key="showKeyModal = true"
      />

      <!-- 2. Aggregated 3-Cluster Stats -->
      <AggregatedStats
        :stats="stats"
        :balance="userBalance"
      />

      <!-- 3. Lower Grid: Activity Feed & Fast Tools -->
      <div class="grid grid-cols-1 gap-6 lg:grid-cols-12">
        <!-- Recent Usage Activity (8 cols) -->
        <div class="lg:col-span-8">
          <RecentActivityCard
            :data="recentUsage"
            :loading="loadingRecent"
          />
        </div>

        <!-- Quick Developer Actions & Navigation (4 cols) -->
        <div class="space-y-4 lg:col-span-4">
          <!-- Quick Keys Card -->
          <div class="rounded-2xl border border-gray-200/80 bg-white/80 p-5 shadow-sm backdrop-blur-xl dark:border-dark-700 dark:bg-dark-900/80">
            <h3 class="text-xs font-bold text-gray-800 dark:text-gray-200">{{ pt('dashboard.quickActions') }}</h3>
            <div class="mt-3 space-y-2">
              <button
                @click="showKeyModal = true"
                class="flex w-full items-center justify-between rounded-xl bg-gray-50 p-3 text-left transition-all hover:bg-gray-100 dark:bg-dark-800/60 dark:hover:bg-dark-800"
              >
                <div class="flex items-center gap-3">
                  <span class="flex h-8 w-8 items-center justify-center rounded-lg bg-primary-100 text-primary-600 dark:bg-primary-950 dark:text-primary-400">
                    🔑
                  </span>
                  <div>
                    <div class="text-xs font-bold text-gray-900 dark:text-white">{{ pt('dashboard.createKey') }}</div>
                    <div class="text-[11px] text-gray-500 dark:text-dark-400">{{ pt('dashboard.createKeyDetail') }}</div>
                  </div>
                </div>
                <svg class="h-4 w-4 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" /></svg>
              </button>

              <router-link
                to="/purchase"
                class="flex w-full items-center justify-between rounded-xl bg-gray-50 p-3 text-left transition-all hover:bg-gray-100 dark:bg-dark-800/60 dark:hover:bg-dark-800"
              >
                <div class="flex items-center gap-3">
                  <span class="flex h-8 w-8 items-center justify-center rounded-lg bg-emerald-100 text-emerald-600 dark:bg-emerald-950 dark:text-emerald-400">
                    💳
                  </span>
                  <div>
                    <div class="text-xs font-bold text-gray-900 dark:text-white">{{ pt('dashboard.purchase') }}</div>
                    <div class="text-[11px] text-gray-500 dark:text-dark-400">{{ pt('dashboard.purchaseDetail') }}</div>
                  </div>
                </div>
                <svg class="h-4 w-4 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" /></svg>
              </router-link>

              <router-link
                to="/model-plaza"
                class="flex w-full items-center justify-between rounded-xl bg-gray-50 p-3 text-left transition-all hover:bg-gray-100 dark:bg-dark-800/60 dark:hover:bg-dark-800"
              >
                <div class="flex items-center gap-3">
                  <span class="flex h-8 w-8 items-center justify-center rounded-lg bg-blue-100 text-blue-600 dark:bg-blue-950 dark:text-blue-400">
                    🧩
                  </span>
                  <div>
                    <div class="text-xs font-bold text-gray-900 dark:text-white">{{ pt('dashboard.modelPlaza') }}</div>
                    <div class="text-[11px] text-gray-500 dark:text-dark-400">{{ pt('dashboard.modelPlazaDetail') }}</div>
                  </div>
                </div>
                <svg class="h-4 w-4 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" /></svg>
              </router-link>

              <router-link
                to="/redeem"
                class="flex w-full items-center justify-between rounded-xl bg-gray-50 p-3 text-left transition-all hover:bg-gray-100 dark:bg-dark-800/60 dark:hover:bg-dark-800"
              >
                <div class="flex items-center gap-3">
                  <span class="flex h-8 w-8 items-center justify-center rounded-lg bg-amber-100 text-amber-600 dark:bg-amber-950 dark:text-amber-400">
                    🎁
                  </span>
                  <div>
                    <div class="text-xs font-bold text-gray-900 dark:text-white">{{ pt('dashboard.redeem') }}</div>
                    <div class="text-[11px] text-gray-500 dark:text-dark-400">{{ pt('dashboard.redeemDetail') }}</div>
                  </div>
                </div>
                <svg class="h-4 w-4 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7" /></svg>
              </router-link>
            </div>
          </div>
        </div>
      </div>
    </main>

    <!-- Quick Key Creation Modal -->
    <QuickKeyModal
      :show="showKeyModal"
      @close="showKeyModal = false"
      @created="onKeyCreated"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import ConsoleNav from '../components/dashboard/ConsoleNav.vue'
import DeveloperOnboardingCard from '../components/dashboard/DeveloperOnboardingCard.vue'
import AggregatedStats from '../components/dashboard/AggregatedStats.vue'
import RecentActivityCard from '../components/dashboard/RecentActivityCard.vue'
import QuickKeyModal from '../components/dashboard/QuickKeyModal.vue'
import { usageAPI, type UserDashboardStats } from '@/api/usage'
import { list as listKeys } from '@/api/keys'
import { useAuthStore } from '@/stores'
import type { UsageLog } from '@/types'
import { formatDateLocalInput } from '@/utils/format'
import { usePortalI18n } from '../i18n'

const { pt } = usePortalI18n()
const authStore = useAuthStore()
const userBalance = computed(() => authStore.user?.balance || 0)

const stats = ref<UserDashboardStats | null>(null)
const recentUsage = ref<UsageLog[]>([])
const keyCount = ref(0)
const loadingRecent = ref(false)
const showKeyModal = ref(false)

async function loadDashboardData() {
  try {
    await authStore.refreshUser()
  } catch (err) {
    console.error('Failed to refresh user:', err)
  }

  try {
    stats.value = await usageAPI.getDashboardStats()
  } catch (err) {
    console.error('Failed to load dashboard stats:', err)
  }

  try {
    const keysRes = await listKeys(1, 5)
    keyCount.value = keysRes.total || keysRes.items?.length || 0
  } catch (err) {
    console.error('Failed to load keys count:', err)
  }

  loadingRecent.value = true
  try {
    const startDate = formatDateLocalInput(new Date(Date.now() - 7 * 86400000))
    const endDate = formatDateLocalInput(new Date())
    const res = await usageAPI.getByDateRange(startDate, endDate)
    recentUsage.value = (res.items || []).slice(0, 6)
  } catch (err) {
    console.error('Failed to load recent usage:', err)
  } finally {
    loadingRecent.value = false
  }
}

function onKeyCreated() {
  keyCount.value++
  loadDashboardData()
}

onMounted(() => {
  loadDashboardData()
})
</script>
