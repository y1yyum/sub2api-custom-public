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
          v-if="row.platform !== groupPlatform"
          class="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium"
          :class="platformBadgeLightClass(row.platform)"
          data-testid="row-platform"
        >
          {{ platformLabel(row.platform) }}
        </span>
      </span>

      <span class="flex min-w-[11rem] items-center gap-1.5 text-sm tabular-nums" data-testid="row-price">
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
        v-if="noData"
        class="flex h-5 min-w-[10rem] flex-1 items-center text-xs text-gray-400 dark:text-dark-500 sm:max-w-md"
        data-testid="row-no-data"
      >
        {{ t('modelChannelStatus.row.noData') }}
      </span>
      <CatalogStrip
        v-else
        class="min-w-[10rem] flex-1 sm:max-w-md"
        :buckets="row.buckets"
        :strip="strip"
        :label="t('modelChannelStatus.row.trendLabel', { name: row.name })"
        data-testid="row-buckets"
      />

      <span
        class="w-20 shrink-0 text-right text-xs tabular-nums"
        :class="row.intelligence ? 'text-gray-700 dark:text-dark-200' : 'text-gray-400 dark:text-dark-500'"
        :title="intelligenceTitle"
        data-testid="row-intelligence"
      >
        <span class="text-gray-500 dark:text-dark-400">{{ t('modelChannelStatus.row.intelligence') }}</span>
        {{ row.intelligence ? `${row.intelligence.passed}/${row.intelligence.scored}` : '—' }}
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
import type { CatalogModelRow, CatalogStripConfig } from '@/api/channelStatusCatalog'
import CatalogStrip from './CatalogStrip.vue'
import { inlinePrice, isNoDataStrip } from './catalogFormat'

const props = defineProps<{
  row: CatalogModelRow
  groupPlatform?: string
  selected?: boolean
  strip?: CatalogStripConfig | null
}>()
const emit = defineEmits<{ select: [row: CatalogModelRow] }>()

const { t } = useI18n()

const price = computed(() => inlinePrice(props.row))
const noData = computed(() => isNoDataStrip(props.row.buckets))
const intelligenceTitle = computed(() =>
  props.row.intelligence ? t('modelChannelStatus.row.intelligenceTitle', { ...props.row.intelligence }) : undefined,
)
const priceUnit = computed(() => {
  if (price.value.kind === 'token') return t('modelChannelStatus.row.perMillion')
  return props.row.billing_mode === 'image'
    ? t('modelChannelStatus.row.perImage')
    : t('modelChannelStatus.row.perRequest')
})
</script>
