import type { CatalogGroup, CatalogModelRow } from '@/api/channelStatusCatalog'
import { sortGroups } from './catalogFormat'

const severity = { critical: 0, warning: 1, healthy: 2, unknown: 3 }

export function worstModel(rows: CatalogModelRow[]): CatalogModelRow | undefined {
  return [...rows].sort(
    (a, b) =>
      severity[a.health] - severity[b.health] || (a.success_rate ?? 1) - (b.success_rate ?? 1)
  )[0]
}

/** Mean cache hit rate of the row's buckets that have data; null when none do. */
export function rowCacheRate(row: CatalogModelRow): number | null {
  const rates = row.buckets.flatMap((bucket) =>
    bucket.health === 'unknown' || bucket.cache_rate == null ? [] : [bucket.cache_rate]
  )
  return rates.length ? rates.reduce((sum, value) => sum + value, 0) / rates.length : null
}

export function platformCards(groups: CatalogGroup[]) {
  const partitions = new Map<string, CatalogGroup[]>()
  for (const group of sortGroups(groups)) {
    const items = partitions.get(group.platform) ?? []
    items.push(group)
    partitions.set(group.platform, items)
  }
  return [...partitions].map(([platform, items]) => {
    const rows = items.flatMap((group) => group.models)
    const rates = rows.flatMap((row) =>
      row.health === 'unknown' || row.success_rate == null ? [] : [row.success_rate]
    )
    return {
      platform,
      groups: items,
      rows,
      rate: rates.length ? rates.reduce((sum, rate) => sum + rate, 0) / rates.length : null,
      critical: rows.filter((row) => row.health === 'critical').length,
      warning: rows.filter((row) => row.health === 'warning').length,
      hasData: rows.some((row) => row.health !== 'unknown')
    }
  })
}
