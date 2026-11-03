<template>
  <section class="rounded-lg border border-gray-200 p-4 dark:border-dark-700" data-testid="strip-config">
    <h4 class="m-0 text-sm font-medium text-gray-900 dark:text-white">{{ t('modelChannelStatus.stripConfig.title') }}</h4>
    <p class="m-0 mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('modelChannelStatus.stripConfig.hint') }}</p>

    <div v-if="loading" class="flex justify-center py-4"><LoadingSpinner size="sm" /></div>
    <p v-else-if="loadError" class="m-0 mt-3 text-sm text-red-600 dark:text-red-400" role="alert" data-testid="strip-config-load-error">
      {{ loadError }}
    </p>
    <div v-else class="mt-3 space-y-4">
      <fieldset class="m-0 border-0 p-0">
        <legend class="input-label">{{ t('modelChannelStatus.stripConfig.mode') }}</legend>
        <div class="tabs inline-flex" role="group">
          <button
            v-for="option in modes"
            :key="option"
            type="button"
            class="tab !px-3 !py-1 text-xs"
            :class="form.mode === option ? 'tab-active' : ''"
            :aria-pressed="form.mode === option"
            :data-testid="`strip-mode-${option}`"
            @click="form.mode = option"
          >
            {{ t(option === 'calls' ? 'modelChannelStatus.stripConfig.modeCalls' : 'modelChannelStatus.stripConfig.modeTime') }}
          </button>
        </div>
        <p class="m-0 mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t(form.mode === 'calls' ? 'modelChannelStatus.stripConfig.modeCallsHint' : 'modelChannelStatus.stripConfig.modeTimeHint') }}
        </p>
      </fieldset>

      <div class="grid gap-3 sm:grid-cols-3">
        <label v-if="form.mode === 'calls'" class="block">
          <span class="input-label">{{ t('modelChannelStatus.stripConfig.callWindow') }}</span>
          <input v-model.number="form.call_window" type="number" min="20" max="2000" step="1" class="input" data-testid="strip-call-window" />
        </label>
        <label v-if="form.mode === 'calls'" class="block">
          <span class="input-label">{{ t('modelChannelStatus.stripConfig.blocks') }}</span>
          <input v-model.number="form.blocks" type="number" min="10" max="48" step="1" class="input" data-testid="strip-blocks" />
          <span v-if="perBlock > 0" class="mt-1 block text-xs text-gray-500 dark:text-gray-400">
            {{ t('modelChannelStatus.stripConfig.perBlock', { per: perBlock }) }}
          </span>
        </label>
        <label class="block">
          <span class="input-label">{{ t('modelChannelStatus.stripConfig.minCalls') }}</span>
          <input v-model.number="form.min_calls" type="number" min="1" max="1000" step="1" class="input" data-testid="strip-min-calls" />
          <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('modelChannelStatus.stripConfig.minCallsHint') }}</span>
        </label>
      </div>
      <p v-if="minCallsTooHigh" class="m-0 text-xs text-amber-700 dark:text-amber-300" role="status" data-testid="strip-min-calls-warning">
        {{ t('modelChannelStatus.stripConfig.minCallsTooHigh', { per: perBlock }) }}
      </p>

      <fieldset class="m-0 border-0 p-0">
        <legend class="input-label">{{ t('modelChannelStatus.stripConfig.availability') }}</legend>
        <div class="flex flex-wrap gap-x-6 gap-y-2">
          <label v-for="step in availabilitySteps" :key="step.key" class="flex items-center gap-2 text-xs text-gray-700 dark:text-gray-300">
            <span class="h-2.5 w-2.5 shrink-0 rounded-sm" :class="step.swatch" aria-hidden="true" />
            <span class="shrink-0">{{ t(`modelChannelStatus.stripConfig.${step.key}`) }}</span>
            <input v-model.number="form[step.key]" type="number" min="0" max="100" step="0.1" class="input !w-20" :data-testid="`strip-${step.key}`" />
            <span>%</span>
          </label>
        </div>
        <p class="m-0 mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('modelChannelStatus.stripConfig.red') }}</p>
      </fieldset>

      <fieldset class="m-0 border-0 p-0">
        <legend class="input-label">{{ t('modelChannelStatus.stripConfig.cache') }}</legend>
        <div class="flex flex-wrap gap-x-6 gap-y-2">
          <label v-for="step in cacheSteps" :key="step" class="flex items-center gap-2 text-xs text-gray-700 dark:text-gray-300">
            <span class="flex h-3 w-1.5 shrink-0 items-end" aria-hidden="true">
              <span class="w-full rounded-[1px] bg-gray-400 dark:bg-dark-400" :class="cacheHeightClass(step)" />
            </span>
            <span class="shrink-0">{{ t(`modelChannelStatus.stripConfig.${step}`) }}</span>
            <input v-model.number="form[step]" type="number" min="0" max="100" step="0.1" class="input !w-20" :data-testid="`strip-${step}`" />
            <span>%</span>
          </label>
        </div>
        <p class="m-0 mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('modelChannelStatus.stripConfig.none') }}</p>
      </fieldset>

      <p v-if="invalidKey" class="m-0 text-xs text-red-600 dark:text-red-400" role="alert" data-testid="strip-invalid">
        {{ t(`modelChannelStatus.stripConfig.invalid.${invalidKey}`) }}
      </p>
      <div class="flex justify-end gap-2">
        <button type="button" class="btn btn-secondary btn-sm" :disabled="saving" data-testid="strip-reset" @click="resetDefaults">
          {{ t('modelChannelStatus.stripConfig.reset') }}
        </button>
        <button type="button" class="btn btn-primary btn-sm" :disabled="saving || invalidKey !== ''" data-testid="strip-save" @click="save">
          {{ t('modelChannelStatus.stripConfig.save') }}
        </button>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { useAppStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'
import * as stripAPI from '@/api/admin/catalogStripConfig'
import type { CatalogStripConfig, CatalogStripMode } from '@/api/channelStatusCatalog'
import { cacheHeightClass } from './catalogFormat'

/** Mirrors the server defaults and limits in catalog_strip_config.go. */
const DEFAULTS: CatalogStripConfig = {
  mode: 'time',
  call_window: 100,
  blocks: 20,
  min_calls: 1,
  availability: { green: 0.95, yellow: 0.8 },
  cache: { high: 0.5, mid: 0.2, low: 0.01 },
}

const modes: CatalogStripMode[] = ['time', 'calls']
const availabilitySteps = [
  { key: 'green', swatch: 'bg-emerald-500 dark:bg-emerald-400' },
  { key: 'yellow', swatch: 'bg-amber-400 dark:bg-amber-300' },
] as const
const cacheSteps = ['high', 'mid', 'low'] as const

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const saving = ref(false)
const loadError = ref('')
const form = reactive({ mode: 'time' as CatalogStripMode, call_window: 0, blocks: 0, min_calls: 0, green: 0, yellow: 0, high: 0, mid: 0, low: 0 })

const toPercent = (fraction: number) => Number((fraction * 100).toFixed(2))
const toFraction = (percent: number) => Number((percent / 100).toFixed(4))
const isInt = (value: number, min: number, max: number) => Number.isInteger(value) && value >= min && value <= max
const isPercent = (value: number) => typeof value === 'number' && value >= 0 && value <= 100

function fill(config: CatalogStripConfig) {
  Object.assign(form, {
    mode: config.mode,
    call_window: config.call_window,
    blocks: config.blocks,
    min_calls: config.min_calls,
    green: toPercent(config.availability.green),
    yellow: toPercent(config.availability.yellow),
    high: toPercent(config.cache.high),
    mid: toPercent(config.cache.mid),
    low: toPercent(config.cache.low),
  })
}

function toConfig(): CatalogStripConfig {
  return {
    mode: form.mode,
    call_window: form.call_window,
    blocks: form.blocks,
    min_calls: form.min_calls,
    availability: { green: toFraction(form.green), yellow: toFraction(form.yellow) },
    cache: { high: toFraction(form.high), mid: toFraction(form.mid), low: toFraction(form.low) },
  }
}

/** Same order and limits as the server, so a valid form never comes back with 400. */
const invalidKey = computed(() => {
  if (!isInt(form.call_window, 20, 2000)) return 'callWindow'
  if (!isInt(form.blocks, 10, 48)) return 'blocks'
  if (form.call_window < form.blocks) return 'windowBlocks'
  if (!isInt(form.min_calls, 1, 1000)) return 'minCalls'
  if (!isPercent(form.green) || !isPercent(form.yellow) || form.yellow > form.green) return 'availability'
  if (!isPercent(form.high) || !isPercent(form.mid) || !isPercent(form.low) || form.low > form.mid || form.mid > form.high) return 'cache'
  return ''
})

const perBlock = computed(() => (form.blocks > 0 ? Number((form.call_window / form.blocks).toFixed(1)) : 0))
const minCallsTooHigh = computed(() => form.mode === 'calls' && perBlock.value > 0 && form.min_calls > perBlock.value)

function resetDefaults() {
  fill(DEFAULTS)
}

async function save() {
  if (invalidKey.value) return
  saving.value = true
  try {
    fill(await stripAPI.update(toConfig()))
    appStore.showSuccess(t('modelChannelStatus.stripConfig.saved'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('modelChannelStatus.stripConfig.saveFailed')))
  } finally {
    saving.value = false
  }
}

onMounted(async () => {
  try {
    fill(await stripAPI.get())
  } catch (error) {
    loadError.value = extractApiErrorMessage(error, t('modelChannelStatus.stripConfig.loadFailed'))
  } finally {
    loading.value = false
  }
})
</script>
