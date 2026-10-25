import type { ModelStat } from '@/types'
import type { OverviewMetric } from './usageChartData'

/** Model stats carry no cache breakdown, so cache hit rate ranks by tokens. */
export type RankMetric = 'tokens' | 'requests' | 'cost'

export interface RankItem {
  key: string
  label: string
  value: number
  /** Fraction of the sum over every item, including those folded into "other". */
  share: number
  /** The folded remainder; render its label through i18n. */
  isOther: boolean
}

export const OTHER_RANK_KEY = '__other__'
export const DEFAULT_RANK_LIMIT = 5

type RankSource = Pick<ModelStat, 'requests' | 'total_tokens' | 'actual_cost'>

export function toRankMetric(metric: OverviewMetric | RankMetric): RankMetric {
  return metric === 'cacheHitRate' ? 'tokens' : metric
}

function rankValue(stat: RankSource, metric: RankMetric): number {
  const value = metric === 'tokens' ? stat.total_tokens : metric === 'requests' ? stat.requests : stat.actual_cost
  return Number(value) || 0
}

/** Highest first; the top `limit` stay and the rest merge into one "other" row. */
export function rankItems(items: readonly Omit<RankItem, 'share' | 'isOther'>[], limit = DEFAULT_RANK_LIMIT): RankItem[] {
  const total = items.reduce((sum, item) => sum + item.value, 0)
  const share = (value: number) => (total > 0 ? value / total : 0)
  const sorted = [...items].sort((a, b) => b.value - a.value || a.label.localeCompare(b.label))
  const top = sorted.slice(0, Math.max(0, limit))
  const rest = sorted.slice(top.length)
  const ranked: RankItem[] = top.map((item) => ({ ...item, share: share(item.value), isOther: false }))
  if (rest.length) {
    const value = rest.reduce((sum, item) => sum + item.value, 0)
    ranked.push({ key: OTHER_RANK_KEY, label: OTHER_RANK_KEY, value, share: share(value), isOther: true })
  }
  return ranked
}

export function rankModels(
  stats: readonly ModelStat[],
  metric: OverviewMetric | RankMetric,
  limit = DEFAULT_RANK_LIMIT
): RankItem[] {
  const rankMetric = toRankMetric(metric)
  return rankItems(
    stats.map((stat) => ({ key: stat.model, label: stat.model, value: rankValue(stat, rankMetric) })),
    limit
  )
}
