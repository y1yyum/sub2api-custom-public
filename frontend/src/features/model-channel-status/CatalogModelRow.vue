<template>
  <li :data-testid="`catalog-row-${row.key}`" :data-catalog-row-key="row.key">
    <button
      type="button"
      class="group/row flex w-full flex-wrap items-center gap-x-4 gap-y-2 px-5 py-2.5 text-left transition-colors hover:bg-gray-50 focus-visible:outline focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-primary-500 dark:hover:bg-dark-700/40"
      :class="selected ? 'bg-primary-50/60 dark:bg-primary-900/10' : ''"
      :aria-pressed="selected"
      :aria-label="t('modelChannelStatus.row.viewDetails', { name: row.name })"
      @click="emit('select', row)"
    >
      <span class="flex min-w-0 basis-56 items-center gap-2 sm:flex-1">
        <span class="truncate text-sm font-medium text-gray-900 dark:text-white">{{ row.name }}</span>
        <span
          class="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium"
          :class="platformBadgeLightClass(row.platform)"
        >
          {{ platformLabel(row.platform) }}
        </span>
      </span>

      <span class="flex min-w-[8rem] items-center gap-1.5 text-sm tabular-nums" data-testid="row-price">
        <span v-if="price.kind === 'unset'" class="text-gray-400 dark:text-dark-500">
          {{ t('modelChannelStatus.row.priceUnset') }}
        </span>
        <template v-else>
          <span class="font-medium text-gray-900 dark:text-white">{{ price.text }}</span>
          <span class="text-xs text-gray-500 dark:text-dark-400">{{ priceUnit }}</span>
          <span
            v-if="price.conflict"
            class="rounded bg-amber-50 px-1.5 py-0.5 text-[10px] font-medium text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
            :title="t('modelChannelStatus.row.multiPriceHint')"
            data-testid="row-multi-price"
          >
            {{ t('modelChannelStatus.row.multiPrice') }}
          </span>
        </template>
      </span>

      <span
        class="flex h-5 min-w-[10rem] flex-1 items-stretch gap-px sm:max-w-md"
        role="group"
        :aria-label="t('modelChannelStatus.row.trendLabel', { name: row.name })"
        data-testid="row-buckets"
      >
        <span
          v-for="(bucket, index) in row.buckets"
          :key="index"
          role="img"
          class="min-w-px flex-1 rounded-[1px]"
          :class="healthBlockClass(bucket.health)"
          :title="blockLabel(bucket)"
          :aria-label="blockLabel(bucket)"
        />
        <span
          v-if="row.buckets.length === 0"
          role="img"
          class="flex-1 rounded-[1px]"
          :class="healthBlockClass('unknown')"
          :aria-label="t('modelChannelStatus.health.unknown')"
        />
      </span>

      <span class="w-16 shrink-0 text-right text-xs tabular-nums text-gray-500 dark:text-dark-400" data-testid="row-events">
        {{ row.event_count > 0 ? t('modelChannelStatus.row.events', { count: row.event_count }) : t('modelChannelStatus.row.noEvents') }}
      </span>
      <Icon name="chevronRight" size="sm" class="shrink-0 text-gray-400 group-hover/row:text-gray-600 dark:text-dark-500" />
    </button>
  </li>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { platformBadgeLightClass, platformLabel } from '@/utils/platformColors'
import type { CatalogBucket, CatalogModelRow } from '@/api/channelStatusCatalog'
import { bucketLabel, healthBlockClass, inlinePrice } from './catalogFormat'

const props = defineProps<{
  row: CatalogModelRow
  selected?: boolean
}>()
const emit = defineEmits<{ select: [row: CatalogModelRow] }>()

const { t, locale } = useI18n()

function blockLabel(bucket: CatalogBucket): string {
  return bucketLabel(bucket, t, locale.value)
}

const price = computed(() => inlinePrice(props.row))
const priceUnit = computed(() => {
  if (price.value.kind === 'token') return t('modelChannelStatus.row.perMillion')
  return props.row.billing_mode === 'image'
    ? t('modelChannelStatus.row.perImage')
    : t('modelChannelStatus.row.perRequest')
})
</script>
