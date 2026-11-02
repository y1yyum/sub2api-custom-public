import { formatScaled } from '@/utils/pricing'
import type {
  CatalogBucket,
  CatalogCacheLevel,
  CatalogGroup,
  CatalogHealth,
  CatalogModelRow,
  CatalogPaidPrices,
  CatalogStripConfig,
} from '@/api/channelStatusCatalog'

const PER_MILLION = 1_000_000
const MIN_DECIMALS = 2

/** Lower rank = worse. `unknown` sorts after known problems but before healthy. */
const HEALTH_RANK: Record<CatalogHealth, number> = {
  critical: 0,
  warning: 1,
  unknown: 2,
  healthy: 3,
}

export function healthRank(health: CatalogHealth): number {
  return HEALTH_RANK[health] ?? HEALTH_RANK.unknown
}

export interface SelectedCatalogRow {
  row: CatalogModelRow
  group: CatalogGroup
}

export type StatusFilter = 'all' | 'issues'

export interface CatalogFilters {
  platforms: string[]
  status: StatusFilter
  search: string
}

export const EMPTY_FILTERS: CatalogFilters = { platforms: [], status: 'all', search: '' }

export function isIssue(health: CatalogHealth): boolean {
  return health === 'warning' || health === 'critical'
}

export function effectiveGroupRate(group: Pick<CatalogGroup, 'rate_multiplier' | 'user_rate_multiplier'>): number {
  return group.user_rate_multiplier ?? group.rate_multiplier
}

/** Cheapest effective rate first, then name; mirrors the model plaza order. */
export function sortGroups(groups: CatalogGroup[]): CatalogGroup[] {
  return [...groups].sort((a, b) => {
    const diff = effectiveGroupRate(a) - effectiveGroupRate(b)
    if (diff !== 0) return diff
    return a.name.localeCompare(b.name)
  })
}

/** Drops rows that do not match; groups without matching rows disappear. */
export function filterGroups(groups: CatalogGroup[], filters: CatalogFilters): CatalogGroup[] {
  const needle = filters.search.trim().toLowerCase()
  const platforms = new Set(filters.platforms)
  const out: CatalogGroup[] = []
  for (const group of groups) {
    const models = group.models.filter((row) => {
      if (platforms.size > 0 && !platforms.has(row.platform)) return false
      if (filters.status === 'issues' && !isIssue(row.health)) return false
      if (needle && !row.name.toLowerCase().includes(needle)) return false
      return true
    })
    if (models.length > 0) out.push({ ...group, models })
  }
  return out
}

/**
 * Default open groups: up to three groups all open; otherwise those with a
 * degraded/down model; if everything is healthy, the first group.
 */
export function defaultExpandedIds(groups: CatalogGroup[]): number[] {
  if (groups.length === 0) return []
  if (groups.length <= 3) return groups.map((g) => g.id)
  const withIssues = groups.filter((g) => g.summary.degraded_count + g.summary.down_count > 0)
  return (withIssues.length > 0 ? withIssues : groups.slice(0, 1)).map((g) => g.id)
}

/** Plain multiplier number (no sign); locale strings append the `×`. */
export function formatMultiplier(rate: number): string {
  return String(Number(rate.toFixed(3)))
}

export function formatPercent(rate: number | null | undefined): string {
  if (rate == null || Number.isNaN(rate)) return '-'
  return `${(Math.round(rate * 1000) / 10).toFixed(1)}%`
}

/** Latency for display: `640 ms`, `1.25 s`; missing values render as `-`. */
export function formatLatency(ms: number | null | undefined): string {
  if (ms == null || Number.isNaN(ms)) return '-'
  if (ms < 1000) return `${Math.round(ms)} ms`
  return `${Number((ms / 1000).toFixed(2))} s`
}

/** Configured step as a short percentage: 0.95 → `95%`. */
export function formatStep(step: number): string {
  return `${Number((step * 100).toFixed(1))}%`
}

type Translate = (key: string, named?: Record<string, unknown>) => string

export function isCallsStrip(strip: CatalogStripConfig | null | undefined): strip is CatalogStripConfig {
  return strip?.mode === 'calls'
}

/** Calls covered by block `index` (oldest block first), counted back from the newest call. */
export function callBlockRange(index: number, blocks: number, window: number): { from: number; to: number } {
  const width = window / blocks
  const back = blocks - 1 - index
  return { from: Math.round(back * width) + 1, to: Math.round((back + 1) * width) }
}

