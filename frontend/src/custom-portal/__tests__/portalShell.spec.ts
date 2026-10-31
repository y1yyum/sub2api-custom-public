import './enableI18nJit'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, reactive, ref } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'

const appStore = reactive({
  siteName: 'Sub2API',
  siteLogo: '',
  docUrl: '',
  contactInfo: '',
  publicSettingsLoaded: true,
  backendModeEnabled: false,
  cachedPublicSettings: {} as Record<string, unknown>
})
const authStore = reactive({
  isAuthenticated: true,
  isAdmin: false,
  isSimpleMode: false,
  user: { username: 'alice', email: 'alice@example.com', balance: 12.5, role: 'user' } as Record<string, unknown>,
  logout: vi.fn()
})
const canUseBatchImage = ref(false)
const portalPages = vi.hoisted(() => ({ modelsActive: false }))

vi.mock('@/stores/app', () => ({ useAppStore: () => appStore }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => authStore }))
vi.mock('@/composables/useBatchImageAccess', () => ({
  useBatchImageAccess: () => ({ canUseBatchImage, refreshBatchImageAccess: vi.fn() })
}))
vi.mock('@/components/common/AnnouncementBell.vue', () => ({ default: { template: '<span data-testid="bell" />' } }))
vi.mock('@/components/common/SubscriptionProgressMini.vue', () => ({
  default: { template: '<span data-testid="subscription-mini" />' }
}))

vi.mock('../pageRoutes', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../pageRoutes')>()),
  portalModelsPageActive: () => portalPages.modelsActive
}))

enableAutoUnmount(afterEach)

const Blank = defineComponent({ render: () => h('div') })
const InstantTransition = defineComponent({ setup: (_, { slots }) => () => slots.default?.() })
const paths = [
  '/dashboard',
  '/keys',
  '/usage',
  '/monitor',
  '/available-channels',
  '/model-plaza',
  '/purchase',
  '/subscriptions',
  '/orders',
  '/redeem',
  '/affiliate',
  '/batch-image',
  '/profile',
  '/payment/qrcode',
  '/login',
  '/admin/dashboard'
]

async function mountShell(path = '/dashboard', meta: Record<string, unknown> = {}) {
  const router: Router = createRouter({
    history: createMemoryHistory(),
    routes: [
      ...paths.map((p) => ({
        path: p,
        name: ({ '/purchase': 'PurchaseSubscription', '/monitor': 'ChannelStatus' } as Record<string, string>)[p],
        component: Blank,
        meta: p === path ? meta : {}
      })),
      { path: '/custom/:id', name: 'CustomPage', component: Blank }
    ]
  })
  await router.push(path)
  await router.isReady()
  const i18n = createI18n({ legacy: false, locale: 'zh', fallbackLocale: 'en', messages: {} })
  const { default: PortalShell } = await import('../shell/PortalShell.vue')
  const wrapper = mount(PortalShell, {
    slots: { default: '<p data-testid="page">page</p>' },
    global: { plugins: [i18n, router], stubs: { transition: InstantTransition } },
    attachTo: document.body
  })
  await flushPromises()
  return { wrapper, router }
}

function dockIds(wrapper: Awaited<ReturnType<typeof mountShell>>['wrapper']) {
  return wrapper.findAll('[data-testid^="portal-dock-"]:not([data-testid^="portal-dock-menu"])').map((node) =>
    node.attributes('data-testid')!.replace('portal-dock-', '')
  )
}

beforeEach(() => {
  Object.assign(appStore, { docUrl: '', contactInfo: '', backendModeEnabled: false, cachedPublicSettings: {} })
  Object.assign(authStore, { isAdmin: false, isSimpleMode: false })
  authStore.user = { username: 'alice', email: 'alice@example.com', balance: 12.5, role: 'user' }
  authStore.logout.mockReset()
  canUseBatchImage.value = false
  localStorage.clear()
  document.documentElement.classList.remove('dark')
})

