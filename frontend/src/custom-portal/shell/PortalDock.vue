<template>
  <nav
    ref="dockRef"
    class="portal-dock fixed bottom-3 left-1/2 z-40 -translate-x-1/2 sm:bottom-4"
    :aria-label="pt('shell.dockLabel')"
    data-testid="portal-dock"
    @keydown.esc="openId = null"
  >
    <ul
      class="flex items-stretch gap-0.5 rounded-2xl border border-gray-200 bg-white/90 p-1 shadow-lg backdrop-blur-xl dark:border-dark-700 dark:bg-dark-900/90 sm:gap-1 sm:p-1.5"
    >
      <li v-for="entry in entries" :key="entry.id" class="relative">
        <router-link
          v-if="entry.path"
          :to="entry.path"
          :class="[itemClass, isActive(entry) ? activeClass : idleClass]"
          :aria-current="isActive(entry) ? 'page' : undefined"
          :data-testid="`portal-dock-${entry.id}`"
        >
          <PixelIcon :name="entry.icon" size="md" />
          <span class="portal-dock-label">{{ pt(`shell.dock.${entry.id}`) }}</span>
        </router-link>
        <template v-else>
          <button
            type="button"
            :class="[itemClass, isActive(entry) ? activeClass : idleClass]"
            :aria-expanded="openId === entry.id"
            aria-haspopup="menu"
            :data-testid="`portal-dock-${entry.id}`"
            @click="toggle(entry.id)"
          >
            <PixelIcon :name="entry.icon" size="md" />
            <span class="portal-dock-label">{{ pt(`shell.dock.${entry.id}`) }}</span>
          </button>
          <transition name="portal-dock-pop">
            <ul
              v-if="openId === entry.id"
              class="absolute bottom-full left-1/2 mb-3 w-48 -translate-x-1/2 rounded-xl border border-gray-200 bg-white py-1 shadow-lg dark:border-dark-700 dark:bg-dark-800"
              role="menu"
              :data-testid="`portal-dock-menu-${entry.id}`"
            >
              <li v-for="child in entry.children" :key="child.id" role="none">
                <router-link
                  :to="{ path: child.path, query: child.query }"
                  role="menuitem"
                  class="dropdown-item"
                  :class="{ 'font-semibold text-primary-700 dark:text-primary-300': matchesPath(route.path, child.path) }"
                >
                  <PixelIcon :name="child.icon" size="sm" />
                  {{ childLabel(child.id) }}
                </router-link>
              </li>
            </ul>
          </transition>
        </template>
      </li>
    </ul>
  </nav>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import PixelIcon from '../components/common/PixelIcon.vue'
import { useAppStore } from '@/stores/app'
import { resolveSiteBillingMode } from '@/utils/siteBillingMode'
import { usePortalI18n } from '../i18n'
import { dockEntryPaths, matchesPath, purchaseLabelKey, type ResolvedDockEntry } from './nav'

defineProps<{ entries: ResolvedDockEntry[] }>()

const { pt } = usePortalI18n()
const route = useRoute()
const appStore = useAppStore()
const openId = ref<string | null>(null)
const dockRef = ref<HTMLElement | null>(null)

const itemClass =
  'flex min-w-[3.25rem] flex-col items-center gap-0.5 rounded-xl px-2 py-1.5 text-[11px] font-medium transition-colors sm:min-w-[4rem] sm:px-3'
const activeClass = 'portal-dock-active bg-primary-500 text-gray-900 dark:bg-primary-400 dark:text-dark-900'
const idleClass = 'text-gray-600 hover:bg-gray-100 hover:text-gray-900 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-white'

function isActive(entry: ResolvedDockEntry): boolean {
  return dockEntryPaths(entry).some((path) => matchesPath(route.path, path))
}

function toggle(id: string) {
  openId.value = openId.value === id ? null : id
}

function childLabel(id: string): string {
  const key = id === 'purchase' ? purchaseLabelKey(resolveSiteBillingMode(appStore.cachedPublicSettings)) : id
  return pt(`shell.links.${key}`)
}

function onDocumentClick(event: MouseEvent) {
  if (dockRef.value && !dockRef.value.contains(event.target as Node)) openId.value = null
}

watch(
  () => route.fullPath,
  () => {
    openId.value = null
  }
)
onMounted(() => document.addEventListener('click', onDocumentClick))
onBeforeUnmount(() => document.removeEventListener('click', onDocumentClick))
</script>

<style scoped>
.portal-dock {
  padding-bottom: env(safe-area-inset-bottom);
}

/* Solid pixel bar: the template's translucent fill and blur stay for the upstream shell. */
.portal-dock > ul,
.portal-dock ul[role='menu'] {
  backdrop-filter: none;
  -webkit-backdrop-filter: none;
  border-width: 5px;
  border-color: var(--pp-edge);
  border-image: var(--pp-frame) 5 / 5px;
  background-clip: border-box;
  box-shadow: none;
  /* A canvas-coloured ring lifts the floating bar off the grid instead of a drop shadow. */
  outline: 4px solid #f8f7f2;
  background-color: #ffffff;
}

:where(html.dark) .portal-dock > ul,
:where(html.dark) .portal-dock ul[role='menu'] {
  background-color: #232630;
  outline-color: #1c1e26;
}

@media (forced-colors: active) {
  .portal-dock > ul,
  .portal-dock ul[role='menu'] {
    border-image-source: none;
  }
}

/* The active item is sage with a 2px ink rule drawn inside its edge. */
.portal-dock-active {
  outline: 2px solid currentColor;
  outline-offset: -2px;
}

.portal-dock > ul > li > :focus-visible {
  outline: 2px solid var(--pp-ink);
  outline-offset: 2px;
}

.portal-dock-label {
  font-family: var(--pp-font-pixel);
  font-size: 12px;
  font-synthesis: none;
}

.portal-dock-pop-enter-active,
.portal-dock-pop-leave-active {
  transition: opacity 0.15s ease, transform 0.15s ease;
}

.portal-dock-pop-enter-from,
.portal-dock-pop-leave-to {
  opacity: 0;
  transform: translate(-50%, 4px);
}
</style>
