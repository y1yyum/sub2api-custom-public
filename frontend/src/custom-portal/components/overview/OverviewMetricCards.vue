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
      class="metric-card card flex min-w-0 flex-col gap-1 overflow-hidden p-4 text-left focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 sm:gap-3"
      :style="card.surface"
      :data-testid="`overview-metric-${card.metric}`"
      @click="state.setMetric(card.metric)"
    >
      <span class="flex min-w-0 items-start justify-between gap-2">
        <span class="metric-label truncate text-xs" :class="card.tone.text">
          {{ pt(`pages.overview.metrics.${card.metric}`) }}
        </span>
        <span
          class="metric-icon flex h-10 w-10 shrink-0 items-center justify-center bg-white text-gray-900 dark:bg-dark-800 dark:text-dark-100"
          aria-hidden="true"
          data-testid="overview-metric-icon"
        >
          <PixelIcon :name="card.tone.icon" size="md" />
        </span>
      </span>
      <span class="flex min-w-0 flex-wrap items-end justify-between gap-x-3 gap-y-2">
        <span
          class="metric-value min-w-0 truncate tabular-nums text-gray-900 dark:text-white"
          :class="{ 'opacity-60': state.loading.value }"
          :title="card.display"
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
          class="metric-delta inline-flex shrink-0 items-center gap-0.5 px-1.5 py-0.5 font-medium tabular-nums"
          :class="BADGE_CLASSES[card.badge.kind]"
          :title="card.badge.title"
          :data-testid="`overview-metric-${card.metric}-delta`"
          :data-kind="card.badge.kind"
        >
          <PixelIcon v-if="card.badge.kind === 'up'" name="trendingUp" size="xs" aria-hidden="true" />
          <PixelIcon v-else-if="card.badge.kind === 'down'" name="arrowDown" size="xs" aria-hidden="true" />
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
import PixelIcon from '../common/PixelIcon.vue'
import { usePortalI18n } from '../../i18n'
import { injectOverviewState } from '../../overview/usageChartState'
import { OVERVIEW_METRICS, type DeltaKind, type OverviewMetric } from '../../overview/usageChartData'
import { metricTone, type MetricTone } from '../../overview/metricTones'
import { pixelFrame } from '../../theme/pixelFrame'
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

/** Custom properties read by the scoped frame rules below. */
function surfaceStyle({ surface }: MetricTone): Record<string, string> {
  return {
    '--metric-tint': surface.light.tint,
    '--metric-rule': surface.light.rule,
    '--metric-frame': pixelFrame(surface.light.rule),
    '--metric-tint-dark': surface.dark.tint,
    '--metric-rule-dark': surface.dark.rule,
    '--metric-frame-dark': pixelFrame(surface.dark.rule)
  }
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
      surface: surfaceStyle(metricTone(metric)),
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

<style scoped>
/*
 * The card frame is three rules in the metric's colour; the selected card fills the gaps into one 5px rule.
 * `.card` is repeated to outrank the theme's card surface, including its dark variant.
 */
.metric-card.card {
  background-color: var(--metric-tint);
  border-color: var(--metric-rule);
  border-image-source: var(--metric-frame);
}

:where(html.dark) .metric-card.card {
  background-color: var(--metric-tint-dark);
  border-color: var(--metric-rule-dark);
  border-image-source: var(--metric-frame-dark);
}

.metric-card.card[aria-selected='true'] {
  border-image-source: none;
}

.metric-icon,
.metric-delta {
  border: 3px double currentColor;
}

.metric-label,
.metric-value {
  font-family: var(--pp-font-pixel);
  font-synthesis: none;
  font-weight: 400;
  letter-spacing: 0;
}

.metric-label {
  font-size: 12px;
}

.metric-value {
  font-size: 24px;
  line-height: 1;
}

@media (min-width: 640px) {
  .metric-value {
    font-size: 36px;
  }
}

@media (forced-colors: active) {
  .metric-card.card {
    border-image-source: none;
  }
}
</style>
