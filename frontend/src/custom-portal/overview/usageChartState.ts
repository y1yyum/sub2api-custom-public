import { computed, getCurrentScope, inject, onScopeDispose, provide, ref, shallowRef, watch, type ComputedRef, type InjectionKey, type Ref } from 'vue'
import { usageAPI } from '@/api/usage'
import type { ModelStat, TrendDataPoint, UsageRequestType } from '@/types'
import {
  alignPrevious,
  computeDelta,
  deltaKind,
  fillTrendBuckets,
  isDateKey,
  isFixedRangePreset,
  isOverviewMetric,
  metricSeries,
  metricValue,
  normalizeBucketKey,
  OVERVIEW_METRICS,
  previousRange,
  resolveCustomRange,
  resolveHeatmapRange,
  resolvePresetRange,
  sumTrend,
  type DateRange,
  type DeltaKind,
  type FixedRangePreset,
  type OverviewMetric,
  type RangePreset,
  type TrendTotals
} from './usageChartData'

export interface OverviewFilters {
  apiKeyId?: number
  model?: string
  groupId?: number
  requestType?: UsageRequestType
}

export type OverviewLoadPart = 'trend' | 'previous' | 'models'

export interface MetricSummary {
  value: number | null
  previous: number | null
  delta: number | null
  deltaKind: DeltaKind
  series: (number | null)[]
}

export interface OverviewHeatmapState {
  range: Readonly<Ref<DateRange>>
  /** Zero-filled day buckets for `range`. */
  points: Readonly<Ref<TrendDataPoint[]>>
  loading: Readonly<Ref<boolean>>
  loaded: Readonly<Ref<boolean>>
  error: Readonly<Ref<boolean>>
  /** Loads once per filter set; later filter changes reload it automatically. */
  load: (options?: { force?: boolean }) => Promise<void>
}

export interface OverviewState {
  preset: Readonly<Ref<RangePreset>>
  range: Readonly<Ref<DateRange>>
  previousRange: ComputedRef<DateRange>
  metric: Readonly<Ref<OverviewMetric>>
  filters: Readonly<Ref<Readonly<OverviewFilters>>>
  activeFilterCount: ComputedRef<number>
  /** Zero-filled buckets of the selected range; today stops at the current hour. */
  current: Readonly<Ref<TrendDataPoint[]>>
  /** Zero-filled buckets of the previous range, trimmed to `current.length`. */
  previous: Readonly<Ref<TrendDataPoint[]>>
  /**
   * Summed from the raw API points, so buckets whose labels fall outside the
   * browser-local range (server and browser time zones differ) still count.
   */
  currentTotals: ComputedRef<TrendTotals>
  /** Raw previous-period points, trimmed like `previous` while the current range is partial. */
  previousTotals: ComputedRef<TrendTotals>
  metricSummaries: ComputedRef<Record<OverviewMetric, MetricSummary>>
  models: Readonly<Ref<ModelStat[]>>
  loading: Readonly<Ref<boolean>>
  failedParts: Readonly<Ref<OverviewLoadPart[]>>
  error: ComputedRef<boolean>
  heatmap: OverviewHeatmapState
  setPreset: (preset: RangePreset) => void
  /** Returns false and changes nothing when the range is invalid. */
  setCustomRange: (start: string, end: string) => boolean
  setMetric: (metric: OverviewMetric) => void
  setFilters: (patch: Partial<OverviewFilters>) => void
  toggleModel: (model: string) => void
  /** Narrows to one day at hourly granularity and loads immediately. */
  drillToDay: (date: string) => boolean
  clearFilters: () => void
  /** Recomputes preset ranges against the clock and reloads everything now. */
  refresh: () => Promise<void>
}

export interface OverviewStateOptions {
  now?: () => Date
  storage?: Pick<Storage, 'getItem' | 'setItem'> | null
  debounceMs?: number
}

