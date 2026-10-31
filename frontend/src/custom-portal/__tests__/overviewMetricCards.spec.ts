import './enableI18nJit'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import type { TrendDataPoint } from '@/types'
import { emptyTotals } from '../overview/usageChartData'
import { createOverviewState, OVERVIEW_STATE_KEY, type OverviewState } from '../overview/usageChartState'
import OverviewMetricCards from '../components/overview/OverviewMetricCards.vue'
import UsageSparkline from '../components/overview/UsageSparkline.vue'

const api = vi.hoisted(() => ({
  getDashboardTrend: vi.fn(),
  getDashboardModels: vi.fn()
}))

vi.mock('@/api/usage', () => ({ usageAPI: api }))
vi.mock('@/api/keys', () => ({ keysAPI: { list: vi.fn() } }))
vi.mock('@/api/groups', () => ({ userGroupsAPI: { getAvailable: vi.fn() } }))

enableAutoUnmount(afterEach)

const NOW = new Date(2024, 2, 3, 10, 30)
const CURRENT_END = '2024-03-03'
const point = (date: string, patch: Partial<TrendDataPoint> = {}): TrendDataPoint => ({ date, ...emptyTotals(), ...patch })

const CURRENT = [
  point('2024-03-01', { requests: 10, total_tokens: 2000, input_tokens: 300, cache_read_tokens: 100, actual_cost: 1.5 }),
  point('2024-03-02', { requests: 30, total_tokens: 4000, input_tokens: 500, cache_read_tokens: 100, actual_cost: 2 })
]
const PREVIOUS = [point('2024-01-20', { requests: 20, total_tokens: 6000 })]

/** The current and previous windows share one endpoint; tell them apart by end date. */
function mockTrend(current: () => Promise<unknown>, previous: () => Promise<unknown>) {
  api.getDashboardTrend.mockImplementation((params: { end_date: string }) =>
    params.end_date === CURRENT_END ? current() : previous()
  )
}

const scopes: ReturnType<typeof effectScope>[] = []
async function loadedState(): Promise<OverviewState> {
  const scope = effectScope()
  scopes.push(scope)
  const state = scope.run(() => createOverviewState({ now: () => NOW, storage: null, debounceMs: 0 }))!
  await state.refresh()
  return state
}

function mountCards(state: OverviewState) {
  const i18n = createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: {} })
  return mount(OverviewMetricCards, {
    attachTo: document.body,
    global: { plugins: [i18n], provide: { [OVERVIEW_STATE_KEY as symbol]: state } }
  })
}

beforeEach(() => {
  vi.spyOn(console, 'warn').mockImplementation(() => {})
  mockTrend(
    async () => ({ trend: CURRENT }),
    async () => ({ trend: PREVIOUS })
  )
  api.getDashboardModels.mockResolvedValue({ models: [] })
})

afterEach(() => {
  scopes.splice(0).forEach((scope) => scope.stop())
  vi.clearAllMocks()
  vi.restoreAllMocks()
})

