<template>
  <AppLayout>
    <div v-if="hasContent" class="space-y-4" data-testid="portal-wallet">
      <PortalPageHeading :title="pt('refresh.wallet')" :description="pt('refresh.description.wallet')">
        <div v-if="showPurchase" id="wallet-purchase-tabs" />
        <button v-if="showOrders" type="button" class="btn btn-secondary" @click="selectRecordTab('orders')">{{ pt('shell.links.orders') }}</button>
      </PortalPageHeading>
      <section class="wallet-summary card" data-testid="portal-wallet-summary">
        <div class="wallet-balance-cell">
          <div class="text-xs text-gray-500 dark:text-dark-400">{{ pt('refresh.balance') }}</div>
          <div class="wallet-balance mt-2" data-testid="portal-wallet-balance">{{ formatMoney(authStore.user?.balance) }}</div>
          <p class="mt-2 text-xs text-gray-500 dark:text-dark-400">{{ pt('refresh.frozen') }} {{ formatMoney(authStore.user?.frozen_balance) }}</p>
          <p v-if="sevenDaySpend !== null" class="mt-2 text-xs text-gray-500 dark:text-dark-400">{{ sevenDaySpend > 0 ? pt('refresh.runway', { days: Math.floor(Math.max(0, authStore.user?.balance ?? 0) * 7 / sevenDaySpend) }) : pt('refresh.runwayEmpty') }}</p>
          <p v-if="statsFailed" class="mt-2 text-xs text-red-600 dark:text-red-400" role="alert">{{ pt('refresh.statsFailed') }} <button type="button" class="underline" @click="loadStats">{{ pt('refresh.retry') }}</button></p>
        </div>
        <div class="wallet-spent-cell">
          <div class="text-xs text-gray-500 dark:text-dark-400">{{ pt('pages.wallet.spent') }}</div>
          <div class="text-xl font-semibold tabular-nums text-gray-900 dark:text-white" data-testid="portal-wallet-spent">
            {{ formatMoney(stats?.total_actual_cost) }}
          </div>
        </div>
        <div class="wallet-today-cell">
          <div class="text-xs text-gray-500 dark:text-dark-400">{{ pt('pages.wallet.spentToday') }}</div>
          <div class="text-xl font-semibold tabular-nums text-gray-900 dark:text-white" data-testid="portal-wallet-spent-today">
            {{ formatMoney(stats?.today_actual_cost) }}
          </div>
        </div>
        <div v-if="showRedeem" ref="redeemRef" class="wallet-redeem-cell scroll-mt-24">
          <p class="mb-3 text-sm text-gray-500 dark:text-dark-400">{{ pt('refresh.redeemCode') }}</p>
          <WalletRedeemForm ref="redeemForm" @redeemed="onRedeemed" />
        </div>
      </section>

      <div class="wallet-content grid grid-cols-1 items-start gap-4" :class="{ 'xl:grid-cols-[minmax(0,1.35fr)_minmax(0,1fr)]': showPurchase && (recordTabs.length || hasSubscriptions) }">
        <div v-if="showPurchase" class="min-w-0">
          <section v-if="showPurchase" class="wallet-purchase card" data-testid="portal-wallet-purchase">
            <h2 class="wallet-section-heading flex items-center gap-2 text-sm font-semibold"><PixelIcon name="creditCard" size="sm" />{{ pt('refresh.onlineRecharge') }}</h2>
            <PaymentView :key="paymentKey" tabs-target="#wallet-purchase-tabs" />
          </section>
        </div>
        <div v-if="hasSubscriptions || recordTabs.length" class="min-w-0 space-y-4">
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
        <section
          v-if="recordTabs.length && activeRecordTab"
          ref="recordsRef"
          class="card min-w-0 scroll-mt-24 p-5"
          data-testid="portal-wallet-records"
        >
          <h2 class="mb-4 text-sm font-semibold">{{ pt('refresh.walletRecords') }}</h2>
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
import { useAuthStore } from '@/stores/auth'
import PortalPageHeading from '../components/common/PortalPageHeading.vue'
import PixelIcon from '../components/common/PixelIcon.vue'
import { addDays, todayKey } from '../overview/usageChartData'
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
const authStore = useAuthStore()
const { ctx } = usePortalNavContext()

const redeemRef = ref<HTMLElement | null>(null)
const redeemForm = ref<InstanceType<typeof WalletRedeemForm> | null>(null)
const subscriptionsRef = ref<HTMLElement | null>(null)
const recordsRef = ref<HTMLElement | null>(null)
const historyList = ref<InstanceType<typeof WalletRecordList> | null>(null)
const stats = ref<UserDashboardStats | null>(null)
const sevenDaySpend = ref<number | null>(null)
const statsFailed = ref(false)
let statsSequence = 0

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

