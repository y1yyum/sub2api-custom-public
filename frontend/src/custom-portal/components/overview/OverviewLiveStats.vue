<template>
  <div
    class="flex flex-wrap items-center gap-x-4 gap-y-2 text-xs text-gray-500 dark:text-dark-400"
    :aria-label="pt('pages.overview.live.title')"
    :aria-busy="loading"
    data-testid="overview-live-stats"
  >
    <span
      v-for="item in items"
      :key="item.key"
      class="inline-flex items-center gap-1.5"
      :title="item.hint || undefined"
      :data-testid="`overview-live-${item.key}`"
    >
      <Icon :name="item.icon" size="xs" :class="item.tone" aria-hidden="true" />
      <span>{{ item.label }}</span>
      <span class="font-medium tabular-nums text-gray-900 dark:text-dark-100">{{ item.display }}</span>
    </span>
    <span v-if="failed" class="text-red-600 dark:text-red-400" role="status" data-testid="overview-live-error">
      {{ pt('pages.overview.live.loadFailed') }}
    </span>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useDocumentVisibility } from '@vueuse/core'
import Icon from '@/components/icons/Icon.vue'
import { usageAPI, type UserDashboardStats } from '@/api/usage'
import { usePortalI18n } from '../../i18n'
import { METRIC_TONES } from '../../overview/metricTones'
import { useCountUp } from '../../overview/useCountUp'

const LIVE_STATS_POLL_MS = 60_000

const { pt } = usePortalI18n()
const visibility = useDocumentVisibility()

const stats = ref<UserDashboardStats | null>(null)
const loading = ref(false)
const failed = ref(false)
let timer: ReturnType<typeof setInterval> | null = null
let seq = 0
let disposed = false

async function load() {
  const current = ++seq
  loading.value = true
  try {
    const data = await usageAPI.getDashboardStats()
    if (disposed || current !== seq) return
    stats.value = data
    failed.value = false
  } catch (error) {
    if (disposed || current !== seq) return
    console.error('Failed to load overview live stats:', error)
    failed.value = true
  } finally {
    if (!disposed && current === seq) loading.value = false
  }
}

function stop() {
  if (timer !== null) clearInterval(timer)
  timer = null
}

function start() {
  stop()
  timer = setInterval(() => void load(), LIVE_STATS_POLL_MS)
}

watch(visibility, (state, previous) => {
  if (disposed) return
  if (state === 'hidden') {
    stop()
  } else if (previous === 'hidden') {
    void load()
    start()
  }
})

onMounted(() => {
  void load()
  if (visibility.value !== 'hidden') start()
})

onBeforeUnmount(() => {
  disposed = true
  stop()
})

const rpm = useCountUp(() => stats.value?.rpm ?? 0)
const tpm = useCountUp(() => stats.value?.tpm ?? 0)
const duration = useCountUp(() => stats.value?.average_duration_ms ?? 0)
const todayCost = useCountUp(() => stats.value?.today_actual_cost ?? 0)

function usd(value: number): string {
  return `$${value > 0 && value < 0.01 ? value.toFixed(4) : value.toFixed(2)}`
}

function compact(value: number): string {
  const abs = Math.abs(value)
  if (abs >= 1_000_000) return `${(value / 1_000_000).toFixed(2)}M`
  if (abs >= 10_000) return `${(value / 1_000).toFixed(1)}K`
  return Math.round(value).toLocaleString()
}

function rate(value: number): string {
  return value >= 100 ? Math.round(value).toLocaleString() : value.toFixed(1)
}

function ms(value: number): string {
  return value >= 1000 ? `${(value / 1000).toFixed(2)} s` : `${Math.round(value)} ms`
}

const items = computed(() => {
  const known = stats.value !== null
  const show = (format: (value: number) => string, value: number) => (known ? format(value) : '—')
  return [
    {
      key: 'rpm',
      icon: 'swap' as const,
      tone: METRIC_TONES.requests.text,
      label: pt('pages.overview.live.rpm'),
      hint: pt('pages.overview.live.windowHint'),
      display: show(rate, rpm.value)
    },
    {
      key: 'tpm',
      icon: 'cube' as const,
      tone: METRIC_TONES.tokens.text,
      label: pt('pages.overview.live.tpm'),
      hint: pt('pages.overview.live.windowHint'),
      display: show(compact, tpm.value)
    },
    {
      key: 'duration',
      icon: 'clock' as const,
      tone: 'text-red-500 dark:text-red-400',
      label: pt('pages.overview.live.avgDuration'),
      hint: '',
      display: show(ms, duration.value)
    },
    {
      key: 'todayCost',
      icon: 'dollar' as const,
      tone: METRIC_TONES.cost.text,
      label: pt('pages.overview.live.todayCost'),
      hint: '',
      display: show(usd, todayCost.value)
    }
  ]
})

defineExpose({ reload: load })
</script>
