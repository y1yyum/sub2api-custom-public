<template>
  <div
    :class="[
      'portal-state',
      `portal-state-${kind}`,
      { 'portal-skeleton': kind === 'loading', 'portal-state-compact': compact }
    ]"
    :role="kind === 'error' ? 'alert' : 'status'"
    :aria-busy="kind === 'loading'"
  >
    <template v-if="kind === 'loading'">
      <span class="sr-only">{{ pt('refresh.loading') }}</span>
      <div aria-hidden="true" class="space-y-3">
        <div class="h-4 w-1/3 bg-gray-200 dark:bg-dark-700" />
        <div :class="compact ? 'h-6' : 'h-16'" class="bg-gray-100 dark:bg-dark-900" />
        <div class="h-4 w-2/3 bg-gray-200 dark:bg-dark-700" />
      </div>
    </template>
    <div v-else-if="kind === 'error'" class="portal-error-box">
      <p class="portal-error-title">
        <span class="portal-error-mark" aria-hidden="true" />{{ title || pt('refresh.errorTitle') }}
      </p>
      <p v-if="description" class="mt-1 text-sm">{{ description }}</p>
      <div class="mt-3 flex flex-wrap items-center gap-3">
        <button type="button" class="btn btn-secondary btn-sm" @click="$emit('retry')">
          {{ pt('refresh.retry') }}
        </button>
        <span v-if="lastSuccess" class="text-xs">
          {{ pt('refresh.lastSuccess', { time: lastSuccess }) }}
        </span>
      </div>
    </div>
    <template v-else>
      <PixelIcon name="key" size="lg" class="mb-3" />
      <p class="font-medium">{{ title || pt('refresh.emptyTitle') }}</p>
      <p v-if="description" class="mt-1 text-sm text-gray-500 dark:text-dark-400">
        {{ description }}
      </p>
      <ol v-if="guide" class="mt-4 grid gap-3 text-sm sm:grid-cols-3">
        <li
          v-for="(step, i) in [
            pt('refresh.emptySteps.0'),
            pt('refresh.emptySteps.1'),
            pt('refresh.emptySteps.2')
          ]"
          :key="i"
          class="portal-inset p-3"
        >
          <span class="mr-2 font-mono">0{{ i + 1 }}</span
          >{{ step }}
        </li>
      </ol>
      <slot />
    </template>
  </div>
</template>
<script setup lang="ts">
import PixelIcon from './PixelIcon.vue'
import { usePortalI18n } from '../../i18n'
defineProps<{
  kind: 'loading' | 'error' | 'empty'
  title?: string
  description?: string
  lastSuccess?: string
  guide?: boolean
  compact?: boolean
}>()
defineEmits<{ retry: [] }>()
const { pt } = usePortalI18n()
</script>
<style scoped>
.portal-state {
  min-height: 144px;
  padding: 24px;
}
.portal-state-compact {
  min-height: 0;
  padding: 12px;
}
.portal-state-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
}
.portal-skeleton {
  background-image: repeating-conic-gradient(var(--pp-dither) 0 25%, transparent 0 50%);
  background-size: 4px 4px;
}
.portal-error-box {
  border: 1px solid var(--pp-tone-peach-mid, #e8b4a6);
  background: var(--pp-tone-peach, #f7ece8);
  color: var(--pp-tone-peach-ink, #522b22);
  padding: 16px;
}
.portal-state-compact .portal-error-box {
  padding: 10px 12px;
}
.portal-error-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
}
.portal-error-mark {
  width: 8px;
  height: 8px;
  flex: none;
  background: var(--pp-tone-peach-mid, #e8b4a6);
  box-shadow: 0 0 0 1px currentColor;
}
</style>
