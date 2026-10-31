import './enableI18nJit'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { reactive } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import type { ModelPlazaGroup, ModelPlazaResponse, PlazaModel } from '@/api/modelPlaza'
import { summarizeHomepageModels, type HomepageModel } from '../api/homepage'
import { createHash } from 'node:crypto'
import postcss from 'postcss'
import { contrastRatio, declarationsIn, scopedStyles } from './contrast'
import homeSource from '../components/rhine/RhineHomePage.vue?raw'
import { PIXEL_LOGO_DATA_URI } from '../theme/pixelLogo'
import pricingSource from '../components/rhine/RhineModelPricing.vue?raw'

const fetchHomepageModels = vi.fn()
vi.mock('../api/homepage', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/homepage')>()
  return { ...actual, fetchHomepageModels: (signal?: AbortSignal) => fetchHomepageModels(signal) }
})

const appStore = reactive({
  siteName: 'Sub2API',
  docUrl: '',
  publicSettingsLoaded: true,
  backendModeEnabled: false,
  cachedPublicSettings: null as Record<string, unknown> | null,
  fetchPublicSettings: vi.fn()
})
const authStore = reactive({ isAuthenticated: false, isAdmin: false, checkAuth: vi.fn() })
vi.mock('@/stores', () => ({ useAppStore: () => appStore, useAuthStore: () => authStore }))
const copyToClipboard = vi.fn()
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard }) }))
vi.mock('@/components/common/LocaleSwitcher.vue', () => ({ default: { template: '<span />' } }))
vi.mock('@/views/HomeView.vue', async () => {
  const { h } = await import('vue')
  return { __esModule: true, default: { name: 'UpstreamHomeStub', render: () => h('div', { 'data-testid': 'upstream-home' }) } }
})

const RouterLinkStub = { props: ['to'], template: '<a :href="to"><slot /></a>' }

function mountWith(component: object, locale = 'zh', props: Record<string, unknown> = {}) {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: {} })
  return mount(component, { props, global: { plugins: [i18n], stubs: { RouterLink: RouterLinkStub } } })
}

type Pricing = NonNullable<PlazaModel['pricing']>

function model(name: string, pricing: Partial<Pricing> | null, officialOutput?: number): PlazaModel {
  return {
    name,
    platform: 'anthropic',
    pricing: pricing && {
      billing_mode: 'token',
      input_price: null,
      output_price: null,
      cache_write_price: null,
      cache_read_price: null,
      image_input_price: null,
      image_output_price: null,
      per_request_price: null,
      intervals: [],
      ...pricing
    },
    official_pricing: officialOutput == null ? null : ({ output_price: officialOutput } as PlazaModel['official_pricing'])
  }
}

function group(id: number, models: PlazaModel[], extra: Partial<ModelPlazaGroup> = {}): ModelPlazaGroup {
  return {
    id,
    name: `g${id}`,
    description: '',
    platform: 'anthropic',
    subscription_type: 'standard',
    rate_multiplier: 1,
    peak_rate_enabled: false,
    peak_start: '',
    peak_end: '',
    peak_rate_multiplier: 1,
    is_exclusive: false,
    image_rate_independent: false,
    image_rate_multiplier: 1,
    video_rate_independent: false,
    video_rate_multiplier: 1,
    long_context_pricing_enabled: false,
    models,
    ...extra
  }
}

function plaza(...groups: ModelPlazaGroup[]): ModelPlazaResponse {
  return { description: '', groups }
}

const tokenModel = (name: string, input: number, output: number, officialOutput?: number) =>
  model(name, { input_price: input, output_price: output }, officialOutput)
const imageModel = (name: string, unit: number) => model(name, { billing_mode: 'image', per_request_price: unit })

function setReducedMotion(reduce: boolean) {
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: reduce && query.includes('reduce'),
    media: query,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn()
  })) as unknown as typeof window.matchMedia
}

beforeEach(() => {
  fetchHomepageModels.mockReset()
  fetchHomepageModels.mockReturnValue(new Promise(() => {}))
  Object.assign(appStore, {
    siteName: 'Sub2API',
    docUrl: '',
    publicSettingsLoaded: true,
    backendModeEnabled: false,
    cachedPublicSettings: null
  })
  Object.assign(authStore, { isAuthenticated: false, isAdmin: false })
  authStore.checkAuth.mockReset()
  setReducedMotion(false)
})