export const OVERVIEW_STATE_KEY: InjectionKey<OverviewState> = Symbol('portal-overview-state')
export const OVERVIEW_STORAGE_KEY = 'portal.overview.v1'
export const OVERVIEW_FILTER_DEBOUNCE_MS = 300
export const DEFAULT_OVERVIEW_PRESET: FixedRangePreset = '30d'
export const DEFAULT_OVERVIEW_METRIC: OverviewMetric = 'tokens'

interface PersistedOverview {
  preset: FixedRangePreset
  metric: OverviewMetric
}

function defaultStorage(): Pick<Storage, 'getItem' | 'setItem'> | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage
  } catch {
    return null
  }
}

export function readPersistedOverview(storage: Pick<Storage, 'getItem'> | null): PersistedOverview {
  const fallback: PersistedOverview = { preset: DEFAULT_OVERVIEW_PRESET, metric: DEFAULT_OVERVIEW_METRIC }
  try {
    const raw = storage?.getItem(OVERVIEW_STORAGE_KEY)
    if (!raw) return fallback
    const parsed: unknown = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object') return fallback
    const { preset, metric } = parsed as Record<string, unknown>
    return {
      preset: isFixedRangePreset(preset) ? preset : fallback.preset,
      metric: isOverviewMetric(metric) ? metric : fallback.metric
    }
  } catch {
    return fallback
  }
}

function compactFilters(filters: OverviewFilters): OverviewFilters {
  const next: OverviewFilters = {}
  if (filters.apiKeyId !== undefined && filters.apiKeyId !== null) next.apiKeyId = filters.apiKeyId
  if (filters.model) next.model = filters.model
  if (filters.groupId !== undefined && filters.groupId !== null) next.groupId = filters.groupId
  if (filters.requestType) next.requestType = filters.requestType
  return next
}

/** Query params shared by the trend and model endpoints. */
export function toUsageFilterParams(filters: OverviewFilters) {
  return {
    ...(filters.apiKeyId !== undefined ? { api_key_id: filters.apiKeyId } : {}),
    ...(filters.model ? { model: filters.model } : {}),
    ...(filters.groupId !== undefined ? { group_id: filters.groupId } : {}),
    ...(filters.requestType ? { request_type: filters.requestType } : {})
  }
}

function filterSignature(filters: OverviewFilters): string {
  return JSON.stringify([filters.apiKeyId ?? null, filters.model ?? null, filters.groupId ?? null, filters.requestType ?? null])
}

/** Keeps the previous-period points that precede the first bucket the current range has not reached. */
function trimPrevious(points: readonly TrendDataPoint[], prior: DateRange, kept: readonly TrendDataPoint[]): TrendDataPoint[] {
  const cutoff = fillTrendBuckets([], prior)[kept.length]?.date
  if (!cutoff) return [...points]
  return points.filter((point) => {
    const key = normalizeBucketKey(point.date, prior.granularity)
    return key !== null && key < cutoff
  })
}

