<template>
  <AppLayout>
    <div v-if="hasContent" class="space-y-4" data-testid="portal-wallet">
      <!-- The balance lives in the top bar; this strip carries spending and the redeem field. -->
      <section class="card flex flex-wrap items-center gap-x-8 gap-y-3 px-5 py-4" data-testid="portal-wallet-summary">
        <div>
          <div class="text-xs text-gray-500 dark:text-dark-400">{{ pt('pages.wallet.spent') }}</div>
          <div class="text-xl font-semibold tabular-nums text-gray-900 dark:text-white" data-testid="portal-wallet-spent">
            {{ formatMoney(stats?.total_actual_cost) }}
          </div>
        </div>
        <div>
          <div class="text-xs text-gray-500 dark:text-dark-400">{{ pt('pages.wallet.spentToday') }}</div>
          <div class="text-xl font-semibold tabular-nums text-gray-900 dark:text-white" data-testid="portal-wallet-spent-today">
            {{ formatMoney(stats?.today_actual_cost) }}
          </div>
        </div>
        <div v-if="showRedeem" ref="redeemRef" class="w-full scroll-mt-24 lg:ml-auto lg:w-auto">
          <WalletRedeemForm ref="redeemForm" @redeemed="onRedeemed" />
        </div>
      </section>

      <div class="grid grid-cols-1 items-start gap-4" :class="{ 'xl:grid-cols-2': showPurchase && recordTabs.length }">
        <div v-if="showPurchase || hasSubscriptions" class="min-w-0 space-y-4">
          <section v-if="showPurchase" data-testid="portal-wallet-purchase">
            <PaymentView :key="paymentKey" />
          </section>
          <section
            v-if="hasSubscriptions"
            ref="subscriptionsRef"
            class="scroll-mt-24 space-y-3"
            :aria-label="pt('pages.wallet.subscriptions')"
            data-testid="portal-wallet-subscriptions"
          >
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ pt('pages.wallet.subscriptions') }}</h2>
            <SubscriptionsView />
          </section>
        </div>

        <section
          v-if="recordTabs.length && activeRecordTab"
          ref="recordsRef"
          class="card min-w-0 scroll-mt-24 p-5"
          data-testid="portal-wallet-records"
        >
          <div
            v-if="recordTabs.length > 1"
            class="mb-3 flex flex-wrap gap-1 rounded-xl bg-gray-100 p-1 dark:bg-dark-800"
            role="tablist"
            :aria-label="pt('pages.wallet.recordsLabel')"
          >
            <button
              v-for="tab in recordTabs"
              :id="`wallet-tab-${tab}`"
              :key="tab"
              type="button"
              role="tab"
              class="rounded-lg px-3 py-1.5 text-sm font-medium transition-colors"
              :class="
                tab === activeRecordTab
                  ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
                  : 'text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white'
              "
              :aria-selected="tab === activeRecordTab"
              aria-controls="wallet-records-panel"
              :data-testid="`portal-wallet-tab-${tab}`"
              @click="selectRecordTab(tab)"
            >
              {{ recordTabLabel(tab) }}
            </button>
          </div>
          <h2 v-else class="mb-1 text-base font-semibold text-gray-900 dark:text-white">
            {{ recordTabLabel(activeRecordTab) }}
          </h2>
          <div id="wallet-records-panel" :role="recordTabs.length > 1 ? 'tabpanel' : undefined">
            <WalletRecordList v-if="activeRecordTab === 'history'" ref="historyList" :include-orders="showOrders" />
            <UserOrdersView v-else-if="activeRecordTab === 'orders'" />
            <AffiliateView v-else-if="activeRecordTab === 'affiliate'" />
          </div>
        </section>
      </div>
    </div>

    <div v-else class="card p-10 text-center text-sm text-gray-500 dark:text-dark-400" data-testid="portal-wallet-empty">
      {{ pt('pages.wallet.empty') }}
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { usageAPI, type UserDashboardStats } from '@/api/usage'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useSubscriptionStore } from '@/stores/subscriptions'
import { usePortalI18n } from '../i18n'
import { WALLET_FOCUS_SUBSCRIPTIONS, WALLET_PATH } from '../pageRoutes'
import { walletSections } from '../shell/nav'
import { usePortalNavContext } from '../shell/usePortalNav'
import WalletRecordList from '../components/wallet/WalletRecordList.vue'
import WalletRedeemForm from '../components/wallet/WalletRedeemForm.vue'
import {
  normalizeWalletQuery,
  purchaseKey,
  resolveRecordTab,
  resolveWalletFocus,
  walletRecordTabs,
  type WalletRecordTab
} from './walletSections'

