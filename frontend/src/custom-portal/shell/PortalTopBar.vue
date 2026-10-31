<template>
  <header
    class="glass sticky top-0 z-30 border-b border-gray-200/50 dark:border-dark-700/50"
    :aria-label="pt('shell.topbarLabel')"
  >
    <div class="flex h-16 items-center justify-between gap-2 px-3 sm:px-4 md:px-6">
      <div class="flex min-w-0 items-center gap-3">
        <router-link to="/dashboard" class="flex shrink-0 items-center gap-2" data-testid="portal-brand">
          <span
            class="flex h-8 w-8 items-center justify-center overflow-hidden rounded-lg"
            :class="siteLogo ? 'bg-gray-900 dark:bg-white' : 'pixel-logo'"
          >
            <img :src="siteLogo || PIXEL_LOGO_DATA_URI" :alt="siteName" class="h-full w-full object-contain" />
          </span>
          <span class="hidden text-sm font-semibold text-gray-900 dark:text-white sm:inline">{{ siteName }}</span>
        </router-link>
        <span class="hidden h-4 w-px bg-gray-300 dark:bg-dark-600 sm:block"></span>
        <h1 class="truncate text-sm font-semibold text-gray-700 dark:text-dark-200 sm:text-base" data-testid="portal-page-title">
          {{ pageTitle }}
        </h1>
      </div>

      <div class="flex min-w-0 items-center gap-1 sm:gap-2">
        <PortalDropdown
          v-if="docUrl || docs.length"
          :label="pt('shell.topbar.docs')"
          :trigger-class="topButton"
          test-id="portal-docs-trigger"
        >
          <template #trigger>
            <PixelIcon name="book" size="sm" />
            <span class="hidden sm:inline">{{ pt('shell.topbar.docs') }}</span>
          </template>
          <a v-if="docUrl" :href="docUrl" target="_blank" rel="noopener noreferrer" role="menuitem" class="dropdown-item">
            <PixelIcon name="externalLink" size="sm" />
            {{ pt('shell.links.docs') }}
          </a>
          <router-link v-for="link in docs" :key="link.id" :to="link.path" role="menuitem" class="dropdown-item">
            <PixelIcon :name="link.icon" size="sm" />
            {{ pt(`shell.links.${link.id}`) }}
          </router-link>
        </PortalDropdown>

        <PortalDropdown
          v-if="customPages.length"
          :label="pt('shell.topbar.more')"
          :trigger-class="topButton"
          test-id="portal-more-trigger"
        >
          <template #trigger>
            <PixelIcon name="more" size="sm" />
            <span class="hidden sm:inline">{{ pt('shell.topbar.more') }}</span>
          </template>
          <router-link
            v-for="page in customPages"
            :key="page.id"
            :to="`${CUSTOM_PAGE_PREFIX}${page.id}`"
            role="menuitem"
            class="dropdown-item"
          >
            <span
              v-if="page.icon_svg"
              class="h-4 w-4 shrink-0 [&>svg]:h-full [&>svg]:w-full"
              v-html="sanitizeSvg(page.icon_svg)"
            ></span>
            {{ page.label }}
          </router-link>
        </PortalDropdown>

        <router-link
          v-if="isAdmin"
          :to="ADMIN_HOME_PATH"
          :class="topButton"
          :title="pt('shell.topbar.adminTitle')"
          :aria-label="pt('shell.topbar.adminTitle')"
          data-testid="portal-admin-entry"
        >
          <PixelIcon name="shield" size="sm" />
          <span class="hidden sm:inline">{{ pt('shell.topbar.admin') }}</span>
        </router-link>

        <AnnouncementBell v-if="user" />

        <SubscriptionProgressMini v-if="user && subscriptionFeatureEnabled" />

        <component
          :is="walletPath ? 'router-link' : 'span'"
          v-if="user"
          v-bind="walletPath ? { to: walletPath, title: pt('shell.topbar.balanceTitle') } : {}"
          class="flex items-center gap-1.5 rounded-xl bg-primary-50 px-2 py-1.5 text-sm font-semibold tabular-nums text-primary-700 dark:bg-primary-900/20 dark:text-primary-300 sm:px-3"
          :aria-label="pt('shell.topbar.balance')"
          data-testid="portal-balance"
        >
          <PixelIcon name="dollar" size="sm" class="hidden sm:block" />
          {{ balance }}
          <span
            v-if="frozenBalance > 0"
            class="hidden rounded-full bg-amber-100 px-1.5 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/40 dark:text-amber-200 sm:inline"
            data-testid="portal-frozen-balance"
          >
            {{ pt('shell.topbar.frozen', { amount: formatMoney(frozenBalance) }) }}
          </span>
        </component>

        <PortalAvatarMenu :links="avatar" />
      </div>
    </div>
  </header>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AnnouncementBell from '@/components/common/AnnouncementBell.vue'
