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
      <main class="portal-shell-main px-4 md:px-6 lg:px-8">
        <slot />
      </main>
    </div>
    <PortalDock :entries="dock" />
  </div>
</template>

<script setup lang="ts">
import PortalDock from './PortalDock.vue'
import PortalTopBar from './PortalTopBar.vue'
import { usePortalNav } from './usePortalNav'

const { dock, docs, avatar, docUrl, customPages } = usePortalNav()
</script>

<style scoped>
/* Room for the floating Dock (about 4.5rem tall including its offset). */
.portal-shell {
  --portal-topbar-height: 65px;
  --portal-main-top: 1rem;
  --portal-dock-space: 6rem;
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
  padding-top: var(--portal-main-top);
  padding-bottom: var(--portal-dock-space);
}

/* Upstream sizes table pages to the viewport minus its header and lg padding at
   every width; the Dock replaces the bottom padding, so subtract it instead. */
.portal-shell-main :deep(.table-page-layout) {
  height: calc(100vh - var(--portal-topbar-height) - var(--portal-main-top) - var(--portal-dock-space));
}

/* Mobile table pages scroll with the document, so let them grow with content. */
.portal-shell-main :deep(.table-page-layout.mobile-mode) {
  height: auto;
}
</style>
