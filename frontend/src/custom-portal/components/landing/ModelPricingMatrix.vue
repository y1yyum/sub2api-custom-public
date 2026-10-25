<template>
  <section v-if="status !== 'pending' && status !== 'hidden'" id="models" class="relative py-20 lg:py-28">
    <div class="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
      <!-- Section Header -->
      <div class="flex flex-col md:flex-row md:items-end md:justify-between gap-4">
        <div>
          <div class="inline-flex items-center gap-1.5 rounded-full bg-emerald-100/80 px-3 py-1 text-xs font-semibold text-emerald-800 dark:bg-emerald-950/60 dark:text-emerald-300">
            <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 6v12m-3-2.818l.879.659c1.171.879 3.07.879 4.242 0 1.172-.879 1.172-2.303 0-3.182C13.536 12.219 12.768 12 12 12c-.725 0-1.45-.22-2.003-.659-1.106-.879-1.106-2.303 0-3.182s2.9-.879 4.006 0l.415.33M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            {{ pt('pricing.badge') }}
          </div>
          <h2 class="mt-3 text-3xl font-bold tracking-tight text-gray-900 sm:text-4xl dark:text-white">
            {{ pt('pricing.title') }}
          </h2>
          <p class="mt-2 text-sm text-gray-600 dark:text-dark-300">
            {{ pt('pricing.description') }}
          </p>
        </div>

        <div class="flex items-center gap-3">
          <router-link
            to="/model-plaza"
            class="inline-flex items-center gap-1.5 rounded-xl border border-gray-200 bg-white px-4 py-2 text-xs font-semibold text-gray-700 shadow-sm transition-all hover:border-gray-300 hover:bg-gray-50 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-200 dark:hover:bg-dark-800"
          >
            <span>{{ pt('pricing.openPlaza') }}</span>
            <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
            </svg>
          </router-link>
        </div>
      </div>

      <!-- Filters & Search -->
      <div class="mt-8 flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4">
        <div class="flex flex-wrap gap-1.5">
          <button
            v-for="p in platformFilters"
            :key="p.id"
            @click="activePlatform = p.id"
            class="rounded-xl px-3.5 py-1.5 text-xs font-semibold transition-all"
            :class="activePlatform === p.id ? 'bg-primary-600 text-white shadow-sm' : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-800 dark:text-dark-300 dark:hover:bg-dark-700'"
          >
            {{ p.id === 'all' ? pt('pricing.allPlatforms') : p.name }}
          </button>
        </div>

        <div class="relative w-full sm:w-64">
          <svg class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
          </svg>
          <input
            v-model="searchQuery"
            type="text"
            :placeholder="pt('pricing.searchPlaceholder')"
            class="w-full rounded-xl border border-gray-200 bg-white py-1.5 pl-9 pr-3 text-xs text-gray-900 placeholder-gray-400 focus:border-primary-500 focus:outline-none focus:ring-1 focus:ring-primary-500 dark:border-dark-700 dark:bg-dark-900 dark:text-white"
          />
        </div>
      </div>

      <div
        v-if="status !== 'ready'"
        class="mt-6 rounded-2xl border border-dashed border-gray-200 p-8 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-dark-400"
      >
        <template v-if="status === 'loading'">{{ pt('pricing.loading') }}</template>
        <template v-else-if="status === 'locked'">
          {{ pt('pricing.locked') }}
          <router-link to="/login" class="font-semibold text-primary-600 dark:text-primary-400">{{ pt('pricing.goLogin') }}</router-link>
        </template>
        <template v-else>
          {{ pt('pricing.emptyLead') }}
          <router-link to="/model-plaza" class="font-semibold text-primary-600 dark:text-primary-400">{{ pt('pricing.emptyLink') }}</router-link>
          {{ pt('pricing.emptyTail') }}
        </template>
      </div>

      <div v-else class="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <div
          v-for="model in filteredModels"
          :key="model.name"
          class="group relative overflow-hidden rounded-2xl border border-gray-200/80 bg-white/70 p-5 shadow-sm backdrop-blur-xl transition-all duration-300 hover:-translate-y-1 hover:border-primary-500/40 hover:shadow-lg dark:border-dark-700/80 dark:bg-dark-900/60"
        >
          <span class="inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wider" :class="getPlatformTagClass(model.platform)">
            {{ model.platform }}
          </span>
          <h3 class="mt-2 text-sm font-bold font-mono text-gray-900 dark:text-white group-hover:text-primary-600 dark:group-hover:text-primary-400 transition-colors">
            {{ model.name }}
          </h3>

          <div class="mt-4 grid grid-cols-2 gap-2 rounded-xl bg-gray-50/80 p-3 dark:bg-dark-800/60">
            <div>
              <div class="text-[10px] text-gray-500 dark:text-dark-400">{{ pt('pricing.inputPrice') }}</div>
              <div class="text-xs font-bold font-mono text-gray-900 dark:text-white">
                {{ model.inputPrice }}
              </div>
            </div>
            <div>
              <div class="text-[10px] text-gray-500 dark:text-dark-400">{{ pt('pricing.outputPrice') }}</div>
              <div class="text-xs font-bold font-mono text-gray-900 dark:text-white">
                {{ model.outputPrice }}
              </div>
            </div>
          </div>

          <div v-if="model.supportCache" class="mt-3 text-[11px] font-medium text-primary-600 dark:text-primary-400">
            {{ pt('pricing.cacheRead') }}
          </div>
        </div>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { getModelPlaza, type ModelPlazaResponse } from '@/api/modelPlaza'