enableAutoUnmount(afterEach)

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('summarizeHomepageModels', () => {
  it('reads only standard groups that are not exclusive', () => {
    const rows = summarizeHomepageModels(
      plaza(
        group(1, [tokenModel('open', 1e-6, 2e-6)]),
        group(2, [tokenModel('exclusive', 1e-6, 2e-6)], { is_exclusive: true }),
        group(3, [tokenModel('subscription', 1e-6, 2e-6)], { subscription_type: 'subscription' })
      )
    )
    expect(rows.map((row) => row.name)).toEqual(['open'])
  })

  it('prefers the user rate over the group rate and keeps the lowest price per model', () => {
    const [row] = summarizeHomepageModels(
      plaza(
        group(1, [tokenModel('sonnet', 4e-6, 20e-6)], { rate_multiplier: 0.8 }),
        group(2, [tokenModel('sonnet', 4e-6, 20e-6)], { rate_multiplier: 2, user_rate_multiplier: 0.5 })
      )
    )
    expect(row.price).toEqual({ kind: 'token', input: 2e-6, output: 10e-6 })
  })

  it('prices image and request models per unit with their own rates', () => {
    const rows = summarizeHomepageModels(
      plaza(
        group(
          1,
          [
            imageModel('img', 0.04),
            model('video', { billing_mode: 'video', intervals: [{ per_request_price: 0.3 }, { per_request_price: 0.2 }] as Pricing['intervals'] }),
            model('free', { billing_mode: 'per_request' }),
            model('unpriced', null)
          ],
          { rate_multiplier: 2, image_rate_independent: true, image_rate_multiplier: 0.5 }
        )
      )
    )
    expect(Object.fromEntries(rows.map((row) => [row.name, row.price]))).toEqual({
      img: { kind: 'image', unit: 0.02 },
      video: { kind: 'request', unit: 0.4 }
    })
  })

  it('lists token models first by official output price and keeps at most six', () => {
    const tokens = Array.from({ length: 7 }, (_, i) => tokenModel(`m${i}`, 1e-6, 1e-6, i * 1e-6))
    const rows = summarizeHomepageModels(plaza(group(1, [imageModel('img', 0.01), ...tokens])))
    expect(rows.map((row) => row.name)).toEqual(['m6', 'm5', 'm4', 'm3', 'm2', 'm1'])

    const mixed = summarizeHomepageModels(plaza(group(1, [imageModel('img', 0.01), tokens[0]])))
    expect(mixed.map((row) => row.name)).toEqual(['m0', 'img'])
  })

  it('prefers the token price when groups bill the same model differently, whatever the order', () => {
    const asImage = group(1, [imageModel('dual', 0.01)])
    const asToken = group(2, [tokenModel('dual', 1e-6, 2e-6)])
    for (const groups of [[asImage, asToken], [asToken, asImage]]) {
      expect(summarizeHomepageModels(plaza(...groups))[0].price.kind).toBe('token')
    }
  })
})

