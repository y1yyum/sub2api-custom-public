<template>
  <button
    type="button"
    @click="toggleTheme"
    class="flex h-9 w-9 items-center justify-center rounded-xl p-1.5 text-gray-600 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-white"
    :title="themeTitle"
    :aria-label="themeTitle"
    data-testid="theme-toggle"
  >
    <Icon v-if="isDark" name="sun" size="sm" class="text-amber-500" />
    <Icon v-else name="moon" size="sm" class="text-gray-600 dark:text-dark-300" />
  </button>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const isDark = ref(false)
let observer: MutationObserver | null = null

const themeTitle = computed(() => {
  return isDark.value ? t('nav.lightMode') : t('nav.darkMode')
})

function updateState() {
  if (typeof document !== 'undefined') {
    isDark.value = document.documentElement.classList.contains('dark')
  }
}

function toggleTheme() {
  const next = !isDark.value
  isDark.value = next
  if (typeof document !== 'undefined') {
    document.documentElement.classList.toggle('dark', next)
    localStorage.setItem('theme', next ? 'dark' : 'light')
    window.dispatchEvent(new Event('theme-change'))
  }
}

onMounted(() => {
  updateState()
  if (typeof MutationObserver !== 'undefined' && typeof document !== 'undefined') {
    observer = new MutationObserver(() => {
      updateState()
    })
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['class']
    })
  }
  if (typeof window !== 'undefined') {
    window.addEventListener('theme-change', updateState)
  }
})

onBeforeUnmount(() => {
  if (observer) {
    observer.disconnect()
    observer = null
  }
  if (typeof window !== 'undefined') {
    window.removeEventListener('theme-change', updateState)
  }
})
</script>
