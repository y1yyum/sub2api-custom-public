<template>
  <Teleport to="body">
    <Transition name="drawer">
      <div v-if="selected" class="fixed inset-0 z-50 flex justify-end" data-testid="detail-drawer-root">
        <div class="catalog-detail-backdrop absolute inset-0" aria-hidden="true" data-testid="detail-backdrop" @click="emit('close')" />
        <aside
          ref="panelRef"
          class="catalog-detail-panel card relative flex h-full w-full max-w-[600px] flex-col overflow-hidden !rounded-none bg-white dark:!bg-dark-800"
          role="dialog"
          aria-modal="true"
          :aria-labelledby="titleId"
          data-testid="detail-drawer"
        >
          <header class="flex items-start justify-between gap-3 border-b-[3px] border-double border-gray-300 px-5 py-4 dark:border-dark-600">
            <div class="min-w-0">
              <p class="mb-2 text-xs text-gray-500 dark:text-dark-400">{{ platformLabel(selected.row.platform) }} · {{ selected.group.name }} · {{ selected.group.user_rate_multiplier ?? selected.group.rate_multiplier }}×</p>
              <div class="flex min-w-0 items-center gap-1">
                <h2 :id="titleId" class="detail-model-title m-0 break-all text-gray-900 dark:text-white" data-testid="detail-title">
                  {{ selected.row.name }}
                </h2>
                <button
                  type="button"
                  class="shrink-0 p-1 text-gray-400 transition-colors hover:text-gray-700 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:text-dark-500 dark:hover:text-dark-200"
                  :title="t('modelChannelStatus.detail.copyName')"
                  :aria-label="t('modelChannelStatus.detail.copyName')"
                  data-testid="detail-copy"
                  @click="copyToClipboard(selected.row.name)"
                >
                  <Icon :name="copied ? 'check' : 'copy'" size="sm" />
                </button>
              </div>
              <p class="mt-1 flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-dark-400">
                <span
                  class="rounded px-1.5 py-0.5 text-[10px] font-medium"
                  :class="platformBadgeLightClass(selected.row.platform)"
                >
                  {{ platformLabel(selected.row.platform) }}
                </span>
                <span>{{ selected.group.name }}</span>
                <span class="badge" :class="healthTextClass(selected.row.health)">{{ t(`modelChannelStatus.health.${selected.row.health}`) }}</span>
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

          <div v-if="selected.group.models.length > 1" class="flex flex-wrap gap-1 border-b border-gray-200 px-5 py-3 dark:border-dark-700" role="group" :aria-label="selected.group.name">
            <button v-for="model in selected.group.models" :key="model.key" type="button" class="inline-flex items-center gap-2 border px-2 py-1.5 font-mono text-xs" :class="model.key === selected.row.key ? 'bg-gray-900 text-white dark:bg-dark-100 dark:text-dark-900' : ''" :aria-pressed="model.key === selected.row.key" :title="`${model.name}: ${t(`modelChannelStatus.health.${model.health}`)}`" @click="emit('select', model, selected.group)"><span class="h-2 w-2" :class="healthBlockClass(model.health)" aria-hidden="true" />{{ model.name }}</button>
          </div>
          <nav class="flex flex-wrap gap-1 border-b border-gray-200 px-5 py-2 dark:border-dark-700" :aria-label="pt('refresh.details')">
            <button v-for="section in ['overview', 'events', 'pricing', 'intelligence']" :key="section" type="button" class="px-2 py-2 text-xs hover:bg-gray-100 dark:hover:bg-dark-700" :aria-pressed="activeSection === section" @click="scrollToSection(section)">{{ pt(`refresh.sections.${section}`) }}<span v-if="section === 'events'"> ({{ details?.events.data?.total ?? selected.row.event_count }})</span></button>
          </nav>

          <div class="flex-1 space-y-5 divide-y-[3px] divide-double divide-gray-200 overflow-y-auto px-5 py-4 dark:divide-dark-700 [&>*+*]:pt-5">
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
              <DetailOverview id="catalog-section-overview" class="scroll-mt-4" :block="details.overview" :strip="details.strip" />
              <DetailEvents id="catalog-section-events" class="scroll-mt-4" :block="details.events" :strip="details.strip" />
              <DetailPricing id="catalog-section-pricing" class="scroll-mt-4" :block="details.pricing" />
              <DetailIntelligence id="catalog-section-intelligence" class="scroll-mt-4" :block="details.intelligence" />
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
import { useClipboard } from '@/composables/useClipboard'
import { extractApiErrorMessage } from '@/utils/apiError'
import { platformBadgeLightClass, platformLabel } from '@/utils/platformColors'
import { getCatalogDetails } from '@/api/channelStatusCatalog'
import type { CatalogDetails, CatalogGroup, CatalogModelRow } from '@/api/channelStatusCatalog'
import { usePortalI18n } from '@/custom-portal/i18n'
import DetailEvents from './DetailEvents.vue'
import DetailIntelligence from './DetailIntelligence.vue'
import DetailOverview from './DetailOverview.vue'
import DetailPricing from './DetailPricing.vue'
import { healthBlockClass, healthTextClass, type SelectedCatalogRow } from './catalogFormat'

const props = defineProps<{ selected: SelectedCatalogRow | null }>()
const emit = defineEmits<{ close: []; select: [row: CatalogModelRow, group: CatalogGroup] }>()

const { t } = useI18n()
const { pt } = usePortalI18n()
const activeSection = ref('overview')
function scrollToSection(section: string) {
  activeSection.value = section
  panelRef.value?.querySelector(`#catalog-section-${section}`)?.scrollIntoView({ block: 'start' })
}
const { copied, copyToClipboard } = useClipboard()
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
  activeSection.value = 'overview'
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
.detail-model-title { font: 400 24px/28px var(--pp-font-pixel); font-synthesis: none; }
.catalog-detail-backdrop { background-color: rgba(44, 46, 51, 0.3); background-image: repeating-conic-gradient(rgba(44, 46, 51, 0.15) 0 25%, transparent 0 50%); background-size: 4px 4px; }
.catalog-detail-panel > header { padding: 20px 24px; }
.catalog-detail-panel > nav { padding: 0 24px; background: var(--pp-surface-2); }
.catalog-detail-panel > nav > button { padding: 12px; font-size: 13px; }
.catalog-detail-panel > nav > button { border-bottom: 3px solid transparent; }
.catalog-detail-panel > nav > button[aria-pressed='true'] { border-bottom-color: var(--pp-ink); font-weight: 600; }
.catalog-detail-panel > .overflow-y-auto { padding: 20px 24px; }
.catalog-detail-panel :deep(h3) { margin-bottom: 12px; font-size: 14px; }
@media (max-width: 639px) {
  .catalog-detail-panel > header, .catalog-detail-panel > nav, .catalog-detail-panel > .overflow-y-auto { padding-inline: 16px; }
}
.drawer-enter-active,
.drawer-leave-active {
  transition: opacity 0.15s ease;
}
.drawer-enter-from,
.drawer-leave-to {
  opacity: 0;
}
</style>