describe('RhineModelPricing', () => {
  async function mountPricing(access = 'open') {
    const { default: RhineModelPricing } = await import('../components/rhine/RhineModelPricing.vue')
    return mountWith(RhineModelPricing, 'zh', { access })
  }

  it('waits for the public settings and never requests a plaza the visitor cannot read', async () => {
    const wrapper = await mountPricing('pending')
    expect(wrapper.get('[data-testid="rhine-model-pricing"]').attributes('data-state')).toBe('loading')
    expect(fetchHomepageModels).not.toHaveBeenCalled()

    await wrapper.setProps({ access: 'closed' })
    expect(wrapper.get('[data-testid="rhine-model-pricing"]').attributes('data-state')).toBe('unavailable')
    expect(wrapper.find('[data-testid="rhine-model-refresh"]').attributes('disabled')).toBeDefined()
    expect(fetchHomepageModels).not.toHaveBeenCalled()

    await wrapper.setProps({ access: 'open' })
    expect(fetchHomepageModels).toHaveBeenCalledTimes(1)
  })

  it('shows a busy loading state and disables refresh while the request is pending', async () => {
    const wrapper = await mountPricing()
    const card = wrapper.find('[data-testid="rhine-model-pricing"]')
    expect(card.attributes('data-state')).toBe('loading')
    expect(card.attributes('aria-busy')).toBe('true')
    expect(card.text()).toContain('正在读取模型定价')
    expect(wrapper.find('[data-testid="rhine-model-refresh"]').attributes('disabled')).toBeDefined()
  })

  it('shows the unavailable state without any price when the plaza is closed', async () => {
    fetchHomepageModels.mockRejectedValue({ status: 404 })
    const wrapper = await mountPricing()
    await flushPromises()

    expect(wrapper.find('[data-testid="rhine-model-pricing"]').attributes('data-state')).toBe('unavailable')
    expect(wrapper.text()).toContain('模型定价暂不可用')
    expect(wrapper.findAll('[data-testid="rhine-model-row"]')).toHaveLength(0)
    expect(wrapper.text()).not.toMatch(/\$\d/)
  })

  it('shows the empty state when no standard group has a priced model', async () => {
    fetchHomepageModels.mockResolvedValue([])
    const wrapper = await mountPricing()
    await flushPromises()

    expect(wrapper.text()).toContain('暂无模型定价')
  })

  it('renders names as text and prices per million tokens or per unit', async () => {
    const rows: HomepageModel[] = [
      { id: 'anthropic:<b>A</b>', name: '<b>A</b>', platform: 'anthropic', price: { kind: 'token', input: 3e-6, output: null } },
      { id: 'openai:img', name: 'img', platform: 'openai', price: { kind: 'image', unit: 0.04 } }
    ]
    fetchHomepageModels.mockResolvedValue(rows)
    const wrapper = await mountPricing()
    await flushPromises()

    const [token, image] = wrapper.findAll('[data-testid="rhine-model-row"]')
    expect(token.find('strong').text()).toBe('<b>A</b>')
    expect(token.find('b').exists()).toBe(false)
    expect(token.findAll('.model-metric').map((cell) => cell.text())).toEqual(['$3.00', '-'])
    expect(image.find('.model-unit').text()).toBe('$0.04 / 张')
  })

  it('refreshes on demand and aborts the request on unmount', async () => {
    fetchHomepageModels.mockRejectedValueOnce(new Error('down'))
    const wrapper = await mountPricing()
    await flushPromises()

    fetchHomepageModels.mockReturnValue(new Promise(() => {}))
    await wrapper.get('[data-testid="rhine-model-refresh"]').trigger('click')
    expect(fetchHomepageModels).toHaveBeenCalledTimes(2)
    const signal = fetchHomepageModels.mock.calls[1][0] as AbortSignal

    wrapper.unmount()
    expect(signal.aborted).toBe(true)
  })
})

