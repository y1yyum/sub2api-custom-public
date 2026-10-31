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
  /* 16×16 pixel icons in the light tones (theme/pixelIcons.ts, kept equal by rhineHome.spec.ts). */
  --rp-cube: url("data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' width='16' height='16' shape-rendering='crispEdges'><path fill='%232c2e33' d='M7 1h2v1h-2zM5 2h2v1h-2zM9 2h2v1h-2zM3 3h2v1h-2zM11 3h2v1h-2zM1 4h2v1h-2zM13 4h2v1h-2zM1 5h1v1h-1zM3 5h2v1h-2zM11 5h2v1h-2zM14 5h1v1h-1zM1 6h1v1h-1zM5 6h2v1h-2zM9 6h2v1h-2zM14 6h1v1h-1zM1 7h1v1h-1zM7 7h2v1h-2zM14 7h1v1h-1zM1 8h1v1h-1zM8 8h1v1h-1zM14 8h1v1h-1zM1 9h1v1h-1zM8 9h1v1h-1zM14 9h1v1h-1zM1 10h1v1h-1zM8 10h1v1h-1zM14 10h1v1h-1zM1 11h2v1h-2zM8 11h1v1h-1zM13 11h2v1h-2zM3 12h2v1h-2zM8 12h1v1h-1zM11 12h2v1h-2zM5 13h2v1h-2zM8 13h1v1h-1zM10 13h1v1h-1zM7 14h3v1h-3z'/><path fill='%23f7ece8' d='M7 2h2v1h-2zM5 3h6v1h-6zM3 4h10v1h-10zM5 5h6v1h-6zM7 6h2v1h-2z'/><path fill='%23522b22' d='M2 5h1v1h-1zM2 6h3v1h-3zM2 7h5v1h-5zM2 8h6v1h-6zM2 9h6v1h-6zM2 10h6v1h-6zM3 11h5v1h-5zM5 12h3v1h-3zM7 13h1v1h-1z'/><path fill='%23e8b4a6' d='M13 5h1v1h-1zM11 6h3v1h-3zM9 7h5v1h-5zM9 8h5v1h-5zM9 9h5v1h-5zM9 10h5v1h-5zM9 11h4v1h-4zM9 12h2v1h-2zM9 13h1v1h-1z'/></svg>");
  --rp-shield: url("data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' width='16' height='16' shape-rendering='crispEdges'><path fill='%232c2e33' d='M7 1h2v1h-2zM5 2h2v1h-2zM9 2h2v1h-2zM2 3h3v1h-3zM11 3h3v1h-3zM2 4h1v1h-1zM13 4h1v1h-1zM2 5h1v1h-1zM13 5h1v1h-1zM2 6h1v1h-1zM13 6h1v1h-1zM2 7h1v1h-1zM13 7h1v1h-1zM2 8h1v1h-1zM13 8h1v1h-1zM2 9h1v1h-1zM13 9h1v1h-1zM3 10h1v1h-1zM12 10h1v1h-1zM4 11h1v1h-1zM11 11h1v1h-1zM5 12h1v1h-1zM10 12h1v1h-1zM6 13h1v1h-1zM9 13h1v1h-1zM7 14h2v1h-2z'/><path fill='%23e8f2e8' d='M7 2h1v1h-1zM5 3h3v1h-3zM3 4h5v1h-5zM3 5h5v1h-5zM3 6h5v1h-5zM3 7h5v1h-5zM3 8h5v1h-5zM3 9h5v1h-5zM4 10h4v1h-4zM5 11h3v1h-3zM6 12h2v1h-2zM7 13h1v1h-1z'/><path fill='%23a8cba6' d='M8 2h1v1h-1zM8 3h3v1h-3zM8 4h5v1h-5zM8 5h5v1h-5zM8 6h5v1h-5zM8 7h5v1h-5zM8 8h5v1h-5zM8 9h5v1h-5zM8 10h4v1h-4zM8 11h3v1h-3zM8 12h2v1h-2zM8 13h1v1h-1z'/></svg>");
  --rp-inbox: url("data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' width='16' height='16' shape-rendering='crispEdges'><path fill='%232c2e33' d='M3 2h10v1h-10zM3 3h1v1h-1zM12 3h1v1h-1zM2 4h1v1h-1zM13 4h1v1h-1zM2 5h1v1h-1zM13 5h1v1h-1zM1 6h1v1h-1zM14 6h1v1h-1zM1 7h1v1h-1zM14 7h1v1h-1zM0 8h16v1h-16zM0 9h1v1h-1zM5 9h1v1h-1zM10 9h1v1h-1zM15 9h1v1h-1zM0 10h1v1h-1zM6 10h4v1h-4zM15 10h1v1h-1zM0 11h1v1h-1zM15 11h1v1h-1zM0 12h1v1h-1zM15 12h1v1h-1zM0 13h16v1h-16z'/><path fill='%23ffffff' d='M4 3h8v1h-8zM3 4h10v1h-10zM3 5h10v1h-10zM2 6h12v1h-12zM2 7h12v1h-12z'/><path fill='%23d4c5b0' d='M1 9h4v1h-4zM11 9h4v1h-4zM1 10h5v1h-5zM10 10h5v1h-5zM1 11h14v1h-14zM1 12h14v1h-14z'/></svg>");
  /* 2px square dots on a 6px grid, and 4px stair-stepped stripes on an 8px tile. */
  --rp-dots: url("data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' width='6' height='6' shape-rendering='crispEdges'><path fill='%232c2e33' fill-opacity='0.12' d='M0 0h2v2H0z'/></svg>");
  --rp-stripes: url("data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' width='8' height='8' shape-rendering='crispEdges'><path fill='%232c2e33' d='M0 0h4v1H0zM7 1h1v1H7zM0 1h3v1H0zM6 2h2v1H6zM0 2h2v1H0zM5 3h3v1H5zM0 3h1v1H0zM4 4h4v1H4zM3 5h4v1H3zM2 6h4v1H2zM1 7h4v1H1z'/></svg>");
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
  border: 3px double var(--rh-ink);
  border-radius: 0;
  color: var(--rh-ink);
  background: #f8f7f2;
  cursor: pointer;
  transition: background-color 0.15s steps(2), color 0.15s steps(2);
  background-clip: border-box;
}

