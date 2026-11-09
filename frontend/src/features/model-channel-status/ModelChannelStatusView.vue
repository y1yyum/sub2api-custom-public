<template>
  <AppLayout>
    <div class="space-y-5" data-testid="model-channel-status">
      <PortalPageHeading
        :title="t('modelChannelStatus.title')"
        :description="pt('refresh.description.models')"
      >
        <span v-if="refreshing" class="text-xs">{{ t('modelChannelStatus.updating') }}</span>
        <span
          v-else-if="catalog?.coverage?.data_through"
          class="text-xs text-gray-500 dark:text-dark-400"
          data-testid="updated-to"
          >{{
            t('modelChannelStatus.updatedTo', {
              time: formatCheckTime(catalog.coverage.data_through, true)
            })
          }}</span
        >
        <span
          class="badge badge-gray"
          :title="
            callsStrip
              ? t('modelChannelStatus.callsWindowHint', {
                  count: callsStrip.call_window,
                  blocks: callsStrip.blocks,
                  per: Number((callsStrip.call_window / callsStrip.blocks).toFixed(1))
                })
              : undefined
          "
          data-testid="calls-window"
          >{{
            callsStrip
              ? t('modelChannelStatus.callsWindow', { count: callsStrip.call_window })
              : t('channelMonitorV2.ranges.24h')
          }}</span
        >
        <span
          v-if="catalog?.coverage && !catalog.coverage.coverage_complete"
          class="badge badge-warning"
          >{{ t('modelChannelStatus.partialCoverage') }}</span
        >
        <button
          type="button"
          class="btn btn-secondary btn-icon"
          :disabled="loading || refreshing"
          :aria-label="t('common.refresh')"
          data-testid="refresh"
          @click="load(false)"
        >
          <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading || refreshing }" />
        </button>
      </PortalPageHeading>
      <PortalState
        v-if="loadError"
        kind="error"
        :title="loadError"
        :last-success="
          catalog?.coverage?.data_through
            ? formatCheckTime(catalog.coverage.data_through, true)
            : undefined
        "
        data-testid="load-error"
        @retry="load(false)"
      />
      <p
        v-if="catalog?.status_error_code"
        class="border border-amber-300 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200"
        role="status"
        data-testid="status-unavailable"
      >
        {{ t('modelChannelStatus.statusUnavailable') }}
      </p>
      <div v-if="loading && !catalog" class="space-y-4" data-testid="loading">
        <PortalState kind="loading" compact class="card" />
        <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3" aria-hidden="true">
          <PortalState v-for="n in 3" :key="n" kind="loading" class="card" />
        </div>
      </div>
      <template v-if="catalog">
        <section
          class="catalog-summary card grid grid-cols-2 sm:grid-cols-5"
          :aria-label="t('modelChannelStatus.legend.title')"
        >
          <div class="catalog-average p-4" :title="pt('refresh.average')">
            <p class="text-xs text-gray-500 dark:text-dark-400">{{ pt('refresh.average') }}</p>
            <p class="mt-2">{{ formatPercent(averageAvailability) }}</p>
            <p class="mt-2 text-xs text-gray-500 dark:text-dark-400">{{ pt('refresh.models', { count: allRows.length }) }}</p>
          </div>
          <div
            v-for="health in legendStates"
            :key="health"
            class="p-4"
            :title="healthLegendTitle(health)"
            :data-testid="`legend-${health}`"
          >
            <p class="flex items-center gap-2 text-xs text-gray-500 dark:text-dark-400">
              <span class="h-2 w-2" :class="healthBlockClass(health)" aria-hidden="true" />{{
                health === 'unknown'
                  ? t('modelChannelStatus.legend.unknown')
                  : t(`modelChannelStatus.health.${health}`)
              }}
            </p>
            <p class="mt-2 font-mono text-2xl">{{ rowsByHealth[health].length }}</p>
            <p
              v-if="(health === 'warning' || health === 'critical') && rowsByHealth[health].length"
              class="mt-1 truncate text-xs text-gray-500 dark:text-dark-400"
              :title="affectedNames(health)"
              :data-testid="`legend-${health}-models`"
            >
              {{ affectedNames(health) }}
            </p>
          </div>
        </section>
        <p class="text-xs text-gray-500 dark:text-dark-400" data-testid="legend-cache">
          {{ t('modelChannelStatus.legend.cache') }} ·
          <span
            v-for="level in cacheLevels"
            :key="level"
            class="mr-3 inline-flex items-center gap-1"
            :title="cacheLegendTitle(level)"
            :data-testid="`legend-cache-${level}`"
            ><span class="flex h-3 w-2 items-end" aria-hidden="true"
              ><span class="w-full bg-gray-400" :class="cacheHeightClass(level)" /></span
            >{{ t(`modelChannelStatus.legend.cacheLevel.${level}`) }}</span
          >
        </p>
        <PortalState
          v-if="!catalog.groups.length"
          kind="empty"
          :title="t('modelChannelStatus.emptyAll')"
          data-testid="empty"
        />
        <section
          v-for="platform in platforms"
          :key="platform.platform"
          class="space-y-4"
          :data-platform="platform.platform"
        >
          <h2>
            <button
              type="button"
              class="platform-heading flex w-full flex-wrap items-center gap-3 border-b-[2px] border-gray-900 py-3 text-left dark:border-dark-500"
              :aria-label="pt('refresh.platformToggle', { name: platformLabel(platform.platform) })"
              :aria-expanded="isExpanded(platform.platform, platform.hasData)"
              :aria-controls="`platform-${platform.platform}`"
              @click="
                expanded[platform.platform] = !isExpanded(platform.platform, platform.hasData)
              "
            >
              <Icon
                name="chevronDown"
                size="sm"
                :class="{ '-rotate-90': !isExpanded(platform.platform, platform.hasData) }"
              />
              <span class="platform-name">{{ platformLabel(platform.platform) }}</span>
              <span class="text-xs font-normal text-gray-500 dark:text-dark-400"
                >{{ pt('refresh.groups', { count: platform.groups.length }) }} ·
                {{ pt('refresh.models', { count: platform.rows.length }) }} ·
                <span :title="pt('refresh.average')">{{
                  platform.hasData ? formatPercent(platform.rate) : pt('refresh.platformNoData')
                }}</span></span
              >
              <span v-if="platform.critical" class="badge badge-danger">{{
                pt('refresh.issuesCritical', { count: platform.critical })
              }}</span>
              <span v-if="platform.warning" class="badge badge-warning">{{
                pt('refresh.issuesWarning', { count: platform.warning })
              }}</span>
            </button>
          </h2>
          <div
            v-show="isExpanded(platform.platform, platform.hasData)"
            :id="`platform-${platform.platform}`"
            class="grid items-stretch gap-4 md:grid-cols-2 xl:grid-cols-3"
          >
            <CatalogGroupCard
              v-for="group in platform.groups"
              :key="group.id"
              :group="group"
              :strip="catalog.strip"
              @select="onSelect"
            />
          </div>
        </section>
      </template>
      <CatalogDetailDrawer :selected="selected" @close="closeDetail" @select="onSelect" />
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import PortalPageHeading from '@/custom-portal/components/common/PortalPageHeading.vue'
import PortalState from '@/custom-portal/components/common/PortalState.vue'
import { usePortalI18n } from '@/custom-portal/i18n'
import { extractApiErrorMessage } from '@/utils/apiError'
import { platformLabel } from '@/utils/platformColors'
import {
  getCatalog,
  type CatalogCacheLevel,
  type CatalogGroup,
  type CatalogHealth,
  type CatalogModelRow,
  type ChannelStatusCatalog
} from '@/api/channelStatusCatalog'
import CatalogGroupCard from './CatalogGroupCard.vue'
import CatalogDetailDrawer from './CatalogDetailDrawer.vue'
import {
  cacheHeightClass,
  formatCheckTime,
  formatPercent,
  formatStep,
  healthBlockClass,
  isCallsStrip,
  type SelectedCatalogRow
} from './catalogFormat'
import { platformCards } from './platformCards'
const { t } = useI18n()
const { pt } = usePortalI18n()
const legendStates: CatalogHealth[] = ['healthy', 'warning', 'critical', 'unknown']
const cacheLevels: CatalogCacheLevel[] = ['high', 'mid', 'low', 'none']
const catalog = ref<ChannelStatusCatalog | null>(null)
const loading = ref(false)
const refreshing = ref(false)
const loadError = ref('')
const selected = ref<SelectedCatalogRow | null>(null)
const expanded = ref<Record<string, boolean>>({})
const platforms = computed(() => platformCards(catalog.value?.groups ?? []))
const allRows = computed(() => catalog.value?.groups.flatMap((group) => group.models) ?? [])
const rowsByHealth = computed(() =>
  Object.fromEntries(legendStates.map((health) => [health, allRows.value.filter((row) => row.health === health)]))
)
const affectedNames = (health: CatalogHealth) =>
  [...new Set(rowsByHealth.value[health].map((row) => row.name))].join(' · ')
