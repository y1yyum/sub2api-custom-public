<template>
  <footer class="border-t border-gray-200/60 bg-white/50 px-4 py-12 backdrop-blur-md dark:border-dark-800 dark:bg-dark-950/50 sm:px-6 lg:px-8">
    <div class="mx-auto max-w-7xl">
      <div class="flex flex-col items-center justify-between gap-6 sm:flex-row">
        <!-- Brand & Info -->
        <div class="flex items-center gap-3">
          <div class="flex h-8 w-8 items-center justify-center rounded-xl bg-gradient-to-br from-primary-500 to-primary-700 text-white font-bold text-xs shadow-sm">
            S2
          </div>
          <div>
            <div class="text-sm font-bold text-gray-900 dark:text-white">{{ siteName }}</div>
            <div class="text-[11px] text-gray-500 dark:text-dark-400">{{ pt('footer.tagline') }}</div>
          </div>
        </div>

        <!-- Links -->
        <div class="flex flex-wrap items-center gap-6 text-xs text-gray-600 dark:text-dark-400">
          <a href="#features" class="hover:text-primary-600 dark:hover:text-white transition-colors">{{ pt('footer.features') }}</a>
          <a href="#playground" class="hover:text-primary-600 dark:hover:text-white transition-colors">{{ pt('footer.playground') }}</a>
          <a href="#clients" class="hover:text-primary-600 dark:hover:text-white transition-colors">{{ pt('footer.clients') }}</a>
          <router-link to="/model-plaza" class="hover:text-primary-600 dark:hover:text-white transition-colors">{{ pt('footer.modelPlaza') }}</router-link>
          <router-link to="/key-usage" class="hover:text-primary-600 dark:hover:text-white transition-colors">{{ pt('footer.keyUsage') }}</router-link>
          <a v-if="docUrl" :href="docUrl" target="_blank" rel="noopener noreferrer" class="hover:text-primary-600 dark:hover:text-white transition-colors">{{ pt('footer.docs') }}</a>
        </div>
      </div>

      <div class="mt-8 border-t border-gray-100 pt-6 text-center text-xs text-gray-400 dark:border-dark-800 dark:text-dark-500 sm:flex sm:justify-between">
        <div>&copy; {{ currentYear }} {{ siteName }}. {{ pt('footer.rights') }}</div>
        <div class="mt-2 sm:mt-0">{{ pt('footer.poweredBy') }}</div>
      </div>
    </div>
  </footer>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useAppStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'
import { usePortalI18n } from '../../i18n'

const { pt } = usePortalI18n()

const appStore = useAppStore()
const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const docUrl = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''))
const currentYear = computed(() => new Date().getFullYear())
</script>