describe('OverviewMetricCards', () => {
  it('exposes the full formatted amount when a narrow card may truncate it', async () => {
    mockTrend(
      async () => ({ trend: [point('2024-03-01', { actual_cost: 1234567.89 })] }),
      async () => ({ trend: PREVIOUS })
    )
    const wrapper = mountCards(await loadedState())
    await flushPromises()

    expect(wrapper.get('[data-testid="overview-metric-cost-value"]').text()).toBe('$1234567.89')
    expect(wrapper.get('[data-testid="overview-metric-cost-value"]').attributes('title')).toBe('$1234567.89')
  })

  it('renders four tabs with formatted values and comparison badges', async () => {
    const wrapper = mountCards(await loadedState())
    await flushPromises()

    const tabs = wrapper.findAll('[role="tab"]')
    expect(tabs).toHaveLength(4)
    expect(wrapper.get('[role="tablist"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="overview-metric-tokens"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get('[data-testid="overview-metric-tokens"]').attributes('tabindex')).toBe('0')
    expect(wrapper.get('[data-testid="overview-metric-cost"]').attributes('tabindex')).toBe('-1')

    expect(wrapper.get('[data-testid="overview-metric-tokens-value"]').text()).toBe('6,000')
    expect(wrapper.get('[data-testid="overview-metric-requests-value"]').text()).toBe('40')
    expect(wrapper.get('[data-testid="overview-metric-cost-value"]').text()).toBe('$3.50')
    expect(wrapper.get('[data-testid="overview-metric-cacheHitRate-value"]').text()).toBe('20.0%')

    const requests = wrapper.get('[data-testid="overview-metric-requests-delta"]')
    expect(requests.attributes('data-kind')).toBe('up')
    expect(requests.text()).toContain('+100.0%')
    expect(wrapper.get('[data-testid="overview-metric-tokens-delta"]').attributes('data-kind')).toBe('flat')
    // The previous period had no spend, so the badge reads "new" instead of a percentage.
    const cost = wrapper.get('[data-testid="overview-metric-cost-delta"]')
    expect(cost.attributes('data-kind')).toBe('new')
    expect(cost.text()).not.toContain('%')
    // No prompt tokens in the previous period: no comparison is possible.
    expect(wrapper.get('[data-testid="overview-metric-cacheHitRate-delta"]').attributes('data-kind')).toBe('none')
  })

  it('switches metrics with arrow keys, Home and End and moves focus', async () => {
    const state = await loadedState()
    const wrapper = mountCards(state)
    const tablist = wrapper.get('[role="tablist"]')

    await tablist.trigger('keydown', { key: 'ArrowRight' })
    expect(state.metric.value).toBe('requests')
    expect(wrapper.get('[data-testid="overview-metric-requests"]').attributes('aria-selected')).toBe('true')
    expect(document.activeElement?.id).toBe('overview-metric-tab-requests')

    await tablist.trigger('keydown', { key: 'End' })
    expect(state.metric.value).toBe('cacheHitRate')
    await tablist.trigger('keydown', { key: 'ArrowRight' })
    expect(state.metric.value).toBe('tokens')
    await tablist.trigger('keydown', { key: 'ArrowLeft' })
    expect(state.metric.value).toBe('cacheHitRate')
    await tablist.trigger('keydown', { key: 'Home' })
    expect(state.metric.value).toBe('tokens')
    expect(document.activeElement?.id).toBe('overview-metric-tab-tokens')

    await wrapper.get('[data-testid="overview-metric-cost"]').trigger('click')
    expect(state.metric.value).toBe('cost')
  })

  it('shows a dash when the trend fails and no badge when the previous period fails', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    mockTrend(
      async () => ({ trend: CURRENT }),
      () => Promise.reject(new Error('previous down'))
    )
    const wrapper = mountCards(await loadedState())
    expect(wrapper.get('[data-testid="overview-metric-cost-delta"]').attributes('data-kind')).toBe('none')

    mockTrend(
      () => Promise.reject(new Error('trend down')),
      () => Promise.reject(new Error('previous down'))
    )
    const failed = mountCards(await loadedState())
    expect(failed.get('[data-testid="overview-metric-tokens-value"]').text()).toBe('—')
    expect(failed.find('[data-testid="usage-sparkline-baseline"]').exists()).toBe(true)
  })
})

describe('UsageSparkline', () => {
  const bars = (values: (number | null)[]) =>
    mount(UsageSparkline, { props: { values } })
      .findAll('[data-testid="usage-sparkline-bar"]')
      .map((bar) => ({ x: Number(bar.attributes('x')), height: Number(bar.attributes('height')), opacity: Number(bar.attributes('fill-opacity')) }))

  it('draws square bars in 4px steps on an 80×32 grid, hidden from assistive tech', () => {
    const wrapper = mount(UsageSparkline, { props: { values: [1, 3, null, 2, 4] } })
    const svg = wrapper.get('svg')
    expect(svg.attributes()).toMatchObject({ 'aria-hidden': 'true', viewBox: '0 0 80 32', 'shape-rendering': 'crispEdges' })
    expect(bars([1, 3, null, 2, 4])).toEqual([
      { x: 0, height: 8, opacity: 0.52 },
      { x: 8, height: 24, opacity: 0.64 },
      { x: 24, height: 16, opacity: 0.88 },
      { x: 32, height: 32, opacity: 1 }
    ])
  })

  it('averages long series into ten bars that fade in from the left', () => {
    const drawn = bars(Array.from({ length: 30 }, (_, index) => index + 1))
    expect(drawn).toHaveLength(10)
    for (const bar of drawn) expect(bar.height % 4).toBe(0)
    expect(drawn.map((bar) => bar.x)).toEqual([0, 8, 16, 24, 32, 40, 48, 56, 64, 72])
    expect(drawn.map((bar) => bar.opacity)).toEqual([...drawn.map((bar) => bar.opacity)].sort((a, b) => a - b))
    expect(drawn.at(-1)).toMatchObject({ height: 32, opacity: 1 })
  })

  it('keeps a baseline without data or without usage', () => {
    for (const values of [[], [null, null], [0, 0, 0]]) {
      const wrapper = mount(UsageSparkline, { props: { values } })
      expect(wrapper.find('[data-testid="usage-sparkline-baseline"]').exists(), JSON.stringify(values)).toBe(true)
      expect(wrapper.find('[data-testid="usage-sparkline-bar"]').exists()).toBe(false)
    }
  })
})