import { useAppStore, useAuthStore } from '@/stores'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import { formatScaled } from '@/utils/pricing'
import { usePortalI18n } from '../../i18n'

interface DisplayModel {
  name: string
  platform: string
  inputPrice: string
  outputPrice: string
  supportCache: boolean
}

type Status = 'pending' | 'hidden' | 'loading' | 'locked' | 'empty' | 'ready'

const PER_MILLION = 1_000_000
const MIN_DECIMALS = 2

const { pt } = usePortalI18n()
const appStore = useAppStore()
const authStore = useAuthStore()

const activePlatform = ref('all')
const searchQuery = ref('')
const status = ref<Status>('pending')
const modelsList = ref<DisplayModel[]>([])

const platformFilters = [
  { id: 'all', name: '' },
  { id: 'anthropic', name: 'Anthropic Claude' },
  { id: 'openai', name: 'OpenAI GPT' },
  { id: 'google', name: 'Google Gemini' },
  { id: 'deepseek', name: 'DeepSeek' }
]

const abort = new AbortController()
onBeforeUnmount(() => abort.abort())

function officialPerMillion(value: number | null | undefined): string {
  return value == null ? '-' : formatScaled(value, PER_MILLION, MIN_DECIMALS)
}

function toDisplayModels(res: ModelPlazaResponse): DisplayModel[] {
  const seen = new Map<string, DisplayModel>()
  for (const group of res.groups ?? []) {
    for (const m of group.models ?? []) {
      const official = m.official_pricing
      if (seen.has(m.name) || !official) continue
      if (official.input_price == null && official.output_price == null) continue
      seen.set(m.name, {
        name: m.name,
        platform: m.platform || group.platform || 'unknown',
        inputPrice: officialPerMillion(official.input_price),
        outputPrice: officialPerMillion(official.output_price),
        supportCache: official.cache_read_price != null
      })
    }
  }
  return [...seen.values()]
}

onMounted(async () => {
  await appStore.fetchPublicSettings()
  const hiddenByBackendMode = appStore.backendModeEnabled && !authStore.isAdmin
  if (!isFeatureFlagEnabled(FeatureFlags.modelPlaza) || hiddenByBackendMode) {
    status.value = 'hidden'
    return
  }
  const requiresAuth = appStore.cachedPublicSettings?.model_plaza_require_auth === true
  if (requiresAuth && !authStore.isAuthenticated) {
    status.value = 'locked'
    return
  }
  status.value = 'loading'
  try {
    modelsList.value = toDisplayModels(await getModelPlaza({ signal: abort.signal }))
    status.value = modelsList.value.length > 0 ? 'ready' : 'empty'
  } catch {
    status.value = 'empty'
  }
})

const filteredModels = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()
  return modelsList.value.filter(m => {
    const matchPlatform = activePlatform.value === 'all' || m.platform.toLowerCase().includes(activePlatform.value)
    return matchPlatform && (!query || m.name.toLowerCase().includes(query))
  })
})

function getPlatformTagClass(p: string): string {
  const norm = p.toLowerCase()
  if (norm.includes('anthropic') || norm.includes('claude')) {
    return 'bg-orange-100 text-orange-700 dark:bg-orange-950/60 dark:text-orange-300'
  }
  if (norm.includes('openai') || norm.includes('gpt')) {
    return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-300'
  }
  if (norm.includes('google') || norm.includes('gemini')) {
    return 'bg-blue-100 text-blue-700 dark:bg-blue-950/60 dark:text-blue-300'
  }
  if (norm.includes('deepseek')) {
    return 'bg-indigo-100 text-indigo-700 dark:bg-indigo-950/60 dark:text-indigo-300'
  }
  return 'bg-gray-100 text-gray-700 dark:bg-dark-800 dark:text-gray-300'
}
</script>
