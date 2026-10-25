<template>
  <div class="flex max-w-full flex-wrap items-center gap-2" data-testid="overview-toolbar">
    <p v-if="invalidRange" class="text-xs text-red-600 dark:text-red-400" role="alert" data-testid="overview-range-invalid">
      {{ pt('pages.overview.toolbar.invalidRange', { days: MAX_CUSTOM_RANGE_DAYS }) }}
    </p>
    <div ref="filterRoot" class="relative" @keydown.esc.stop="closeFilters(true)">
      <button
        ref="filterTrigger"
        type="button"
        class="btn btn-secondary btn-icon relative"
        aria-haspopup="dialog"
        :title="pt('pages.overview.toolbar.filters')"
        :aria-label="pt('pages.overview.toolbar.filters')"
        :aria-expanded="filtersOpen"
        :aria-controls="filtersOpen ? panelId : undefined"
        data-testid="overview-filter-trigger"
        @click="toggleFilters"
      >
        <Icon name="filter" size="sm" aria-hidden="true" />
        <span
          v-if="state.activeFilterCount.value"
          class="absolute -right-1.5 -top-1.5 inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-primary-600 px-1 text-[10px] font-semibold text-white"
          data-testid="overview-filter-count"
        >
          {{ state.activeFilterCount.value }}
          <span class="sr-only">{{ pt('pages.overview.toolbar.activeCount', { count: state.activeFilterCount.value }) }}</span>
        </span>
      </button>

      <div
        v-if="filtersOpen"
        :id="panelId"
        class="absolute right-0 z-30 mt-2 w-72 space-y-3 rounded-xl border border-gray-200 bg-white p-4 shadow-lg dark:border-dark-700 dark:bg-dark-800"
        role="dialog"
        :aria-label="pt('pages.overview.toolbar.filterPanel')"
        data-testid="overview-filter-panel"
      >
        <div class="space-y-1">
          <label :for="`${panelId}-key`" class="text-xs font-medium text-gray-600 dark:text-dark-300">
            {{ pt('pages.overview.toolbar.apiKey') }}
          </label>
          <select
            :id="`${panelId}-key`"
            ref="firstField"
            class="input w-full"
            :value="state.filters.value.apiKeyId ?? ''"
            data-testid="overview-filter-key"
            @change="onKeyChange"
          >
            <option value="">{{ pt('pages.overview.toolbar.all') }}</option>
            <option v-if="options.apiKeysLoading.value" disabled value="loading">{{ pt('pages.overview.toolbar.loadingKeys') }}</option>
            <option
              v-if="selectedKeyMissing"
              :value="state.filters.value.apiKeyId"
            >
              #{{ state.filters.value.apiKeyId }}
            </option>
            <option v-for="option in options.apiKeys.value" :key="option.value" :value="option.value">{{ option.label }}</option>
          </select>
          <p v-if="options.apiKeysError.value" class="flex items-center gap-2 text-xs text-red-600 dark:text-red-400" role="alert">
            {{ pt('pages.overview.toolbar.keysFailed') }}
            <button type="button" class="underline" @click="options.loadApiKeys()">{{ pt('pages.overview.toolbar.retry') }}</button>
          </p>
        </div>

        <div class="space-y-1">
          <label :for="`${panelId}-model`" class="text-xs font-medium text-gray-600 dark:text-dark-300">
            {{ pt('pages.overview.toolbar.model') }}
          </label>
          <select
            :id="`${panelId}-model`"
            class="input w-full"
            :value="state.filters.value.model ?? ''"
            data-testid="overview-filter-model"
            @change="onModelChange"
          >
            <option value="">{{ pt('pages.overview.toolbar.all') }}</option>
            <option v-for="option in options.modelOptions.value" :key="option.value" :value="option.value">{{ option.label }}</option>
          </select>
        </div>

        <div class="space-y-1">
          <label :for="`${panelId}-group`" class="text-xs font-medium text-gray-600 dark:text-dark-300">
            {{ pt('pages.overview.toolbar.group') }}
          </label>
          <select
            :id="`${panelId}-group`"
            class="input w-full"
            :value="state.filters.value.groupId ?? ''"
            data-testid="overview-filter-group"
            @change="onGroupChange"
          >
            <option value="">{{ pt('pages.overview.toolbar.all') }}</option>
            <option v-for="option in options.groupOptions.value" :key="option.value" :value="option.value">{{ option.label }}</option>
          </select>
        </div>

        <div class="space-y-1">
          <label :for="`${panelId}-type`" class="text-xs font-medium text-gray-600 dark:text-dark-300">
            {{ pt('pages.overview.toolbar.requestType') }}
          </label>
          <select
            :id="`${panelId}-type`"
            class="input w-full"
            :value="state.filters.value.requestType ?? ''"
            data-testid="overview-filter-type"
            @change="onTypeChange"
          >
            <option value="">{{ pt('pages.overview.toolbar.all') }}</option>
            <option v-for="option in options.requestTypeOptions" :key="option.value" :value="option.value">
              {{ translateGlobal(option.labelKey) }}
            </option>
          </select>
        </div>

        <div class="flex items-center justify-between gap-2 border-t border-gray-100 pt-3 dark:border-dark-700">
          <button
            type="button"
            class="text-xs font-medium text-gray-600 hover:text-gray-900 disabled:opacity-50 dark:text-dark-300 dark:hover:text-white"
            :disabled="!state.activeFilterCount.value"
            data-testid="overview-filter-reset"
            @click="state.clearFilters()"
          >
            {{ pt('pages.overview.toolbar.reset') }}
          </button>
          <button type="button" class="btn btn-primary btn-sm" data-testid="overview-filter-close" @click="closeFilters(true)">
            {{ pt('pages.overview.toolbar.close') }}
          </button>
        </div>
      </div>
    </div>

    <button
      type="button"
      class="btn btn-secondary btn-icon shrink-0"
      :disabled="refreshing"
      :aria-busy="refreshing"
      :title="pt('pages.overview.toolbar.refresh')"
      :aria-label="pt('pages.overview.toolbar.refresh')"
      data-testid="overview-refresh"
      @click="emit('refresh')"
    >
      <Icon
        name="refresh"
        size="sm"
        aria-hidden="true"
        :class="{ 'animate-spin motion-reduce:animate-none': refreshing }"
      />
    </button>

    <DateRangePicker :start-date="state.range.value.start" :end-date="state.range.value.end" @change="applyRange" />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Icon from '@/components/icons/Icon.vue'