const formatMoney = (value: number | undefined) => value === undefined ? '—' : `$${value.toFixed(2)}`

function recordTabLabel(tab: WalletRecordTab): string {
  return tab === 'history' ? pt('pages.wallet.history') : pt(`shell.links.${tab}`)
}

function selectRecordTab(tab: WalletRecordTab) {
  if (tab !== activeRecordTab.value) void router.replace({ path: WALLET_PATH, query: { section: tab } })
}

async function loadStats() {
  const sequence = ++statsSequence
  const end = todayKey()
  const results = await Promise.allSettled([usageAPI.getDashboardStats(), usageAPI.getDashboardTrend({ start_date: addDays(end, -6), end_date: end, granularity: 'day', billing_type: 0 })])
  if (sequence !== statsSequence) return
  stats.value = results[0].status === 'fulfilled' ? results[0].value : null
  sevenDaySpend.value = results[1].status === 'fulfilled' ? results[1].value.trend.reduce((sum, point) => sum + point.actual_cost, 0) : null
  statsFailed.value = results.some((result) => result.status === 'rejected')
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
<style scoped>
.wallet-balance { font: 400 36px/40px var(--pp-font-pixel); font-synthesis: none; }
.wallet-summary { display: grid; grid-template-columns: 1.3fr 1fr 1fr 1.4fr; }
.wallet-summary > div { padding: 20px; min-width: 0; }
.wallet-summary > div + div { border-left: 1px solid var(--pp-divider); }
.wallet-balance-cell { background: var(--pp-tone-mint); }
.wallet-summary .wallet-spent-cell { order: 2; }
.wallet-summary .wallet-today-cell { order: 1; }
.wallet-summary .wallet-redeem-cell { order: 3; }
.wallet-spent-cell > :last-child, .wallet-today-cell > :last-child { margin-top: 12px; font: 400 24px/28px var(--pp-font-pixel); font-synthesis: none; }
.wallet-summary :deep(form), .wallet-summary :deep(input) { width: 100%; }
.wallet-section-heading { padding: 14px 16px; border-bottom: 1px solid var(--pp-divider); }
.wallet-purchase :deep(.payment-view) { padding: 16px; max-width: none; }
.wallet-purchase :deep(.recharge-account) { display: none; }
.wallet-purchase :deep(.recharge-amounts), .wallet-purchase :deep(.recharge-methods), .wallet-purchase :deep(.recharge-summary) { padding: 0; border: 0; border-image: none; background: transparent; }
.wallet-purchase :deep(.recharge-amounts) { margin-top: 0; }
.wallet-purchase :deep(.recharge-summary) { padding: 16px; background: var(--pp-surface-2); border: 1px solid var(--pp-divider); }
.wallet-purchase :deep(.recharge-submit) { background: var(--pp-tone-sage); color: #2c2e33; }
.wallet-purchase :deep(.recharge-submit:disabled) { opacity: 0.5; }
.wallet-purchase :deep(.recharge-summary .text-lg) { font: 400 24px/28px var(--pp-font-pixel); font-synthesis: none; }
.wallet-purchase :deep(.portal-amount-input button) { min-height: 84px; text-align: left; padding: 12px; border: 3px double var(--pp-divider); color: var(--pp-ink); }
.wallet-purchase :deep(.portal-amount-input button[aria-pressed='true']) { border-color: var(--pp-control); background: var(--pp-tone-mint); }
.wallet-purchase :deep(.amount-value) { font: 400 24px/28px var(--pp-font-pixel); font-synthesis: none; }
.wallet-purchase :deep([data-testid='quick-amount-credited']) { font-size: 12px; margin-top: 6px; color: var(--pp-ink); opacity: 0.8; }
.wallet-purchase :deep([data-testid='payment-method-grid'] > button) { border: 3px double var(--pp-divider); color: var(--pp-ink); background: var(--pp-surface); }
.wallet-purchase :deep([data-testid='payment-method-grid'] > button[aria-pressed='true']) { border-color: var(--pp-control); border-bottom-color: var(--pp-tone-sage); }
@media (max-width: 1023px) {
  .wallet-summary { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .wallet-summary > div:nth-child(2) { border-left: 0; border-top: 1px solid var(--pp-divider); }
  .wallet-summary > div:nth-child(4) { border-top: 1px solid var(--pp-divider); }
}
@media (max-width: 639px) {
  .wallet-summary { grid-template-columns: minmax(0, 1fr); }
  .wallet-summary > div + div { border-left: 0; border-top: 1px solid var(--pp-divider); }
  .wallet-summary > div { padding: 16px; }
  .wallet-purchase :deep(.portal-amount-input > div > .grid) { gap: 8px; }
  .wallet-purchase :deep(.portal-amount-input button) { padding: 10px; }
}
</style>
