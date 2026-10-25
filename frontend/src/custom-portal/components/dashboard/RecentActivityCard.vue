<template>
  <div class="overflow-hidden rounded-2xl border border-gray-200/80 bg-white/80 p-5 shadow-sm backdrop-blur-xl dark:border-dark-700 dark:bg-dark-900/80">
    <div class="flex items-center justify-between border-b border-gray-100 pb-3 dark:border-dark-800">
      <div class="flex items-center gap-2">
        <span class="flex h-7 w-7 items-center justify-center rounded-lg bg-gray-100 text-gray-700 dark:bg-dark-800 dark:text-gray-300">
          <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 6v6h4.5m4.5 0a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
        </span>
        <h3 class="text-xs font-bold text-gray-800 dark:text-gray-200">{{ pt('activity.title') }}</h3>
      </div>
      <router-link to="/usage" class="text-[11px] font-semibold text-primary-600 hover:text-primary-700 dark:text-primary-400">
        {{ pt('activity.viewAll') }} &rarr;
      </router-link>
    </div>

    <!-- Loading State -->
    <div v-if="loading" class="flex h-44 items-center justify-center">
      <div class="h-6 w-6 animate-spin rounded-full border-2 border-primary-500 border-t-transparent"></div>
    </div>

    <!-- Empty State -->
    <div v-else-if="!data || data.length === 0" class="flex h-44 flex-col items-center justify-center text-center text-gray-400 dark:text-dark-400">
      <svg class="mb-2 h-8 w-8 text-gray-300 dark:text-dark-600" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" />
      </svg>
      <p class="text-xs font-medium">{{ pt('activity.empty') }}</p>
      <p class="mt-0.5 text-[11px]">{{ pt('activity.emptyHint') }}</p>
    </div>

    <!-- Logs Table/List -->
    <div v-else class="mt-3 divide-y divide-gray-100 dark:divide-dark-800">
      <div
        v-for="item in data"
        :key="item.id"
        class="flex flex-col sm:flex-row sm:items-center sm:justify-between py-2.5 gap-2 text-xs"
      >
        <div class="flex items-center gap-2.5">
          <span v-if="item.stream" class="rounded bg-blue-50 px-1.5 py-0.5 text-[10px] text-blue-600 dark:bg-blue-950/50 dark:text-blue-300">
            SSE
          </span>
          <span class="font-bold font-mono text-gray-900 dark:text-white">{{ item.model || 'unknown-model' }}</span>
          <span class="text-[11px] text-gray-400 font-mono">{{ formatTime(item.created_at) }}</span>
        </div>

        <div class="flex items-center gap-4 text-[11px] text-gray-500 dark:text-dark-400 font-mono">
          <span>Tokens: {{ (item.input_tokens || 0) + (item.output_tokens || 0) }}</span>
          <span>{{ pt('activity.duration', { ms: item.duration_ms ?? 0 }) }}</span>
          <span class="font-bold text-gray-700 dark:text-gray-300">${{ (item.actual_cost || 0).toFixed(4) }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { UsageLog } from '@/types'
import { usePortalI18n } from '../../i18n'

defineProps<{
  data: UsageLog[]
  loading: boolean
}>()

const { pt } = usePortalI18n()

function formatTime(iso: string): string {
  if (!iso) return ''
  try {
    const d = new Date(iso)
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
  } catch {
    return iso
  }
}
</script>
