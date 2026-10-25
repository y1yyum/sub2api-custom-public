<template>
  <div
    class="grid grid-cols-2 gap-4 lg:grid-cols-4"
    role="tablist"
    :aria-label="pt('pages.overview.metrics.label')"
    data-testid="overview-metric-cards"
    @keydown="onKeydown"
  >
    <button
      v-for="card in cards"
      :id="tabId(card.metric)"
      :key="card.metric"
      :ref="(el) => setTabRef(card.metric, el)"
      type="button"
      role="tab"
      :aria-selected="card.selected"
      :aria-controls="controls"
      :tabindex="card.selected ? 0 : -1"
      class="card flex min-w-0 flex-col gap-1 overflow-hidden p-4 text-left transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 motion-reduce:transition-none sm:gap-3"
      :class="
        card.selected
          ? 'border-primary-600 dark:border-primary-500/70'
          : 'hover:border-gray-300 dark:hover:border-dark-500'
      "
      :data-testid="`overview-metric-${card.metric}`"
      @click="state.setMetric(card.metric)"
    >
      <span class="flex min-w-0 items-center gap-2">
        <span
          class="flex h-5 w-5 shrink-0 items-center justify-center rounded-md sm:h-7 sm:w-7"
          :class="[card.tone.bg, card.tone.text]"
          aria-hidden="true"
        >
          <Icon :name="card.tone.icon" size="sm" />
        </span>
        <span class="truncate text-xs font-medium text-gray-500 dark:text-dark-400">
          {{ pt(`pages.overview.metrics.${card.metric}`) }}
        </span>
      </span>
      <span class="flex min-w-0 items-end justify-between gap-3">
        <span
          class="min-w-0 truncate text-2xl font-semibold tracking-tight tabular-nums text-gray-900 dark:text-white"
          :class="{ 'opacity-60': state.loading.value }"
          :data-testid="`overview-metric-${card.metric}-value`"
        >
          {{ card.display }}
        </span>
        <span
          class="h-8 w-20 shrink-0"
          :class="card.selected ? 'text-primary-600 dark:text-primary-500' : 'text-gray-400 dark:text-dark-500'"
        >
          <UsageSparkline :values="card.series" />
        </span>
      </span>
      <span class="flex min-w-0 flex-wrap items-center gap-1 text-xs text-gray-500 dark:text-dark-400 sm:gap-2">
        <span
          class="inline-flex shrink-0 items-center gap-0.5 rounded-full px-2 py-0.5 font-medium tabular-nums"
          :class="BADGE_CLASSES[card.badge.kind]"
          :title="card.badge.title"
          :data-testid="`overview-metric-${card.metric}-delta`"
          :data-kind="card.badge.kind"
        >
          <Icon v-if="card.badge.kind === 'up'" name="trendingUp" size="xs" aria-hidden="true" />
          <Icon v-else-if="card.badge.kind === 'down'" name="arrowDown" size="xs" aria-hidden="true" />
          <span aria-hidden="true">{{ card.badge.text }}</span>
          <span class="sr-only">{{ card.badge.title }}</span>
        </span>
        <span v-if="card.badge.kind !== 'none'" class="truncate" aria-hidden="true">{{ pt('pages.overview.metrics.vsPrevious') }}</span>
      </span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed, type ComponentPublicInstance, type Ref } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import { usePortalI18n } from '../../i18n'
import { injectOverviewState } from '../../overview/usageChartState'
import { OVERVIEW_METRICS, type DeltaKind, type OverviewMetric } from '../../overview/usageChartData'
import { metricTone } from '../../overview/metricTones'
import { useCountUp } from '../../overview/useCountUp'
import UsageSparkline from './UsageSparkline.vue'

withDefaults(defineProps<{ controls?: string }>(), { controls: 'overview-usage-chart' })

const { pt } = usePortalI18n()
const state = injectOverviewState()

