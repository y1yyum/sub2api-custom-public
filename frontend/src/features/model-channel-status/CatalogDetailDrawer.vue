<template>
  <Teleport to="body">
    <Transition name="drawer">
      <div v-if="selected" class="fixed inset-0 z-50 flex justify-end" data-testid="detail-drawer-root">
        <div class="absolute inset-0 bg-black/30" aria-hidden="true" data-testid="detail-backdrop" @click="emit('close')" />
        <aside
          ref="panelRef"
          class="relative flex h-full w-full max-w-md flex-col overflow-hidden bg-white shadow-2xl dark:bg-dark-800"
          role="dialog"
          aria-modal="true"
          :aria-labelledby="titleId"
          data-testid="detail-drawer"
        >
          <header class="flex items-start justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700">
            <div class="min-w-0">
              <h2 :id="titleId" class="m-0 truncate text-base font-semibold text-gray-900 dark:text-white" data-testid="detail-title">
                {{ selected.row.name }}
              </h2>
              <p class="mt-1 flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-dark-400">
                <span
                  class="rounded px-1.5 py-0.5 text-[10px] font-medium"
                  :class="platformBadgeLightClass(selected.row.platform)"
                >
                  {{ platformLabel(selected.row.platform) }}
                </span>
                <span>{{ selected.group.name }}</span>
              </p>
            </div>
            <button
              ref="closeRef"
              type="button"
              class="-mr-2 rounded-xl p-2 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/40 dark:text-dark-500 dark:hover:bg-dark-700 dark:hover:text-dark-300"
              :aria-label="t('modelChannelStatus.detail.close')"
              data-testid="detail-close"
              @click="emit('close')"
            >
              <Icon name="x" size="md" />
            </button>
          </header>

          <div class="flex-1 space-y-6 overflow-y-auto px-5 py-4">
            <div v-if="loading" class="flex justify-center py-10" data-testid="detail-loading">
              <LoadingSpinner />
            </div>
            <div v-else-if="error" class="py-8 text-center" role="alert" data-testid="detail-error">
              <p class="text-sm text-red-700 dark:text-red-300">{{ error }}</p>
              <button type="button" class="btn btn-secondary btn-sm mt-3" @click="load">
                {{ t('modelChannelStatus.detail.retry') }}
              </button>
            </div>
            <template v-else-if="details">
              <DetailOverview :block="details.overview" />
              <DetailPricing :block="details.pricing" />
              <DetailEvents :block="details.events" />
            </template>
          </div>
        </aside>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { extractApiErrorMessage } from '@/utils/apiError'
import { platformBadgeLightClass, platformLabel } from '@/utils/platformColors'
import { getCatalogDetails } from '@/api/channelStatusCatalog'
import type { CatalogDetails } from '@/api/channelStatusCatalog'
import DetailEvents from './DetailEvents.vue'
import DetailOverview from './DetailOverview.vue'
import DetailPricing from './DetailPricing.vue'
import type { SelectedCatalogRow } from './catalogFormat'

const props = defineProps<{ selected: SelectedCatalogRow | null }>()
const emit = defineEmits<{ close: [] }>()

const { t } = useI18n()
const titleId = 'catalog-detail-title'

const details = ref<CatalogDetails | null>(null)
const loading = ref(false)
const error = ref('')
const panelRef = ref<HTMLElement | null>(null)
const closeRef = ref<HTMLButtonElement | null>(null)

let controller: AbortController | null = null
let sequence = 0
let opener: HTMLElement | null = null
let openerRowKey: string | null = null

async function load() {
  const target = props.selected
  if (!target) return
  controller?.abort()
  const current = new AbortController()
  controller = current
  const token = ++sequence
  loading.value = true
  error.value = ''
  details.value = null
  try {
    const next = await getCatalogDetails(
      { groupId: target.group.id, platform: target.row.platform, model: target.row.name },
      current.signal,
    )
    if (token !== sequence) return
    details.value = next
  } catch (err) {
    if (token !== sequence || current.signal.aborted) return
    error.value = extractApiErrorMessage(err, t('modelChannelStatus.detail.loadFailed'))
  } finally {
    if (token === sequence) loading.value = false
  }
}

// Buttons do not take focus on click in every browser, so the opener can be <body>;
// fall back to the row button that opened the drawer.
function restoreFocus() {
  const target = opener && opener !== document.body && document.contains(opener) ? opener : findOpenerRow()
  target?.focus()
  opener = null
  openerRowKey = null
}

function findOpenerRow(): HTMLElement | null {
  if (!openerRowKey) return null
  for (const row of document.querySelectorAll<HTMLElement>('[data-catalog-row-key]')) {
    if (row.dataset.catalogRowKey === openerRowKey) return row.querySelector<HTMLElement>('button')
  }
  return null
}

// Keyed on the row identity so the 60s list refresh (new object, same key)
// does not refetch the open drawer or reset its content.
watch(
  () => (props.selected ? `${props.selected.group.id}:${props.selected.row.key}` : null),
  async (key, previous) => {
    if (key == null) {
      controller?.abort()
      sequence++
      loading.value = false
      details.value = null
      error.value = ''
      restoreFocus()
      return
    }
    if (previous == null) {
      opener = document.activeElement as HTMLElement | null
      openerRowKey = props.selected?.row.key ?? null
    }
    void load()
    if (previous == null) {
      await nextTick()
      closeRef.value?.focus()
    }
  },
  { immediate: true },
)

const FOCUSABLE = 'a[href], button:not([disabled]), summary, input, select, textarea, [tabindex]:not([tabindex="-1"])'

const isEscape = (event: KeyboardEvent) => event.key === 'Escape' || event.key === 'Esc' || event.code === 'Escape'

function onKeydown(event: KeyboardEvent) {
  if (!props.selected) return
  if (isEscape(event)) {
    emit('close')
    return
  }
  if (event.key !== 'Tab' || !panelRef.value) return
  const focusable = [...panelRef.value.querySelectorAll<HTMLElement>(FOCUSABLE)]
  if (focusable.length === 0) return
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  const active = document.activeElement
  if (!panelRef.value.contains(active)) {
    event.preventDefault()
    first.focus()
  } else if (event.shiftKey && active === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && active === last) {
    event.preventDefault()
    first.focus()
  }
}

// Capture on window: runs before page-level handlers and also sees events dispatched at window or document.
onMounted(() => window.addEventListener('keydown', onKeydown, true))

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown, true)
  controller?.abort()
  sequence++
})
</script>

<style scoped>
.drawer-enter-active,
.drawer-leave-active {
  transition: opacity 0.15s ease;
}
.drawer-enter-from,
.drawer-leave-to {
  opacity: 0;
}
</style>
