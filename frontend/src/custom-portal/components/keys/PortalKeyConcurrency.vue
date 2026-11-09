<template>
  <span class="flex items-center gap-2 text-xs tabular-nums text-gray-500 dark:text-dark-400" data-test="portal-key-concurrency">
    {{ pt('refresh.concurrency') }}
    <progress
      v-if="max > 0"
      class="h-2 w-16"
      :class="value >= max ? 'accent-amber-500' : 'accent-primary-500'"
      :value="value"
      :data-full="value >= max"
      :max="max"
      :aria-label="`${value} / ${max}`"
    />
    <span class="font-medium text-gray-900 dark:text-white">{{ value }}<template v-if="max > 0"> / {{ max }}</template></span>
  </span>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { usePortalI18n } from '../../i18n'
defineProps<{ value: number }>()
const auth = useAuthStore()
const { pt } = usePortalI18n()
const max = computed(() => auth.user?.concurrency ?? 0)
</script>
<style scoped>
progress { appearance: none; border: 1px solid var(--pp-control); background: var(--pp-surface-2); }
progress::-webkit-progress-bar { background: var(--pp-surface-2); }
progress::-webkit-progress-value { background: var(--pp-tone-sage); }
progress[data-full='true']::-webkit-progress-value { background: var(--pp-tone-butter-mid); }
progress::-moz-progress-bar { background: var(--pp-tone-sage); }
progress[data-full='true']::-moz-progress-bar { background: var(--pp-tone-butter-mid); }
</style>
