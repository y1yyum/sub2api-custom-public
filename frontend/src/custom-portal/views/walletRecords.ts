import type { RedeemHistoryItem } from '@/api/redeem'
import type { PaymentOrder } from '@/types/payment'

export type WalletRecord =
  | { key: string; at: string; source: 'order'; order: PaymentOrder }
  | { key: string; at: string; source: 'redeem'; redeem: RedeemHistoryItem }

const timeOf = (value: string) => {
  const time = Date.parse(value)
  return Number.isNaN(time) ? 0 : time
}

/** Paid orders and redeemed codes as one recharge history, newest first. */
export function mergeWalletRecords(
  orders: readonly PaymentOrder[],
  redeems: readonly RedeemHistoryItem[],
  limit: number
): WalletRecord[] {
  const records: WalletRecord[] = [
    ...orders.map((order) => ({
      key: `order-${order.id}`,
      at: order.paid_at || order.created_at,
      source: 'order' as const,
      order
    })),
    ...redeems.map((redeem) => ({
      key: `redeem-${redeem.id}`,
      at: redeem.used_at || redeem.created_at,
      source: 'redeem' as const,
      redeem
    }))
  ]
  return records.sort((a, b) => timeOf(b.at) - timeOf(a.at)).slice(0, limit)
}
