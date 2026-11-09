<template>
  <section :aria-labelledby="headingId" data-testid="detail-events">
    <h3 :id="headingId" class="mb-2 text-sm font-semibold text-gray-900 dark:text-white">
      {{ t('modelChannelStatus.detail.events.title') }}
    </h3>
    <p v-if="block.error_code" class="text-sm text-amber-700 dark:text-amber-300" data-testid="events-unavailable">
      {{ t('modelChannelStatus.detail.statusUnavailable') }}
    </p>
    <p v-else-if="!block.data || block.data.items.length === 0" class="text-sm text-gray-500 dark:text-dark-400" data-testid="events-empty">
      {{ callsWindow ? t('modelChannelStatus.detail.events.callsNone', { count: callsWindow }) : t('modelChannelStatus.detail.events.none') }}
    </p>
    <template v-else>
      <p class="mb-2 text-xs text-gray-500 dark:text-dark-400">
        {{ t(callsWindow ? 'modelChannelStatus.detail.events.callsTotal' : 'modelChannelStatus.detail.events.total', { count: callsWindow, total: block.data.total, shown: block.data.items.length }) }}
      </p>
      <ul class="event-timeline m-0 list-none divide-y divide-gray-100 p-0 dark:divide-dark-700/60">
        <li v-for="(item, index) in block.data.items" :key="index" class="flex flex-wrap items-center gap-x-3 gap-y-1 py-2 text-sm" data-testid="event-item">
          <span class="inline-flex items-center gap-1.5 font-medium" :class="healthTextClass(item.level)">
            <span class="h-2 w-2 rounded-full" :class="healthBlockClass(item.level)" aria-hidden="true" />
            {{ levelLabel(item.level) }}
          </span>
          <time class="text-xs tabular-nums text-gray-500 dark:text-dark-400" :datetime="item.start">{{ formatStart(item.start) }}</time>
          <span v-if="item.duration_seconds != null" class="text-xs text-gray-500 dark:text-dark-400" data-testid="event-duration">
            {{ t('modelChannelStatus.detail.events.duration', { duration: formatDuration(item.duration_seconds) }) }}
          </span>
          <span v-if="item.min_success_rate != null" class="text-xs tabular-nums text-gray-500 dark:text-dark-400">
            {{ t('modelChannelStatus.detail.events.minRate', { rate: formatPercent(item.min_success_rate) }) }}
          </span>
        </li>
      </ul>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CatalogBlock, CatalogEvents, CatalogHealth, CatalogStripConfig } from '@/api/channelStatusCatalog'
import { formatDuration, formatPercent, healthBlockClass, healthTextClass, isCallsStrip } from './catalogFormat'

const props = defineProps<{ block: CatalogBlock<CatalogEvents>; strip?: CatalogStripConfig | null }>()

const { t, locale } = useI18n()
const headingId = 'catalog-detail-events-heading'
const callsWindow = computed(() => (isCallsStrip(props.strip) ? props.strip.call_window : 0))

function levelLabel(level: CatalogHealth): string {
  return level === 'critical'
    ? t('modelChannelStatus.detail.events.level.critical')
    : t('modelChannelStatus.detail.events.level.warning')
}

function formatStart(start: string): string {
  return new Date(start).toLocaleString(locale.value, {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}
</script>
<style scoped>
.event-timeline { border-left: 3px double var(--pp-control); margin-left: 4px; padding-left: 16px; }
.event-timeline > li { position: relative; }
.event-timeline > li::before { content: ''; position: absolute; left: -21px; top: 14px; width: 7px; height: 7px; background: var(--pp-tone-peach-mid); border: 1px solid var(--pp-control); }
</style>
