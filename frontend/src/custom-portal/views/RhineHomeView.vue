<template>
  <UpstreamHomeView v-if="adminCustomizedHome" />
  <RhineHomePage v-else />
</template>

<script setup lang="ts">
import { computed, defineAsyncComponent, defineComponent, h } from 'vue'
import { useAppStore } from '@/stores'
import RhineHomePage from '../components/rhine/RhineHomePage.vue'

// A failed chunk load falls back to the portal homepage instead of a blank page.
// The wrapper drops the `error` prop Vue passes, so it never lands on <main> as an attribute.
const UpstreamHomeView = defineAsyncComponent({
  loader: () => import('@/views/HomeView.vue'),
  errorComponent: defineComponent({ inheritAttrs: false, render: () => h(RhineHomePage) })
})

const appStore = useAppStore()

/**
 * Admin-configured `home_content` (iframe/HTML) and the compact home are
 * upstream features; when either is set the upstream HomeView keeps serving
 * them instead of the portal homepage.
 */
const adminCustomizedHome = computed(() => {
  const settings = appStore.cachedPublicSettings
  if (!settings) return false
  return Boolean(settings.home_content?.trim()) || settings.compact_home_enabled === true
})
</script>
