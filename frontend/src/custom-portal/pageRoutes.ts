import type { LocationQuery, RouteLocationGeneric, RouteMeta, Router, RouteRecordNormalized, RouteRecordRaw } from 'vue-router'
import { isPlainRecord } from './routes'

export const WALLET_PATH = '/purchase'
export const WALLET_FOCUS_SUBSCRIPTIONS = 'subscriptions'
export const MODELS_PATH = '/monitor'
export const MODELS_ROUTE_NAME = 'ChannelStatus'

let pagesActive = false
let modelsPageActive = false

/**
 * True once the portal overview and wallet replaced the upstream pages. Upstream
 * `UsageView` reads it to drop the charts that moved to the overview, so it stays
 * false in upstream tests and whenever the upstream shell is forced.
 */
export function portalPagesActive(): boolean {
  return pagesActive
}

/** True once the portal models page replaced upstream channel status. */
export function portalModelsPageActive(): boolean {
  return modelsPageActive
}

export function resetPortalPagesForTest(): void {
  pagesActive = false
  modelsPageActive = false
}

type Build = (record: RouteRecordNormalized) => RouteRecordRaw

interface PageReplacement {
  name: string
  path: string
  build: Build
}

function withoutPaymentGate(meta: RouteMeta): RouteMeta {
  const { requiresPayment: _requiresPayment, ...rest } = meta
  return rest
}

function redirectToWallet(extra: (query: LocationQuery) => LocationQuery): Build {
  return (record) => ({
    path: record.path,
    name: record.name ?? undefined,
    redirect: (to: RouteLocationGeneric) => ({ path: WALLET_PATH, query: extra(to.query), hash: to.hash })
  })
}

const sectionRedirect = (section: string) => redirectToWallet((query) => ({ ...query, section }))

export const pageReplacements: PageReplacement[] = [
  {
    name: 'Dashboard',
    path: '/dashboard',
    build: (record) => ({
      path: record.path,
      name: 'Dashboard',
      component: () => import('./views/PortalOverviewView.vue'),
      meta: { ...record.meta },
      beforeEnter: record.beforeEnter
    })
  },
  {
    name: 'PurchaseSubscription',
    path: WALLET_PATH,
    // The wallet also hosts redeem codes and invites, so the payment switch only
    // hides its purchase tab instead of closing the whole page.
    build: (record) => ({
      path: record.path,
      name: 'PurchaseSubscription',
      component: () => import('./views/PortalWalletView.vue'),
      meta: withoutPaymentGate(record.meta),
      beforeEnter: record.beforeEnter
    })
  },
  {
    name: 'Subscriptions',
    path: '/subscriptions',
    build: redirectToWallet((query) => ({ ...query, focus: WALLET_FOCUS_SUBSCRIPTIONS }))
  },
  { name: 'OrderList', path: '/orders', build: redirectToWallet((query) => ({ ...query, records: 'orders' })) },
  { name: 'Redeem', path: '/redeem', build: sectionRedirect('redeem') },
  { name: 'Affiliate', path: '/affiliate', build: sectionRedirect('affiliate') }
]

/**
 * The models page keeps upstream channel status as its status tab and adds the
 * model plaza pricing; it is swapped on its own so an upstream change to
 * `/monitor` never takes the overview and wallet down with it.
 */
export const modelsPageReplacements: PageReplacement[] = [
  {
    name: MODELS_ROUTE_NAME,
    path: MODELS_PATH,
    build: (record) => ({
      path: record.path,
      name: MODELS_ROUTE_NAME,
      component: () => import('./views/PortalModelsView.vue'),
      meta: { ...record.meta },
      beforeEnter: record.beforeEnter
    })
  }
]

function blocker(router: Router, replacement: PageReplacement): string | null {
  const routes = router.getRoutes()
  const record = routes.find((route) => route.name === replacement.name)
  if (!record || record.path !== replacement.path) return `"${replacement.name}" no longer owns "${replacement.path}"`
  if (routes.filter((route) => route.path === replacement.path).length !== 1) {
    return `"${replacement.path}" has more than one owner`
  }
  if (record.beforeEnter && !replacement.build(record).component) {
    return `"${replacement.name}" gained a beforeEnter guard a redirect would drop`
  }
  if (!isPlainRecord(record, routes)) return `"${replacement.name}" gained children, aliases, props or named views`
  return null
}

function replaceAll(router: Router, replacements: PageReplacement[], label: string): boolean {
  const problems = replacements.map((item) => blocker(router, item)).filter(Boolean)
  if (problems.length > 0) {
    console.warn(`[custom-portal] Keeping upstream ${label}: ${problems.join('; ')}.`)
    return false
  }
  for (const item of replacements) {
    const record = router.getRoutes().find((route) => route.name === item.name)!
    const replacement = item.build(record)
    router.removeRoute(item.name)
    router.addRoute(replacement)
  }
  return true
}

/**
 * Swaps the upstream overview and wallet pages in one step: either every
 * upstream record still has the expected shape and all are replaced, or none
 * is, so the pages never end up half portal and half upstream.
 */
export function replacePortalPages(router: Router): boolean {
  pagesActive = replaceAll(router, pageReplacements, 'user pages')
  return pagesActive
}

export function replacePortalModelsPage(router: Router): boolean {
  modelsPageActive = replaceAll(router, modelsPageReplacements, 'channel status page')
  return modelsPageActive
}
