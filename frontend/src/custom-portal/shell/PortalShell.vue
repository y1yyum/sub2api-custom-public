<template>
  <div class="portal-shell relative min-h-screen bg-gray-50 dark:bg-dark-950" data-portal-shell="user">
    <div class="pointer-events-none fixed inset-0 bg-mesh-gradient"></div>
    <div class="relative">
      <PortalTopBar
        :dock="dock"
        :docs="docs"
        :avatar="avatar"
        :doc-url="docUrl"
        :custom-pages="customPages"
      />
      <main class="portal-shell-main" :class="{ 'portal-shell-main--wide': wide }">
        <slot />
      </main>
    </div>
    <PortalDock :entries="dock" />
  </div>
</template>

<script setup lang="ts">
import PortalDock from './PortalDock.vue'
import PortalTopBar from './PortalTopBar.vue'
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { usePortalNav } from './usePortalNav'
import { isWideTablePath } from './wideRoutes'

const { dock, docs, avatar, docUrl, customPages } = usePortalNav()
const route = useRoute()
const wide = computed(() => isWideTablePath(route.path))
</script>

<style scoped>
/* Room for the floating Dock (about 4.5rem tall including its offset). */
.portal-shell {
  --portal-topbar-height: 56px;
  --portal-main-top: 1rem;
  --portal-dock-space: calc(120px + env(safe-area-inset-bottom));
}

@media (min-width: 768px) {
  .portal-shell {
    --portal-main-top: 1.5rem;
  }
}

@media (min-width: 1024px) {
  .portal-shell {
    --portal-main-top: 2rem;
  }
}

.portal-shell-main {
  width: min(100% - 32px, 1296px);
  margin-inline: auto;
  padding-top: var(--portal-main-top);
  padding-bottom: var(--portal-dock-space);
}

@media (min-width: 768px) {
  .portal-shell-main { width: min(100% - 64px, 1296px); }
}

.portal-shell-main--wide {
  width: calc(100% - 32px);
}

@media (min-width: 768px) {
  .portal-shell-main--wide { width: calc(100% - 64px); }
}

/* Upstream fits table pages to one viewport and scrolls the table inside it, which
   leaves room for only a few rows under the filters. Let them grow with the page
   size instead; the main padding keeps pagination clear of the Dock. */
.portal-shell-main :deep(.table-page-layout) {
  height: auto;
}
</style>
