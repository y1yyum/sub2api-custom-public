<template>
  <AppLayout>
    <div class="space-y-4 pb-[calc(3rem+env(safe-area-inset-bottom))]" data-testid="model-channel-status">
      <header class="flex flex-wrap items-center gap-x-3 gap-y-1">
        <h1 class="m-0 flex items-center gap-2 text-lg font-black text-gray-900 dark:text-white">
          <Icon name="chart" size="sm" class="text-primary-500 dark:text-primary-400" />
          {{ t('modelChannelStatus.title') }}
        </h1>
        <span v-if="refreshing" class="inline-flex items-center gap-1 text-xs text-primary-600 dark:text-primary-300">
          <LoadingSpinner size="sm" />
          {{ t('modelChannelStatus.updating') }}
        </span>
        <span v-else-if="catalog?.coverage?.data_through" class="text-xs tabular-nums text-gray-500 dark:text-dark-400" data-testid="updated-to">
          {{ t('modelChannelStatus.updatedTo', { time: formatCheckTime(catalog.coverage.data_through, true) }) }}
        </span>
        <span v-if="catalog?.coverage && !catalog.coverage.coverage_complete" class="badge badge-warning">
          {{ t('modelChannelStatus.partialCoverage') }}
        </span>
        <span class="ml-auto flex items-center gap-2">
          <button
            type="button"
            class="btn btn-secondary btn-icon flex h-8 w-8 items-center justify-center"
            :title="t('common.refresh')"
            :aria-label="t('common.refresh')"
            :disabled="loading"
            data-testid="refresh"
            @click="load(false)"
          >
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
          </button>
        </span>
      </header>

      <section class="card px-4 py-3">
        <div class="flex flex-wrap items-center gap-2">
          <span
            v-if="callsStrip"
            class="badge badge-gray shrink-0"
            :title="t('modelChannelStatus.callsWindowHint', callsWindowParams)"
            data-testid="calls-window"
          >
            {{ t('modelChannelStatus.callsWindow', { count: callsStrip.call_window }) }}
          </span>
          <div v-else class="tabs inline-flex shrink-0" role="group" :aria-label="t('modelChannelStatus.rangeLabel')">
            <button
              v-for="option in ranges"
              :key="option"
              type="button"
              class="tab !px-2 !py-1 text-xs sm:!px-2.5"
              :class="range === option ? 'tab-active' : ''"
              :aria-pressed="range === option"
              :data-testid="`range-${option}`"
              @click="setRange(option)"
            >
              {{ t(`channelMonitorV2.ranges.${option}`) }}
            </button>
          </div>

          <FilterMultiSelect
            v-model="filters.platforms"
            compact
            :label="t('modelChannelStatus.filters.platform')"
            :all-label="t('modelChannelStatus.filters.allPlatforms')"
            :options="platformOptions"
          />

          <label class="relative inline-flex items-center">
            <span class="sr-only">{{ t('modelChannelStatus.filters.search') }}</span>
            <Icon name="search" size="sm" class="pointer-events-none absolute left-2.5 text-gray-400" />
            <input
              v-model="filters.search"
              type="search"
              class="input !h-8 !w-48 !py-1 !pl-8 text-xs"
              :placeholder="t('modelChannelStatus.filters.search')"
              data-testid="search-input"
            />
          </label>

          <label class="inline-flex cursor-pointer items-center gap-1.5 text-xs text-gray-600 dark:text-dark-300">
            <input v-model="issuesOnly" type="checkbox" class="rounded" data-testid="issues-only" />
            {{ t('modelChannelStatus.filters.issuesOnly') }}
          </label>

          <button
            type="button"
            class="btn btn-ghost btn-sm !px-2 !py-1 text-xs"
            :disabled="visibleGroups.length === 0 || filtersActive"
            data-testid="toggle-all"
            @click="allExpanded ? collapseAll() : expandAll()"
          >
            {{ allExpanded ? t('modelChannelStatus.collapseAll') : t('modelChannelStatus.expandAll') }}
          </button>

          <button
            v-if="filtersActive"
            type="button"
            class="btn btn-ghost btn-sm !px-2 !py-1 text-xs text-primary-600 dark:text-primary-300"
            data-testid="clear-filters"
            @click="clearFilters"
          >
            {{ t('modelChannelStatus.filters.clear') }}
          </button>
        </div>

        <ul
          class="m-0 mt-2 flex list-none flex-wrap items-center gap-x-3 gap-y-1 p-0 text-[11px] text-gray-500 dark:text-dark-400"
          :aria-label="t('modelChannelStatus.legend.title')"
        >
          <li
            v-for="state in legendStates"
            :key="state"
            class="inline-flex items-center gap-1"
            :title="healthLegendTitle(state)"
            :data-testid="`legend-${state}`"
          >
            <span class="h-2.5 w-2.5 rounded-sm" :class="healthBlockClass(state)" aria-hidden="true" />
            {{ state === 'unknown' ? t('modelChannelStatus.legend.unknown') : t(`modelChannelStatus.health.${state}`) }}
          </li>
          <li class="inline-flex items-center gap-2" data-testid="legend-cache">
            <span>{{ t('modelChannelStatus.legend.cache') }}</span>
            <span
              v-for="level in cacheLevels"
              :key="level"
              class="inline-flex items-center gap-1"
              :title="cacheLegendTitle(level)"
              :data-testid="`legend-cache-${level}`"
            >
              <span class="flex h-2.5 w-1.5 items-end" aria-hidden="true">
                <span class="w-full rounded-[1px] bg-gray-400 dark:bg-dark-400" :class="cacheHeightClass(level)" />
              </span>
              {{ t(`modelChannelStatus.legend.cacheLevel.${level}`) }}
            </span>
          </li>
        </ul>
      </section>

      <div
        v-if="catalog?.status_error_code"
        class="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900/40 dark:bg-amber-950/30 dark:text-amber-200"
        role="status"
        data-testid="status-unavailable"
      >
        {{ t('modelChannelStatus.statusUnavailable') }}
      </div>

      <div v-if="loading && !catalog" class="flex justify-center py-16" data-testid="loading">
        <LoadingSpinner />
      </div>

      <div
        v-else-if="loadError && !catalog"
        class="rounded-2xl border border-red-200 bg-red-50 px-5 py-6 text-center dark:border-red-900/40 dark:bg-red-950/30"
        role="alert"
        data-testid="load-error"
      >
        <p class="text-sm text-red-700 dark:text-red-300">{{ loadError }}</p>
        <button type="button" class="btn btn-secondary btn-sm mt-3" @click="load(false)">
          {{ t('modelChannelStatus.retry') }}
        </button>
      </div>

      <p
        v-else-if="catalog && visibleGroups.length === 0"
        class="py-16 text-center text-sm text-gray-500 dark:text-dark-400"
        data-testid="empty"
      >
        {{ sortedGroups.length === 0 ? t('modelChannelStatus.emptyAll') : t('modelChannelStatus.emptyFiltered') }}
      </p>

      <div v-else class="space-y-4">
        <CatalogGroupSection
          v-for="group in visibleGroups"
          :key="group.id"
          :group="group"
          :expanded="filtersActive || isExpanded(group.id)"
          :selected-key="selected?.row.key ?? null"
          :strip="catalog?.strip"
          @toggle="toggle"
          @select="onSelect"
        />
      </div>

      <CatalogDetailDrawer :selected="selected" @close="closeDetail" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import FilterMultiSelect from '@/features/channel-monitor-v2/FilterMultiSelect.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { platformLabel } from '@/utils/platformColors'