describe('RhineHomePage', () => {
  async function mountPage(locale = 'zh') {
    const { default: RhineHomePage } = await import('../components/rhine/RhineHomePage.vue')
    return mountWith(RhineHomePage, locale)
  }

  it('requests plaza pricing only when the plaza is open to this visitor', async () => {
    appStore.cachedPublicSettings = { model_plaza_enabled: true, model_plaza_require_auth: true }
    const wrapper = await mountPage()
    expect(fetchHomepageModels).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="rhine-model-pricing"]').attributes('data-state')).toBe('unavailable')

    authStore.isAuthenticated = true
    await flushPromises()
    expect(fetchHomepageModels).toHaveBeenCalledTimes(1)
  })

  it('leaves the pricing card closed while the plaza switch is off', async () => {
    appStore.cachedPublicSettings = { model_plaza_enabled: false }
    const wrapper = await mountPage()
    expect(fetchHomepageModels).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="rhine-nav-models"]').exists()).toBe(false)
  })

  it('sends visitors to login and signed-in users, admins included, to the user console', async () => {
    const wrapper = await mountPage()
    const start = () => wrapper.get('[data-testid="rhine-nav-start"]')
    expect(start().attributes('href')).toBe('/login?redirect=/dashboard')
    expect(start().text()).toContain('立即使用')

    authStore.isAuthenticated = true
    await flushPromises()
    expect(start().attributes('href')).toBe('/dashboard')
    expect(start().text()).toContain('进入控制台')

    authStore.isAdmin = true
    await flushPromises()
    expect(start().attributes('href')).toBe('/dashboard')

    appStore.backendModeEnabled = true
    await flushPromises()
    expect(start().attributes('href')).toBe('/admin/dashboard')
  })

  it('uses the public site name and only links a sanitized doc_url', async () => {
    appStore.cachedPublicSettings = { site_name: 'ai2e.cc', doc_url: 'javascript:alert(1)' }
    const wrapper = await mountPage('en')
    expect(wrapper.find('[data-testid="rhine-nav-docs"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('AI2E.CC / 01')
    expect(wrapper.text()).toContain('Access AI services through ai2e.cc')

    appStore.cachedPublicSettings = { site_name: 'ai2e.cc', doc_url: 'https://docs.example.com/start' }
    await flushPromises()
    const docs = wrapper.get('[data-testid="rhine-nav-docs"]')
    expect(docs.attributes('href')).toBe('https://docs.example.com/start')
    expect(docs.attributes('rel')).toBe('noopener noreferrer')
  })

  it('shows a signed-out visitor the base URL and copies it or a curl example', async () => {
    copyToClipboard.mockReset().mockResolvedValue(true)
    appStore.cachedPublicSettings = { api_base_url: 'https://api.example.com/' }
    const wrapper = await mountPage()
    expect(wrapper.get('h1').text()).toBe('快速接入')
    expect(wrapper.get('[data-testid="rhine-base-url"]').text()).toBe('https://api.example.com')

    await wrapper.get('[data-testid="rhine-copy-url"]').trigger('click')
    await flushPromises()
    expect(copyToClipboard).toHaveBeenLastCalledWith('https://api.example.com', '已复制')
    expect(wrapper.get('[data-testid="rhine-copy-url"]').text()).toBe('已复制')

    await wrapper.get('[data-testid="rhine-copy-curl"]').trigger('click')
    await flushPromises()
    expect(copyToClipboard.mock.lastCall?.[0]).toContain('curl https://api.example.com/v1/chat/completions')
    expect(wrapper.get('[data-testid="rhine-copy-url"]').text()).toBe('复制地址')
  })

  it('falls back to this origin and reports a failed copy', async () => {
    copyToClipboard.mockReset().mockResolvedValue(false)
    const wrapper = await mountPage()
    expect(wrapper.get('[data-testid="rhine-base-url"]').text()).toBe(window.location.origin)

    await wrapper.get('[data-testid="rhine-copy-url"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="rhine-copy-url"]').text()).toBe('复制失败')
  })

  it('re-checks the stored session on focus, pageshow and storage only while signed out', async () => {
    const wrapper = await mountPage()
    expect(authStore.checkAuth).toHaveBeenCalledTimes(1)

    window.dispatchEvent(new Event('focus'))
    window.dispatchEvent(new Event('pageshow'))
    window.dispatchEvent(new StorageEvent('storage', { key: 'auth_token' }))
    expect(authStore.checkAuth).toHaveBeenCalledTimes(4)

    authStore.isAuthenticated = true
    window.dispatchEvent(new Event('focus'))
    expect(authStore.checkAuth).toHaveBeenCalledTimes(4)

    authStore.isAuthenticated = false
    wrapper.unmount()
    window.dispatchEvent(new Event('focus'))
    window.dispatchEvent(new Event('pageshow'))
    window.dispatchEvent(new StorageEvent('storage', { key: 'auth_token' }))
    expect(authStore.checkAuth).toHaveBeenCalledTimes(4)
  })

  it('runs the entry animation once and clears its timer on unmount', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    const wrapper = await mountPage()
    await flushPromises()
    const root = wrapper.get('[data-testid="rhine-home"]')
    expect(root.classes()).toContain('is-entering')

    vi.advanceTimersByTime(3100)
    await flushPromises()
    expect(root.classes()).toContain('entered')

    await wrapper.get('[data-testid="rhine-replay"]').trigger('click')
    await flushPromises()
    expect(root.classes()).toContain('is-entering')
    expect(vi.getTimerCount()).toBe(1)

    wrapper.unmount()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('preloads the pixel font once', async () => {
    document.head.querySelectorAll('link[rel="preload"]').forEach((link) => link.remove())
    await mountPage()
    await mountPage()
    const links = document.head.querySelectorAll<HTMLLinkElement>('link[rel="preload"][as="font"]')
    expect(links).toHaveLength(1)
    expect(links[0].getAttribute('href')).toMatch(/fusion-pixel-12px-proportional-zh_hans\.subset\.woff2/)
    expect(links[0].type).toBe('font/woff2')
    expect(links[0].hasAttribute('crossorigin')).toBe(true)
  })

  it('skips the entry animation when reduced motion is preferred', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    setReducedMotion(true)
    const wrapper = await mountPage()
    await flushPromises()

    expect(wrapper.get('[data-testid="rhine-home"]').classes()).toContain('entered')
    expect(vi.getTimerCount()).toBe(0)
  })
})

