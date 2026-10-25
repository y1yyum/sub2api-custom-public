import type { TrendDataPoint } from '@/types'
import { formatDateLocalInput } from '@/utils/format'

export type RangePreset = 'today' | '7d' | '30d' | '90d' | 'custom'
export type FixedRangePreset = Exclude<RangePreset, 'custom'>
export type Granularity = 'day' | 'hour'
export type OverviewMetric = 'tokens' | 'requests' | 'cost' | 'cacheHitRate'

export interface DateRange {
  /** Local `YYYY-MM-DD`, inclusive. */
  start: string
  /** Local `YYYY-MM-DD`, inclusive. */
  end: string
  granularity: Granularity
}

export type TrendTotals = Omit<TrendDataPoint, 'date'>

export interface PeakAndMean {
  peakIndex: number
  peak: number
  mean: number
}

export type DeltaKind = 'up' | 'down' | 'flat' | 'new' | 'none'

export const RANGE_PRESETS: readonly RangePreset[] = ['today', '7d', '30d', '90d', 'custom']
export const FIXED_RANGE_PRESETS: readonly FixedRangePreset[] = ['today', '7d', '30d', '90d']
export const OVERVIEW_METRICS: readonly OverviewMetric[] = ['tokens', 'requests', 'cost', 'cacheHitRate']
export const MAX_CUSTOM_RANGE_DAYS = 366
export const HEATMAP_DAYS = 365

const PRESET_DAYS: Record<Exclude<FixedRangePreset, 'today'>, number> = { '7d': 7, '30d': 30, '90d': 90 }
const DATE_KEY = /^(\d{4})-(\d{2})-(\d{2})$/
const HOUR_KEY = /^(\d{4}-\d{2}-\d{2})[ T](\d{2})/
const DAY_MS = 86400000

export function isRangePreset(value: unknown): value is RangePreset {
  return typeof value === 'string' && (RANGE_PRESETS as readonly string[]).includes(value)
}

export function isFixedRangePreset(value: unknown): value is FixedRangePreset {
  return typeof value === 'string' && (FIXED_RANGE_PRESETS as readonly string[]).includes(value)
}

export function isOverviewMetric(value: unknown): value is OverviewMetric {
  return typeof value === 'string' && (OVERVIEW_METRICS as readonly string[]).includes(value)
}

/** Parses a local `YYYY-MM-DD`; rejects impossible dates such as `2025-02-29`. */
export function parseDateKey(key: string): Date | null {
  const match = DATE_KEY.exec(key)
  if (!match) return null
  const [year, month, day] = [Number(match[1]), Number(match[2]), Number(match[3])]
  const date = new Date(year, month - 1, day)
  if (date.getFullYear() !== year || date.getMonth() !== month - 1 || date.getDate() !== day) return null
  return date
}

export function isDateKey(value: unknown): value is string {
  return typeof value === 'string' && parseDateKey(value) !== null
}

export function todayKey(now: Date = new Date()): string {
  return formatDateLocalInput(now)
}

/** Calendar arithmetic on local dates, immune to DST and UTC offsets. */
export function addDays(key: string, days: number): string {
  const date = parseDateKey(key)
  if (!date) throw new RangeError(`Invalid date key: ${key}`)
  date.setDate(date.getDate() + days)
  return formatDateLocalInput(date)
}

function dayIndex(key: string): number {
  const match = DATE_KEY.exec(key)
  if (!match) throw new RangeError(`Invalid date key: ${key}`)
  return Math.round(Date.UTC(Number(match[1]), Number(match[2]) - 1, Number(match[3])) / DAY_MS)
}

/** Inclusive number of calendar days from `start` to `end`. */
export function daysInRange(start: string, end: string): number {
  return dayIndex(end) - dayIndex(start) + 1
}

export function hourKey(date: string, hour: number): string {
  return `${date} ${String(hour).padStart(2, '0')}:00`
}

export function isTodayRange(range: DateRange, now: Date = new Date()): boolean {
  const today = todayKey(now)
  return range.start === today && range.end === today
}

/** Every fixed preset ends today; `today` uses hourly buckets. */
export function resolvePresetRange(preset: FixedRangePreset, now: Date = new Date()): DateRange {
  const end = todayKey(now)
  if (preset === 'today') return { start: end, end, granularity: 'hour' }
  return { start: addDays(end, 1 - PRESET_DAYS[preset]), end, granularity: 'day' }
}

/** A single day is hourly; returns null for malformed, reversed or over-long ranges. */
export function resolveCustomRange(start: string, end: string): DateRange | null {
  if (!isDateKey(start) || !isDateKey(end)) return null
  const days = daysInRange(start, end)
  if (days < 1 || days > MAX_CUSTOM_RANGE_DAYS) return null
  return { start, end, granularity: days === 1 ? 'hour' : 'day' }
}

/** The equally long window that ends the day before `range` starts. */
export function previousRange(range: DateRange): DateRange {
  const days = daysInRange(range.start, range.end)
  return { start: addDays(range.start, -days), end: addDays(range.start, -1), granularity: range.granularity }
}

/** Day-granular window of `days` days ending today. */
export function resolveHeatmapRange(now: Date = new Date(), days: number = HEATMAP_DAYS): DateRange {
  const end = todayKey(now)
  return { start: addDays(end, 1 - days), end, granularity: 'day' }
}

