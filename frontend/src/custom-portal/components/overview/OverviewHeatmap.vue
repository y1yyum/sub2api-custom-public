<template>
  <section ref="root" class="card space-y-3 p-4" :aria-labelledby="titleId" data-testid="overview-heatmap">
    <div class="flex items-center justify-between gap-2">
      <h2 :id="titleId" class="text-sm font-semibold text-gray-900 dark:text-white" :title="pt('pages.overview.heatmap.hint')">
        {{ pt('pages.overview.heatmap.title') }}
      </h2>
      <div class="flex shrink-0 items-center gap-1 text-xs text-gray-500 dark:text-dark-400" data-testid="overview-heatmap-legend">
        <span>{{ pt('pages.overview.heatmap.less') }}</span>
        <span v-for="(levelClass, level) in HEATMAP_LEVEL_CLASSES" :key="level" class="h-3 w-3 rounded-sm" :class="levelClass" aria-hidden="true"></span>
        <span>{{ pt('pages.overview.heatmap.more') }}</span>
      </div>
    </div>
    <p v-if="heatmap.loaded.value" class="sr-only" data-testid="overview-heatmap-summary">
      {{ pt('pages.overview.heatmap.summary', { requests: totalRequests.toLocaleString(), days: activeDays }) }}
    </p>

    <div
      v-if="heatmap.loading.value && !heatmap.loaded.value"
      class="h-32 animate-pulse rounded-lg bg-gray-100 motion-reduce:animate-none dark:bg-dark-800"
      aria-hidden="true"
      data-testid="overview-heatmap-loading"
    ></div>

    <div
      v-else-if="heatmap.error.value"
      class="flex flex-col items-center justify-center gap-2 py-8 text-sm text-red-600 dark:text-red-400"
      role="alert"
      data-testid="overview-heatmap-error"
    >
      <span>{{ pt('pages.overview.heatmap.error') }}</span>
      <button
        type="button"
        class="btn btn-secondary btn-sm"
        :disabled="heatmap.loading.value"
        data-testid="overview-heatmap-retry"
        @click="retry"
      >
        {{ pt('pages.overview.heatmap.retry') }}
      </button>
    </div>

    <div v-else-if="heatmap.loaded.value" class="relative" :class="{ 'opacity-60': heatmap.loading.value }">
      <div ref="scroller" class="overflow-x-auto pb-1">
        <div class="inline-flex flex-col" :style="{ gap: `${GAP}px` }">
          <div class="flex" :style="{ gap: `${GAP}px`, paddingLeft: `${LABEL_WIDTH + GAP}px` }" aria-hidden="true">
            <span
              v-for="(label, index) in monthLabels"
              :key="index"
              class="overflow-visible whitespace-nowrap text-[10px] leading-3 text-gray-500 dark:text-dark-400"
              :style="{ width: `${cellSize}px`, minWidth: `${cellSize}px` }"
            >
              {{ label }}
            </span>
          </div>

          <div
            ref="grid"
            role="grid"
            class="flex flex-col"
            :style="{ gap: `${GAP}px` }"
            :aria-label="pt('pages.overview.heatmap.gridLabel')"
            :aria-describedby="tip ? tipId : undefined"
            @keydown="onKeydown"
          >
            <div v-for="(row, weekday) in rows" :key="weekday" role="row" class="flex items-center" :style="{ gap: `${GAP}px` }">
              <span
                role="rowheader"
                class="shrink-0 text-[10px] leading-none text-gray-500 dark:text-dark-400"
                :style="{ width: `${LABEL_WIDTH}px` }"
              >
                {{ weekday % 2 === 0 && weekday < 6 ? weekdayLabels[weekday] : '' }}
              </span>
              <template v-for="(cell, week) in row" :key="week">
                <div
                  v-if="cell"
                  role="gridcell"
                  class="shrink-0 cursor-pointer rounded-sm outline-none ring-primary-500 ring-offset-1 transition-shadow hover:ring-2 focus-visible:ring-2 motion-reduce:transition-none dark:ring-offset-dark-900"
                  :class="HEATMAP_LEVEL_CLASSES[cell.level]"
                  :style="cellStyle"
                  :tabindex="cell.date === focusDate ? 0 : -1"
                  :aria-label="pt('pages.overview.heatmap.cellLabel', { date: cell.date, requests: cell.point.requests.toLocaleString() })"
                  :data-date="cell.date"
                  :data-level="cell.level"
                  data-testid="overview-heatmap-cell"
                  @click="drill(cell.date)"
                  @focus="onCellFocus(cell.date, $event)"
                  @blur="hideTip(cell.date)"
                  @mouseenter="showTip(cell.date, $event)"
                  @mouseleave="hideTip(cell.date)"
                ></div>
                <div v-else class="shrink-0" :style="cellStyle" aria-hidden="true"></div>
              </template>
            </div>
          </div>
        </div>
      </div>

      <div
        v-if="tip && tipCell"
        :id="tipId"
        role="tooltip"
        class="pointer-events-none absolute z-10 -translate-x-1/2 -translate-y-full whitespace-nowrap rounded-lg bg-gray-900 px-3 py-2 text-xs text-white shadow-lg dark:bg-dark-700"
        :style="{ left: `${tip.x}px`, top: `${tip.y - 6}px` }"
        data-testid="overview-heatmap-tooltip"
      >
        <p class="font-semibold">{{ formatDay(tipCell.date) }}</p>
        <p>{{ pt('pages.overview.heatmap.requests') }}: {{ tipCell.point.requests.toLocaleString() }}</p>
        <p>{{ pt('pages.overview.heatmap.tokens') }}: {{ formatCompactNumber(tipCell.point.total_tokens) }}</p>
        <p>{{ pt('pages.overview.heatmap.cost') }}: {{ formatUsd(tipCell.point.actual_cost) }}</p>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, useId } from 'vue'
