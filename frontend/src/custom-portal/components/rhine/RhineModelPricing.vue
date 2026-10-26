<template>
  <section
    class="model-status"
    :aria-label="state === 'unavailable' ? pt('rhineHome.pricing.labelUnavailable') : pt('rhineHome.pricing.label')"
    :aria-busy="state === 'loading'"
    data-testid="rhine-model-pricing"
    :data-state="state"
  >
    <div class="model-columns" :title="pt('rhineHome.pricing.priceHint')">
      <span>{{ pt('rhineHome.pricing.columnModel') }}</span>
      <span>{{ pt('rhineHome.pricing.columnInput') }}</span>
      <span>{{ pt('rhineHome.pricing.columnOutput') }}</span>
    </div>
    <div class="model-rows" aria-live="polite">
      <p v-if="state === 'loading'" class="status-empty">{{ pt('rhineHome.pricing.loading') }}</p>
      <p v-else-if="state === 'unavailable'" class="status-empty">{{ pt('rhineHome.pricing.unavailable') }}</p>
      <p v-else-if="models.length === 0" class="status-empty">{{ pt('rhineHome.pricing.empty') }}</p>
      <template v-else>
        <div v-for="model in models" :key="model.id" class="model-row" data-testid="rhine-model-row">
          <div class="model-description">
            <strong :title="model.name">{{ model.name }}</strong>
            <span>{{ model.platform }}</span>
          </div>
          <template v-if="model.price.kind === 'token'">
            <span class="model-metric">{{ perMillion(model.price.input) }}</span>
            <span class="model-metric">{{ perMillion(model.price.output) }}</span>
          </template>
          <span v-else class="model-metric model-unit">{{ unitPrice(model.price) }}</span>
        </div>
      </template>
    </div>
    <div class="status-card-bottom">
      <span class="status-unit">{{ pt('rhineHome.pricing.unit') }}</span>
      <button
        type="button"
        :aria-label="pt('rhineHome.pricing.refresh')"
        :title="pt('rhineHome.pricing.refresh')"
        :disabled="state === 'loading' || access !== 'open'"
        data-testid="rhine-model-refresh"
        @click="load"
      >
        ↻
      </button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { formatScaled } from '@/utils/pricing'
import { usePortalI18n } from '../../i18n'
import {
  fetchHomepageModels,
  HOMEPAGE_MODELS_TIMEOUT_MS,
  type HomepageModel,
  type HomepageModelPrice,
  type PlazaAccess
} from '../../api/homepage'

type LoadState = 'loading' | 'ready' | 'unavailable'

const props = defineProps<{ access: PlazaAccess }>()

const PER_MILLION = 1_000_000
const MIN_DECIMALS = 2

const { pt } = usePortalI18n()
const state = ref<LoadState>('loading')
const models = shallowRef<HomepageModel[]>([])
let controller: AbortController | null = null

function perMillion(value: number | null): string {
  return formatScaled(value, PER_MILLION, MIN_DECIMALS)
}

function unitPrice(price: Extract<HomepageModelPrice, { unit: number }>): string {
  const suffix = price.kind === 'image' ? pt('rhineHome.pricing.perImage') : pt('rhineHome.pricing.perRequest')
  return `${formatScaled(price.unit, 1, MIN_DECIMALS)} ${suffix}`
}

async function load(): Promise<void> {
  if (controller || props.access !== 'open') return
  const current = new AbortController()
  controller = current
  state.value = 'loading'
  const timer = setTimeout(() => current.abort(), HOMEPAGE_MODELS_TIMEOUT_MS)
  try {
    models.value = await fetchHomepageModels(current.signal)
    state.value = 'ready'
  } catch {
    models.value = []
    state.value = 'unavailable'
  } finally {
    clearTimeout(timer)
    if (controller === current) controller = null
  }
}

function abort() {
  controller?.abort()
  controller = null
}

watch(
  () => props.access,
  (access) => {
    if (access === 'open') return void load()
    abort()
    models.value = []
    state.value = access === 'pending' ? 'loading' : 'unavailable'
  },
  { immediate: true }
)
onBeforeUnmount(abort)

defineExpose({ load })
</script>

<style scoped>
.model-status {
  padding: 14px;
}

.model-columns,
.model-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 54px 54px;
  gap: 6px;
  align-items: center;
}

.model-columns {
  font-size: 8px;
  font-family: ui-monospace, 'Courier New', monospace;
  font-weight: 700;
  letter-spacing: 0.06em;
  color: #62695c;
  border-bottom: 2px solid #e0ded6;
  padding-bottom: 6px;
  background-image: repeating-conic-gradient(rgba(44, 46, 51, 0.06) 0 25%, transparent 0 50%);
  background-size: 4px 4px;
}

.model-columns > span:not(:first-child),
.model-metric {
  text-align: right;
}

.model-rows {
  display: flex;
  flex: 1;
  min-height: 0;
  flex-direction: column;
  justify-content: space-around;
  overflow-y: auto;
  scrollbar-width: thin;
}

.model-row {
  padding: 5px 0;
  border-bottom: 1px solid #ebe8df;
  flex-shrink: 0;
}

.model-row:last-child {
  border-bottom: none;
}

.model-description {
  min-width: 0;
}

.model-description strong {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: clamp(11px, 0.9vw, 13px);
  font-weight: 700;
  color: #2c2e33;
}

.model-description > span {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 8px;
  font-family: ui-monospace, 'Courier New', monospace;
  margin-top: 3px;
  color: #62695c;
  white-space: nowrap;
}

.model-unit {
  grid-column: span 2;
}

.model-metric {
  font-size: 12px;
  font-family: var(--pp-font-pixel);
  font-variant-numeric: tabular-nums;
  font-weight: 700;
  color: #4a6344;
}

.status-card-bottom {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  flex-shrink: 0;
  font-size: 8px;
  font-family: ui-monospace, 'Courier New', monospace;
  color: #62695c;
  border-top: 2px solid #e0ded6;
  padding-top: 6px;
}

.status-unit {
  white-space: nowrap;
}

.status-card-bottom button {
  font-size: 12px;
  font-family: var(--pp-font-pixel);
  padding: 1px 5px;
  line-height: 1.2;
  border: 2px solid var(--rh-ink);
  border-radius: 0;
  color: var(--rh-ink);
  background: #f8f7f2;
  box-shadow: 1px 1px 0 var(--rh-ink);
  cursor: pointer;
  transition: transform 0.08s steps(2), box-shadow 0.08s steps(2), background-color 0.15s steps(2);
  border-image: var(--rh-notch-sm) 2 / 2px;
  background-clip: padding-box;
}

.status-card-bottom button:hover:not(:disabled) {
  background-color: #e8ded2;
  transform: translate(-1px, -1px);
  box-shadow: 2px 2px 0 var(--rh-ink);
}

.status-card-bottom button:active:not(:disabled) {
  transform: translate(1px, 1px);
  box-shadow: 0 0 0 var(--rh-ink);
}

.status-card-bottom button:disabled {
  opacity: 0.4;
  cursor: wait;
}

.status-empty {
  margin: 1em 0;
  font-size: 11px;
  line-height: 1.8;
  color: #62695c;
}

/* Forced colours repaint borders but not border images, which would keep the landing ink. */
@media (forced-colors: active) {
  .status-card-bottom button {
    border-image-source: none;
  }
}
</style>
