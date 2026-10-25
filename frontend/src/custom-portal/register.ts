import router from '@/router'
import { replacePortalModelsPage, replacePortalPages } from './pageRoutes'
import { registerCustomPortalRoutes, replaceHomeRoute } from './routes'
import { readShellOverride } from './shell/shellKind'
import { loadRhineChartTheme } from './theme/chartTheme'
import { installRhineTheme } from './theme/install'
import rhineThemeCss from './theme/rhine-theme.css?inline'

registerCustomPortalRoutes(router)
replaceHomeRoute(router)
// The upstream-shell switch also restores the upstream user pages.
if (readShellOverride(window.localStorage) !== 'upstream') {
  replacePortalPages(router)
  replacePortalModelsPage(router)
}
installRhineTheme(document, rhineThemeCss)

// Charts only live behind login (upstream treats a missing requiresAuth as true),
// so public pages never download chart.js for the theme.
const stopChartThemeTrigger = router.beforeEach((to) => {
  if (to.meta.requiresAuth === false) return
  stopChartThemeTrigger()
  void loadRhineChartTheme(document)
})