import { useIntersectionObserver, useResizeObserver } from '@vueuse/core'
import type { TrendDataPoint } from '@/types'
import { formatCompactNumber } from '@/utils/format'
import { usePortalI18n } from '../../i18n'
import { injectOverviewState } from '../../overview/usageChartState'
import { addDays, parseDateKey } from '../../overview/usageChartData'
import { createHeatmapScale, type HeatmapLevel } from '../../overview/heatmapLevels'
import { HEATMAP_LEVEL_CLASSES } from '../../overview/metricTones'

interface HeatCell {
  date: string
  point: TrendDataPoint
  level: HeatmapLevel
}

const GAP = 3
const LABEL_WIDTH = 28
const MIN_CELL = 10
const MAX_CELL = 18
const DEFAULT_CELL = 12

const emit = defineEmits<{ drill: [date: string] }>()

const { pt, locale } = usePortalI18n()
const state = injectOverviewState()
const heatmap = state.heatmap

const uid = useId()
const titleId = `${uid}-title`
const tipId = `${uid}-tip`

const root = ref<HTMLElement | null>(null)
const scroller = ref<HTMLElement | null>(null)
const grid = ref<HTMLElement | null>(null)
const containerWidth = ref(0)

const intlLocale = computed(() => (locale.value === 'zh' ? 'zh-CN' : 'en-US'))

/** Weeks start on Monday: 0 = Monday … 6 = Sunday. */
function weekdayIndex(date: string): number {
  return ((parseDateKey(date)?.getDay() ?? 1) + 6) % 7
}

const cellsByDate = computed(() => {
  const points = heatmap.points.value
  const scale = createHeatmapScale(points.map((point) => point.requests))
  const cells = new Map<string, HeatCell>()
  for (const point of points) cells.set(point.date, { date: point.date, point, level: scale(point.requests) })
  return cells
})

const orderedDates = computed(() => [...cellsByDate.value.keys()].sort())

/** Column-major weeks; slots outside the loaded days (including the future) stay null. */
const weeks = computed<(HeatCell | null)[][]>(() => {
  const dates = orderedDates.value
  if (!dates.length) return []
  const first = addDays(dates[0], -weekdayIndex(dates[0]))
  const last = dates[dates.length - 1]
  const result: (HeatCell | null)[][] = []
  for (let weekStart = first; weekStart <= last; weekStart = addDays(weekStart, 7)) {
    result.push(Array.from({ length: 7 }, (_, offset) => cellsByDate.value.get(addDays(weekStart, offset)) ?? null))
  }
  return result
})

const rows = computed(() => Array.from({ length: 7 }, (_, weekday) => weeks.value.map((week) => week[weekday])))