/** Bucket keys for `range`; with `now`, buckets after the current day (or hour, today) are omitted. */
export function bucketKeys(range: DateRange, now?: Date): string[] {
  const today = now ? todayKey(now) : null
  const currentHour = now ? now.getHours() : 23
  const keys: string[] = []
  const days = daysInRange(range.start, range.end)
  for (let offset = 0; offset < days; offset++) {
    const date = addDays(range.start, offset)
    if (today && date > today) break
    if (range.granularity === 'day') {
      keys.push(date)
      continue
    }
    const lastHour = date === today ? currentHour : 23
    for (let hour = 0; hour <= lastHour; hour++) keys.push(hourKey(date, hour))
  }
  return keys
}

export function normalizeBucketKey(date: string, granularity: Granularity): string | null {
  if (granularity === 'day') {
    const day = date.slice(0, 10)
    return isDateKey(day) ? day : null
  }
  const match = HOUR_KEY.exec(date)
  return match ? `${match[1]} ${match[2]}:00` : null
}

export function emptyTotals(): TrendTotals {
  return {
    requests: 0,
    input_tokens: 0,
    output_tokens: 0,
    cache_creation_tokens: 0,
    cache_read_tokens: 0,
    total_tokens: 0,
    cost: 0,
    actual_cost: 0
  }
}

function accumulate(target: TrendTotals, point: TrendTotals): void {
  target.requests += Number(point.requests) || 0
  target.input_tokens += Number(point.input_tokens) || 0
  target.output_tokens += Number(point.output_tokens) || 0
  target.cache_creation_tokens += Number(point.cache_creation_tokens) || 0
  target.cache_read_tokens += Number(point.cache_read_tokens) || 0
  target.total_tokens += Number(point.total_tokens) || 0
  target.cost += Number(point.cost) || 0
  target.actual_cost += Number(point.actual_cost) || 0
}

/**
 * Zero-filled, chronologically ordered buckets for `range`. Points outside the
 * range are dropped and duplicate keys are summed.
 */
export function fillTrendBuckets(
  points: readonly TrendDataPoint[] | null | undefined,
  range: DateRange,
  now?: Date
): TrendDataPoint[] {
  const buckets = new Map<string, TrendDataPoint>()
  for (const key of bucketKeys(range, now)) buckets.set(key, { date: key, ...emptyTotals() })
  for (const point of points ?? []) {
    const key = normalizeBucketKey(point.date, range.granularity)
    const bucket = key ? buckets.get(key) : undefined
    if (bucket) accumulate(bucket, point)
  }
  return [...buckets.values()]
}

/** Trims the previous period to the buckets elapsed in the current one, so "today" compares equal hours. */
export function alignPrevious(previous: readonly TrendDataPoint[], current: readonly TrendDataPoint[]): TrendDataPoint[] {
  return previous.slice(0, current.length)
}

export function sumTrend(points: readonly TrendDataPoint[]): TrendTotals {
  const totals = emptyTotals()
  for (const point of points) accumulate(totals, point)
  return totals
}

/** `cache_read ÷ (input + cache_creation + cache_read)`; null when nothing was read or written. */
export function cacheHitRate(
  totals: Pick<TrendTotals, 'input_tokens' | 'cache_creation_tokens' | 'cache_read_tokens'>
): number | null {
  const denominator = totals.input_tokens + totals.cache_creation_tokens + totals.cache_read_tokens
  return denominator > 0 ? totals.cache_read_tokens / denominator : null
}

export function metricValue(totals: TrendTotals, metric: OverviewMetric): number | null {
  switch (metric) {
    case 'tokens':
      return totals.total_tokens
    case 'requests':
      return totals.requests
    case 'cost':
      return totals.actual_cost
    case 'cacheHitRate':
      return cacheHitRate(totals)
  }
}

export function metricSeries(points: readonly TrendDataPoint[], metric: OverviewMetric): (number | null)[] {
  return points.map((point) => metricValue(point, metric))
}

/** Relative change; null when either side is unknown or the previous period is zero. */
export function computeDelta(current: number | null, previous: number | null): number | null {
  if (current === null || previous === null || previous === 0) return null
  return (current - previous) / previous
}

/** How a delta badge should read; `new` when growth starts from zero. */
export function deltaKind(current: number | null, previous: number | null, epsilon = 1e-9): DeltaKind {
  if (current === null || previous === null) return 'none'
  if (previous === 0) return current > 0 ? 'new' : 'none'
  const delta = (current - previous) / previous
  if (Math.abs(delta) <= epsilon) return 'flat'
  return delta > 0 ? 'up' : 'down'
}

/** Peak (first highest) and mean over the known values; null when none are known. */
export function peakAndMean(series: readonly (number | null)[]): PeakAndMean | null {
  let peakIndex = -1
  let peak = -Infinity
  let sum = 0
  let count = 0
  series.forEach((value, index) => {
    if (value === null || !Number.isFinite(value)) return
    sum += value
    count++
    if (value > peak) {
      peak = value
      peakIndex = index
    }
  })
  return count ? { peakIndex, peak, mean: sum / count } : null
}