import { getCatalog } from '@/api/channelStatusCatalog'
import type { CatalogCacheLevel, CatalogGroup, CatalogHealth, CatalogModelRow, ChannelStatusCatalog } from '@/api/channelStatusCatalog'
import type { MonitorRange } from '@/api/channelMonitorV2'
import CatalogGroupSection from './CatalogGroupSection.vue'
import CatalogDetailDrawer from './CatalogDetailDrawer.vue'
import {
  EMPTY_FILTERS,
  cacheHeightClass,
  filterGroups,
  formatCheckTime,
  formatStep,
  healthBlockClass,
  isCallsStrip,
  sortGroups,
} from './catalogFormat'
import type { SelectedCatalogRow } from './catalogFormat'
import { useGroupExpansion } from './useGroupExpansion'

const AUTO_REFRESH_MS = 60_000
const ranges: MonitorRange[] = ['90m', '24h', '7d', '30d']
const legendStates: CatalogHealth[] = ['healthy', 'warning', 'critical', 'unknown']
const cacheLevels: CatalogCacheLevel[] = ['high', 'mid', 'low', 'none']

const { t } = useI18n()
const appStore = useAppStore()

const range = ref<MonitorRange>('24h')
const catalog = ref<ChannelStatusCatalog | null>(null)
const loading = ref(false)
const refreshing = ref(false)
const loadError = ref('')
const filters = reactive({ ...EMPTY_FILTERS, platforms: [] as string[] })
const selected = ref<SelectedCatalogRow | null>(null)