describe('RhineHomeView', () => {
  async function mountView() {
    const { default: RhineHomeView } = await import('../views/RhineHomeView.vue')
    const wrapper = mountWith(RhineHomeView)
    await vi.dynamicImportSettled()
    await flushPromises()
    return wrapper
  }

  it('serves the portal homepage by default', async () => {
    const wrapper = await mountView()
    expect(wrapper.find('[data-testid="rhine-home"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="upstream-home"]').exists()).toBe(false)
  })

  it.each([
    ['home_content', { home_content: '<p>custom</p>' }],
    ['compact_home_enabled', { compact_home_enabled: true }]
  ])('falls back to the upstream HomeView when %s is configured', async (_name, settings) => {
    appStore.cachedPublicSettings = { home_content: '', compact_home_enabled: false, ...settings }
    const wrapper = await mountView()
    expect(wrapper.find('[data-testid="upstream-home"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="rhine-home"]').exists()).toBe(false)
  })
})

describe('Rhine homepage styles', () => {
  it('never target global selectors that would leak into login or console pages', () => {
    const sources = import.meta.glob(['../components/rhine/*.vue', '../views/RhineHomeView.vue'], {
      query: '?raw',
      import: 'default',
      eager: true
    }) as Record<string, string>

    for (const [path, source] of Object.entries(sources)) {
      const styles = [...source.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((m) => m[1]).join('\n')
      if (source.includes('<style')) expect(source, path).toMatch(/<style scoped>/)
      const selectors = styles
        .replace(/\/\*[\s\S]*?\*\//g, '')
        .replace(/@keyframes[^{]+\{(?:[^{}]*\{[^}]*\})*[^}]*\}/g, '')
        .split('{')
        .slice(0, -1)
        .map((chunk) => chunk.split('}').pop()!.trim())
        .filter((selector) => selector && !selector.startsWith('@'))
      const leaking = selectors
        .flatMap((selector) => selector.split(/,(?![^(]*\))/))
        .map((selector) => selector.trim())
        .filter((selector) => /^(:root|html|body|\*|footer|main|button|a)\b|^\*|:global\(/.test(selector))
      expect(leaking, path).toEqual([])
    }
  })

  it('keeps the DOM of the landing page identical to the pre-restyle templates', () => {
    // sha256 of each <template> block at 9ef2bea2e^; the pixel restyle must not touch markup.
    const templateHash = (source: string) =>
      createHash('sha256')
        .update(source.replace(/\r\n/g, '\n').match(/^<template>[\s\S]*?^<\/template>$/m)![0])
        .digest('hex')
    expect(templateHash(homeSource)).toBe('fecbbe2e3031aec87c5fbff72f281b93f0c3a730b0cf60e5ca80d86364f56545')
    expect(templateHash(pricingSource)).toBe('ffd9a8f10fc5a8e1ae466f32585f7c378e00f8b5e6d50b6edf00a2b12e74bc87')
  })

  it('sets pixel-font text only at whole multiples of the 12px grid', () => {
    const pixelSelectors = new Set<string>()
    const sizes = new Map<string, string[]>()
    for (const css of [scopedStyles(homeSource), scopedStyles(pricingSource)]) {
      postcss.parse(css).walkRules((rule) => {
        if (rule.parent?.type === 'atrule' && /keyframes$/.test((rule.parent as { name: string }).name)) return
        rule.walkDecls((decl) => {
          for (const selector of rule.selectors) {
            if (decl.prop === 'font-family' && decl.value.includes('--pp-font-pixel')) pixelSelectors.add(selector)
            if (decl.prop === 'font-size') sizes.set(selector, [...(sizes.get(selector) ?? []), decl.value])
          }
        })
      })
    }
    expect([...pixelSelectors]).toEqual(
      expect.arrayContaining(['.brand-name', '.greeting h1', '.quick-url', '.quick-actions button', '.nav-name', '.information-title h2', '.model-metric'])
    )
    const blurry = [...pixelSelectors].flatMap((selector) =>
      (sizes.get(selector) ?? []).filter((size) => !/^(12|24|36|48)px$/.test(size)).map((size) => `${selector}: ${size}`)
    )
    expect(blurry).toEqual([])
  })

  describe('text contrast', () => {
    const home = scopedStyles(homeSource)
    const pricing = scopedStyles(pricingSource)
    const tokens = declarationsIn(home, '.rhine-home')
    const resolve = (value: string) => value.replace(/var\((--[\w-]+)\)/g, (_, name: string) => tokens[name])
    const paper = tokens['--rh-paper']
    const surface = '#ffffff'

    type Sample = [css: string, selector: string, background: string, foreground?: string]
    const samples: Sample[] = [
      [home, '.side-label', paper],
      [home, '.brand-caption', paper],
      [home, '.quick-label', surface],
      [home, '.quick-actions button', 'self'],
      [home, '.quick-actions button:hover', 'self'],
      [home, '.rhine-home :deep(::selection)', 'self'],
      [home, '.greeting p', paper],
      [home, '.panel-topline', surface],
      [home, '.nav-number', surface],
      [home, '.nav-number', '#f4f2ea'],
      [home, '.nav-item.primary-action', 'self'],
      [home, '.primary-action .nav-number', 'var(--rh-accent)'],
      [home, '.primary-action .nav-number', 'var(--rh-accent-hover)'],
      [home, '.nav-item.primary-action:hover', 'self', declarationsIn(home, '.nav-item.primary-action').color],
      [home, '.information-tab', 'self'],
      [home, '.information-title > span', 'var(--rh-ink)'],
      [home, '.information-copy p', '#faf9f5'],
      [home, '.panel-index', surface],
      [home, '.identity-label', paper],
      [home, '.identity-bottom', paper],
      [home, '.rh-footer', paper],
      [home, '.footer-copy span + span', paper],
      [pricing, '.model-columns', surface],
      [pricing, '.model-description strong', surface],
      [pricing, '.model-description > span', surface],
      [pricing, '.model-metric', surface],
      [pricing, '.status-card-bottom', surface],
      [pricing, '.status-empty', surface]
    ]

    const cases = samples.map(([css, selector, background, foreground]) => ({ css, selector, background, foreground }))

    it.each(cases)('$selector on $background reaches 4.5:1', ({ css, selector, background, foreground }) => {
      const own = declarationsIn(css, selector)
      const fg = resolve(foreground ?? own.color)
      const bg = resolve(background === 'self' ? (own['background-color'] ?? own.background) : background)
      expect(contrastRatio(fg, bg), `${selector}: ${fg} on ${bg}`).toBeGreaterThanOrEqual(4.5)
    })
  })

  describe('pixel cues', () => {
    const home = scopedStyles(homeSource)
    const pricing = scopedStyles(pricingSource)
    const tokens = declarationsIn(home, '.rhine-home')

    const outsideMedia = (rule: postcss.Rule) => rule.parent?.type !== 'atrule'
    /** Property names of every rule outside @media listing `selector`, in source order. */
    const propertyOrder = (css: string, selector: string) => {
      const order: string[] = []
      postcss.parse(css).walkRules((rule) => {
        if (outsideMedia(rule) && rule.selectors.includes(selector)) rule.walkDecls((decl) => void order.push(decl.prop))
      })
      return order
    }
    const mediaSelectors = (css: string, query: string) => {
      const selectors: string[] = []
      postcss.parse(css).walkAtRules('media', (media) => {
        if (media.params === query) media.walkRules((rule) => void selectors.push(...rule.selectors))
      })
      return selectors
    }
    const blend = (rgba: string, under: string) => {
      const [r, g, b, a] = /rgba\((\d+), (\d+), (\d+), ([\d.]+)\)/.exec(rgba)!.slice(1).map(Number)
      const base = [1, 3, 5].map((i) => parseInt(under.slice(i, i + 2), 16))
      return `#${[r, g, b].map((c, k) => Math.round(c * a + base[k] * (1 - a)).toString(16).padStart(2, '0')).join('')}`
    }

    const framed: [css: string, selector: string][] = [
      [home, '.quick-start'],
      [home, '.display-panel'],
      [home, '.image-card']
    ]
    const doubled: [css: string, selector: string][] = [
      [home, '.quick-url'],
      [home, '.quick-actions button'],
      [home, '.nav-item.primary-action'],
      [home, '.replay'],
      [pricing, '.status-card-bottom button']
    ]

    it('draws its own light-only frame of three 1px rules in the landing ink', () => {
      expect(tokens['--rh-frame']).toMatch(/^url\("data:image\/svg\+xml,.*shape-rendering='crispEdges'.*fill='%232c2e33'/)
      expect(tokens['--rh-frame']).toContain("fill-rule='evenodd' d='M0 0h11v11H0zM1 1v9h9V1zM2 2h7v7H2zM3 3v5h5V3zM4 4h3v3H4zM5 5v1h1V5z'")
      expect(tokens['--rh-ink']).toBe('#2c2e33')
    })

    it.each(framed.map(([css, selector]) => ({ css, selector })))('frames $selector with three rules', ({ css, selector }) => {
      const own = declarationsIn(css, selector)
      expect(own.border).toBe('5px solid var(--rh-ink)')
      expect(own['border-image']).toBe('var(--rh-frame) 5 / 5px')
      expect(own['background-clip']).toBe('border-box')
      const order = propertyOrder(css, selector)
      expect(order.lastIndexOf('border-image'), 'a later border shorthand would reset the image').toBeGreaterThan(order.lastIndexOf('border'))
    })

    it.each(doubled.map(([css, selector]) => ({ css, selector })))('outlines $selector with two rules', ({ css, selector }) => {
      const own = declarationsIn(css, selector)
      expect(own.border).toBe('3px double var(--rh-ink)')
      expect(own['border-image']).toBeUndefined()
    })

    it('keeps hover and press states from resetting the clipped background', () => {
      for (const css of [home, pricing]) {
        postcss.parse(css).walkRules((rule) => {
          if (!/:(hover|active)/.test(rule.selector)) return
          rule.walkDecls((decl) => expect(decl.prop, rule.selector).not.toMatch(/^(background|border)$/))
        })
      }
    })

    it('falls back to plain borders in forced colours', () => {
      for (const css of [home, pricing]) {
        const forced = mediaSelectors(css, '(forced-colors: active)')
        const expected = framed.filter(([source]) => source === css).map(([, selector]) => selector)
        expect(forced).toEqual(expect.arrayContaining(expected))
        postcss.parse(css).walkAtRules('media', (media) => {
          if (media.params === '(forced-colors: active)') media.walkDecls((decl) => expect(`${decl.prop}: ${decl.value}`).toBe('border-image-source: none'))
        })
      }
    })

    it('drops every shadow and presses buttons by inverting them to ink', () => {
      for (const css of [home, pricing]) {
        postcss.parse(css).walkDecls('box-shadow', (decl) => expect(decl.value, decl.parent!.toString().split('{')[0]).toBe('none'))
        postcss.parse(css).walkRules((rule) => {
          // Only the hovered element itself must stay put; its arrow glyph may still point the way.
          if (!/:(hover|active|focus-within)[^ ]*$/.test(rule.selector)) return
          rule.walkDecls('transform', (decl) => expect(decl.value, rule.selector).toBe('none'))
        })
      }
      expect(declarationsIn(home, '.rhine-home.entered .display-panel:hover')['border-image-source']).toBe('none')
      for (const [css, selector] of [
        [home, '.quick-actions button:active'],
        [home, '.nav-item.primary-action:active'],
        [home, '.replay:active'],
        [pricing, '.status-card-bottom button:active:not(:disabled)']
      ]) {
        const pressed = declarationsIn(css, selector)
        expect(pressed['background-color'], selector).toBe('var(--rh-ink)')
        expect(contrastRatio(pressed.color.replace('var(--rh-paper)', tokens['--rh-paper']), '#2c2e33'), selector).toBeGreaterThanOrEqual(4.5)
      }
    })

    it('moves in whole frames', () => {
      for (const css of [home, pricing]) {
        postcss.parse(css).walkDecls('transition', (decl) => {
          if (decl.value.startsWith('none')) return
          for (const part of decl.value.split(',')) expect(part, decl.parent!.toString().split('{')[0]).toMatch(/ steps\([2-4]\)$/)
        })
      }
      expect(declarationsIn(home, '.replay span').transition).toBe('transform 0.6s steps(4)')
    })

    it('shows the pixel logo at 64px beside the wordmark and 32px in the identity strip', () => {
      expect(tokens['--rh-logo']).toBe(`url("${PIXEL_LOGO_DATA_URI}")`)
      for (const [selector, size] of [['.brand-name::before', '64px'], ['.identity-name i', '32px']]) {
        const own = declarationsIn(home, selector)
        expect(own.background, selector).toBe(`var(--rh-logo) center / ${size} ${size} no-repeat`)
        expect([own.width, own.height], selector).toEqual([size, size])
        expect(own['image-rendering'], selector).toBe('pixelated')
      }
    })

    it('underlines the wordmark with a square-spiral band instead of a rule', () => {
      expect(tokens['--rh-huiwen']).toMatch(/^url\("data:image\/svg\+xml,<svg [^>]*width='8' height='8' shape-rendering='crispEdges'>/)
      const subtitle = declarationsIn(home, '.brand-subtitle')
      expect(subtitle['border-top']).toBe('0')
      expect(subtitle.background).toBe('var(--rh-huiwen) left top / 8px 8px repeat-x')
    })

    it.each([
      { css: home, selector: '.greeting-label' },
      { css: pricing, selector: '.model-columns' }
    ])('dithers $selector with a 2px checkerboard', ({ css, selector }) => {
      const own = declarationsIn(css, selector)
      expect(own['background-image']).toMatch(/^repeating-conic-gradient\(rgba\(44, 46, 51, [\d.]+\) 0 25%, transparent 0 50%\)$/)
      expect(own['background-size']).toBe('4px 4px')
    })

    it.each([
      { css: home, selector: '.greeting-label', under: tokens['--rh-paper'], ink: tokens['--rh-ink'] },
      { css: pricing, selector: '.model-columns', under: '#ffffff', ink: declarationsIn(pricing, '.model-columns').color }
    ])('keeps $selector text readable on the darkest dither cell', ({ css, selector, under, ink }) => {
      const cell = blend(declarationsIn(css, selector)['background-image'], under)
      expect(contrastRatio(ink, cell), `${ink} on ${cell}`).toBeGreaterThanOrEqual(4.5)
    })

    it('stops every animation and transition when reduced motion is preferred', () => {
      const reduced = postcss.parse(home).nodes.find(
        (node): node is postcss.AtRule => node.type === 'atrule' && node.params === '(prefers-reduced-motion: reduce)'
      )!
      const rule = reduced.nodes!.find((node): node is postcss.Rule => node.type === 'rule')!
      expect(rule.selectors).toEqual(expect.arrayContaining(['.rhine-home :deep(*)', '.rhine-home :deep(*::before)', '.rhine-home :deep(*::after)']))
      expect(rule.toString()).toMatch(/animation: none !important; transition: none !important;/)
    })
  })
})