const monthLabels = computed(() => {
  const formatter = new Intl.DateTimeFormat(intlLocale.value, { month: 'short' })
  const labels = weeks.value.map((week) => {
    const firstOfMonth = week.find((cell) => cell?.date.endsWith('-01'))
    return firstOfMonth ? formatter.format(parseDateKey(firstOfMonth.date)!) : ''
  })
  const firstCell = weeks.value[0]?.find((cell) => cell !== null)
  const nextLabel = labels.findIndex((label, index) => index > 0 && label !== '')
  if (firstCell && !labels[0] && (nextLabel === -1 || nextLabel >= 3)) {
    labels[0] = formatter.format(parseDateKey(firstCell.date)!)
  }
  return labels
})

const weekdayLabels = computed(() => {
  const formatter = new Intl.DateTimeFormat(intlLocale.value, { weekday: 'short' })
  // 2024-01-01 is a Monday.
  return Array.from({ length: 7 }, (_, offset) => formatter.format(new Date(2024, 0, 1 + offset)))
})

const cellSize = computed(() => {
  const columns = weeks.value.length
  if (!columns || !containerWidth.value) return DEFAULT_CELL
  const available = containerWidth.value - LABEL_WIDTH - GAP
  const size = Math.floor((available + GAP) / columns) - GAP
  return Math.min(MAX_CELL, Math.max(MIN_CELL, size))
})
const cellStyle = computed(() => ({ width: `${cellSize.value}px`, height: `${cellSize.value}px` }))

const totalRequests = computed(() => heatmap.points.value.reduce((sum, point) => sum + point.requests, 0))
const activeDays = computed(() => heatmap.points.value.filter((point) => point.requests > 0).length)

useResizeObserver(root, (entries) => {
  const entry = entries[0]
  if (entry) containerWidth.value = entry.contentRect.width
})

const { isSupported, stop } = useIntersectionObserver(
  root,
  (entries) => {
    if (!entries.some((entry) => entry.isIntersecting)) return
    stop()
    void heatmap.load()
  },
  { rootMargin: '200px' }
)

onMounted(() => {
  if (!isSupported.value) void heatmap.load()
})

function retry() {
  void heatmap.load({ force: true })
}

// Roving tabindex: one focusable day, defaulting to the most recent.
const activeDate = ref<string | null>(null)
const focusDate = computed(() => {
  const dates = orderedDates.value
  if (activeDate.value && cellsByDate.value.has(activeDate.value)) return activeDate.value
  return dates[dates.length - 1] ?? null
})

const KEY_STEPS: Record<string, number> = { ArrowUp: -1, ArrowDown: 1, ArrowLeft: -7, ArrowRight: 7 }

function onKeydown(event: KeyboardEvent) {
  const current = focusDate.value
  if (!current) return
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault()
    drill(current)
    return
  }
  const dates = orderedDates.value
  let target: string | null = null
  if (event.key in KEY_STEPS) target = addDays(current, KEY_STEPS[event.key])
  else if (event.key === 'Home') target = dates[0]
  else if (event.key === 'End') target = dates[dates.length - 1]
  else return
  event.preventDefault()
  if (!target || !cellsByDate.value.has(target)) return
  activeDate.value = target
  void nextTick(() => {
    grid.value?.querySelector<HTMLElement>(`[data-date="${target}"]`)?.focus()
  })
}

function drill(date: string) {
  activeDate.value = date
  if (state.drillToDay(date)) emit('drill', date)
}

const tip = ref<{ date: string; x: number; y: number } | null>(null)
const tipCell = computed(() => (tip.value ? cellsByDate.value.get(tip.value.date) ?? null : null))

function showTip(date: string, event: Event) {
  const target = event.currentTarget as HTMLElement | null
  const wrapper = scroller.value?.parentElement
  if (!target || !wrapper) return
  const cell = target.getBoundingClientRect()
  const box = wrapper.getBoundingClientRect()
  tip.value = { date, x: cell.left - box.left + cell.width / 2, y: cell.top - box.top }
}

function onCellFocus(date: string, event: Event) {
  activeDate.value = date
  showTip(date, event)
}

function hideTip(date: string) {
  if (tip.value?.date === date) tip.value = null
}

function formatDay(date: string): string {
  const parsed = parseDateKey(date)
  if (!parsed) return date
  return new Intl.DateTimeFormat(intlLocale.value, { year: 'numeric', month: 'short', day: 'numeric', weekday: 'short' }).format(parsed)
}

function formatUsd(value: number): string {
  return `$${value.toFixed(value >= 1 ? 2 : 4)}`
}
</script>
