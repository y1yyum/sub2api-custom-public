<template>
  <AppLayout>
    <div class="space-y-4" data-testid="portal-models">
      <div
        v-if="tabs.length > 1"
        class="flex flex-wrap gap-1 rounded-xl bg-gray-100 p-1 dark:bg-dark-800"
        role="tablist"
        :aria-label="pt('pages.models.tabsLabel')"
      >
        <button
          v-for="tab in tabs"
          :id="`models-tab-${tab}`"
          :key="tab"
          type="button"
          role="tab"
          class="rounded-lg px-4 py-2 text-sm font-medium transition-colors"
          :class="
            tab === activeTab
              ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
              : 'text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white'
          "
          :aria-selected="tab === activeTab"
          aria-controls="models-panel"
          :data-testid="`portal-models-tab-${tab}`"
          @click="selectTab(tab)"
        >
          {{ pt(`pages.models.tabs.${tab}`) }}
        </button>
      </div>

      <div v-if="activeTab" id="models-panel" role="tabpanel" :aria-labelledby="`models-tab-${activeTab}`">
        <ChannelStatusView v-if="activeTab === 'status'" />
        <ModelPlazaContent v-else :response="pricing" :loading="pricingLoading" :error="pricingFailed" embedded>
          <template v-if="statusEnabled" #model-status-header>
            <span class="whitespace-nowrap">{{ pt('pages.models.status.header') }}</span>
          </template>
          <template v-if="statusEnabled" #model-status="{ group, model }">
            <ModelStatusCell
              :status="statusOf(group, model)"
              :model="model.name"
              @open="openStatus(group, model)"
            />
          </template>
        </ModelPlazaContent>
      </div>
      <div v-else class="card p-10 text-center text-sm text-gray-500 dark:text-dark-400" data-testid="portal-models-empty">
        {{ pt('pages.models.empty') }}
      </div>
    </div>
    <ModelStatusDrawer v-if="statusEnabled" :target="statusTarget" @close="statusTarget = null" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineAsyncComponent, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import ModelPlazaContent from '@/components/modelPlaza/ModelPlazaContent.vue'
import { getModelPlaza, type ModelPlazaGroup, type ModelPlazaResponse, type PlazaModel } from '@/api/modelPlaza'
import { userGroupsAPI } from '@/api/groups'
import { getMatrix } from '@/api/channelMonitorV2'
import { isChannelMonitorV2Mode, isUnifiedModelChannelStatusEnabled } from '@/utils/featureFlags'
import ModelStatusCell from '../components/models/ModelStatusCell.vue'
import ModelStatusDrawer from '../components/models/ModelStatusDrawer.vue'
import { usePortalI18n } from '../i18n'
import { MODELS_PATH } from '../pageRoutes'
import { dockSections } from '../shell/nav'
import { usePortalNavContext } from '../shell/usePortalNav'
import { MODELS_TABS, modelsTabQuery, openedPlazaGroups, resolveModelsTab, type ModelsTab } from './modelsTabs'
import { indexModelStatus, MODEL_STATUS_RANGE, type ModelStatusCellState, type ModelStatusTarget } from './modelStatus'

// Upstream channel status renders inside this page's layout, so its own AppLayout only renders the slot.
const ChannelStatusView = defineAsyncComponent(() => import('@/views/user/ChannelStatusView.vue'))

const SECTION_TABS: Record<string, ModelsTab> = { channelStatus: 'status', pricing: 'pricing' }

const { pt } = usePortalI18n()
const route = useRoute()
const router = useRouter()
const { ctx } = usePortalNavContext()

const pricing = shallowRef<ModelPlazaResponse | null>(null)
const pricingLoading = ref(false)
const pricingFailed = ref(false)
// The plaza answers 404 while its switch is off, even if the public settings were stale.
const pricingClosed = ref(false)
let pricingRequested = false

const tabs = computed(() => {
  const visible = new Set(dockSections('models', ctx.value).map((link) => SECTION_TABS[link.id]))
  if (pricingClosed.value) visible.delete('pricing')
  return MODELS_TABS.filter((tab) => visible.has(tab))
})
const activeTab = computed(() => resolveModelsTab(route.query, tabs.value))

// The unified page replaces the standalone pricing view; a stale ?view=pricing link lands on it.
const unifiedPage = computed(() => isUnifiedModelChannelStatusEnabled())
watch(
  [unifiedPage, () => route.query.view],
  ([unified, view]) => {
    if (unified && view === 'pricing') void router.replace({ path: MODELS_PATH, query: modelsTabQuery(route.query, 'status') })
  },
  { immediate: true }
)

function selectTab(tab: ModelsTab) {
  if (tab !== activeTab.value) void router.replace({ path: MODELS_PATH, query: modelsTabQuery(route.query, tab) })
}

async function loadPricing() {
  pricingRequested = true
  pricingLoading.value = true
  pricingFailed.value = false
  try {
    const [plaza, opened] = await Promise.allSettled([getModelPlaza(), userGroupsAPI.getAvailable()])
    if (plaza.status === 'rejected' && (plaza.reason as { status?: number } | null)?.status === 404) {
      pricingClosed.value = true
    } else if (plaza.status === 'rejected' || opened.status === 'rejected') {
      pricingFailed.value = true
      // Opening the tab again retries.
      pricingRequested = false
    } else {
      pricing.value = openedPlazaGroups(plaza.value, new Set(opened.value.map((group) => group.id)))
    }
  } finally {
    pricingLoading.value = false
  }
}

// Per-model status needs V2: V1 only knows free-text group names, which cannot be joined reliably.
const statusEnabled = computed(() => isChannelMonitorV2Mode())
const statusLookup = shallowRef<ReturnType<typeof indexModelStatus> | null>(null)
const statusFailed = ref(false)
const statusTarget = shallowRef<ModelStatusTarget | null>(null)
let statusRequested = false

function modelPlatform(group: ModelPlazaGroup, model: PlazaModel) {
  return model.platform || group.platform
}

function statusOf(group: ModelPlazaGroup, model: PlazaModel): ModelStatusCellState {
  if (statusFailed.value) return { kind: 'failed' }
  if (!statusLookup.value) return { kind: 'loading' }
  return statusLookup.value(group.id, modelPlatform(group, model), model.name)
}

function openStatus(group: ModelPlazaGroup, model: PlazaModel) {
  statusTarget.value = { groupId: group.id, groupName: group.name, platform: modelPlatform(group, model), model: model.name }
}

// Status failures stay inside the status column; pricing renders regardless.
async function loadStatus() {
  statusRequested = true
  statusFailed.value = false
  try {
    const matrix = await getMatrix(
      { range: MODEL_STATUS_RANGE, platforms: [], groupIds: [], models: [] },
      'platform_group_model'
    )
    statusLookup.value = indexModelStatus(matrix.items || [])
  } catch {
    statusFailed.value = true
    statusRequested = false
  }
}

watch(
  activeTab,
  (tab) => {
    if (tab === 'pricing' && !pricingRequested) void loadPricing()
  },
  { immediate: true }
)

watch(
  [activeTab, statusEnabled],
  ([tab, enabled]) => {
    if (tab === 'pricing' && enabled && !statusRequested) void loadStatus()
  },
  { immediate: true }
)
</script>