let controller: AbortController | null = null
let sequence = 0
let timer: number | null = null

const callsStrip = computed(() => {
  const strip = catalog.value?.strip
  return isCallsStrip(strip) ? strip : null
})
const callsWindowParams = computed(() => {
  const strip = callsStrip.value
  if (!strip) return {}
  return { count: strip.call_window, blocks: strip.blocks, per: Number((strip.call_window / strip.blocks).toFixed(1)) }
})

function healthLegendTitle(state: CatalogHealth): string | undefined {
  const strip = catalog.value?.strip
  if (state === 'unknown') return t('modelChannelStatus.legend.unknownHint', { min: strip?.min_calls ?? 1 })
  if (!strip) return undefined
  const { green, yellow } = strip.availability
  if (state === 'healthy') return t('modelChannelStatus.legend.successAtLeast', { rate: formatStep(green) })
  if (state === 'warning') return t('modelChannelStatus.legend.successBetween', { low: formatStep(yellow), high: formatStep(green) })
  return t('modelChannelStatus.legend.successBelow', { rate: formatStep(yellow) })
}

function cacheLegendTitle(level: CatalogCacheLevel): string | undefined {
  const steps = catalog.value?.strip?.cache
  if (!steps) return undefined
  if (level === 'high') return t('modelChannelStatus.legend.cacheAtLeast', { rate: formatStep(steps.high) })
  if (level === 'mid') return t('modelChannelStatus.legend.cacheBetween', { low: formatStep(steps.mid), high: formatStep(steps.high) })
  if (level === 'low') return t('modelChannelStatus.legend.cacheBetween', { low: formatStep(steps.low), high: formatStep(steps.mid) })
  return t('modelChannelStatus.legend.cacheNone', { rate: formatStep(steps.low) })
}

const sortedGroups = computed(() => sortGroups(catalog.value?.groups ?? []))
const issuesOnly = computed({
  get: () => filters.status === 'issues',
  set: (value: boolean) => {
    filters.status = value ? 'issues' : 'all'
  },
})
const filtersActive = computed(
  () => filters.platforms.length > 0 || filters.status !== 'all' || filters.search.trim() !== '',
)
const visibleGroups = computed(() => filterGroups(sortedGroups.value, filters))
const platformOptions = computed(() => {
  const seen = new Set<string>()
  for (const group of sortedGroups.value) {
    for (const row of group.models) seen.add(row.platform)
  }
  return [...seen].sort().map((value) => ({ value, label: platformLabel(value) }))
})

const { isExpanded, toggle, expandAll, collapseAll, allExpanded } = useGroupExpansion(sortedGroups)

async function load(silent: boolean) {
  controller?.abort()
  const current = new AbortController()
  controller = current
  const token = ++sequence
  if (silent) refreshing.value = true
  else loading.value = true
  try {
    const next = await getCatalog(range.value, current.signal)
    if (token !== sequence) return
    catalog.value = next
    loadError.value = ''
  } catch (error) {
    if (token !== sequence || current.signal.aborted) return
    const message = extractApiErrorMessage(error, t('modelChannelStatus.loadFailed'))
    if (catalog.value == null) loadError.value = message
    else if (!silent) appStore.showError(message)
  } finally {
    if (token === sequence) {
      loading.value = false
      refreshing.value = false
    }
  }
}

function setRange(next: MonitorRange) {
  if (range.value === next) return
  range.value = next
  void load(false)
}

function clearFilters() {
  filters.platforms = []
  filters.status = 'all'
  filters.search = ''
}

function onSelect(row: CatalogModelRow, group: CatalogGroup) {
  selected.value = { row, group }
}

function closeDetail() {
  selected.value = null
}

function startTimer() {
  timer = window.setInterval(() => {
    if (document.hidden) return
    void load(true)
  }, AUTO_REFRESH_MS)
}

onMounted(() => {
  void load(false)
  startTimer()
})

onBeforeUnmount(() => {
  controller?.abort()
  sequence++
  if (timer != null) window.clearInterval(timer)
})

defineExpose({ selected, closeDetail })
</script>