describe('portal Dock', () => {
  it('shows the five entries with upstream default flags', async () => {
    const { wrapper } = await mountShell()
    expect(dockIds(wrapper)).toEqual(['overview', 'keys', 'usage', 'models', 'wallet'])
    expect(wrapper.get('[data-testid="portal-dock-overview"]').attributes('aria-current')).toBe('page')
    expect(wrapper.get('[data-testid="page"]').text()).toBe('page')
  })

  it('links Models and Wallet straight to their pages', async () => {
    const { wrapper } = await mountShell()
    for (const [id, href] of [['models', '/monitor'], ['wallet', '/purchase']]) {
      const entry = wrapper.get(`[data-testid="portal-dock-${id}"]`)
      expect(entry.element.tagName).toBe('A')
      expect(entry.attributes('href')).toBe(href)
      expect(wrapper.find(`[data-testid="portal-dock-menu-${id}"]`).exists()).toBe(false)
    }
  })

  it('marks the wallet active on payment pages', async () => {
    const { wrapper } = await mountShell('/payment/qrcode')
    expect(wrapper.get('[data-testid="portal-dock-wallet"]').attributes('aria-current')).toBe('page')
  })

  it('keeps Models for pricing alone and marks it active on the pricing tab', async () => {
    appStore.cachedPublicSettings = {
      available_channels_enabled: true,
      model_plaza_enabled: true,
      channel_monitor_enabled: false
    }
    const { wrapper } = await mountShell('/monitor?view=pricing')
    const models = wrapper.get('[data-testid="portal-dock-models"]')
    expect(models.attributes('href')).toBe('/monitor')
    expect(models.attributes('aria-current')).toBe('page')
    expect(wrapper.html()).not.toContain('/available-channels')
    expect(wrapper.html()).not.toContain('/model-plaza')
  })

  it('keeps Models in simple mode while the model plaza is enabled', async () => {
    authStore.isSimpleMode = true
    appStore.cachedPublicSettings = { model_plaza_enabled: true, channel_monitor_enabled: false }
    const { wrapper } = await mountShell()
    expect(dockIds(wrapper)).toEqual(['overview', 'keys', 'models'])
  })

  it('drops to Overview and Keys in simple mode without monitor or plaza', async () => {
    authStore.isSimpleMode = true
    appStore.cachedPublicSettings = { channel_monitor_enabled: false }
    const { wrapper } = await mountShell()
    expect(dockIds(wrapper)).toEqual(['overview', 'keys'])
  })

  it('keeps the wallet for redeem codes when payment and subscriptions are off', async () => {
    appStore.cachedPublicSettings = { payment_enabled: false, subscription_enabled: false }
    const { wrapper } = await mountShell()
    expect(wrapper.get('[data-testid="portal-dock-wallet"]').attributes('href')).toBe('/purchase')
  })
})

describe('portal top bar', () => {
  it('offers the admin console only to admins', async () => {
    const member = await mountShell()
    expect(member.wrapper.find('[data-testid="portal-admin-entry"]').exists()).toBe(false)
    member.wrapper.unmount()

    authStore.isAdmin = true
    const { wrapper } = await mountShell()
    expect(wrapper.get('[data-testid="portal-admin-entry"]').text()).toBe('管理后台')
  })

  it('lists docs and user custom pages only when available', async () => {
    const empty = await mountShell()
    expect(empty.wrapper.find('[data-testid="portal-docs-trigger"]').exists()).toBe(false)
    expect(empty.wrapper.find('[data-testid="portal-more-trigger"]').exists()).toBe(false)
    empty.wrapper.unmount()

    appStore.docUrl = 'https://docs.example.com'
    canUseBatchImage.value = true
    appStore.cachedPublicSettings = {
      custom_menu_items: [
        { id: 'b', label: 'Second', icon_svg: '', visibility: 'user', sort_order: 2 },
        { id: 'adm', label: 'Admin only', icon_svg: '', visibility: 'admin', sort_order: 0 },
        { id: 'a', label: 'First', icon_svg: '', visibility: 'user', sort_order: 1 }
      ]
    }
    const { wrapper } = await mountShell()
    await wrapper.get('[data-testid="portal-docs-trigger"]').trigger('click')
    const docLinks = wrapper.findAll('[role="menu"] a').map((a) => a.attributes('href'))
    expect(docLinks).toEqual(['https://docs.example.com/', '/batch-image'])

    await wrapper.get('[data-testid="portal-more-trigger"]').trigger('click')
    const more = wrapper.findAll('[role="menu"] a').map((a) => a.text())
    expect(more).toEqual(['First', 'Second'])
  })

  it('titles the models page as Models only while the portal page replaced channel status', async () => {
    const { wrapper } = await mountShell('/monitor?view=pricing', { titleKey: 'nav.channelStatus' })
    expect(wrapper.get('[data-testid="portal-page-title"]').text()).not.toBe('模型')

    portalPages.modelsActive = true
    const replaced = await mountShell('/monitor?view=pricing', { titleKey: 'nav.channelStatus' })
    expect(replaced.wrapper.get('[data-testid="portal-page-title"]').text()).toBe('模型')
    portalPages.modelsActive = false
  })

  it('titles custom pages by their menu label and links the balance to the wallet', async () => {
    appStore.cachedPublicSettings = {
      custom_menu_items: [{ id: 'a', label: 'Status board', icon_svg: '', visibility: 'user', sort_order: 1 }]
    }
    const { wrapper } = await mountShell('/custom/a')
    expect(wrapper.get('[data-testid="portal-page-title"]').text()).toBe('Status board')
    const balance = wrapper.get('[data-testid="portal-balance"]')
    expect(balance.text()).toContain('$12.50')
    expect(balance.attributes('href')).toBe('/purchase')
  })

  it('links admins into the admin console only through the top-bar entry', async () => {
    authStore.isAdmin = true
    authStore.user = { username: 'root', email: 'root@example.com', balance: 0, role: 'admin' }
    const { wrapper } = await mountShell()
    await wrapper.get('[data-testid="portal-avatar-trigger"]').trigger('click')
    await wrapper.get('[data-testid="portal-dock-models"]').trigger('click')
    const hrefs = wrapper.findAll('a').map((a) => a.attributes('href') ?? '')
    expect(hrefs.length).toBeGreaterThan(3)
    expect(hrefs.filter((href) => href.startsWith('/admin') || href.includes('github.com'))).toEqual([
      '/admin/dashboard'
    ])
    expect(wrapper.get('[data-testid="portal-admin-entry"]').attributes('href')).toBe('/admin/dashboard')
  })
})

