import { formatScaled } from '@/utils/pricing'
import type {
  CatalogBucket,
  CatalogGroup,
  CatalogHealth,
  CatalogModelRow,
  CatalogPaidPrices,
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

type Translate = (key: string, named?: Record<string, unknown>) => string

/** Tooltip / accessible name of one timeline block; grey blocks say why they are grey. */
export function bucketLabel(bucket: CatalogBucket, t: Translate, locale: string): string {
  const time = new Date(bucket.start).toLocaleString(locale, {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
  if (bucket.health === 'unknown') {
    const status = bucket.reason === 'low_sample' ? 'modelChannelStatus.row.blockLowSample' : 'modelChannelStatus.row.blockNoData'
    return t('modelChannelStatus.row.blockUnknown', { time, status: t(status) })
  }
  return t('modelChannelStatus.row.blockLabel', {
    time,
    status: t(`modelChannelStatus.health.${bucket.health}`),
    rate: formatPercent(bucket.success_rate),
  })
}

export interface InlinePrice {
  /** `unset` when no price is configured; never rendered as zero. */
  kind: 'unset' | 'token' | 'unit'
  text: string
  conflict: boolean
}

/** Row price cell: `$2.4 / 1M` for token models, a per-unit price otherwise. */
export function inlinePrice(row: CatalogModelRow): InlinePrice {
  if (row.billing_mode === 'token') {
    if (row.input_price == null) return { kind: 'unset', text: '', conflict: false }
    return { kind: 'token', text: formatScaled(row.input_price, PER_MILLION, MIN_DECIMALS), conflict: row.price_conflict }
  }
  if (row.per_request_price == null) return { kind: 'unset', text: '', conflict: false }
  return { kind: 'unit', text: formatScaled(row.per_request_price, 1, MIN_DECIMALS), conflict: row.price_conflict }
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
