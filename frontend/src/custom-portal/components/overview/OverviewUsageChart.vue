<template>
  <section
    :id="id"
    class="card flex min-w-0 flex-col p-4"
    role="tabpanel"
    :aria-labelledby="`overview-metric-tab-${metric}`"
    :aria-busy="state.loading.value"
    data-testid="overview-usage-chart"
  >
    <div class="mb-4 flex min-h-7 flex-wrap items-center justify-between gap-2">
      <div class="flex items-baseline gap-2">
        <h2 class="text-sm font-semibold text-gray-900 dark:text-white">
          {{ pt('pages.overview.chart.title', { metric: pt(`pages.overview.metrics.${metric}`) }) }}
        </h2>
        <span class="text-xs text-gray-500 dark:text-dark-400" data-testid="overview-chart-granularity">
          {{ pt(`pages.overview.chart.granularity.${state.range.value.granularity}`) }}
        </span>
      </div>
      <div
        v-if="metric === 'tokens'"
        class="flex flex-wrap gap-2"
        role="group"
        :aria-label="pt('pages.overview.chart.legend')"
        data-testid="overview-chart-legend"
      >
        <button
          v-for="series in TOKEN_SERIES"
          :key="series"
          type="button"
          class="legend-chip"
          :class="hiddenSeries.has(series) ? 'legend-chip-off' : 'legend-chip-on'"
          :aria-pressed="!hiddenSeries.has(series)"
          :data-testid="`overview-chart-legend-${series}`"
          @click="toggleSeries(series)"
        >
          <span
            class="h-2 w-2 rounded-full"
            :class="{ 'opacity-30': hiddenSeries.has(series) }"
            :style="{ backgroundColor: TOKEN_SERIES_COLORS[series] }"
            aria-hidden="true"
          ></span>
          {{ pt(`pages.overview.chart.series.${series}`) }}
        </button>
      </div>
      <div v-else class="flex flex-wrap gap-2" data-testid="overview-chart-key">
        <span class="legend-chip legend-chip-on">
          <span class="h-0.5 w-3 rounded-full" :style="{ backgroundColor: tone.chart }" aria-hidden="true"></span>
          {{ pt('pages.overview.chart.current') }}
        </span>
        <button
          type="button"
          class="legend-chip"
          :class="showPrevious ? 'legend-chip-on' : 'legend-chip-off'"
          :aria-pressed="showPrevious"
          data-testid="overview-chart-previous-toggle"
          @click="showPrevious = !showPrevious"
        >
          <span
            class="w-3 border-t-2 border-dashed"
            :class="{ 'opacity-30': !showPrevious }"
            :style="{ borderColor: COMPARISON_LINE_COLOR }"
            aria-hidden="true"
          ></span>
          {{ pt('pages.overview.chart.previous') }}
        </button>
      </div>
    </div>

    <div class="relative h-64 sm:h-80">
      <div
        v-if="trendFailed"
        class="flex h-full flex-col items-center justify-center gap-3 text-sm text-gray-500 dark:text-dark-400"
        role="alert"
        data-testid="overview-chart-error"
      >
        <span>{{ pt('pages.overview.chart.error') }}</span>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="state.loading.value" @click="state.refresh()">
          {{ pt('pages.overview.chart.retry') }}
        </button>
      </div>
      <div
        v-else-if="state.loading.value && !hasBuckets"
        class="flex h-full items-center justify-center"
        data-testid="overview-chart-loading"
      >
        <LoadingSpinner />
        <span class="sr-only">{{ pt('pages.overview.chart.loading') }}</span>
      </div>
      <div
        v-else-if="isEmpty"
        class="flex h-full items-center justify-center text-sm text-gray-500 dark:text-dark-400"
        data-testid="overview-chart-empty"
      >
        {{ pt('pages.overview.chart.empty') }}
      </div>
      <template v-else>
        <Line :key="metric === 'tokens' ? 'tokens' : 'compare'" :data="chartData" :options="chartOptions" />
        <div
          v-if="state.loading.value"
          class="absolute inset-0 flex items-center justify-center bg-white/40 dark:bg-dark-900/40"
          data-testid="overview-chart-refreshing"
        >
          <LoadingSpinner />
        </div>
        <p v-if="summaryText" class="sr-only" data-testid="overview-chart-summary">{{ summaryText }}</p>
      </template>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMutationObserver, usePreferredReducedMotion } from '@vueuse/core'