import type { UsageRequestType } from '@/types'
import { usePortalI18n } from '../../i18n'
import { injectOverviewState } from '../../overview/usageChartState'
import { MAX_CUSTOM_RANGE_DAYS, type FixedRangePreset } from '../../overview/usageChartData'
import { useOverviewFilterOptions } from '../../overview/useOverviewFilterOptions'

const { pt } = usePortalI18n()
// Request-type labels are existing upstream keys resolved at runtime.
const { t: translateGlobal } = useI18n()
const state = injectOverviewState()
const options = useOverviewFilterOptions(state)

defineProps<{ refreshing?: boolean }>()
/** The page owns refresh because it spans sections outside the shared state. */
const emit = defineEmits<{ refresh: [] }>()

const panelId = 'overview-filter-panel'

/** `DateRangePicker` shortcut values that match a fixed preset, so refresh keeps tracking the clock. */
const PICKER_PRESETS: Record<string, FixedRangePreset> = { today: 'today', '7days': '7d', '30days': '30d' }

const invalidRange = ref(false)

function applyRange(range: { startDate: string; endDate: string; preset: string | null }) {
  const preset = range.preset ? PICKER_PRESETS[range.preset] : undefined
  if (preset) {
    invalidRange.value = false
    state.setPreset(preset)
    return
  }
  invalidRange.value = !state.setCustomRange(range.startDate, range.endDate)
}

const filtersOpen = ref(false)
const filterRoot = ref<HTMLElement | null>(null)
const filterTrigger = ref<HTMLButtonElement | null>(null)
const firstField = ref<HTMLSelectElement | null>(null)

function onDocumentPointerDown(event: Event) {
  if (filterRoot.value && !filterRoot.value.contains(event.target as Node)) closeFilters(false)
}

async function openFilters() {
  filtersOpen.value = true
  document.addEventListener('mousedown', onDocumentPointerDown, true)
  document.addEventListener('touchstart', onDocumentPointerDown, true)
  void options.loadApiKeys()
  await nextTick()
  firstField.value?.focus()
}

function closeFilters(restoreFocus: boolean) {
  if (!filtersOpen.value) return
  filtersOpen.value = false
  document.removeEventListener('mousedown', onDocumentPointerDown, true)
  document.removeEventListener('touchstart', onDocumentPointerDown, true)
  if (restoreFocus) filterTrigger.value?.focus()
}

function toggleFilters() {
  if (filtersOpen.value) closeFilters(true)
  else void openFilters()
}

onBeforeUnmount(() => closeFilters(false))

const selectValue = (event: Event) => (event.target as HTMLSelectElement).value
const toId = (value: string) => (value === '' ? undefined : Number(value))

const onKeyChange = (event: Event) => state.setFilters({ apiKeyId: toId(selectValue(event)) })
const onModelChange = (event: Event) => state.setFilters({ model: selectValue(event) || undefined })
const onGroupChange = (event: Event) => state.setFilters({ groupId: toId(selectValue(event)) })
const onTypeChange = (event: Event) =>
  state.setFilters({ requestType: (selectValue(event) || undefined) as UsageRequestType | undefined })

const selectedKeyMissing = computed(() => {
  const id = state.filters.value.apiKeyId
  return id !== undefined && !options.apiKeys.value.some((option) => option.value === id)
})
</script>
