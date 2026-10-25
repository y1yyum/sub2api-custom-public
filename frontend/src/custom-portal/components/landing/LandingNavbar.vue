<template>
  <header class="fixed inset-x-0 top-0 z-50 transition-all duration-300" :class="scrolled ? 'border-b border-gray-200/60 bg-white/80 backdrop-blur-xl shadow-sm dark:border-dark-800/80 dark:bg-dark-950/80' : 'bg-transparent'">
    <nav class="mx-auto flex h-18 max-w-7xl items-center justify-between px-4 sm:px-6 lg:px-8">
      <!-- Brand Logo & Name -->
      <router-link to="/v2" class="flex items-center gap-3 group">
        <div class="relative flex h-10 w-10 items-center justify-center overflow-hidden rounded-xl bg-gradient-to-br from-primary-500 to-primary-700 shadow-md shadow-primary-500/20 transition-transform group-hover:scale-105">
          <img v-if="siteLogo" :src="siteLogo" alt="Logo" class="h-full w-full object-contain p-1" />
          <span v-else class="text-lg font-black tracking-wider text-white">S2</span>
        </div>
        <div class="flex flex-col">
          <div class="flex items-center gap-2">
            <span class="text-base font-bold tracking-tight text-gray-900 transition-colors group-hover:text-primary-600 dark:text-white dark:group-hover:text-primary-400">
              {{ siteName }}
            </span>
            <span class="rounded-full bg-primary-100/80 px-2 py-0.5 text-[10px] font-semibold text-primary-700 dark:bg-primary-950/60 dark:text-primary-300 border border-primary-300/40 dark:border-primary-800/40">
              Gateway v2
            </span>
          </div>
          <span class="hidden text-[11px] text-gray-500 dark:text-dark-400 sm:inline">{{ pt('navbar.tagline') }}</span>
        </div>
      </router-link>

      <!-- Center Links (Desktop) -->
      <div class="hidden items-center gap-1 rounded-full border border-gray-200/60 bg-white/60 px-4 py-1.5 backdrop-blur-md dark:border-dark-800/80 dark:bg-dark-900/60 lg:flex">
        <a href="#features" class="rounded-full px-3 py-1 text-xs font-medium text-gray-600 transition-colors hover:text-primary-600 dark:text-dark-300 dark:hover:text-white">
          {{ pt('navbar.features') }}
        </a>
        <a href="#playground" class="rounded-full px-3 py-1 text-xs font-medium text-gray-600 transition-colors hover:text-primary-600 dark:text-dark-300 dark:hover:text-white">
          {{ pt('navbar.playground') }}
        </a>
        <a href="#clients" class="rounded-full px-3 py-1 text-xs font-medium text-gray-600 transition-colors hover:text-primary-600 dark:text-dark-300 dark:hover:text-white">
          {{ pt('navbar.clients') }}
        </a>
        <a href="#models" class="rounded-full px-3 py-1 text-xs font-medium text-gray-600 transition-colors hover:text-primary-600 dark:text-dark-300 dark:hover:text-white">
          {{ pt('navbar.models') }}
        </a>
        <router-link v-if="showModelPlazaEntry" to="/model-plaza" class="rounded-full px-3 py-1 text-xs font-medium text-gray-600 transition-colors hover:text-primary-600 dark:text-dark-300 dark:hover:text-white">
          {{ pt('navbar.modelPlaza') }}
        </router-link>
        <router-link to="/key-usage" class="rounded-full px-3 py-1 text-xs font-medium text-gray-600 transition-colors hover:text-primary-600 dark:text-dark-300 dark:hover:text-white">
          {{ pt('navbar.keyUsage') }}
        </router-link>
      </div>

      <!-- Right Actions -->
      <div class="flex items-center gap-2.5">
        <!-- Docs -->
        <a
          v-if="docUrl"
          :href="docUrl"
          target="_blank"
          rel="noopener noreferrer"
          class="flex h-9 w-9 items-center justify-center rounded-xl border border-gray-200/60 bg-white/60 text-gray-600 transition-all hover:border-gray-300 hover:text-gray-900 dark:border-dark-800 dark:bg-dark-900/60 dark:text-dark-300 dark:hover:border-dark-700 dark:hover:text-white"
          :title="pt('navbar.docs')"
        >
          <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 6.042A8.967 8.967 0 006 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 016 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 016-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0018 18a8.967 8.967 0 00-6 2.292m0-14.25v14.25" />
          </svg>
        </a>

        <!-- Theme Toggle -->
        <button
          @click="toggleTheme"
          type="button"
          class="flex h-9 w-9 items-center justify-center rounded-xl border border-gray-200/60 bg-white/60 text-gray-600 transition-all hover:border-gray-300 hover:text-gray-900 dark:border-dark-800 dark:bg-dark-900/60 dark:text-dark-300 dark:hover:border-dark-700 dark:hover:text-white"
          :title="isDark ? pt('common.themeToLight') : pt('common.themeToDark')"
        >
          <svg v-if="isDark" class="h-4 w-4 text-amber-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 3v2.25m6.364.386l-1.591 1.591M21 12h-2.25m-.386 6.364l-1.591-1.591M12 18.75V21m-4.773-4.227l-1.591 1.591M5.25 12H3m4.227-4.773L5.636 5.636M15.75 12a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0z" />
          </svg>
          <svg v-else class="h-4 w-4 text-gray-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
            <path stroke-linecap="round" stroke-linejoin="round" d="M21.752 15.002A9.718 9.718 0 0118 15.75c-5.385 0-9.75-4.365-9.75-9.75 0-1.33.266-2.597.748-3.752A9.753 9.753 0 003 11.25C3 16.635 7.365 21 12.75 21a9.753 9.753 0 009.002-5.998z" />
          </svg>
        </button>

        <!-- Auth Buttons -->
        <template v-if="isAuthenticated">
          <router-link
            to="/v2/dashboard"
            class="group inline-flex items-center gap-2 rounded-xl bg-gray-900 px-3.5 py-2 text-xs font-semibold text-white shadow-md shadow-gray-900/10 transition-all hover:bg-gray-800 active:scale-95 dark:bg-white dark:text-gray-900 dark:hover:bg-gray-100"
          >
            <span class="flex h-5 w-5 items-center justify-center rounded-lg bg-gradient-to-br from-primary-400 to-primary-600 text-[10px] font-bold text-white">
              {{ userInitial }}
            </span>
            <span>{{ pt('navbar.enterConsole') }}</span>
            <svg class="h-3.5 w-3.5 transition-transform group-hover:translate-x-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
            </svg>
          </router-link>
        </template>
        <template v-else>
          <router-link
            to="/login"
            class="hidden rounded-xl px-3.5 py-2 text-xs font-semibold text-gray-700 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:text-gray-300 dark:hover:bg-dark-800 dark:hover:text-white sm:inline-block"
          >
            {{ pt('navbar.login') }}
          </router-link>
          <router-link
            to="/register"
            class="inline-flex items-center gap-1.5 rounded-xl bg-gradient-to-r from-primary-600 to-primary-500 px-4 py-2 text-xs font-semibold text-white shadow-md shadow-primary-500/25 transition-all hover:from-primary-500 hover:to-primary-600 hover:shadow-lg hover:shadow-primary-500/35 active:scale-95"
          >
            <span>{{ pt('navbar.getStarted') }}</span>
            <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
            </svg>
          </router-link>
        </template>
      </div>
    </nav>
  </header>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useAuthStore, useAppStore } from '@/stores'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import { sanitizeUrl } from '@/utils/url'
