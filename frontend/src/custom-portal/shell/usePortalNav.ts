import { computed, onMounted } from 'vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { useBatchImageAccess } from '@/composables/useBatchImageAccess'
import { FeatureFlags, isUnifiedModelChannelStatusEnabled, makeSidebarFlag } from '@/utils/featureFlags'
import { sanitizeUrl } from '@/utils/url'
import {
  avatarLinks,
  docsLinks,
  isLinkVisible,
  resolveDockEntries,
  type NavVisibilityContext,
  type SidebarFlagName
} from './nav'

/** Reactive visibility context, using upstream's sidebar flag getters. */
export function usePortalNavContext() {
  const appStore = useAppStore()
  const authStore = useAuthStore()
  const { canUseBatchImage, refreshBatchImageAccess } = useBatchImageAccess()

  const modelPlazaFlag = makeSidebarFlag(FeatureFlags.modelPlaza)

  const flags: Record<SidebarFlagName, () => boolean | undefined> = {
    flagBatchImageAccess: () => canUseBatchImage.value,
    flagAvailableChannels: makeSidebarFlag(FeatureFlags.availableChannels),
    flagChannelMonitor: makeSidebarFlag(FeatureFlags.channelMonitor),
    flagSubscription: makeSidebarFlag(FeatureFlags.subscription),
    flagPayment: makeSidebarFlag(FeatureFlags.payment),
    flagAffiliate: makeSidebarFlag(FeatureFlags.affiliate),
    // The unified Models & Channels page already carries prices, so the standalone pricing tab goes away.
    flagModelPlaza: () => (isUnifiedModelChannelStatusEnabled() ? false : modelPlazaFlag())
  }

  const ctx = computed<NavVisibilityContext>(() => {
    // Touch the reactive sources so the computed tracks flag changes.
    void appStore.cachedPublicSettings
    void canUseBatchImage.value
    return { isSimpleMode: authStore.isSimpleMode, flag: (name) => flags[name]() }
  })

  return { ctx, refreshBatchImageAccess }
}

/** Reactive visibility for the portal shell. */
export function usePortalNav() {
  const appStore = useAppStore()
  const { ctx, refreshBatchImageAccess } = usePortalNavContext()

  const dock = computed(() => resolveDockEntries(ctx.value))
  const docs = computed(() => docsLinks.filter((link) => isLinkVisible(link, ctx.value)))
  const avatar = computed(() => avatarLinks.filter((link) => isLinkVisible(link, ctx.value)))
  const docUrl = computed(() => sanitizeUrl(appStore.docUrl))
  const customPages = computed(() =>
    (appStore.cachedPublicSettings?.custom_menu_items ?? [])
      .filter((item) => item.visibility === 'user')
      .sort((a, b) => a.sort_order - b.sort_order)
  )

  onMounted(() => {
    void refreshBatchImageAccess()
  })

  return { dock, docs, avatar, docUrl, customPages }
}
