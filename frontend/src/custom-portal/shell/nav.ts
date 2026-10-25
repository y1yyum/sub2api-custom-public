/**
 * Single source of truth for the user-side portal navigation.
 *
 * Visibility mirrors upstream `AppSidebar.vue` / `AppHeader.vue` exactly: each
 * entry names the sidebar flag getter it inherits (`sidebarFlag`) and whether
 * upstream hides it in simple mode. The menu coverage test parses the upstream
 * sources and fails when an upstream entry is missing here or its rules drift.
 */

export type SidebarFlagName =
  | 'flagBatchImageAccess'
  | 'flagAvailableChannels'
  | 'flagChannelMonitor'
  | 'flagSubscription'
  | 'flagPayment'
  | 'flagAffiliate'
  | 'flagModelPlaza'

export type PortalIconName =
  | 'home'
  | 'key'
  | 'chartBar'
  | 'cube'
  | 'creditCard'
  | 'grid'
  | 'globe'
  | 'bolt'
  | 'gift'
  | 'users'
  | 'document'
  | 'book'
  | 'user'

export interface PortalNavLink {
  id: string
  path: string
  query?: Record<string, string>
  icon: PortalIconName
  hideInSimpleMode?: boolean
  sidebarFlag?: SidebarFlagName
}

export interface PortalDockEntry extends Omit<PortalNavLink, 'path'> {
  /**
   * Direct target. Entries without one open their `children` as a popover;
   * entries with both link directly and use `children` as the page's sections.
   */
  path?: string
  children?: PortalNavLink[]
  /** Extra route prefixes that mark the entry active. */
  activePaths?: string[]
}

export const dockEntries: PortalDockEntry[] = [
  { id: 'overview', path: '/dashboard', icon: 'home' },
  { id: 'keys', path: '/keys', icon: 'key' },
  { id: 'usage', path: '/usage', icon: 'chartBar', hideInSimpleMode: true },
  {
    id: 'models',
    path: '/monitor',
    icon: 'cube',
    // Tabs of the models page; pricing is the model plaza content moved in.
    children: [
      { id: 'channelStatus', path: '/monitor', icon: 'bolt', sidebarFlag: 'flagChannelMonitor' },
      { id: 'pricing', path: '/monitor', query: { view: 'pricing' }, icon: 'grid', sidebarFlag: 'flagModelPlaza' }
    ]
  },
  {
    id: 'wallet',
    path: '/purchase',
    icon: 'creditCard',
    activePaths: ['/payment'],
    // Sections of the wallet page; the old paths redirect to them.
    children: [
      { id: 'purchase', path: '/purchase', icon: 'creditCard', hideInSimpleMode: true, sidebarFlag: 'flagPayment' },
      {
        id: 'subscriptions',
        path: '/subscriptions',
        icon: 'document',
        hideInSimpleMode: true,
        sidebarFlag: 'flagSubscription'
      },
      { id: 'orders', path: '/orders', icon: 'document', hideInSimpleMode: true, sidebarFlag: 'flagPayment' },
      { id: 'redeem', path: '/redeem', icon: 'gift', hideInSimpleMode: true },
      { id: 'affiliate', path: '/affiliate', icon: 'users', hideInSimpleMode: true, sidebarFlag: 'flagAffiliate' }
    ]
  }
]

/** Top-bar "Docs" menu; the external doc_url link is prepended at runtime. */
export const docsLinks: PortalNavLink[] = [
  {
    id: 'batchImage',
    path: '/batch-image',
    icon: 'book',
    hideInSimpleMode: true,
    sidebarFlag: 'flagBatchImageAccess'
  }
]

export const avatarLinks: PortalNavLink[] = [{ id: 'profile', path: '/profile', icon: 'user' }]

/** Custom pages with `visibility === 'user'` populate the top-bar "More" menu. */
export const CUSTOM_PAGE_PREFIX = '/custom/'

/**
 * Upstream entries that intentionally get no portal entry of their own,
 * with the reason recorded for the coverage test.
 */
export const unlistedUpstreamPaths: Record<string, string> = {
  '/available-channels': 'Not offered yet; the upstream page and route stay as they are.',
  '/model-plaza': 'Its content is the pricing tab of /monitor; the page stays as a fallback with no entry.'
}

export interface NavVisibilityContext {
  isSimpleMode: boolean
  flag: (name: SidebarFlagName) => boolean | undefined
}

export function isLinkVisible(link: PortalNavLink | PortalDockEntry, ctx: NavVisibilityContext): boolean {
  if (link.hideInSimpleMode && ctx.isSimpleMode) return false
  if (link.sidebarFlag && ctx.flag(link.sidebarFlag) === false) return false
  return true
}

export interface ResolvedDockEntry extends PortalDockEntry {
  children?: PortalNavLink[]
}

export function resolveDockEntries(ctx: NavVisibilityContext): ResolvedDockEntry[] {
  const out: ResolvedDockEntry[] = []
  for (const entry of dockEntries) {
    if (!isLinkVisible(entry, ctx)) continue
    if (!entry.children) {
      out.push(entry)
      continue
    }
    const children = entry.children.filter((child) => isLinkVisible(child, ctx))
    if (children.length > 0) out.push({ ...entry, children })
  }
  return out
}

export function dockEntryPaths(entry: PortalDockEntry): string[] {
  return [
    ...(entry.path ? [entry.path] : []),
    ...(entry.children ?? []).map((child) => child.path),
    ...(entry.activePaths ?? [])
  ]
}

export function matchesPath(current: string, path: string): boolean {
  return current === path || current.startsWith(`${path}/`)
}

export type PurchaseLabelKey = 'purchase' | 'purchaseRecharge' | 'purchaseSubscribe'

/** The purchase label follows the site billing mode, as in upstream AppSidebar. */
export function purchaseLabelKey(billingMode: string): PurchaseLabelKey {
  if (billingMode === 'recharge_only') return 'purchaseRecharge'
  if (billingMode === 'subscription_only') return 'purchaseSubscribe'
  return 'purchase'
}

/** Visible sections of a page the Dock links to directly. */
export function dockSections(id: string, ctx: NavVisibilityContext): PortalNavLink[] {
  const entry = dockEntries.find((item) => item.id === id)
  if (!entry || !isLinkVisible(entry, ctx)) return []
  return (entry.children ?? []).filter((child) => isLinkVisible(child, ctx))
}

export function walletSections(ctx: NavVisibilityContext): PortalNavLink[] {
  return dockSections('wallet', ctx)
}

/** Every user-side path the portal shell exposes, for the coverage test. */
export function allPortalPaths(): string[] {
  return [
    ...dockEntries.flatMap(dockEntryPaths),
    ...docsLinks.map((link) => link.path),
    ...avatarLinks.map((link) => link.path)
  ]
}
