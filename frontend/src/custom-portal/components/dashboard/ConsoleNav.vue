<template>
  <header class="sticky top-0 z-40 border-b border-gray-200/70 bg-white/80 px-4 py-3 backdrop-blur-xl dark:border-dark-800/80 dark:bg-dark-950/80 sm:px-6 lg:px-8">
    <div class="mx-auto flex max-w-7xl items-center justify-between gap-4">
      <!-- Left: Logo & Context -->
      <div class="flex items-center gap-3">
        <router-link to="/v2" class="flex items-center gap-2.5">
          <div class="flex h-9 w-9 items-center justify-center rounded-xl bg-gradient-to-br from-primary-500 to-primary-700 text-sm font-bold text-white shadow-sm">
            S2
          </div>
          <span class="text-sm font-bold tracking-tight text-gray-900 dark:text-white">
            {{ siteName }}
          </span>
        </router-link>

        <span class="h-4 w-px bg-gray-200 dark:bg-dark-700"></span>

        <!-- Workspace Breadcrumb / Switcher -->
        <div class="flex items-center gap-2 text-xs">
          <span class="rounded-lg bg-gray-100 px-2 py-1 font-medium text-gray-700 dark:bg-dark-800 dark:text-dark-300">
            {{ pt('console.workspace') }}
          </span>
          <router-link
            to="/dashboard"
            class="hidden text-[11px] text-gray-500 hover:text-primary-600 dark:text-dark-400 dark:hover:text-primary-400 sm:inline"
            :title="pt('console.switchToClassicTitle')"
          >
            {{ pt('console.switchToClassic') }}
          </router-link>
        </div>
      </div>

      <!-- Center: API Base URL Fast Copy (Desktop) -->
      <div class="hidden items-center gap-2 rounded-xl border border-gray-200/80 bg-gray-50/80 px-3 py-1.5 text-xs dark:border-dark-800 dark:bg-dark-900/60 md:flex">
        <span class="text-gray-400 font-mono text-[11px]">Base URL:</span>
        <code class="font-mono text-gray-700 dark:text-gray-300">{{ baseUrl }}</code>
        <button
          @click="copyBaseUrl"
          class="ml-1 inline-flex items-center text-primary-600 hover:text-primary-700 dark:text-primary-400"
          :title="copied ? pt('common.copied') : pt('console.copyBaseUrl')"
        >
          <svg v-if="copied" class="h-3.5 w-3.5 text-emerald-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M4.5 12.75l6 6 9-13.5" />
          </svg>
          <svg v-else class="h-3.5 w-3.5 text-gray-400 hover:text-gray-600 dark:hover:text-gray-200" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M15.666 3.888A2.25 2.25 0 0013.5 2.25h-3c-1.03 0-1.9.693-2.166 1.638m7.332 0c.055.194.084.4.084.612v0a.75.75 0 01-.75.75H9a.75.75 0 01-.75-.75v0c0-.212.03-.418.084-.612m7.332 0c.646.049 1.288.11 1.927.184 1.1.128 1.907 1.077 1.907 2.185V19.5a2.25 2.25 0 01-2.25 2.25H6.75A2.25 2.25 0 014.5 19.5V6.257c0-1.108.806-2.057 1.907-2.185a48.208 48.208 0 011.927-.184" />
          </svg>
        </button>
      </div>

      <!-- Right: Balance + Quick Key + Profile -->
      <div class="flex items-center gap-2.5">
        <!-- Balance Badge & Recharge -->
        <div class="flex items-center rounded-xl border border-emerald-500/20 bg-emerald-50/60 p-1 dark:border-emerald-500/20 dark:bg-emerald-950/30">
          <div class="px-2.5 py-0.5 text-xs font-semibold text-emerald-700 dark:text-emerald-300">
            ${{ balance.toFixed(2) }}
          </div>
          <router-link
            to="/purchase"
            class="rounded-lg bg-emerald-600 px-2 py-1 text-[11px] font-semibold text-white shadow-xs transition-colors hover:bg-emerald-500"
          >
            {{ pt('console.recharge') }}
          </router-link>
        </div>

        <!-- Quick Create Key Button -->
        <button
          @click="$emit('open-create-key')"
          class="hidden sm:inline-flex items-center gap-1.5 rounded-xl bg-gray-900 px-3 py-1.5 text-xs font-semibold text-white shadow-sm transition-all hover:bg-gray-800 active:scale-95 dark:bg-white dark:text-gray-900 dark:hover:bg-gray-100"
        >
          <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
          </svg>
          <span>{{ pt('console.newKey') }}</span>
        </button>

        <!-- Theme Toggle -->
        <button
          @click="toggleTheme"
          type="button"
          :title="isDark ? pt('common.themeToLight') : pt('common.themeToDark')"
          class="flex h-8 w-8 items-center justify-center rounded-xl border border-gray-200 bg-white text-gray-500 hover:text-gray-800 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-400 dark:hover:text-white"
        >
          <svg v-if="isDark" class="h-3.5 w-3.5 text-amber-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 3v2.25m6.364.386l-1.591 1.591M21 12h-2.25m-.386 6.364l-1.591-1.591M12 18.75V21m-4.773-4.227l-1.591 1.591M5.25 12H3m4.227-4.773L5.636 5.636M15.75 12a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0z" />
          </svg>
          <svg v-else class="h-3.5 w-3.5 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M21.752 15.002A9.718 9.718 0 0118 15.75c-5.385 0-9.75-4.365-9.75-9.75 0-1.33.266-2.597.748-3.752A9.753 9.753 0 003 11.25C3 16.635 7.365 21 12.75 21a9.753 9.753 0 009.002-5.998z" />
          </svg>
        </button>

        <!-- Profile Link -->
        <router-link
          to="/profile"
          class="flex items-center gap-1.5 rounded-xl border border-gray-200/80 bg-white p-1 text-xs text-gray-700 hover:border-gray-300 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-300"
          :title="pt('console.profile')"
        >
          <div class="flex h-6 w-6 items-center justify-center rounded-lg bg-gradient-to-br from-primary-500 to-primary-600 text-[11px] font-bold text-white">
            {{ userInitial }}
          </div>
          <span class="hidden max-w-[100px] truncate text-xs font-medium md:inline">{{ userDisplayName }}</span>
        </router-link>
      </div>
    </div>
  </header>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useAuthStore, useAppStore } from '@/stores'
import { usePortalI18n } from '../../i18n'

defineEmits<{
  (e: 'open-create-key'): void
}>()

const { pt } = usePortalI18n()
const authStore = useAuthStore()
const appStore = useAppStore()

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const balance = computed(() => authStore.user?.balance || 0)
const userDisplayName = computed(() => authStore.user?.username || authStore.user?.email || 'User')
const userInitial = computed(() => userDisplayName.value.charAt(0).toUpperCase())

const baseUrl = computed(() => {
  if (appStore.cachedPublicSettings?.api_base_url) {
    return appStore.cachedPublicSettings.api_base_url
  }
  return window.location.origin
})

const copied = ref(false)
async function copyBaseUrl() {
  try {
    await navigator.clipboard.writeText(baseUrl.value)
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  } catch (err) {
    console.error(err)
  }
}

const isDark = ref(document.documentElement.classList.contains('dark'))
function toggleTheme() {
  const next = !isDark.value
  isDark.value = next
  document.documentElement.classList.toggle('dark', next)
  localStorage.setItem('theme', next ? 'dark' : 'light')
}
</script>
