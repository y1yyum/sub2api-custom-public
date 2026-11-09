<template>
  <section
    class="card catalog-group-card flex min-w-0 flex-col overflow-hidden"
    :data-testid="`catalog-group-${group.id}`"
    :data-health="group.summary.health"
  >
    <header class="group-card-header space-y-3 border-b border-gray-200 p-4 dark:border-dark-700">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <GroupBadge
          class="group-card-title"
          :name="group.name"
          :platform="group.platform as GroupPlatform"
          :rate-multiplier="group.rate_multiplier"
          :user-rate-multiplier="group.user_rate_multiplier"
          :peak-rate-enabled="group.peak_rate_enabled"
          :peak-start="group.peak_start"
          :peak-end="group.peak_end"
          :peak-rate-multiplier="group.peak_rate_multiplier"
          always-show-rate
        />
        <span v-if="group.is_exclusive" class="badge badge-purple">{{
          t('modelChannelStatus.group.exclusive')
        }}</span>
        <span class="text-xs text-gray-500 dark:text-dark-400"
          >{{ pt('refresh.models', { count: group.models.length }) }} ·
          {{ formatPercent(group.summary.availability_rate) }}</span
        >
      </div>
      <div
        v-if="group.models.length > 1"
        class="flex flex-wrap gap-1.5"
        role="group"
        :aria-label="group.name"
      >
        <button
          v-for="model in group.models"
          :key="model.key"
          type="button"
          class="model-switch inline-flex min-w-0 items-center gap-2 border border-gray-300 px-2 py-1.5 text-xs dark:border-dark-600"
          :class="
            active?.key === model.key
              ? 'bg-gray-900 text-white dark:bg-dark-100 dark:text-dark-900'
              : 'bg-white dark:bg-dark-800'
          "
          :aria-pressed="active?.key === model.key"
          :title="`${model.name}: ${t(`modelChannelStatus.health.${model.health}`)}`"
          @click="activeKey = model.key"
        >
          <span
            class="h-2 w-2 shrink-0"
            :class="healthBlockClass(model.health)"
            aria-hidden="true"
          /><span class="break-all font-mono">{{ model.name }}</span>
        </button>
      </div>
      <p v-else-if="active" class="break-all font-mono text-sm">{{ active.name }}</p>
    </header>
    <div v-if="active" class="flex flex-1 flex-col gap-3 p-4" :data-catalog-row-key="active.key">
      <div class="flex items-center justify-between gap-2 text-xs">
        <span class="flex items-center gap-2" :class="healthTextClass(active.health)"
          ><span class="h-2 w-2" :class="healthBlockClass(active.health)" aria-hidden="true" />{{
            t(`modelChannelStatus.health.${active.health}`)
          }}</span
        >
        <span
          :class="active.event_count ? 'badge badge-warning' : 'text-gray-500 dark:text-dark-400'"
          >{{
            active.event_count
              ? t('modelChannelStatus.row.events', { count: active.event_count })
              : pt('refresh.noEvents')
          }}</span
        >
      </div>
      <div class="flex flex-wrap items-end justify-between gap-3">
        <strong class="availability">{{ formatPercent(active.success_rate) }}</strong>
        <div class="text-right text-xs">
          <p class="font-mono" data-testid="row-price">
            {{ price.text || t('modelChannelStatus.row.priceUnset') }}
          </p>
          <p class="mt-1 text-gray-500 dark:text-dark-400">
            {{
              price.kind === 'token'
                ? t('modelChannelStatus.row.perMillion')
                : t(
                    active.billing_mode === 'image'
                      ? 'modelChannelStatus.row.perImage'
                      : 'modelChannelStatus.row.perRequest'
                  )
            }}
          </p>
          <span v-if="price.conflict" class="badge badge-warning">{{
            t('modelChannelStatus.row.multiPrice')
          }}</span>
        </div>
      </div>
      <CatalogStrip
        class="!h-6"
        :buckets="active.buckets"
        :strip="strip"
        :label="t('modelChannelStatus.detail.overview.trend')"
        data-testid="row-buckets"
      />
      <p class="flex justify-between text-xs text-gray-500 dark:text-dark-400">
        <span>{{
          isCallsStrip(strip)
            ? t('modelChannelStatus.detail.overview.callsTrendStart', { count: strip.call_window })
            : t('modelChannelStatus.detail.overview.trendStart')
        }}</span
        ><span>{{ t('modelChannelStatus.detail.overview.trendEnd') }}</span>
      </p>
      <footer
        class="mt-auto flex flex-wrap items-center gap-3 border-t border-dashed border-gray-200 pt-3 text-xs dark:border-dark-700"
      >
        <span
          >{{ t('modelChannelStatus.row.intelligence') }}
          {{
            active.intelligence
              ? `${active.intelligence.passed}/${active.intelligence.scored}`
              : '—'
          }}</span
        >
        <span :title="pt('refresh.bucketCache')"
          >{{ pt('refresh.cache') }} {{ formatPercent(cacheRate) }}</span
        >
        <button
          type="button"
          class="ml-auto font-semibold underline-offset-4 hover:underline"
          data-testid="group-details"
          @click="$emit('select', active, group)"
        >
          {{ pt('refresh.details') }} →
        </button>
      </footer>
    </div>
  </section>
