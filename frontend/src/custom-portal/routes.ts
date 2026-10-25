import type { Router, RouteRecordNormalized, RouteRecordRaw } from 'vue-router'

// Titles stay static: upstream `resolveDocumentTitle` only translates
// `titleKey` against the global i18n scope, while portal copy is local-scoped.
export const customPortalRoutes: RouteRecordRaw[] = [
  {
    path: '/v2',
    name: 'NewHome',
    component: () => import('./views/NewHomeView.vue'),
    meta: {
      requiresAuth: false,
      title: 'Sub2API Gateway v2'
    }
  },
  {
    path: '/v2/dashboard',
    name: 'NewDashboard',
    component: () => import('./views/NewDashboardView.vue'),
    meta: {
      requiresAuth: true,
      title: 'Console v2'
    }
  }
]

export const HOME_ROUTE_NAME = 'Home'
export const HOME_ROUTE_PATH = '/home'
export const portalHomeComponent = () => import('./views/RhineHomeView.vue')

function findConflict(router: Router, route: RouteRecordRaw): string | null {
  if (route.name && router.hasRoute(route.name)) {
    return `name "${String(route.name)}"`
  }
  if (router.getRoutes().some((existing) => existing.path === route.path)) {
    return `path "${route.path}"`
  }
  return null
}

/**
 * Adds portal routes, skipping any whose name or path is already taken so an
 * upstream route introduced later is never shadowed or overwritten.
 */
export function registerCustomPortalRoutes(router: Router): void {
  for (const route of customPortalRoutes) {
    const conflict = findConflict(router, route)
    if (conflict) {
      console.warn(`[custom-portal] Route ${conflict} already registered; skipping portal route.`)
      continue
    }
    router.addRoute(route)
  }
}

/** Only name, path, meta and beforeEnter are carried over, so anything else must be absent. */
export function isPlainRecord(record: RouteRecordNormalized, routes: RouteRecordNormalized[]): boolean {
  const viewNames = Object.keys(record.components ?? {})
  return (
    record.children.length === 0 &&
    !record.redirect &&
    viewNames.length === 1 &&
    viewNames[0] === 'default' &&
    !Object.values(record.props).some(Boolean) &&
    !routes.some((route) => route.aliasOf === record)
  )
}

/**
 * The one intentional override: swaps the component of upstream's `Home`
 * route for the portal homepage while keeping its name, path and meta, so the
 * `/` redirect and navigation guards behave as before. It only proceeds when
 * upstream still owns `Home` at `/home` and no other route shares that path;
 * otherwise it warns and leaves the router untouched.
 */
export function replaceHomeRoute(router: Router): boolean {
  const routes = router.getRoutes()
  const home = routes.find((route) => route.name === HOME_ROUTE_NAME)
  const pathOwners = routes.filter((route) => route.path === HOME_ROUTE_PATH)
  if (!home || home.path !== HOME_ROUTE_PATH || pathOwners.length !== 1) {
    console.warn(
      `[custom-portal] Upstream route "${HOME_ROUTE_NAME}" is not the sole owner of "${HOME_ROUTE_PATH}"; keeping upstream home.`
    )
    return false
  }
  if (!isPlainRecord(home, routes)) {
    console.warn(
      `[custom-portal] Upstream route "${HOME_ROUTE_NAME}" gained children, aliases, props or named views; keeping upstream home.`
    )
    return false
  }
  router.removeRoute(HOME_ROUTE_NAME)
  router.addRoute({
    path: HOME_ROUTE_PATH,
    name: HOME_ROUTE_NAME,
    component: portalHomeComponent,
    meta: { ...home.meta },
    beforeEnter: home.beforeEnter
  })
  return true
}
