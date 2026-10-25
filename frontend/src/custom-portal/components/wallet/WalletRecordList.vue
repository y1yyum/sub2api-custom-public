<template>
  <div data-testid="wallet-history">
    <div v-if="loading && !records.length" class="flex justify-center py-10">
      <div class="h-6 w-6 animate-spin rounded-full border-2 border-primary-500 border-t-transparent"></div>
    </div>
    <p
      v-else-if="!records.length"
      class="py-10 text-center text-sm text-gray-500 dark:text-dark-400"
      data-testid="wallet-history-empty"
    >
      {{ failed ? pt('pages.wallet.historyFailed') : pt('pages.wallet.historyEmpty') }}
    </p>
    <ul v-else class="divide-y divide-gray-100 dark:divide-dark-700">
      <li
        v-for="record in records"
        :key="record.key"
        class="flex items-center justify-between gap-3 py-3"
        :data-testid="`wallet-record-${record.source}`"
      >
        <div class="min-w-0">
          <div class="truncate text-sm font-medium text-gray-900 dark:text-white">{{ title(record) }}</div>
          <div class="text-xs text-gray-500 dark:text-dark-400">{{ formatDateTimeToMinute(record.at) }}</div>
        </div>
        <div class="flex shrink-0 items-center gap-2">
          <OrderStatusBadge v-if="record.source === 'order'" :status="record.order.status" />
          <span class="text-sm font-semibold tabular-nums" :class="amountClass(record)">{{ amount(record) }}</span>
        </div>
      </li>
    </ul>
    <p v-if="records.length" class="pt-2 text-xs text-gray-400 dark:text-dark-500">
      {{ pt('pages.wallet.historyHint', { count: HISTORY_LIMIT }) }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { paymentAPI } from '@/api/payment'
import { redeemAPI, type RedeemHistoryItem } from '@/api/redeem'
import OrderStatusBadge from '@/components/payment/OrderStatusBadge.vue'
import { currencySymbol } from '@/components/payment/currency'
import { formatDateTimeToMinute } from '@/utils/format'
import { usePortalI18n } from '../../i18n'
import { mergeWalletRecords, type WalletRecord } from '../../views/walletRecords'

const props = defineProps<{ includeOrders: boolean }>()

const HISTORY_LIMIT = 20

const { t: translateGlobal } = useI18n()
const { pt } = usePortalI18n()

const records = ref<WalletRecord[]>([])
const loading = ref(false)
const failed = ref(false)
let request = 0

const REDEEM_TITLES: Record<string, string> = {
  balance: 'redeem.balanceAddedRedeem',
  concurrency: 'redeem.concurrencyAddedRedeem',
  subscription: 'redeem.subscriptionAssigned'
}

function redeemTitle(item: RedeemHistoryItem): string {
  if (item.type === 'admin_balance') {
    return translateGlobal(item.value >= 0 ? 'redeem.balanceAddedAdmin' : 'redeem.balanceDeductedAdmin')
  }
  if (item.type === 'admin_concurrency') {
    return translateGlobal(item.value >= 0 ? 'redeem.concurrencyAddedAdmin' : 'redeem.concurrencyReducedAdmin')
  }
  return REDEEM_TITLES[item.type] ? translateGlobal(REDEEM_TITLES[item.type]) : translateGlobal('common.unknown')
}

function title(record: WalletRecord): string {
  if (record.source === 'redeem') return redeemTitle(record.redeem)
  return pt(record.order.order_type === 'subscription' ? 'pages.wallet.orderSubscription' : 'pages.wallet.orderBalance')
}

const signed = (value: number, text: string) => `${value >= 0 ? '+' : '-'}${text}`

function amount(record: WalletRecord): string {
  if (record.source === 'order') {
    const order = record.order
    if (order.order_type === 'subscription') return `${currencySymbol(order.currency)}${order.pay_amount.toFixed(2)}`
    return `+$${order.amount.toFixed(2)}`
  }
  const item = record.redeem
  if (item.type === 'balance' || item.type === 'admin_balance') return signed(item.value, `$${Math.abs(item.value).toFixed(2)}`)
  if (item.type === 'subscription') {
    const days = translateGlobal('redeem.subscriptionDays', { days: item.validity_days || Math.round(item.value) })
    return item.group?.name ? `${days} · ${item.group.name}` : days
  }
  return signed(item.value, `${Math.abs(item.value)} ${translateGlobal('redeem.requests')}`)
}

function amountClass(record: WalletRecord): string {
  const negative = record.source === 'redeem' && record.redeem.value < 0
  return negative ? 'text-red-600 dark:text-red-400' : 'text-gray-900 dark:text-white'
}

async function refresh() {
  const current = ++request
  loading.value = true
  const [orders, redeems] = await Promise.allSettled([
    props.includeOrders
      ? paymentAPI.getMyOrders({ page: 1, page_size: HISTORY_LIMIT }).then((res) => res.data.items ?? [])
      : Promise.resolve([]),
    redeemAPI.getHistory(1, HISTORY_LIMIT).then((res) => res.items)
  ])
  if (current !== request) return
  records.value = mergeWalletRecords(
    orders.status === 'fulfilled' ? orders.value : [],
    redeems.status === 'fulfilled' ? redeems.value : [],
    HISTORY_LIMIT
  )
  failed.value = orders.status === 'rejected' && redeems.status === 'rejected'
  loading.value = false
}

onMounted(refresh)
defineExpose({ refresh })
</script>