</template>
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import GroupBadge from '@/components/common/GroupBadge.vue'
import type { GroupPlatform } from '@/types'
import type { CatalogGroup, CatalogModelRow, CatalogStripConfig } from '@/api/channelStatusCatalog'
import { usePortalI18n } from '@/custom-portal/i18n'
import CatalogStrip from './CatalogStrip.vue'
import {
  formatPercent,
  healthBlockClass,
  healthTextClass,
  inlinePrice,
  isCallsStrip
} from './catalogFormat'
import { rowCacheRate, worstModel } from './platformCards'
const props = defineProps<{ group: CatalogGroup; strip: CatalogStripConfig }>()
defineEmits<{ select: [row: CatalogModelRow, group: CatalogGroup] }>()
const { t } = useI18n()
const { pt } = usePortalI18n()
const activeKey = ref<string | null>(null)
const active = computed(
  () =>
    props.group.models.find((row) => row.key === activeKey.value) ?? worstModel(props.group.models)
)
const price = computed(() =>
  active.value ? inlinePrice(active.value) : { kind: 'unset', text: '', conflict: false }
)
const cacheRate = computed(() => (active.value ? rowCacheRate(active.value) : null))
</script>
<style scoped>
.catalog-group-card {
  position: relative;
}
.catalog-group-card::before {
  content: '';
  height: 4px;
  background: var(--pp-control);
}
.catalog-group-card[data-health='healthy']::before {
  background: var(--pp-tone-mint-mid);
}
.catalog-group-card[data-health='warning']::before {
  background: var(--pp-tone-butter-mid);
}
.catalog-group-card[data-health='critical']::before {
  background: var(--pp-tone-peach-mid);
}
.availability {
  font: 400 36px/40px var(--pp-font-pixel);
  font-synthesis: none;
}
.model-switch:focus-visible {
  outline: 2px solid var(--pp-ink);
  outline-offset: 2px;
}
.group-card-header { min-height: 108px; }
.group-card-title { padding: 0; gap: 8px; background: transparent; color: var(--pp-ink); }
.group-card-title :deep(> svg), .group-card-title :deep(> img) { display: none; }
.group-card-title :deep(> span:first-of-type) { font-size: 16px; font-weight: 600; }
.group-card-title :deep(> span:nth-of-type(2)) { border: 3px double var(--pp-control); background: var(--pp-tone-mint); color: var(--pp-ink); padding: 1px 6px; font: 400 12px/16px var(--pp-font-pixel); }
.catalog-group-card > div { gap: 10px; }
.catalog-group-card[data-health='critical'] > div { background: var(--pp-tone-peach); }
.model-switch { font-size: 12px; padding: 4px 6px; }
.catalog-group-card footer { margin-top: 8px; padding-top: 12px; }
</style>