describe('portal top bar parity with upstream AppHeader', () => {
  it('shows the frozen balance and the subscription widget behind its flag', async () => {
    authStore.user = { username: 'alice', email: 'alice@example.com', balance: 12.5, frozen_balance: 3, role: 'user' }
    const shown = await mountShell()
    expect(shown.wrapper.get('[data-testid="portal-frozen-balance"]').text()).toContain('$3.00')
    expect(shown.wrapper.find('[data-testid="subscription-mini"]').exists()).toBe(true)
    shown.wrapper.unmount()

    appStore.cachedPublicSettings = { subscription_enabled: false }
    authStore.user = { username: 'alice', email: 'alice@example.com', balance: 12.5, role: 'user' }
    const { wrapper } = await mountShell()
    expect(wrapper.find('[data-testid="portal-frozen-balance"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="subscription-mini"]').exists()).toBe(false)
  })

  it('marks dropdown entries as menu items', async () => {
    appStore.docUrl = 'https://docs.example.com'
    const { wrapper } = await mountShell()
    await wrapper.get('[data-testid="portal-docs-trigger"]').trigger('click')
    const items = wrapper.findAll('[role="menu"] a')
    expect(items.length).toBeGreaterThan(0)
    expect(items.every((item) => item.attributes('role') === 'menuitem')).toBe(true)
  })
})

describe('portal avatar menu', () => {
  it('reads the current theme each time it opens', async () => {
    const { wrapper } = await mountShell()
    document.documentElement.classList.add('dark')
    await wrapper.get('[data-testid="portal-avatar-trigger"]').trigger('click')
    expect(wrapper.get('[data-testid="portal-theme-dark"]').attributes('aria-pressed')).toBe('true')
    expect(wrapper.get('[data-testid="portal-logout"]').attributes('role')).toBe('menuitem')
  })

  it('links the profile, switches theme and signs out', async () => {
    const { wrapper, router } = await mountShell()
    await wrapper.get('[data-testid="portal-avatar-trigger"]').trigger('click')
    expect(wrapper.get('[data-testid="portal-avatar-profile"]').attributes('href')).toBe('/profile')
    expect(wrapper.text()).toContain('alice@example.com')

    await wrapper.get('[data-testid="portal-theme-dark"]').trigger('click')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(localStorage.getItem('theme')).toBe('dark')

    await wrapper.get('[data-testid="portal-logout"]').trigger('click')
    await flushPromises()
    expect(authStore.logout).toHaveBeenCalledOnce()
    expect(router.currentRoute.value.path).toBe('/login')
  })
})

describe('portal TopBar logo', () => {
  afterEach(() => {
    appStore.siteLogo = ''
  })

  it('shows the pixel logo on a bare tile when no site logo is configured', async () => {
    const { PIXEL_LOGO_DATA_URI } = await import('../theme/pixelLogo')
    const { wrapper } = await mountShell()
    const brand = wrapper.get('[data-testid="portal-brand"]')
    expect(brand.get('img').attributes('src')).toBe(PIXEL_LOGO_DATA_URI)
    expect(brand.get('span').classes()).toContain('pixel-logo')
    expect(brand.get('span').classes()).not.toContain('bg-gray-900')
  })

  it('keeps the configured site logo on its dark tile', async () => {
    appStore.siteLogo = '/brand.png'
    const { wrapper } = await mountShell()
    const brand = wrapper.get('[data-testid="portal-brand"]')
    expect(brand.get('img').attributes('src')).toBe('/brand.png')
    expect(brand.get('span').classes()).toEqual(expect.arrayContaining(['bg-gray-900', 'dark:bg-white']))
    expect(brand.get('span').classes()).not.toContain('pixel-logo')
  })
})