/** Where a block sits: its local start time, or its call range in calls mode. */
function blockPosition(bucket: CatalogBucket, index: number, count: number, strip: CatalogStripConfig | null | undefined, t: Translate, locale: string): string {
  if (isCallsStrip(strip)) return t('modelChannelStatus.row.blockCalls', callBlockRange(index, count, strip.call_window))
  if (!bucket.start) return ''
  return new Date(bucket.start).toLocaleString(locale, {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

/** Tooltip / accessible name of one block; grey blocks say why they are grey. */
export function bucketLabel(
  bucket: CatalogBucket,
  t: Translate,
  locale: string,
  position: { index: number; count: number; strip?: CatalogStripConfig | null } = { index: 0, count: 1 },
): string {
  const time = blockPosition(bucket, position.index, position.count, position.strip, t, locale)
  if (bucket.health === 'unknown') {
    let status = 'modelChannelStatus.row.blockNoData'
    if (bucket.reason === 'low_sample') status = 'modelChannelStatus.row.blockLowSample'
    else if (isCallsStrip(position.strip)) status = 'modelChannelStatus.row.blockNoCalls'
    return t('modelChannelStatus.row.blockUnknown', { time, status: t(status) })
  }
  const label = t('modelChannelStatus.row.blockLabel', {
    time,
    status: t(`modelChannelStatus.health.${bucket.health}`),
    rate: formatPercent(bucket.success_rate),
  })
  const cache = bucket.cache_level ? t('modelChannelStatus.row.blockCache', { rate: formatPercent(bucket.cache_rate ?? 0) }) : ''
  const ttft = bucket.ttft_ms == null ? '' : t('modelChannelStatus.row.blockTTFT', { value: formatLatency(bucket.ttft_ms) })
  return label + cache + ttft
}

const CACHE_HEIGHT: Record<CatalogCacheLevel, string> = {
  high: 'h-full',
  mid: 'h-[70%]',
  low: 'h-[45%]',
  none: 'h-[20%]',
}

/** Block height class per cache level; rows not billed per token stay full height. */
export function cacheHeightClass(level: CatalogCacheLevel | undefined): string {
  return level ? CACHE_HEIGHT[level] : CACHE_HEIGHT.high
}

export interface InlinePrice {
  /** `unset` when no price is configured; never rendered as zero. */
  kind: 'unset' | 'token' | 'unit'
  text: string
  conflict: boolean
}

/** Row price cell: `$2.50 / $10.00` (input / output per 1M) for token models, a per-unit price otherwise. */
export function inlinePrice(row: CatalogModelRow): InlinePrice {
  if (row.billing_mode === 'token') {
    if (row.input_price == null && row.output_price == null) return { kind: 'unset', text: '', conflict: false }
    return { kind: 'token', text: `${perMillion(row.input_price)} / ${perMillion(row.output_price)}`, conflict: row.price_conflict }
  }
  if (row.per_request_price == null) return { kind: 'unset', text: '', conflict: false }
  return { kind: 'unit', text: formatScaled(row.per_request_price, 1, MIN_DECIMALS), conflict: row.price_conflict }
}

/** True when no bucket saw any traffic, so the strip would be all grey. */
export function isNoDataStrip(buckets: CatalogBucket[]): boolean {
  return buckets.every((bucket) => bucket.health === 'unknown' && bucket.reason !== 'low_sample')
}

const pad2 = (n: number) => String(n).padStart(2, '0')

function localDay(date: Date): string {
  return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}`
}

/** `HH:mm`, or `MM-DD HH:mm` when `withDate`; local time. */
export function formatCheckTime(iso: string, withDate: boolean): string {
  const date = new Date(iso)
  const clock = `${pad2(date.getHours())}:${pad2(date.getMinutes())}`
  return withDate ? `${pad2(date.getMonth() + 1)}-${pad2(date.getDate())} ${clock}` : clock
}

/** Whether any timestamp falls on a local day other than `now`'s, so times need a date. */
export function spansDays(isoTimes: string[], now: Date = new Date()): boolean {
  const today = localDay(now)
  return isoTimes.some((iso) => localDay(new Date(iso)) !== today)
}

export function perMillion(value: number | null | undefined): string {
  if (value == null) return '-'
  return formatScaled(value, PER_MILLION, MIN_DECIMALS)
}

export function perUnit(value: number | null | undefined): string {
  if (value == null) return '-'
  return formatScaled(value, 1, MIN_DECIMALS)
}

export function hasCachePrices(paid: CatalogPaidPrices): boolean {
  return paid.cache_read != null || paid.cache_write != null || paid.cache_write_1h != null
}

export function formatTokens(n: number): string {
  if (n >= 1_000_000 && n % 1_000_000 === 0) return `${n / 1_000_000}M`
  if (n >= 1000 && n % 1000 === 0) return `${n / 1000}K`
  return String(n)
}

export function formatDuration(seconds: number): string {
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  const rest = minutes % 60
  return rest === 0 ? `${hours}h` : `${hours}h ${rest}m`
}

/** Block background class per health; unknown stays neutral gray. */
export function healthBlockClass(health: CatalogHealth): string {
  switch (health) {
    case 'healthy':
      return 'bg-emerald-500 dark:bg-emerald-400'
    case 'warning':
      return 'bg-amber-400 dark:bg-amber-300'
    case 'critical':
      return 'bg-red-500 dark:bg-red-400'
    default:
      return 'bg-gray-200 dark:bg-dark-600'
  }
}

export function healthTextClass(health: CatalogHealth): string {
  switch (health) {
    case 'healthy':
      return 'text-emerald-600 dark:text-emerald-400'
    case 'warning':
      return 'text-amber-600 dark:text-amber-400'
    case 'critical':
      return 'text-red-600 dark:text-red-400'
    default:
      return 'text-gray-500 dark:text-dark-400'
  }
}
