<template>
  <PortalDropdown
    v-if="user"
    :label="pt('shell.avatar.menu')"
    trigger-class="flex items-center gap-2 rounded-xl p-1.5 transition-colors hover:bg-gray-100 dark:hover:bg-dark-800"
    panel-class="right-0 w-64"
    test-id="portal-avatar-trigger"
    @open="syncTheme"
  >
    <template #trigger>
      <span v-if="avatarUrl" class="flex h-8 w-8 items-center justify-center overflow-hidden rounded-xl bg-primary-600">
        <img :src="avatarUrl" :alt="displayName" class="h-full w-full object-cover" />
      </span>
      <span
        v-else
        class="avatar-cat flex h-10 w-10 items-center justify-center bg-violet-50 text-gray-900 dark:bg-violet-900 dark:text-dark-100"
        data-testid="portal-avatar-cat"
      >
        <PixelIcon name="cat" size="md" />
      </span>
      <span class="hidden max-w-[10rem] truncate text-sm font-medium text-gray-900 dark:text-white md:block">
        {{ displayName }}
      </span>
      <PixelIcon name="chevronDown" size="sm" class="hidden text-gray-400 md:block" />
    </template>

    <div class="border-b border-gray-100 px-4 py-3 dark:border-dark-700">
      <div class="truncate text-sm font-medium text-gray-900 dark:text-white">{{ displayName }}</div>
      <div class="truncate text-xs text-gray-500 dark:text-dark-400">{{ user.email }}</div>
    </div>

    <div class="py-1">
      <router-link
        v-for="link in links"
        :key="link.id"
        :to="link.path"
        role="menuitem"
        class="dropdown-item"
        :data-testid="`portal-avatar-${link.id}`"
      >
        <PixelIcon :name="link.icon" size="sm" />
        {{ pt(`shell.links.${link.id}`) }}
      </router-link>
    </div>

    <div class="space-y-2 border-t border-gray-100 px-4 py-3 dark:border-dark-700">
      <div class="flex items-center justify-between gap-3 text-xs">
        <span class="text-gray-500 dark:text-dark-400">{{ pt('shell.avatar.language') }}</span>
        <div class="flex gap-1" role="group" :aria-label="pt('shell.avatar.language')">
          <button
            v-for="option in availableLocales"
            :key="option.code"
            type="button"
            class="rounded-lg px-2 py-1 font-medium transition-colors"
            :class="option.code === locale ? activeChip : idleChip"
            :aria-pressed="option.code === locale"
            @click="selectLocale(option.code)"
          >
            {{ option.name }}
          </button>
        </div>
      </div>
      <div class="flex items-center justify-between gap-3 text-xs">
        <span class="text-gray-500 dark:text-dark-400">{{ pt('shell.avatar.theme') }}</span>
        <div class="flex gap-1" role="group" :aria-label="pt('shell.avatar.theme')">
          <button
            v-for="option in themeOptions"
            :key="option.dark ? 'dark' : 'light'"
            type="button"
            class="flex items-center gap-1 rounded-lg px-2 py-1 font-medium transition-colors"
            :class="option.dark === isDark ? activeChip : idleChip"
            :aria-pressed="option.dark === isDark"
            :data-testid="`portal-theme-${option.dark ? 'dark' : 'light'}`"
            @click="setTheme(option.dark)"
          >
            <PixelIcon :name="option.dark ? 'moon' : 'sun'" size="xs" />
            {{ pt(option.label) }}
          </button>
        </div>
      </div>
    </div>

    <div v-if="contactInfo" class="border-t border-gray-100 px-4 py-2.5 text-xs dark:border-dark-700">
      <span class="text-gray-500 dark:text-dark-400">{{ pt('shell.avatar.contact') }}:</span>
      <span class="ml-1 font-medium text-gray-700 dark:text-gray-300">{{ contactInfo }}</span>
    </div>

    <div class="border-t border-gray-100 py-1 dark:border-dark-700">
      <button
        type="button"
        role="menuitem"
        class="dropdown-item w-full text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20"
        data-testid="portal-logout"
        @click="logout"
      >
        <PixelIcon name="login" size="sm" class="rotate-180" />
        {{ pt('shell.avatar.logout') }}
      </button>
    </div>
  </PortalDropdown>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import PixelIcon from '../components/common/PixelIcon.vue'
import { availableLocales, setLocale } from '@/i18n'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { usePortalI18n } from '../i18n'
import type { PortalNavLink } from './nav'
import PortalDropdown from './PortalDropdown.vue'

defineProps<{ links: PortalNavLink[] }>()

const { pt, locale } = usePortalI18n()
const router = useRouter()
const appStore = useAppStore()
const authStore = useAuthStore()

const activeChip = 'bg-primary-600 text-white'
const idleChip = 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-700 dark:text-dark-300 dark:hover:bg-dark-600'
const themeOptions = [
  { dark: false, label: 'shell.avatar.themeLight' },
  { dark: true, label: 'shell.avatar.themeDark' }
]

const user = computed(() => authStore.user)
const contactInfo = computed(() => appStore.contactInfo)
const avatarUrl = computed(() => user.value?.avatar_url?.trim() || '')
const displayName = computed(() => user.value?.username || user.value?.email?.split('@')[0] || '')
const isDark = ref(false)

// The theme can change elsewhere (e.g. the homepage toggle), so read it on open.
function syncTheme() {
  isDark.value = document.documentElement.classList.contains('dark')
}

function setTheme(dark: boolean) {
  isDark.value = dark
  document.documentElement.classList.toggle('dark', dark)
  localStorage.setItem('theme', dark ? 'dark' : 'light')
}

async function selectLocale(code: string) {
  if (code === locale.value) return
  await setLocale(code)
  // Mirrors upstream LocaleSwitcher: backend-localized data needs a reload.
  window.location.reload()
}

async function logout() {
  try {
    await authStore.logout()
  } catch (error) {
    console.error('Logout error:', error)
  }
  await router.push('/login')
}
</script>

<style scoped>
/* No photo: the 16×16 pixel cat at 2×, in a square double-rule control frame. */
.avatar-cat {
  border: 3px double currentColor;
}
</style>
