<template>
  <div class="relative overflow-hidden rounded-2xl border border-primary-500/30 bg-gradient-to-r from-primary-50/70 via-teal-50/50 to-white/70 p-6 shadow-sm backdrop-blur-xl dark:border-primary-500/20 dark:from-primary-950/40 dark:via-dark-900/40 dark:to-dark-950/60">
    <div class="flex items-start justify-between gap-4">
      <div>
        <div class="inline-flex items-center gap-1.5 rounded-full bg-primary-100/80 px-2.5 py-0.5 text-[11px] font-semibold text-primary-800 dark:bg-primary-950/60 dark:text-primary-300">
          {{ pt('onboarding.badge') }}
        </div>
        <h2 class="mt-2 text-lg font-bold text-gray-900 dark:text-white">
          {{ pt('onboarding.title') }}
        </h2>
        <p class="mt-1 text-xs text-gray-600 dark:text-dark-300">
          {{ pt('onboarding.description') }}
        </p>
      </div>

      <button
        @click="collapsed = !collapsed"
        class="text-xs text-gray-500 hover:text-gray-800 dark:text-dark-400 dark:hover:text-white"
      >
        {{ collapsed ? pt('onboarding.expand') : pt('onboarding.collapse') }}
      </button>
    </div>

    <!-- 3 Steps -->
    <div v-show="!collapsed" class="mt-6 grid grid-cols-1 gap-4 md:grid-cols-3">
      <!-- Step 1 -->
      <div class="flex flex-col justify-between rounded-xl border border-white/80 bg-white/80 p-4 shadow-xs dark:border-dark-700/60 dark:bg-dark-900/80">
        <div>
          <div class="flex items-center justify-between">
            <span class="flex h-6 w-6 items-center justify-center rounded-full bg-primary-100 text-xs font-bold text-primary-700 dark:bg-primary-950/80 dark:text-primary-300">1</span>
            <span v-if="balance > 0" class="text-[11px] font-semibold text-emerald-600 dark:text-emerald-400">{{ pt('onboarding.balanceReady', { amount: formatUsd(balance) }) }}</span>
            <span v-else class="text-[11px] font-semibold text-amber-600 dark:text-amber-400">{{ pt('onboarding.balancePending') }}</span>
          </div>
          <h3 class="mt-3 text-xs font-bold text-gray-900 dark:text-white">{{ pt('onboarding.balanceTitle') }}</h3>
          <p class="mt-1 text-[11px] leading-relaxed text-gray-500 dark:text-dark-400">
            {{ pt('onboarding.balanceDetail') }}
          </p>
        </div>
        <div class="mt-4 flex items-center gap-2">
          <router-link to="/purchase" class="text-xs font-semibold text-primary-600 hover:text-primary-700 dark:text-primary-400">
            {{ pt('onboarding.goRecharge') }} &rarr;
          </router-link>
          <span class="text-gray-300">|</span>
          <router-link to="/redeem" class="text-xs font-semibold text-gray-500 hover:text-gray-700 dark:text-dark-400">
            {{ pt('onboarding.redeemCode') }}
          </router-link>
        </div>
      </div>

      <!-- Step 2 -->
      <div class="flex flex-col justify-between rounded-xl border border-white/80 bg-white/80 p-4 shadow-xs dark:border-dark-700/60 dark:bg-dark-900/80">
        <div>
          <div class="flex items-center justify-between">
            <span class="flex h-6 w-6 items-center justify-center rounded-full bg-primary-100 text-xs font-bold text-primary-700 dark:bg-primary-950/80 dark:text-primary-300">2</span>
            <span v-if="hasKeys" class="text-[11px] font-semibold text-emerald-600 dark:text-emerald-400">{{ pt('onboarding.keysReady', { count: keyCount }) }}</span>
            <span v-else class="text-[11px] font-semibold text-amber-600 dark:text-amber-400">{{ pt('onboarding.keysPending') }}</span>
          </div>
          <h3 class="mt-3 text-xs font-bold text-gray-900 dark:text-white">{{ pt('onboarding.keysTitle') }}</h3>
          <p class="mt-1 text-[11px] leading-relaxed text-gray-500 dark:text-dark-400">
            {{ pt('onboarding.keysDetail') }}
          </p>
        </div>
        <div class="mt-4 flex items-center gap-2">
          <button
            @click="$emit('open-create-key')"
            class="text-xs font-semibold text-primary-600 hover:text-primary-700 dark:text-primary-400"
          >
            {{ pt('onboarding.quickCreate') }} &rarr;
          </button>
          <span class="text-gray-300">|</span>
          <router-link to="/keys" class="text-xs font-semibold text-gray-500 hover:text-gray-700 dark:text-dark-400">
            {{ pt('onboarding.manageKeys') }}
          </router-link>
        </div>
      </div>

      <!-- Step 3 -->
      <div class="flex flex-col justify-between rounded-xl border border-white/80 bg-white/80 p-4 shadow-xs dark:border-dark-700/60 dark:bg-dark-900/80">
        <div>
          <div class="flex items-center justify-between">
            <span class="flex h-6 w-6 items-center justify-center rounded-full bg-primary-100 text-xs font-bold text-primary-700 dark:bg-primary-950/80 dark:text-primary-300">3</span>
            <span class="text-[11px] font-semibold text-primary-600 dark:text-primary-400">{{ pt('onboarding.toolsReady') }}</span>
          </div>
          <h3 class="mt-3 text-xs font-bold text-gray-900 dark:text-white">{{ pt('onboarding.toolsTitle') }}</h3>
          <p class="mt-1 text-[11px] leading-relaxed text-gray-500 dark:text-dark-400">
            {{ pt('onboarding.toolsDetail') }}
          </p>
        </div>
        <div class="mt-4 flex items-center gap-2">
          <router-link to="/v2#clients" class="text-xs font-semibold text-primary-600 hover:text-primary-700 dark:text-primary-400">
            {{ pt('onboarding.toolsGuide') }} &rarr;
          </router-link>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { usePortalI18n } from '../../i18n'

defineProps<{
  balance: number
  keyCount: number
  hasKeys: boolean
}>()

defineEmits<{
  (e: 'open-create-key'): void
}>()

const { pt } = usePortalI18n()
const collapsed = ref(false)

function formatUsd(value: number): string {
  return `$${value.toFixed(2)}`
}
</script>
