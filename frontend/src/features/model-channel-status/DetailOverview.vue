<template>
  <section :aria-labelledby="headingId" data-testid="detail-overview">
    <h3 :id="headingId" class="mb-2 text-sm font-semibold text-gray-900 dark:text-white">
      {{ t('modelChannelStatus.detail.overview.title') }}
    </h3>
    <p v-if="block.error_code" class="text-sm text-amber-700 dark:text-amber-300" data-testid="overview-unavailable">
      {{ t('modelChannelStatus.detail.statusUnavailable') }}
    </p>
    <p v-else-if="!block.data || isEmpty(block.data)" class="text-sm text-gray-500 dark:text-dark-400" data-testid="overview-empty">
      {{ t('modelChannelStatus.detail.noData') }}
    </p>
    <template v-else>
      <dl class="m-0 grid grid-cols-2 gap-3 sm:grid-cols-3">
        <div>
          <dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('modelChannelStatus.detail.overview.status') }}</dt>
          <dd class="m-0 mt-0.5 flex items-center gap-1.5 text-sm font-medium" :class="healthTextClass(block.data.health)" data-testid="overview-health">
            <span class="h-2 w-2 rounded-full" :class="healthBlockClass(block.data.health)" aria-hidden="true" />
            {{ t(`modelChannelStatus.health.${block.data.health}`) }}
          </dd>
        </div>
        <div>
          <dt class="text-xs text-gray-500 dark:text-dark-400">{{ callsWindow ? t('modelChannelStatus.detail.overview.callsAvailability', { count: callsWindow }) : t('modelChannelStatus.detail.overview.availability') }}</dt>
          <dd class="m-0 mt-0.5 text-sm font-medium tabular-nums text-gray-900 dark:text-white" data-testid="overview-rate">
            {{ formatPercent(block.data.success_rate) }}
          </dd>
        </div>
        <div>
          <dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('modelChannelStatus.detail.overview.cacheRate') }}</dt>
          <dd class="m-0 mt-0.5 text-sm font-medium tabular-nums text-gray-900 dark:text-white" data-testid="overview-cache">
            {{ formatPercent(block.data.cache_rate) }}
          </dd>
        </div>
        <div>
          <dt class="text-xs text-gray-500 dark:text-dark-400" :title="t('modelChannelStatus.detail.overview.ttftHint')">{{ t('modelChannelStatus.detail.overview.ttft') }}</dt>
          <dd class="m-0 mt-0.5 text-sm font-medium tabular-nums text-gray-900 dark:text-white" data-testid="overview-ttft">
            {{ formatLatency(block.data.ttft_ms) }}
          </dd>
        </div>
        <div>
          <dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('modelChannelStatus.detail.overview.avgResponse') }}</dt>
          <dd class="m-0 mt-0.5 text-sm font-medium tabular-nums text-gray-900 dark:text-white" data-testid="overview-response">
            {{ formatLatency(block.data.avg_response_ms) }}
          </dd>
        </div>
        <div>
          <dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('modelChannelStatus.detail.overview.events') }}</dt>
          <dd class="m-0 mt-0.5 text-sm font-medium tabular-nums text-gray-900 dark:text-white" data-testid="overview-events">{{ block.data.event_count }}</dd>
        </div>
      </dl>
      <div class="mt-3">
        <CatalogStrip
          :buckets="block.data.buckets"
          :strip="strip"
          :label="callsWindow ? t('modelChannelStatus.detail.overview.callsTrend', { count: callsWindow }) : t('modelChannelStatus.detail.overview.trend')"
        />
        <p class="m-0 mt-1 flex justify-between text-[11px] text-gray-500 dark:text-dark-400" aria-hidden="true" data-testid="overview-trend-axis">
          <template v-if="callsWindow">
            <span>{{ t('modelChannelStatus.detail.overview.callsTrendStart', { count: callsWindow }) }}</span>
            <span>{{ t('modelChannelStatus.detail.overview.callsTrendEnd') }}</span>
          </template>
          <template v-else>
            <span>{{ t('modelChannelStatus.detail.overview.trendStart') }}</span>
            <span>{{ t('modelChannelStatus.detail.overview.trendEnd') }}</span>
          </template>
        </p>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CatalogBlock, CatalogOverview, CatalogStripConfig } from '@/api/channelStatusCatalog'
import CatalogStrip from './CatalogStrip.vue'
import { formatLatency, formatPercent, healthBlockClass, healthTextClass, isCallsStrip, isNoDataStrip } from './catalogFormat'

const props = defineProps<{ block: CatalogBlock<CatalogOverview>; strip?: CatalogStripConfig | null }>()

const { t } = useI18n()
const headingId = 'catalog-detail-overview-heading'
const callsWindow = computed(() => (isCallsStrip(props.strip) ? props.strip.call_window : 0))

function isEmpty(data: CatalogOverview): boolean {
  return data.success_rate == null && data.avg_response_ms == null && data.ttft_ms == null && data.event_count === 0 && isNoDataStrip(data.buckets)
}
</script>
