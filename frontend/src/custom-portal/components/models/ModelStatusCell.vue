<template>
  <button
    v-if="status.kind === 'row'"
    type="button"
    class="inline-flex items-center gap-1.5 whitespace-nowrap rounded-md px-1.5 py-0.5 text-xs font-medium text-gray-700 hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-dark-700"
    :title="pt(`pages.models.status.health.${health}`)"
    :aria-label="pt('pages.models.status.open', { model })"
    data-testid="model-status-cell"
    :data-health="health"
    @click="emit('open')"
  >
    <span class="h-2 w-2 shrink-0 rounded-full" :class="HEALTH_DOT[health]"></span>
    <span class="tabular-nums">{{ formatMonitorSuccessRateFromError(status.row.metrics.error_rate) }}</span>
  </button>
  <span v-else class="whitespace-nowrap text-xs text-gray-400 dark:text-dark-500" data-testid="model-status-cell">
    {{ pt(`pages.models.status.${status.kind}`) }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { HealthState } from '@/api/channelMonitorV2'
import { formatMonitorSuccessRateFromError } from '@/features/channel-monitor-v2/monitorFormat'
import { usePortalI18n } from '../../i18n'
import type { ModelStatusCellState } from '../../views/modelStatus'

const HEALTH_DOT: Record<HealthState, string> = {
  healthy: 'bg-emerald-500',
  warning: 'bg-amber-500',
  critical: 'bg-red-500',
  unknown: 'bg-gray-300 dark:bg-dark-500'
}

const props = defineProps<{ status: ModelStatusCellState; model: string }>()
const emit = defineEmits<{ open: [] }>()
const { pt } = usePortalI18n()

const health = computed<HealthState>(() =>
  props.status.kind === 'row' ? props.status.row.health.overall || 'unknown' : 'unknown'
)
</script>