import {
  CategoryScale,
  Chart as ChartJS,
  Filler,
  Legend,
  LinearScale,
  LineElement,
  PointElement,
  Title,
  Tooltip,
  type ChartData,
  type ChartOptions,
  type ScriptableLineSegmentContext,
  type TooltipItem
} from 'chart.js'
import { Line } from 'vue-chartjs'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import type { TrendDataPoint } from '@/types'
import { usePortalI18n } from '../../i18n'
import { injectOverviewState } from '../../overview/usageChartState'
import { metricSeries, peakAndMean, todayKey, type Granularity, type OverviewMetric } from '../../overview/usageChartData'
import { COMPARISON_LINE_COLOR, metricTone, TOKEN_SERIES, TOKEN_SERIES_COLORS, type TokenSeries } from '../../overview/metricTones'

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Title, Tooltip, Legend, Filler)

withDefaults(defineProps<{ id?: string }>(), { id: 'overview-usage-chart' })

const { pt } = usePortalI18n()
const state = injectOverviewState()
const reducedMotion = usePreferredReducedMotion()

const TOKEN_FIELDS: Record<TokenSeries, keyof Omit<TrendDataPoint, 'date'>> = {
  input: 'input_tokens',
  output: 'output_tokens',
  cache_creation: 'cache_creation_tokens',
  cache_read: 'cache_read_tokens'
}

const metric = computed(() => state.metric.value)
const tone = computed(() => metricTone(metric.value))
const trendFailed = computed(() => state.failedParts.value.includes('trend'))
const hasBuckets = computed(() => state.current.value.length > 0)
const isEmpty = computed(() => {
  const totals = state.currentTotals.value
  return !hasBuckets.value || (totals.requests === 0 && totals.total_tokens === 0 && totals.actual_cost === 0)
})

const isDark = ref(typeof document !== 'undefined' && document.documentElement.classList.contains('dark'))
if (typeof document !== 'undefined') {
  useMutationObserver(
    document.documentElement,
    () => {
      isDark.value = document.documentElement.classList.contains('dark')
    },
    { attributes: true, attributeFilter: ['class'] }
  )
}
// Upstream gray hex values; the Rhine theme remaps them.
const axisColors = computed(() => ({
  text: isDark.value ? '#9ca3af' : '#6b7280',
  grid: isDark.value ? '#374151' : '#e5e7eb'
}))

const showPrevious = ref(true)
const hiddenSeries = ref(new Set<TokenSeries>())
function toggleSeries(series: TokenSeries) {
  const next = new Set(hiddenSeries.value)
  if (next.has(series)) next.delete(series)
  else next.add(series)
  hiddenSeries.value = next
}

function formatBucketLabel(date: string, granularity: Granularity): string {
  return granularity === 'hour' ? date.slice(11, 16) || date : date.slice(5, 10) || date
}

function formatValue(kind: OverviewMetric, value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return '—'
  switch (kind) {
    case 'tokens':
    case 'requests':
      return compact(value)
    case 'cost':
      return `$${value > 0 && value < 0.01 ? value.toFixed(4) : value.toFixed(2)}`
    case 'cacheHitRate':
      return `${value.toFixed(1)}%`
  }
}