.status-card-bottom button:hover:not(:disabled) {
  background-color: #e8ded2;
}

.status-card-bottom button:active:not(:disabled) {
  background-color: var(--rh-ink);
  color: #f8f7f2;
}

.status-card-bottom button:disabled {
  opacity: 0.4;
  cursor: wait;
}

/* Loading, unavailable and empty share one placeholder: a dotted field with a 48px pixel icon. */
.status-empty {
  --rp-icon: var(--rp-inbox);
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  margin: 1em 0;
  padding: 24px 12px;
  font-size: 11px;
  line-height: 1.8;
  text-align: center;
  color: #62695c;
  background: var(--rp-dots) 0 0 / 6px 6px;
}

.status-empty::before {
  content: '';
  width: 48px;
  height: 48px;
  background: #f8f7f2 var(--rp-icon) center / 48px 48px no-repeat;
  image-rendering: pixelated;
}

[data-state='loading'] .status-empty {
  --rp-icon: var(--rp-cube);
}

[data-state='unavailable'] .status-empty {
  --rp-icon: var(--rp-shield);
}

/* Only loading gets the bar: stair-stepped stripes inside two rules, moving 4px per step. */
[data-state='loading'] .status-empty::after {
  content: '';
  width: 96px;
  height: 12px;
  border: 3px double var(--rh-ink, #2c2e33);
  background: #f8f7f2 var(--rp-stripes) 0 0 / 8px 8px;
  image-rendering: pixelated;
  animation: rp-stripes 400ms steps(2) infinite;
}

@keyframes rp-stripes {
  to {
    background-position: 8px 0;
  }
}

@media (prefers-reduced-motion: reduce) {
  [data-state='loading'] .status-empty::after {
    animation: none;
  }
}

/* Forced colours repaint borders but not border images, which would keep the landing ink. */</style>
