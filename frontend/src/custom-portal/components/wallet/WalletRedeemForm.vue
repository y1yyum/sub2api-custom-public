<template>
  <form class="flex w-full items-center gap-2 sm:w-auto" data-testid="wallet-redeem" @submit.prevent="submit">
    <label class="sr-only" for="wallet-redeem-code">{{ translateGlobal('redeem.redeemCodeLabel') }}</label>
    <input
      id="wallet-redeem-code"
      ref="inputRef"
      v-model="code"
      type="text"
      autocomplete="off"
      class="input min-w-0 flex-1 sm:w-64"
      :placeholder="translateGlobal('redeem.redeemCodePlaceholder')"
      :disabled="submitting"
      data-testid="wallet-redeem-input"
    />
    <button
      type="submit"
      class="btn btn-primary shrink-0"
      :disabled="submitting || !code.trim()"
      data-testid="wallet-redeem-submit"
    >
      {{ submitting ? translateGlobal('redeem.redeeming') : translateGlobal('redeem.redeemButton') }}
    </button>
  </form>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { redeemAPI } from '@/api/redeem'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { useSubscriptionStore } from '@/stores/subscriptions'

const emit = defineEmits<{ redeemed: [] }>()

const { t: translateGlobal } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const subscriptionStore = useSubscriptionStore()

const code = ref('')
const submitting = ref(false)
const inputRef = ref<HTMLInputElement | null>(null)

function errorDetail(error: unknown): string | undefined {
  const detail = (error as { response?: { data?: { detail?: unknown } } })?.response?.data?.detail
  return typeof detail === 'string' && detail ? detail : undefined
}

// Mirrors upstream RedeemView: refresh the balance, then subscriptions for subscription codes.
async function submit() {
  const value = code.value.trim()
  if (!value || submitting.value) return
  submitting.value = true
  try {
    const result = await redeemAPI.redeem(value)
    code.value = ''
    await authStore.refreshUser().catch(() => appStore.showWarning(translateGlobal('redeem.userRefreshFailed')))
    if (result.type === 'subscription') {
      await subscriptionStore
        .fetchActiveSubscriptions(true)
        .catch(() => appStore.showWarning(translateGlobal('redeem.subscriptionRefreshFailed')))
    }
    appStore.showSuccess(translateGlobal('redeem.codeRedeemSuccess'))
    emit('redeemed')
  } catch (error) {
    appStore.showError(errorDetail(error) ?? translateGlobal('redeem.failedToRedeem'))
  } finally {
    submitting.value = false
  }
}

defineExpose({ focus: () => inputRef.value?.focus() })
</script>
