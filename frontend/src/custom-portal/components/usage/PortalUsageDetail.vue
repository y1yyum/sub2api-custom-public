<template>
  <details class="mt-2 max-w-sm whitespace-normal text-xs" data-testid="usage-row-detail">
    <summary class="cursor-pointer text-primary-600 dark:text-primary-300">
      {{ pt('refresh.details') }}
    </summary>
    <div
      class="mt-2 space-y-3 border border-gray-200 bg-gray-50 p-3 dark:border-dark-600 dark:bg-dark-900"
    >
      <p class="flex items-start gap-2">
        <span class="shrink-0">{{ pt('refresh.requestId') }}</span
        ><code class="break-all">{{ row.request_id || '—' }}</code
        ><button
          v-if="row.request_id"
          type="button"
          class="shrink-0 underline"
          @click="copyToClipboard(row.request_id)"
        >
          {{ copied ? pt('refresh.copied') : pt('refresh.copy') }}
        </button>
      </p>
      <dl class="grid grid-cols-[1fr_auto_auto] gap-x-3 gap-y-2">
        <template v-for="part in parts" :key="part.label"
          ><dt>{{ pt(`refresh.${part.label}`) }}</dt>
          <dd class="m-0 text-right font-mono">{{ part.tokens.toLocaleString() }}</dd>
          <dd class="m-0 text-right font-mono">${{ part.cost.toFixed(6) }}</dd></template
        >
      </dl>
    </div>
  </details>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import type { UsageLog } from '@/types'
import { useClipboard } from '@/composables/useClipboard'
import { usePortalI18n } from '../../i18n'
const props = defineProps<{ row: UsageLog }>()
const { pt } = usePortalI18n()
const { copied, copyToClipboard } = useClipboard()
const parts = computed(() => [
  { label: 'input', tokens: props.row.input_tokens, cost: props.row.input_cost },
  { label: 'output', tokens: props.row.output_tokens, cost: props.row.output_cost },
  {
    label: 'cacheWrite',
    tokens: props.row.cache_creation_tokens,
    cost: props.row.cache_creation_cost
  },
  { label: 'cacheRead', tokens: props.row.cache_read_tokens, cost: props.row.cache_read_cost }
])
</script>