import { usePortalI18n } from '../../i18n'

const { pt } = usePortalI18n()

const authStore = useAuthStore()
const appStore = useAppStore()

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const docUrl = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''))

const isAuthenticated = computed(() => authStore.isAuthenticated)
const userInitial = computed(() => {
  const email = authStore.user?.email || authStore.user?.username || 'U'
  return email.charAt(0).toUpperCase()
})

const modelPlazaEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.modelPlaza))
const modelPlazaRequiresAuth = computed(() => appStore.cachedPublicSettings?.model_plaza_require_auth === true)
const showModelPlazaEntry = computed(() => modelPlazaEnabled.value && (isAuthenticated.value || !modelPlazaRequiresAuth.value))

const isDark = ref(document.documentElement.classList.contains('dark'))
function toggleTheme() {
  const next = !isDark.value
  isDark.value = next
  document.documentElement.classList.toggle('dark', next)
  localStorage.setItem('theme', next ? 'dark' : 'light')
}

const scrolled = ref(false)
function onScroll() {
  scrolled.value = window.scrollY > 20
}

onMounted(() => {
  window.addEventListener('scroll', onScroll, { passive: true })
  onScroll()
})

onBeforeUnmount(() => {
  window.removeEventListener('scroll', onScroll)
})
</script>