function compact(value: number): string {
  const abs = Math.abs(value)
  if (abs >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(2)}B`
  if (abs >= 1_000_000) return `${(value / 1_000_000).toFixed(2)}M`
  if (abs >= 1_000) return `${(value / 1_000).toFixed(1)}K`
  return Math.round(value).toLocaleString()
}

/** Chart values: hit rate is plotted as a percentage. */
function plotted(points: readonly TrendDataPoint[], kind: OverviewMetric): (number | null)[] {
  const series = metricSeries(points, kind)
  return kind === 'cacheHitRate' ? series.map((value) => (value === null ? null : value * 100)) : series
}

const currentSeries = computed(() => (metric.value === 'tokens' ? [] : plotted(state.current.value, metric.value)))
const previousSeries = computed(() => {
  if (metric.value === 'tokens') return []
  const prior = plotted(state.previous.value, metric.value)
  return state.current.value.map((_, index) => prior[index] ?? null)
})
/** The last bucket of a range that ends today is still filling, so its segment is dashed. */
const inProgress = computed(() => state.range.value.end === todayKey())
const lastIndex = computed(() => state.current.value.length - 1)
const segment = {
  borderDash: (ctx: ScriptableLineSegmentContext) =>
    inProgress.value && ctx.p1DataIndex === lastIndex.value ? [4, 4] : undefined
}

const markers = computed(() => (metric.value === 'tokens' ? null : peakAndMean(currentSeries.value)))

const labels = computed(() => state.current.value.map((point) => formatBucketLabel(point.date, state.range.value.granularity)))

const chartData = computed<ChartData<'line', (number | null)[], string>>(() => {
  if (metric.value === 'tokens') {
    return {
      labels: labels.value,
      datasets: TOKEN_SERIES.map((series) => ({
        label: pt(`pages.overview.chart.series.${series}`),
        data: state.current.value.map((point) => Number(point[TOKEN_FIELDS[series]]) || 0),
        borderColor: TOKEN_SERIES_COLORS[series],
        backgroundColor: TOKEN_SERIES_COLORS[series],
        fill: false,
        tension: 0.35,
        pointRadius: 0,
        pointHoverRadius: 3,
        borderWidth: 2,
        segment,
        hidden: hiddenSeries.value.has(series)
      }))
    }
  }
  return {
    labels: labels.value,
    datasets: [
      {
        label: pt('pages.overview.chart.current'),
        data: currentSeries.value,
        borderColor: tone.value.chart,
        backgroundColor: tone.value.chartFill,
        fill: 'origin',
        tension: 0.3,
        borderWidth: 2,
        spanGaps: false,
        segment,
        pointRadius: 0,
        pointBackgroundColor: tone.value.chart,
        pointHoverRadius: 4
      },
      {
        label: pt('pages.overview.chart.previous'),
        data: previousSeries.value,
        borderColor: COMPARISON_LINE_COLOR,
        backgroundColor: 'transparent',
        borderDash: [5, 5],
        fill: false,
        tension: 0.3,
        borderWidth: 1.5,
        spanGaps: false,
        pointRadius: 0,
        pointHoverRadius: 3,
        hidden: !showPrevious.value
      }
    ]
  }
})

function tooltipTitle(items: TooltipItem<'line'>[]): string {
  const index = items[0]?.dataIndex
  return index === undefined ? '' : state.current.value[index]?.date ?? ''
}

function tooltipLabel(item: TooltipItem<'line'>): string {
  const value = item.raw as number | null
  if (metric.value === 'tokens') return `${item.dataset.label}: ${formatValue('tokens', value)}`
  if (item.datasetIndex === 1) {
    const date = state.previous.value[item.dataIndex]?.date
    const label = date ? pt('pages.overview.chart.previousAt', { date }) : pt('pages.overview.chart.previous')
    return `${label}: ${formatValue(metric.value, value)}`
  }
  return `${item.dataset.label}: ${formatValue(metric.value, value)}`
}

const chartOptions = computed<ChartOptions<'line'>>(() => {
  return {
    responsive: true,
    maintainAspectRatio: false,
    animation: reducedMotion.value === 'reduce' ? false : undefined,
    interaction: { intersect: false, mode: 'index' },
    plugins: {
      legend: { display: false },
      tooltip: { callbacks: { title: tooltipTitle, label: tooltipLabel } }
    },
    scales: {
      x: {
        grid: { display: false },
        ticks: { color: axisColors.value.text, font: { size: 10 }, maxRotation: 0, autoSkip: true }
      },
      y: {
        beginAtZero: true,
        border: { display: false },
        ...(metric.value === 'cacheHitRate' ? { max: 100 } : {}),
        grid: { color: axisColors.value.grid, tickBorderDash: [4, 4] },
        ticks: {
          color: axisColors.value.text,
          font: { size: 10 },
          callback: (value: string | number) => formatValue(metric.value, Number(value))
        }
      }
    }
  }
})

const summaryText = computed(() => {
  const mark = markers.value
  if (!mark) return ''
  return pt('pages.overview.chart.summary', {
    peak: formatValue(metric.value, mark.peak),
    date: state.current.value[mark.peakIndex]?.date ?? '',
    mean: formatValue(metric.value, mark.mean)
  })
})
</script>

<style scoped>
.legend-chip {
  @apply inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 motion-reduce:transition-none;
}

.legend-chip-on {
  @apply border-gray-200 text-gray-700 dark:border-dark-600 dark:text-dark-200;
}

.legend-chip-off {
  @apply border-gray-100 text-gray-400 dark:border-dark-700 dark:text-dark-500;
}
</style>
