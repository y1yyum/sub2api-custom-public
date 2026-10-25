<template>
  <AppLayout>
    <div class="space-y-6" data-testid="portal-wallet">
      <section class="card grid grid-cols-1 gap-4 p-5 sm:grid-cols-3">
        <div>
          <div class="text-xs text-gray-500 dark:text-dark-400">{{ pt('pages.wallet.available') }}</div>
          <div class="mt-1 text-2xl font-semibold text-gray-900 dark:text-white" data-testid="portal-wallet-available">
            {{ formatMoney(available) }}
          </div>
        </div>
        <div v-if="frozen > 0">
          <div class="text-xs text-gray-500 dark:text-dark-400">{{ pt('pages.wallet.frozen') }}</div>
          <div class="mt-1 text-2xl font-semibold text-amber-700 dark:text-amber-300">{{ formatMoney(frozen) }}</div>
        </div>
        <div v-if="frozen > 0">
          <div class="text-xs text-gray-500 dark:text-dark-400">{{ pt('pages.wallet.total') }}</div>
          <div class="mt-1 text-2xl font-semibold text-gray-900 dark:text-white">{{ formatMoney(available + frozen) }}</div>
        </div>
      </section>

      <section
        v-if="showSubscriptions"
        id="wallet-subscriptions"
        ref="subscriptionsRef"
        class="scroll-mt-24 space-y-3"
        :aria-label="pt('pages.wallet.subscriptions')"
        data-testid="portal-wallet-subscriptions"
      >
        <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ pt('pages.wallet.subscriptions') }}</h2>
        <SubscriptionsView />
      </section>

      <section v-if="tabs.length" class="space-y-4">
        <div
          class="flex flex-wrap gap-1 rounded-xl bg-gray-100 p-1 dark:bg-dark-800"
          role="tablist"
          :aria-label="pt('pages.wallet.sectionsLabel')"
        >
          <button
            v-for="tab in tabs"
            :id="`wallet-tab-${tab}`"
            :key="tab"
            type="button"
            role="tab"
            class="rounded-lg px-4 py-2 text-sm font-medium transition-colors"
            :class="
              tab === activeTab
                ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
                : 'text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white'
            "
            :aria-selected="tab === activeTab"
            aria-controls="wallet-panel"
            :data-testid="`portal-wallet-tab-${tab}`"
            @click="selectTab(tab)"
          >
            {{ tabLabel(tab) }}
          </button>
        </div>
        <div id="wallet-panel" role="tabpanel" :aria-labelledby="`wallet-tab-${activeTab}`">
          <PaymentView v-if="activeTab === 'purchase'" :key="paymentKey" />
          <RedeemView v-else-if="activeTab === 'redeem'" />
          <AffiliateView v-else-if="activeTab === 'affiliate'" />
          <UserOrdersView v-else-if="activeTab === 'orders'" />
        </div>
      </section>

      <div
        v-if="!tabs.length && !showSubscriptions"
        class="card p-10 text-center text-sm text-gray-500 dark:text-dark-400"
        data-testid="portal-wallet-empty"
      >
        {{ pt('pages.wallet.empty') }}
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { resolveSiteBillingMode } from '@/utils/siteBillingMode'
import { usePortalI18n } from '../i18n'
import { WALLET_FOCUS_SUBSCRIPTIONS, WALLET_PATH } from '../pageRoutes'
import { purchaseLabelKey, walletSections } from '../shell/nav'
import { usePortalNavContext } from '../shell/usePortalNav'
import { normalizeWalletQuery, purchaseKey, resolveWalletTab, WALLET_TABS, type WalletTab } from './walletSections'

// Upstream pages render inside this page's layout, so their own AppLayout only renders the slot.
const PaymentView = defineAsyncComponent(() => import('@/views/user/PaymentView.vue'))
const RedeemView = defineAsyncComponent(() => import('@/views/user/RedeemView.vue'))
const AffiliateView = defineAsyncComponent(() => import('@/views/user/AffiliateView.vue'))
const UserOrdersView = defineAsyncComponent(() => import('@/views/user/UserOrdersView.vue'))
const SubscriptionsView = defineAsyncComponent(() => import('@/views/user/SubscriptionsView.vue'))

const { pt } = usePortalI18n()
const route = useRoute()
const router = useRouter()
const appStore = useAppStore()
const authStore = useAuthStore()
const { ctx } = usePortalNavContext()
const subscriptionsRef = ref<HTMLElement | null>(null)

const visibleIds = computed(() => walletSections(ctx.value).map((link) => link.id))
const tabs = computed(() => WALLET_TABS.filter((tab) => visibleIds.value.includes(tab)))
const showSubscriptions = computed(() => visibleIds.value.includes('subscriptions'))
const activeTab = computed(() => resolveWalletTab(route.query, tabs.value))
const paymentKey = computed(() => purchaseKey(route.query))

const available = computed(() => Number(authStore.user?.balance || 0))
const frozen = computed(() => Number(authStore.user?.frozen_balance || 0))
const formatMoney = (value: number) => `$${value.toFixed(2)}`

function tabLabel(tab: WalletTab): string {
  if (tab !== 'purchase') return pt(`shell.links.${tab}`)
  return pt(`shell.links.${purchaseLabelKey(resolveSiteBillingMode(appStore.cachedPublicSettings))}`)
}

function selectTab(tab: WalletTab) {
  if (tab !== activeTab.value) void router.replace({ path: WALLET_PATH, query: { section: tab } })
}

async function focusSubscriptions() {
  if (route.query.focus !== WALLET_FOCUS_SUBSCRIPTIONS) return
  await nextTick()
  subscriptionsRef.value?.scrollIntoView?.({ block: 'start' })
}

watch(
  () => route.query,
  (query) => {
    const normalized = normalizeWalletQuery(query)
    if (normalized) void router.replace({ path: WALLET_PATH, query: normalized })
  },
  { immediate: true }
)
watch(() => route.query.focus, focusSubscriptions)
onMounted(focusSubscriptions)
</script>
