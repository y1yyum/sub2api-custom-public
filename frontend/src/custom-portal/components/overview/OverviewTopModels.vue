<template>
  <section
    class="card flex min-w-0 flex-col p-4"
    :aria-labelledby="titleId"
    :aria-busy="state.loading.value"
    data-testid="overview-top-models"
  >
    <div class="mb-4 flex min-h-7 items-center justify-between gap-2">
      <div class="flex min-w-0 items-baseline gap-2">
        <h2 :id="titleId" class="flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white">
          <PixelIcon name="chartBar" size="sm" class="text-primary-600 dark:text-primary-500" aria-hidden="true" />
          {{ pt('pages.overview.ranks.title') }}
        </h2>
        <span class="truncate text-xs text-gray-500 dark:text-dark-400" data-testid="overview-top-models-subtitle">
          {{ pt(`pages.overview.ranks.by.${rankMetric}`) }}
        </span>
      </div>
      <router-link
        to="/usage"
        class="inline-flex shrink-0 items-center gap-1 text-xs font-medium text-primary-600 hover:text-primary-700 dark:text-primary-500 dark:hover:text-primary-400"
        data-testid="overview-top-models-all"
      >
        {{ pt('pages.overview.ranks.viewAll') }}
        <PixelIcon name="chevronRight" size="xs" aria-hidden="true" />
      </router-link>
    </div>

    <p
      v-if="failed"
      class="flex flex-1 items-center justify-center py-8 text-sm text-red-600 dark:text-red-400"
      role="alert"
      data-testid="overview-top-models-error"
    >
      {{ pt('pages.overview.ranks.error') }}
    </p>
    <div
      v-else-if="!rows.length"
      class="flex flex-1 flex-col items-center justify-center gap-2 py-8 text-sm text-gray-500 dark:text-dark-400"
      data-testid="overview-top-models-empty"
    >
      <PixelIcon name="cube" size="lg" class="text-gray-300 dark:text-dark-600" aria-hidden="true" />
      {{ pt('pages.overview.ranks.empty') }}
    </div>
    <ol v-else class="flex flex-1 flex-col gap-1">
      <li v-for="(row, index) in rows" :key="row.key">
        <button
          type="button"
          class="group w-full rounded-lg px-2 py-2 text-left transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 motion-reduce:transition-none"
          :class="row.active ? 'bg-primary-500/10 ring-1 ring-primary-500/20' : 'hover:bg-gray-50 dark:hover:bg-dark-800'"
          :aria-pressed="row.active"
          :title="pt(row.active ? 'pages.overview.ranks.clearFilter' : 'pages.overview.ranks.applyFilter')"
          :data-testid="`overview-top-model-${row.key}`"
          @click="state.toggleModel(row.key)"
        >
          <span class="flex min-w-0 items-center gap-2">
            <span
              class="rank-index w-4 shrink-0 text-center text-xs tabular-nums"
              :class="index === 0 ? 'rank-first text-gray-900 dark:text-white' : 'text-gray-400 dark:text-dark-500'"
            >
              {{ index + 1 }}
            </span>
            <ModelIcon :model="row.key" size="16px" class="shrink-0" />
            <span
              class="min-w-0 flex-1 truncate text-sm"
              :class="row.active ? 'font-medium text-primary-700 dark:text-primary-400' : 'text-gray-800 dark:text-dark-100'"
            >
              {{ row.key }}
            </span>
            <span class="shrink-0 text-sm font-medium tabular-nums text-gray-900 dark:text-white">{{ row.value }}</span>
          </span>
          <span class="mt-2 flex items-center gap-2 pl-6">
            <span class="rank-track h-3 flex-1 overflow-hidden bg-white text-gray-400 dark:bg-dark-800 dark:text-dark-500">
              <span
                class="rank-fill block h-full bg-primary-300 transition-[width] duration-500 motion-reduce:transition-none dark:bg-primary-600"
                :style="{ width: `${row.share * 100}%`, opacity: barOpacity(index) }"
              ></span>
            </span>
            <span class="w-12 shrink-0 text-right text-xs tabular-nums text-gray-500 dark:text-dark-400">{{ row.shareText }}</span>
          </span>
        </button>
      </li>
      <li
        v-if="others"
        class="mt-auto flex items-center justify-between gap-2 px-2 pt-2 text-xs text-gray-500 dark:text-dark-400"
        data-testid="overview-top-models-others"
      >
        <span class="truncate">{{ pt('pages.overview.ranks.others', { count: others.count }) }}</span>
        <span class="shrink-0 tabular-nums">{{ others.value }} · {{ others.shareText }}</span>
      </li>
    </ol>
  </section>
</template>

<script setup lang="ts">
import { computed, useId } from 'vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import PixelIcon from '../common/PixelIcon.vue'
import { formatCompactNumber } from '@/utils/format'
import { usePortalI18n } from '../../i18n'
import { injectOverviewState } from '../../overview/usageChartState'
import { rankModels, toRankMetric } from '../../overview/topRanks'

const { pt } = usePortalI18n()
const state = injectOverviewState()
const titleId = `${useId()}-title`

const rankMetric = computed(() => toRankMetric(state.metric.value))
const failed = computed(() => state.failedParts.value.includes('models'))
const ranked = computed(() => rankModels(state.models.value, state.metric.value))

function formatValue(value: number): string {
  if (rankMetric.value === 'tokens') return formatCompactNumber(value)
  if (rankMetric.value === 'requests') return value.toLocaleString()
  return `$${value.toFixed(value >= 1 ? 2 : 4)}`
}

const formatShare = (share: number) => `${Math.round(share * 100)}%`

/** Bars fade down the ranking so the leader reads first. */
const barOpacity = (index: number) => Math.max(0.35, 1 - index * 0.15)

const rows = computed(() =>
  ranked.value
    .filter((item) => !item.isOther)
    .map((item) => ({
      key: item.key,
      value: formatValue(item.value),
      share: Math.min(item.share, 1),
      shareText: formatShare(item.share),
      active: state.filters.value.model === item.key
    }))
)

const others = computed(() => {
  const rest = ranked.value.find((item) => item.isOther)
  if (!rest) return null
  return {
    count: state.models.value.length - rows.value.length,
    value: formatValue(rest.value),
    shareText: formatShare(rest.share)
  }
})
</script>

<style scoped>
.rank-index {
  font-family: var(--pp-font-pixel);
  font-synthesis: none;
}

/* The leader's number sits on butter, the one warm accent in the ranking. */
.rank-first {
  background-color: var(--pp-tone-butter-mid);
}

/* Two rules around the track; the fill is inset 1px inside them and dithered. */
.rank-track {
  border: 3px double currentColor;
  padding: 1px;
}

.rank-fill {
  background-image: repeating-conic-gradient(rgba(44, 46, 51, 0.18) 0 25%, transparent 0 50%);
  background-size: 2px 2px;
}
</style>