const averageAvailability = computed(() => {
  const rates = allRows.value.filter((row) => row.health !== 'unknown' && row.success_rate != null).map((row) => row.success_rate!)
  return rates.length ? rates.reduce((sum, rate) => sum + rate, 0) / rates.length : null
})
const callsStrip = computed(() =>
  isCallsStrip(catalog.value?.strip) ? catalog.value!.strip : null
)
const isExpanded = (platform: string, hasData: boolean) => expanded.value[platform] ?? hasData
function healthLegendTitle(health: CatalogHealth) {
  const strip = catalog.value?.strip
  if (!strip) return undefined
  if (health === 'unknown')
    return t('modelChannelStatus.legend.unknownHint', { min: strip.min_calls })
  if (health === 'healthy')
    return t('modelChannelStatus.legend.successAtLeast', {
      rate: formatStep(strip.availability.green)
    })
  if (health === 'warning')
    return t('modelChannelStatus.legend.successBetween', {
      low: formatStep(strip.availability.yellow),
      high: formatStep(strip.availability.green)
    })
  return t('modelChannelStatus.legend.successBelow', {
    rate: formatStep(strip.availability.yellow)
  })
}
function cacheLegendTitle(level: CatalogCacheLevel) {
  const cache = catalog.value?.strip.cache
  if (!cache) return undefined
  if (level === 'high')
    return t('modelChannelStatus.legend.cacheAtLeast', { rate: formatStep(cache.high) })
  if (level === 'none')
    return t('modelChannelStatus.legend.cacheNone', { rate: formatStep(cache.low) })
  return t('modelChannelStatus.legend.cacheBetween', {
    low: formatStep(level === 'mid' ? cache.mid : cache.low),
    high: formatStep(level === 'mid' ? cache.high : cache.mid)
  })
}
let controller: AbortController | null = null
let sequence = 0
let timer: number | null = null
async function load(silent: boolean) {
  controller?.abort()
  const current = new AbortController()
  controller = current
  const token = ++sequence
  if (silent) refreshing.value = true
  else loading.value = true
  try {
    const next = await getCatalog('24h', current.signal)
    if (token !== sequence) return
    catalog.value = next
    loadError.value = ''
    if (selected.value) {
      const group = next.groups.find((item) => item.id === selected.value!.group.id)
      const row = group?.models.find((item) => item.key === selected.value!.row.key)
      selected.value = row && group ? { row, group } : null
    }
  } catch (error) {
    if (token !== sequence || current.signal.aborted) return
    // A failed background poll keeps the cards; the data-through time shows their age.
    if (silent && catalog.value) return
    loadError.value = extractApiErrorMessage(error, t('modelChannelStatus.loadFailed'))
  } finally {
    if (token === sequence) {
      loading.value = false
      refreshing.value = false
    }
  }
}
function onSelect(row: CatalogModelRow, group: CatalogGroup) {
  selected.value = { row, group }
}
function closeDetail() {
  selected.value = null
}
onMounted(() => {
  void load(false)
  timer = window.setInterval(() => {
    if (!document.hidden) void load(true)
  }, 60_000)
})
onBeforeUnmount(() => {
  controller?.abort()
  sequence++
  if (timer != null) window.clearInterval(timer)
})
defineExpose({ selected, closeDetail })
</script>
<style scoped>
.catalog-summary > div { border-left: 1px solid var(--pp-divider); }
.catalog-summary > div:first-child { border-left: 0; }
.catalog-summary > div > p:nth-child(2) { font: 400 24px/28px var(--pp-font-pixel); font-synthesis: none; }
.catalog-summary > div > p:first-child { font-size: 13px; }
.catalog-average { background: var(--pp-surface-2); }
.platform-heading { margin-top: 24px; }
.platform-name { font: 400 24px/28px var(--pp-font-pixel); font-synthesis: none; }
@media (max-width: 639px) {
  .catalog-average { grid-column: 1 / -1; }
  .catalog-summary > div:nth-child(even) { border-left: 0; }
  .catalog-summary > div:nth-child(n+2) { border-top: 1px solid var(--pp-divider); }
}
</style>
