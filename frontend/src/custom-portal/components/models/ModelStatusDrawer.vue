<template>
  <BaseDialog :show="target !== null" :title="title" width="wide" @close="emit('close')">
    <div class="space-y-5" data-testid="model-status-drawer">
      <section>
        <h3 class="mb-2 text-sm font-semibold text-gray-700 dark:text-gray-200">{{ pt('pages.models.status.trend') }}</h3>
        <p v-if="failed" class="text-sm text-red-600 dark:text-red-300" data-testid="model-status-failed">
          {{ pt('pages.models.status.loadFailed') }}
        </p>
        <MonitorTrendChart v-else :trend="snapshot?.trend || []" :coverage="snapshot?.coverage || null" :loading="loading" />
      </section>
      <section v-if="!failed">
        <h3 class="mb-2 text-sm font-semibold text-gray-700 dark:text-gray-200">{{ pt('pages.models.status.errors') }}</h3>
        <p v-if="loading" class="text-sm text-gray-400">{{ pt('pages.models.status.loading') }}</p>
        <p v-else-if="errors.length === 0" class="text-sm text-gray-500 dark:text-dark-400" data-testid="model-status-no-errors">
          {{ pt('pages.models.status.noErrors') }}
        </p>
        <ul v-else class="space-y-2">
          <li
            v-for="row in errors"
            :key="row.category"
            class="grid grid-cols-[minmax(100px,200px)_1fr_auto] items-center gap-3 rounded-xl bg-gray-50 px-4 py-2.5 text-sm dark:bg-dark-900/30"
            :class="row.ignored ? 'opacity-60' : ''"
            data-testid="model-status-error"
          >
            <span class="truncate text-gray-700 dark:text-gray-200">{{ errorLabel(row.category) }}</span>
            <span class="h-2 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700">
              <i class="block h-full rounded-full bg-red-500" :style="{ width: `${Math.max(2, row.rate * 100)}%` }"></i>
            </span>
            <small class="w-14 text-right text-xs tabular-nums text-gray-500">{{ formatMonitorPercent(row.rate) }}</small>
          </li>
        </ul>
      </section>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import MonitorTrendChart from '@/features/channel-monitor-v2/MonitorTrendChart.vue'
import { formatMonitorPercent, monitorErrorCategoryLabel } from '@/features/channel-monitor-v2/monitorFormat'
import { getErrors, getSnapshot, type MonitorErrorRow, type MonitorSnapshot } from '@/api/channelMonitorV2'
import { usePortalI18n } from '../../i18n'
import { modelStatusFilter, type ModelStatusTarget } from '../../views/modelStatus'

const props = defineProps<{ target: ModelStatusTarget | null }>()
const emit = defineEmits<{ close: [] }>()
const { pt } = usePortalI18n()
const { t, te } = useI18n()

const snapshot = shallowRef<MonitorSnapshot | null>(null)
const errors = shallowRef<MonitorErrorRow[]>([])
const loading = ref(false)
const failed = ref(false)
let controller: AbortController | null = null

// Keeps the last row so the title survives the closing transition.
const shown = shallowRef<ModelStatusTarget | null>(null)
const title = computed(() =>
  shown.value ? pt('pages.models.status.title', { model: shown.value.model, group: shown.value.groupName }) : ''
)

function errorLabel(category: string) {
  const key = `channelMonitorV2.errorCategories.${category}`
  return te(key) ? t(key) : monitorErrorCategoryLabel(category)
}

async function load(target: ModelStatusTarget) {
  controller?.abort()
  const request = new AbortController()
  controller = request
  snapshot.value = null
  errors.value = []
  failed.value = false
  loading.value = true
  const filter = modelStatusFilter(target)
  try {
    const [nextSnapshot, nextErrors] = await Promise.all([
      getSnapshot(filter, false, request.signal),
      getErrors(filter, false, request.signal)
    ])
    if (request.signal.aborted) return
    snapshot.value = nextSnapshot
    errors.value = nextErrors.items || []
  } catch {
    if (!request.signal.aborted) failed.value = true
  } finally {
    if (controller === request) loading.value = false
  }
}

watch(
  () => props.target,
  (target) => {
    if (target) {
      shown.value = target
      void load(target)
    }
    else controller?.abort()
  },
  { immediate: true }
)

onBeforeUnmount(() => controller?.abort())
</script>
