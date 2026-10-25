import type { LocationQuery, LocationQueryValue } from 'vue-router'

/** Tabs of the wallet records card; purchase, subscriptions and redeem sit on the page itself. */
export type WalletRecordTab = 'history' | 'orders' | 'affiliate'

/** Where a wallet link lands: the redeem field, the subscription list or the records card. */
export type WalletFocus = 'redeem' | 'subscriptions' | 'records'

function single(value: LocationQueryValue | LocationQueryValue[] | undefined): string | undefined {
  const first = Array.isArray(value) ? value[0] : value
  return typeof first === 'string' ? first : undefined
}

/**
 * Records tabs for the visible wallet sections (ids from `walletSections`):
 * the history merges redeem codes and paid orders, the order list keeps
 * cancel and refund actions, invites follow the affiliate switch.
 */
export function walletRecordTabs(visibleIds: readonly string[]): WalletRecordTab[] {
  const tabs: WalletRecordTab[] = []
  if (visibleIds.includes('redeem') || visibleIds.includes('orders')) tabs.push('history')
  if (visibleIds.includes('orders')) tabs.push('orders')
  if (visibleIds.includes('affiliate')) tabs.push('affiliate')
  return tabs
}

const RECORD_SECTIONS: Record<string, WalletRecordTab> = {
  history: 'history',
  orders: 'orders',
  records: 'orders',
  affiliate: 'affiliate'
}

/** Picks the records tab a link asks for (`section`, then `records=orders`), else the first visible one. */
export function resolveRecordTab(query: LocationQuery, visible: readonly WalletRecordTab[]): WalletRecordTab | null {
  const section = single(query.section)
  const requested: (WalletRecordTab | undefined)[] = [
    section ? RECORD_SECTIONS[section] : undefined,
    single(query.records) === 'orders' ? 'orders' : undefined
  ]
  return requested.find((tab): tab is WalletRecordTab => !!tab && visible.includes(tab)) ?? visible[0] ?? null
}

/** The part of the page an old link (`/redeem`, `/orders`, `/subscriptions`, `/affiliate`) should scroll to. */
export function resolveWalletFocus(query: LocationQuery, subscriptionsFocus: string): WalletFocus | null {
  const section = single(query.section)
  if (section === 'redeem') return 'redeem'
  if (single(query.focus) === subscriptionsFocus) return 'subscriptions'
  if ((section && RECORD_SECTIONS[section]) || single(query.records) === 'orders') return 'records'
  return null
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
