<template>
  <div class="group relative overflow-hidden rounded-xl border border-gray-200/80 bg-gray-900/95 font-mono text-xs text-gray-200 shadow-xl dark:border-dark-700 dark:bg-dark-900/95">
    <!-- Top toolbar -->
    <div class="flex items-center justify-between border-b border-gray-800 bg-gray-950/70 px-4 py-2.5 text-gray-400">
      <div class="flex items-center gap-2">
        <span class="flex gap-1.5">
          <span class="h-2.5 w-2.5 rounded-full bg-red-500/80"></span>
          <span class="h-2.5 w-2.5 rounded-full bg-amber-500/80"></span>
          <span class="h-2.5 w-2.5 rounded-full bg-emerald-500/80"></span>
        </span>
        <span v-if="title" class="ml-2 font-sans text-xs font-medium text-gray-400">{{ title }}</span>
        <span v-else-if="language" class="ml-2 font-mono text-[11px] uppercase tracking-wider text-primary-400">{{ language }}</span>
      </div>
      <button
        type="button"
        @click="copyCode"
        class="inline-flex items-center gap-1.5 rounded-md border border-gray-700/60 bg-gray-800/80 px-2.5 py-1 text-[11px] font-sans font-medium text-gray-300 transition-all hover:border-primary-500/60 hover:bg-gray-700/80 hover:text-white active:scale-95"
        :class="{ '!border-emerald-500/60 !text-emerald-400': copied }"
      >
        <svg v-if="copied" class="h-3.5 w-3.5 text-emerald-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M4.5 12.75l6 6 9-13.5" />
        </svg>
        <svg v-else class="h-3.5 w-3.5 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M15.666 3.888A2.25 2.25 0 0013.5 2.25h-3c-1.03 0-1.9.693-2.166 1.638m7.332 0c.055.194.084.4.084.612v0a.75.75 0 01-.75.75H9a.75.75 0 01-.75-.75v0c0-.212.03-.418.084-.612m7.332 0c.646.049 1.288.11 1.927.184 1.1.128 1.907 1.077 1.907 2.185V19.5a2.25 2.25 0 01-2.25 2.25H6.75A2.25 2.25 0 014.5 19.5V6.257c0-1.108.806-2.057 1.907-2.185a48.208 48.208 0 011.927-.184" />
        </svg>
        <span>{{ copied ? (copiedText || pt('common.copied')) : (copyText || pt('common.copy')) }}</span>
      </button>
    </div>

    <!-- Code body -->
    <div class="relative overflow-x-auto p-4 leading-relaxed">
      <pre class="overflow-x-auto"><code>{{ code.trim() }}</code></pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { usePortalI18n } from '../../i18n'

const { pt } = usePortalI18n()

const props = withDefaults(
  defineProps<{
    code: string
    language?: string
    title?: string
    copyText?: string
    copiedText?: string
  }>(),
  {
    language: 'bash',
    title: '',
    copyText: '',
    copiedText: ''
  }
)

const copied = ref(false)

async function copyCode() {
  try {
    await navigator.clipboard.writeText(props.code.trim())
    copied.value = true
    setTimeout(() => {
      copied.value = false
    }, 2000)
  } catch (err) {
    console.error('Failed to copy code:', err)
  }
}
</script>