/** Standalone state; must run inside a component setup or an effect scope. */
export function createOverviewState(options: OverviewStateOptions = {}): OverviewState {
  const now = options.now ?? (() => new Date())
  const storage = options.storage === undefined ? defaultStorage() : options.storage
  const debounceMs = options.debounceMs ?? OVERVIEW_FILTER_DEBOUNCE_MS
  const persisted = readPersistedOverview(storage)

  const preset = ref<RangePreset>(persisted.preset)
  const range = shallowRef<DateRange>(resolvePresetRange(persisted.preset, now()))
  const metric = ref<OverviewMetric>(persisted.metric)
  const filters = shallowRef<Readonly<OverviewFilters>>({})

  const current = shallowRef<TrendDataPoint[]>([])
  const previous = shallowRef<TrendDataPoint[]>([])
  const currentRaw = shallowRef<TrendDataPoint[]>([])
  const previousRaw = shallowRef<TrendDataPoint[]>([])
  const models = shallowRef<ModelStat[]>([])
  const loading = ref(false)
  const failedParts = shallowRef<OverviewLoadPart[]>([])

  const heatmapRange = shallowRef<DateRange>(resolveHeatmapRange(now()))
  const heatmapPoints = shallowRef<TrendDataPoint[]>([])
  const heatmapLoading = ref(false)
  const heatmapLoaded = ref(false)
  const heatmapError = ref(false)
  let heatmapActive = false
  let heatmapLoadedSignature: string | null = null

  let requestSeq = 0
  let heatmapSeq = 0
  let debounceTimer: ReturnType<typeof setTimeout> | null = null
  // A debounced filter change still owes the heatmap a reload when a range change cancels the timer.
  let heatmapReloadPending = false

  const previousDateRange = computed(() => previousRange(range.value))
  const currentTotals = computed(() => sumTrend(currentRaw.value))
  const previousTotals = computed(() => sumTrend(previousRaw.value))
  const activeFilterCount = computed(() => Object.keys(filters.value).length)
  const metricSummaries = computed(() => {
    const summaries = {} as Record<OverviewMetric, MetricSummary>
    // A failed previous-period request leaves empty totals, which must not read as "new".
    const previousKnown = !failedParts.value.includes('previous')
    for (const key of OVERVIEW_METRICS) {
      const value = metricValue(currentTotals.value, key)
      const prior = previousKnown ? metricValue(previousTotals.value, key) : null
      summaries[key] = {
        value,
        previous: prior,
        delta: computeDelta(value, prior),
        deltaKind: deltaKind(value, prior),
        series: metricSeries(current.value, key)
      }
    }
    return summaries
  })

  function cancelDebounce() {
    if (debounceTimer !== null) clearTimeout(debounceTimer)
    debounceTimer = null
  }

  async function loadMain(): Promise<void> {
    cancelDebounce()
    if (heatmapReloadPending) {
      heatmapReloadPending = false
      if (heatmapActive) void loadHeatmap()
    }
    const seq = ++requestSeq
    const span = range.value
    const prior = previousRange(span)
    const filterParams = toUsageFilterParams(filters.value)
    loading.value = true
    const [currentTrend, previousTrend, modelStats] = await Promise.allSettled([
      usageAPI.getDashboardTrend({ start_date: span.start, end_date: span.end, granularity: span.granularity, ...filterParams }),
      usageAPI.getDashboardTrend({ start_date: prior.start, end_date: prior.end, granularity: prior.granularity, ...filterParams }),
      usageAPI.getDashboardModels({ start_date: span.start, end_date: span.end, ...filterParams, model_source: 'requested' })
    ])
    if (seq !== requestSeq) return

    const failed: OverviewLoadPart[] = []
    const currentPoints = currentTrend.status === 'fulfilled' ? currentTrend.value.trend ?? [] : []
    const filled = fillTrendBuckets(currentPoints, span, now())
    current.value = filled
    currentRaw.value = currentPoints
    if (currentTrend.status === 'rejected') failed.push('trend')
    if (previousTrend.status === 'fulfilled') {
      const priorPoints = previousTrend.value.trend ?? []
      previous.value = alignPrevious(fillTrendBuckets(priorPoints, prior), filled)
      previousRaw.value = trimPrevious(priorPoints, prior, filled)
    } else {
      previous.value = []
      previousRaw.value = []
      failed.push('previous')
    }
    models.value = modelStats.status === 'fulfilled' ? modelStats.value.models ?? [] : []
    if (modelStats.status === 'rejected') failed.push('models')
    for (const result of [currentTrend, previousTrend, modelStats]) {
      if (result.status === 'rejected') console.error('Failed to load overview usage:', result.reason)
    }
    failedParts.value = failed
    loading.value = false
  }

  async function loadHeatmap({ force = false }: { force?: boolean } = {}): Promise<void> {
    heatmapActive = true
    const signature = filterSignature(filters.value)
    if (!force && heatmapLoaded.value && heatmapLoadedSignature === signature) return
    const seq = ++heatmapSeq
    const span = resolveHeatmapRange(now())
    heatmapLoading.value = true
    try {
      const response = await usageAPI.getDashboardTrend({
        start_date: span.start,
        end_date: span.end,
        granularity: 'day',
        ...toUsageFilterParams(filters.value)
      })
      if (seq !== heatmapSeq) return
      heatmapRange.value = span
      heatmapPoints.value = fillTrendBuckets(response.trend, span, now())
      heatmapLoaded.value = true
      heatmapLoadedSignature = signature
      heatmapError.value = false
    } catch (error) {
      if (seq !== heatmapSeq) return
      console.error('Failed to load overview heatmap:', error)
      heatmapError.value = true
    }
    heatmapLoading.value = false
  }

  function scheduleFilterLoad() {
    cancelDebounce()
    heatmapReloadPending = true
    debounceTimer = setTimeout(() => {
      debounceTimer = null
      void loadMain()
    }, debounceMs)
  }

  function applyFilters(next: OverviewFilters) {
    const compacted = compactFilters(next)
    if (filterSignature(compacted) === filterSignature(filters.value)) return
    filters.value = compacted
    scheduleFilterLoad()
  }

  function setRange(nextPreset: RangePreset, nextRange: DateRange) {
    const unchanged =
      preset.value === nextPreset &&
      range.value.start === nextRange.start &&
      range.value.end === nextRange.end &&
      range.value.granularity === nextRange.granularity
    preset.value = nextPreset
    if (unchanged) return
    range.value = nextRange
    void loadMain()
  }

  function setPreset(next: RangePreset) {
    if (next === 'custom') {
      preset.value = 'custom'
      return
    }
    setRange(next, resolvePresetRange(next, now()))
  }

  function setCustomRange(start: string, end: string): boolean {
    const next = resolveCustomRange(start, end)
    if (!next) return false
    setRange('custom', next)
    return true
  }

  function drillToDay(date: string): boolean {
    if (!isDateKey(date)) return false
    return setCustomRange(date, date)
  }

  function setMetric(next: OverviewMetric) {
    if (isOverviewMetric(next)) metric.value = next
  }

  function setFilters(patch: Partial<OverviewFilters>) {
    applyFilters({ ...filters.value, ...patch })
  }

  function toggleModel(model: string) {
    setFilters({ model: filters.value.model === model ? undefined : model })
  }

  function clearFilters() {
    applyFilters({})
  }

  async function refresh(): Promise<void> {
    if (preset.value !== 'custom') range.value = resolvePresetRange(preset.value, now())
    heatmapReloadPending = false
    const tasks = [loadMain()]
    if (heatmapActive) tasks.push(loadHeatmap({ force: true }))
    await Promise.all(tasks)
  }

  let lastFixedPreset = persisted.preset
  watch([preset, metric], ([nextPreset, nextMetric]) => {
    if (isFixedRangePreset(nextPreset)) lastFixedPreset = nextPreset
    const snapshot: PersistedOverview = { preset: lastFixedPreset, metric: nextMetric }
    try {
      storage?.setItem(OVERVIEW_STORAGE_KEY, JSON.stringify(snapshot))
    } catch {
      // Storage may be full or disabled; persistence is best-effort.
    }
  })

  if (getCurrentScope()) {
    onScopeDispose(() => {
      cancelDebounce()
      requestSeq++
      heatmapSeq++
    })
  }

  return {
    preset,
    range,
    previousRange: previousDateRange,
    metric,
    filters,
    activeFilterCount,
    current,
    previous,
    currentTotals,
    previousTotals,
    metricSummaries,
    models,
    loading,
    failedParts,
    error: computed(() => failedParts.value.length > 0),
    heatmap: {
      range: heatmapRange,
      points: heatmapPoints,
      loading: heatmapLoading,
      loaded: heatmapLoaded,
      error: heatmapError,
      load: loadHeatmap
    },
    setPreset,
    setCustomRange,
    setMetric,
    setFilters,
    toggleModel,
    drillToDay,
    clearFilters,
    refresh
  }
}

export function provideOverviewState(options?: OverviewStateOptions): OverviewState {
  const state = createOverviewState(options)
  provide(OVERVIEW_STATE_KEY, state)
  return state
}

export function injectOverviewState(): OverviewState {
  const state = inject(OVERVIEW_STATE_KEY, null)
  if (!state) throw new Error('injectOverviewState() requires provideOverviewState() in an ancestor')
  return state
}