const BADGE_CLASSES: Record<DeltaKind, string> = {
  up: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300',
  down: 'bg-red-50 text-red-700 dark:bg-red-900/30 dark:text-red-300',
  flat: 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-300',
  new: 'bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300',
  none: 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-dark-400'
}

const tabId = (metric: OverviewMetric) => `overview-metric-tab-${metric}`

const trendFailed = computed(() => state.failedParts.value.includes('trend'))

const counters = Object.fromEntries(
  OVERVIEW_METRICS.map((metric) => [metric, useCountUp(() => state.metricSummaries.value[metric].value ?? 0)])
) as Record<OverviewMetric, Readonly<Ref<number>>>

function formatMetric(metric: OverviewMetric, value: number): string {
  switch (metric) {
    case 'tokens':
    case 'requests':
      return formatCount(value)
    case 'cost':
      return formatUsd(value)
    case 'cacheHitRate':
      return `${(value * 100).toFixed(1)}%`
  }
}

function formatCount(value: number): string {
  const rounded = Math.round(value)
  if (Math.abs(rounded) >= 1_000_000_000) return `${(rounded / 1_000_000_000).toFixed(2)}B`
  if (Math.abs(rounded) >= 1_000_000) return `${(rounded / 1_000_000).toFixed(2)}M`
  if (Math.abs(rounded) >= 10_000) return `${(rounded / 1_000).toFixed(1)}K`
  return rounded.toLocaleString()
}

function formatUsd(value: number): string {
  return `$${value > 0 && value < 0.01 ? value.toFixed(4) : value.toFixed(2)}`
}

function badgeFor(kind: DeltaKind, delta: number | null) {
  const percent = delta === null ? '' : `${Math.abs(delta * 100).toFixed(1)}%`
  switch (kind) {
    case 'up':
      return { kind, text: `+${percent}`, title: pt('pages.overview.metrics.deltaUp', { value: percent }) }
    case 'down':
      return { kind, text: `-${percent}`, title: pt('pages.overview.metrics.deltaDown', { value: percent }) }
    case 'flat':
      return { kind, text: pt('pages.overview.metrics.flat'), title: pt('pages.overview.metrics.deltaFlat') }
    case 'new':
      return { kind, text: pt('pages.overview.metrics.new'), title: pt('pages.overview.metrics.deltaNew') }
    case 'none':
      return { kind, text: '—', title: pt('pages.overview.metrics.noComparison') }
  }
}

const cards = computed(() =>
  OVERVIEW_METRICS.map((metric) => {
    const summary = state.metricSummaries.value[metric]
    const unknown = trendFailed.value || summary.value === null
    const kind: DeltaKind = unknown ? 'none' : summary.deltaKind
    return {
      metric,
      selected: state.metric.value === metric,
      tone: metricTone(metric),
      display: unknown ? '—' : formatMetric(metric, counters[metric].value),
      badge: badgeFor(kind, summary.delta),
      series: trendFailed.value ? [] : summary.series
    }
  })
)

const tabRefs = new Map<OverviewMetric, HTMLElement>()
function setTabRef(metric: OverviewMetric, el: Element | ComponentPublicInstance | null) {
  if (el instanceof HTMLElement) tabRefs.set(metric, el)
  else tabRefs.delete(metric)
}

function onKeydown(event: KeyboardEvent) {
  const index = OVERVIEW_METRICS.indexOf(state.metric.value)
  const last = OVERVIEW_METRICS.length - 1
  let next: number
  switch (event.key) {
    case 'ArrowRight':
    case 'ArrowDown':
      next = index >= last ? 0 : index + 1
      break
    case 'ArrowLeft':
    case 'ArrowUp':
      next = index <= 0 ? last : index - 1
      break
    case 'Home':
      next = 0
      break
    case 'End':
      next = last
      break
    default:
      return
  }
  event.preventDefault()
  const metric = OVERVIEW_METRICS[next]
  state.setMetric(metric)
  tabRefs.get(metric)?.focus()
}
</script>
