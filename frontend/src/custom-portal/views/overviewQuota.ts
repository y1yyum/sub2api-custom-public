import type { UserSubscription } from '@/types'

export type QuotaPeriodKey = 'quotaDaily' | 'quotaWeekly' | 'quotaMonthly'

export interface QuotaPeriod {
  key: QuotaPeriodKey
  used: number
  limit: number
  ratio: number
}

export interface QuotaRow {
  id: number
  name: string
  /** Empty when the group sets no limit at all. */
  periods: QuotaPeriod[]
}

const PERIODS: { key: QuotaPeriodKey; limit: 'daily_limit_usd' | 'weekly_limit_usd' | 'monthly_limit_usd'; used: 'daily_usage_usd' | 'weekly_usage_usd' | 'monthly_usage_usd' }[] = [
  { key: 'quotaDaily', limit: 'daily_limit_usd', used: 'daily_usage_usd' },
  { key: 'quotaWeekly', limit: 'weekly_limit_usd', used: 'weekly_usage_usd' },
  { key: 'quotaMonthly', limit: 'monthly_limit_usd', used: 'monthly_usage_usd' }
]

/** Same limit fields as upstream `SubscriptionProgressMini`; a missing or zero limit means unlimited. */
export function buildQuotaRows(subscriptions: readonly UserSubscription[]): QuotaRow[] {
  return subscriptions.map((sub) => ({
    id: sub.id,
    name: sub.group?.name || `#${sub.group_id}`,
    periods: PERIODS.flatMap((period) => {
      const limit = Number(sub.group?.[period.limit] || 0)
      if (limit <= 0) return []
      const used = Number(sub[period.used] || 0)
      return [{ key: period.key, used, limit, ratio: used / limit }]
    })
  }))
}
