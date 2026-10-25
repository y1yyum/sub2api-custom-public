import type { LocationQuery, LocationQueryValue } from 'vue-router'

/** Wallet tabs, named after the nav links they replace; subscriptions sit above the tabs. */
export type WalletTab = 'purchase' | 'redeem' | 'affiliate' | 'orders'

export const WALLET_TABS: readonly WalletTab[] = ['purchase', 'redeem', 'affiliate', 'orders']

const SECTION_ALIASES: Record<string, WalletTab> = {
  purchase: 'purchase',
  pay: 'purchase',
  recharge: 'purchase',
  plans: 'purchase',
  redeem: 'redeem',
  affiliate: 'affiliate',
  orders: 'orders',
  records: 'orders'
}

function single(value: LocationQueryValue | LocationQueryValue[] | undefined): string | undefined {
  const first = Array.isArray(value) ? value[0] : value
  return typeof first === 'string' ? first : undefined
}

/**
 * Picks the visible tab a link asks for: `section` first, then the
 * `records=orders` shortcut, then upstream's `tab=` purchase links (renewals
 * from the subscription list); anything hidden or unknown falls back to the
 * first visible tab.
 */
export function resolveWalletTab(query: LocationQuery, visible: readonly WalletTab[]): WalletTab | null {
  const section = single(query.section)
  const requested: (WalletTab | undefined)[] = [
    section ? SECTION_ALIASES[section] : undefined,
    single(query.records) === 'orders' ? 'orders' : undefined,
    single(query.tab) ? 'purchase' : undefined
  ]
  return requested.find((tab): tab is WalletTab => !!tab && visible.includes(tab)) ?? visible[0] ?? null
}

/** `section=plans` opens upstream PaymentView on its subscription tab. */
export function normalizeWalletQuery(query: LocationQuery): LocationQuery | null {
  if (single(query.section) === 'plans' && !single(query.tab)) return { ...query, tab: 'subscription' }
  return null
}

/** Remounts PaymentView when a renewal link changes the plan it preselects on mount. */
export function purchaseKey(query: LocationQuery): string {
  return `${single(query.tab) ?? ''}|${single(query.group) ?? ''}`
}