import SubscriptionProgressMini from '@/components/common/SubscriptionProgressMini.vue'
import PixelIcon from '../components/common/PixelIcon.vue'
import { PURCHASE_ROUTE_NAME, resolveRouteMetaKeys } from '@/router/title'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { sanitizeSvg } from '@/utils/sanitize'
import { sanitizeUrl } from '@/utils/url'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import { resolveSiteBillingMode } from '@/utils/siteBillingMode'
import { usePortalI18n } from '../i18n'
import { PIXEL_LOGO_DATA_URI } from '../theme/pixelLogo'
import { MODELS_ROUTE_NAME, portalModelsPageActive, portalPagesActive } from '../pageRoutes'
import { CUSTOM_PAGE_PREFIX, type PortalNavLink, type ResolvedDockEntry } from './nav'
import PortalAvatarMenu from './PortalAvatarMenu.vue'
import PortalDropdown from './PortalDropdown.vue'

const props = defineProps<{
  dock: ResolvedDockEntry[]
  docs: PortalNavLink[]
  avatar: PortalNavLink[]
  docUrl: string
  customPages: { id: string; label: string; icon_svg: string }[]
}>()

const { pt } = usePortalI18n()
// Upstream route titles are global keys resolved at runtime, never literals.
const { t: translateGlobal } = useI18n()
const route = useRoute()
const appStore = useAppStore()
const authStore = useAuthStore()

const topButton =
  'flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white'

const ADMIN_HOME_PATH = '/admin/dashboard'

const user = computed(() => authStore.user)
const isAdmin = computed(() => authStore.isAdmin)
const siteName = computed(() => appStore.siteName)
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const formatMoney = (value: number) => `$${value.toFixed(2)}`
const balance = computed(() => formatMoney(Number(user.value?.balance || 0)))
const frozenBalance = computed(() => Number(user.value?.frozen_balance || 0))
const subscriptionFeatureEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.subscription))
const walletPath = computed(() => props.dock.find((entry) => entry.id === 'wallet')?.path ?? null)

const pageTitle = computed(() => {
  if (route.name === 'CustomPage') {
    const page = props.customPages.find((item) => item.id === route.params.id)
    if (page?.label) return page.label
  }
  if (route.name === PURCHASE_ROUTE_NAME && portalPagesActive()) return pt('shell.dock.wallet')
  if (route.name === MODELS_ROUTE_NAME && portalModelsPageActive()) return pt('shell.dock.models')
  const { titleKey } = resolveRouteMetaKeys(route, {
    billingMode: resolveSiteBillingMode(appStore.cachedPublicSettings)
  })
  if (titleKey) return translateGlobal(titleKey)
  return (route.meta.title as string) || pt('shell.pageFallback')
})
</script>

<style scoped>
/* The bottom edge is the frame's three 1px rules; only the bottom slice is drawn. */
header {
  border-bottom-width: 5px;
  border-color: var(--pp-edge);
  border-image: var(--pp-frame) 5 / 0 0 5px 0;
}

/* The balance is a square mint control framed by two rules. */
[data-testid='portal-balance'] {
  border: 3px double currentColor;
}

@media (forced-colors: active) {
  header {
    border-image-source: none;
  }
}

/* 32px pixel logo at 1:1; its stepped corners are part of the art, so the tile has no background. */
.pixel-logo img {
  image-rendering: pixelated;
}

h1 {
  font-family: var(--pp-font-pixel);
  font-size: 12px;
  font-synthesis: none;
}

@media (min-width: 640px) {
  h1 {
    font-size: 24px;
  }
}
</style>
