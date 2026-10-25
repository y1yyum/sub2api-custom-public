<template>
  <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
    <!-- Group 1: Financial & Quota -->
    <div class="relative overflow-hidden rounded-2xl border border-gray-200/80 bg-white/80 p-5 shadow-sm backdrop-blur-xl dark:border-dark-700 dark:bg-dark-900/80">
      <div class="flex items-center justify-between border-b border-gray-100 pb-3 dark:border-dark-800">
        <div class="flex items-center gap-2">
          <span class="flex h-7 w-7 items-center justify-center rounded-lg bg-emerald-100 text-emerald-600 dark:bg-emerald-950/80 dark:text-emerald-400">
            <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 6v12m-3-2.818l.879.659c1.171.879 3.07.879 4.242 0 1.172-.879 1.172-2.303 0-3.182C13.536 12.219 12.768 12 12 12c-.725 0-1.45-.22-2.003-.659-1.106-.879-1.106-2.303 0-3.182s2.9-.879 4.006 0l.415.33M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
          </span>
          <h3 class="text-xs font-bold text-gray-800 dark:text-gray-200">{{ pt('stats.assetsTitle') }}</h3>
        </div>
        <router-link to="/purchase" class="text-[11px] font-semibold text-emerald-600 hover:text-emerald-700 dark:text-emerald-400">
          {{ pt('stats.recharge') }} &rarr;
        </router-link>
      </div>

      <div class="mt-4">
        <div class="text-[11px] text-gray-500 dark:text-dark-400">{{ pt('stats.availableBalance') }}</div>
        <div class="mt-1 flex items-baseline gap-2">
          <span class="text-3xl font-extrabold tracking-tight text-emerald-600 dark:text-emerald-400 font-mono">
            ${{ balance.toFixed(2) }}
          </span>
          <span class="text-xs text-gray-400">USD</span>
        </div>
      </div>

      <div class="mt-5 grid grid-cols-2 gap-3 border-t border-gray-100 pt-4 dark:border-dark-800 text-xs">
        <div>
          <span class="text-gray-500 dark:text-dark-400 text-[11px]">{{ pt('stats.todayActual') }}</span>
          <div class="mt-0.5 font-bold font-mono text-gray-900 dark:text-white">
            ${{ (stats?.today_actual_cost || 0).toFixed(4) }}
          </div>
          <span class="text-[10px] text-gray-400">{{ pt('stats.standardPrice', { amount: formatUsd(stats?.today_cost || 0, 4) }) }}</span>
        </div>
        <div>
          <span class="text-gray-500 dark:text-dark-400 text-[11px]">{{ pt('stats.totalSpent') }}</span>
          <div class="mt-0.5 font-bold font-mono text-gray-900 dark:text-white">
            ${{ (stats?.total_actual_cost || 0).toFixed(2) }}
          </div>
          <span class="text-[10px] text-gray-400">{{ pt('stats.standardPrice', { amount: formatUsd(stats?.total_cost || 0, 2) }) }}</span>
        </div>
      </div>
    </div>

    <!-- Group 2: Throughput & Health -->
    <div class="relative overflow-hidden rounded-2xl border border-gray-200/80 bg-white/80 p-5 shadow-sm backdrop-blur-xl dark:border-dark-700 dark:bg-dark-900/80">
      <div class="flex items-center justify-between border-b border-gray-100 pb-3 dark:border-dark-800">
        <div class="flex items-center gap-2">
          <span class="flex h-7 w-7 items-center justify-center rounded-lg bg-blue-100 text-blue-600 dark:bg-blue-950/80 dark:text-blue-400">
            <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M3.75 13.5l10.5-11.25L12 10.5h8.25L9.75 21.75 12 13.5H3.75z" />
            </svg>
          </span>
          <h3 class="text-xs font-bold text-gray-800 dark:text-gray-200">{{ pt('stats.throughputTitle') }}</h3>
        </div>
        <router-link to="/usage" class="text-[11px] font-semibold text-blue-600 hover:text-blue-700 dark:text-blue-400">
          {{ pt('stats.usageLogs') }} &rarr;
        </router-link>
      </div>

      <div class="mt-4">
        <div class="text-[11px] text-gray-500 dark:text-dark-400">{{ pt('stats.todayRequests') }}</div>
        <div class="mt-1 flex items-baseline gap-2">
          <span class="text-3xl font-extrabold tracking-tight text-gray-900 dark:text-white font-mono">
            {{ formatNum(stats?.today_requests || 0) }}
          </span>
          <span class="text-xs text-gray-400">{{ pt('stats.totalRequests', { count: formatNum(stats?.total_requests || 0) }) }}</span>
        </div>
      </div>

      <div class="mt-5 grid grid-cols-2 gap-3 border-t border-gray-100 pt-4 dark:border-dark-800 text-xs">
        <div>
          <span class="text-gray-500 dark:text-dark-400 text-[11px]">{{ pt('stats.currentRate') }}</span>
          <div class="mt-0.5 font-bold font-mono text-gray-900 dark:text-white">
            {{ stats?.rpm || 0 }} <span class="text-[10px] font-normal text-gray-400">RPM</span>
          </div>
          <span class="text-[10px] text-gray-400">{{ formatTokens(stats?.tpm || 0) }} TPM</span>
        </div>
        <div>
          <span class="text-gray-500 dark:text-dark-400 text-[11px]">{{ pt('stats.avgLatency') }}</span>
          <div class="mt-0.5 font-bold font-mono text-gray-900 dark:text-white">
            {{ formatDuration(stats?.average_duration_ms || 0) }}
          </div>
        </div>
      </div>
    </div>

    <!-- Group 3: Token Flow & Cache -->
    <div class="relative overflow-hidden rounded-2xl border border-gray-200/80 bg-white/80 p-5 shadow-sm backdrop-blur-xl dark:border-dark-700 dark:bg-dark-900/80">
      <div class="flex items-center justify-between border-b border-gray-100 pb-3 dark:border-dark-800">
        <div class="flex items-center gap-2">
          <span class="flex h-7 w-7 items-center justify-center rounded-lg bg-indigo-100 text-indigo-600 dark:bg-indigo-950/80 dark:text-indigo-400">
            <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M20.25 6.375c0 2.278-3.694 4.125-8.25 4.125S3.75 8.653 3.75 6.375m16.5 0c0-2.278-3.694-4.125-8.25-4.125S3.75 4.097 3.75 6.375m16.5 0v11.25c0 2.278-3.694 4.125-8.25 4.125s-8.25-1.847-8.25-4.125V6.375m16.5 5.625c0 2.278-3.694 4.125-8.25 4.125s-8.25-1.847-8.25-4.125" />
            </svg>
          </span>
          <h3 class="text-xs font-bold text-gray-800 dark:text-gray-200">{{ pt('stats.tokenTitle') }}</h3>
        </div>
        <span class="text-[11px] font-semibold text-primary-600 dark:text-primary-400">
          {{ pt('stats.cacheBadge') }}
        </span>
      </div>

      <div class="mt-4">
        <div class="text-[11px] text-gray-500 dark:text-dark-400">{{ pt('stats.todayTokens') }}</div>
        <div class="mt-1 flex items-baseline gap-2">
          <span class="text-3xl font-extrabold tracking-tight text-indigo-600 dark:text-indigo-400 font-mono">
            {{ formatTokens(stats?.today_tokens || 0) }}
          </span>
          <span class="text-xs text-gray-400">{{ pt('stats.totalTokens', { count: formatTokens(stats?.total_tokens || 0) }) }}</span>
        </div>
      </div>

      <div class="mt-5 grid grid-cols-2 gap-3 border-t border-gray-100 pt-4 dark:border-dark-800 text-xs">
        <div>
          <span class="text-gray-500 dark:text-dark-400 text-[11px]">{{ pt('stats.inputOutput') }}</span>
          <div class="mt-0.5 font-bold font-mono text-gray-900 dark:text-white">
            {{ formatTokens(stats?.today_input_tokens || 0) }} / {{ formatTokens(stats?.today_output_tokens || 0) }}
          </div>
          <span class="text-[10px] text-gray-400">{{ pt('stats.ratio', { ratio: calculateRatio }) }}</span>
        </div>
        <div>
          <span class="text-gray-500 dark:text-dark-400 text-[11px]">{{ pt('stats.cacheHit') }}</span>
          <div class="mt-0.5 font-bold font-mono text-emerald-600 dark:text-emerald-400">
            {{ formatTokens((stats?.today_cache_creation_tokens || 0) + (stats?.today_cache_read_tokens || 0)) }}
          </div>
          <span class="text-[10px] text-gray-400">{{ pt('stats.cacheHint') }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { UserDashboardStats } from '@/api/usage'
import { usePortalI18n } from '../../i18n'

const props = defineProps<{
  stats: UserDashboardStats | null
  balance: number
}>()

const { pt } = usePortalI18n()

function formatUsd(value: number, decimals: number): string {
  return `$${value.toFixed(decimals)}`
}

function formatNum(n: number): string {
  return new Intl.NumberFormat().format(n)
}

function formatTokens(t: number): string {
  if (t >= 1000000) return (t / 1000000).toFixed(2) + 'M'
  if (t >= 1000) return (t / 1000).toFixed(1) + 'K'
  return String(t)
}

function formatDuration(ms: number): string {
  if (ms <= 0) return '0ms'
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(2)}s`
}

const calculateRatio = computed(() => {
  const inT = props.stats?.today_input_tokens || 0
  const outT = props.stats?.today_output_tokens || 0
  if (outT === 0) return '1:0'
  return `1:${(outT / (inT || 1)).toFixed(1)}`
})
</script>
