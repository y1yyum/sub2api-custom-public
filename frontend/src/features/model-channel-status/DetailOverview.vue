<template>
  <section :aria-labelledby="headingId" data-testid="detail-overview">
    <h3 :id="headingId" class="mb-2 text-sm font-semibold text-gray-900 dark:text-white">
      {{ t('modelChannelStatus.detail.overview.title') }}
    </h3>
    <p v-if="block.error_code" class="text-sm text-amber-700 dark:text-amber-300" data-testid="overview-unavailable">
      {{ t('modelChannelStatus.detail.statusUnavailable') }}
    </p>
    <p v-else-if="!block.data" class="text-sm text-gray-500 dark:text-dark-400" data-testid="overview-empty">
      {{ t('modelChannelStatus.detail.noData') }}
    </p>
    <template v-else>
      <dl class="grid grid-cols-2 gap-3">
        <div>
          <dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('modelChannelStatus.detail.overview.status') }}</dt>
          <dd class="mt-0.5 flex items-center gap-1.5 text-sm font-medium" :class="healthTextClass(block.data.health)" data-testid="overview-health">
            <span class="h-2 w-2 rounded-full" :class="healthBlockClass(block.data.health)" aria-hidden="true" />
            {{ t(`modelChannelStatus.health.${block.data.health}`) }}
          </dd>
        </div>
        <div>
          <dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('modelChannelStatus.detail.overview.availability') }}</dt>
          <dd class="mt-0.5 text-sm font-medium tabular-nums text-gray-900 dark:text-white" data-testid="overview-rate">
            {{ formatPercent(block.data.success_rate) }}
          </dd>
        </div>
        <div>
          <dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('modelChannelStatus.detail.overview.events') }}</dt>
          <dd class="mt-0.5 text-sm font-medium tabular-nums text-gray-900 dark:text-white">{{ block.data.event_count }}</dd>
        </div>
        <div>
          <dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('modelChannelStatus.detail.overview.avgResponse') }}</dt>
          <dd class="mt-0.5 text-sm font-medium tabular-nums text-gray-900 dark:text-white" data-testid="overview-response">
            {{ block.data.avg_response_ms == null ? '-' : `${Math.round(block.data.avg_response_ms)} ms` }}
          </dd>
        </div>
      </dl>
      <div class="mt-3">
        <p class="mb-1 text-xs text-gray-500 dark:text-dark-400">{{ t('modelChannelStatus.detail.overview.trend') }}</p>
        <div class="flex h-5 items-stretch gap-px" role="group" :aria-label="t('modelChannelStatus.detail.overview.trend')">
          <span
            v-for="(bucket, index) in block.data.buckets"
            :key="index"
            role="img"
            class="min-w-px flex-1 rounded-[1px]"
            :class="healthBlockClass(bucket.health)"
            :title="blockLabel(bucket)"
            :aria-label="blockLabel(bucket)"
          />
          <span
            v-if="block.data.buckets.length === 0"
            role="img"
            class="flex-1 rounded-[1px]"
            :class="healthBlockClass('unknown')"
            :aria-label="t('modelChannelStatus.health.unknown')"
          />
        </div>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { CatalogBlock, CatalogBucket, CatalogOverview } from '@/api/channelStatusCatalog'
import { bucketLabel, formatPercent, healthBlockClass, healthTextClass } from './catalogFormat'

defineProps<{ block: CatalogBlock<CatalogOverview> }>()

const { t, locale } = useI18n()
const headingId = 'catalog-detail-overview-heading'

function blockLabel(bucket: CatalogBucket): string {
  return bucketLabel(bucket, t, locale.value)
}
</script>