// Upstream pages render inside this page's layout, so their own AppLayout only renders the slot.
const PaymentView = defineAsyncComponent(() => import('@/views/user/PaymentView.vue'))
const AffiliateView = defineAsyncComponent(() => import('@/views/user/AffiliateView.vue'))
const UserOrdersView = defineAsyncComponent(() => import('@/views/user/UserOrdersView.vue'))
const SubscriptionsView = defineAsyncComponent(() => import('@/views/user/SubscriptionsView.vue'))

const { pt } = usePortalI18n()
const route = useRoute()
const router = useRouter()
const subscriptionStore = useSubscriptionStore()
const { ctx } = usePortalNavContext()

const redeemRef = ref<HTMLElement | null>(null)
const redeemForm = ref<InstanceType<typeof WalletRedeemForm> | null>(null)
const subscriptionsRef = ref<HTMLElement | null>(null)
const recordsRef = ref<HTMLElement | null>(null)
const historyList = ref<InstanceType<typeof WalletRecordList> | null>(null)
const stats = ref<UserDashboardStats | null>(null)

const visibleIds = computed(() => walletSections(ctx.value).map((link) => link.id))
const showPurchase = computed(() => visibleIds.value.includes('purchase'))
const showRedeem = computed(() => visibleIds.value.includes('redeem'))
const showOrders = computed(() => visibleIds.value.includes('orders'))
// Plans are bought in PaymentView; the list only appears once the user holds one.
const hasSubscriptions = computed(
  () => visibleIds.value.includes('subscriptions') && subscriptionStore.hasActiveSubscriptions
)
const recordTabs = computed(() => walletRecordTabs(visibleIds.value))
const activeRecordTab = computed(() => resolveRecordTab(route.query, recordTabs.value))
const hasContent = computed(() => visibleIds.value.length > 0)
const paymentKey = computed(() => purchaseKey(route.query))

const formatMoney = (value: number | undefined) => `$${Number(value || 0).toFixed(2)}`

function recordTabLabel(tab: WalletRecordTab): string {
  return tab === 'history' ? pt('pages.wallet.history') : pt(`shell.links.${tab}`)
}

function selectRecordTab(tab: WalletRecordTab) {
  if (tab !== activeRecordTab.value) void router.replace({ path: WALLET_PATH, query: { section: tab } })
}

async function loadStats() {
  try {
    stats.value = await usageAPI.getDashboardStats()
  } catch {
    stats.value = null
  }
}

function onRedeemed() {
  void historyList.value?.refresh()
  void loadStats()
}

async function focusSection() {
  const focus = resolveWalletFocus(route.query, WALLET_FOCUS_SUBSCRIPTIONS)
  if (!focus) return
  await nextTick()
  const target = { redeem: redeemRef, subscriptions: subscriptionsRef, records: recordsRef }[focus].value
  target?.scrollIntoView?.({ block: 'start' })
  if (focus === 'redeem') redeemForm.value?.focus()
}

watch(
  () => route.query,
  (query) => {
    const normalized = normalizeWalletQuery(query)
    if (normalized) void router.replace({ path: WALLET_PATH, query: normalized })
  },
  { immediate: true }
)
watch(() => [route.query.section, route.query.focus, route.query.records], focusSection)
watch(hasSubscriptions, focusSection)
onMounted(() => {
  void loadStats()
  if (visibleIds.value.includes('subscriptions')) void subscriptionStore.fetchActiveSubscriptions().catch(() => {})
  void focusSection()
})
</script>
