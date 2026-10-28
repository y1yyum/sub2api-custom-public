"""Retain upstream assertions while testing the custom routing/branding contracts.

Input hashes are checked by prepare-upgrade-validation.py before this adapter.
No tests are skipped. Additional cases cover backend mode and unified/classic UI.
"""

def once(text, old, new):
    if text.count(old) != 1:
        raise ValueError('frontend adaptation anchor must occur exactly once')
    return text.replace(old, new)


def adapt(path, text):
    if path == 'frontend/src/views/admin/__tests__/SettingsView.spec.ts':
        old = 'https://github.com/Wei-Shaw/sub2api/blob/main/docs/PAYMENT_CN.md'
        if text.count(old) != 2:
            raise ValueError('expected the two payment documentation links')
        return text.replace(old, 'https://github.com/y1yyum/sub2api-custom-public/blob/main/docs/PAYMENT_CN.md')
    if path == 'frontend/src/router/__tests__/feature-access.spec.ts':
        text = once(text,
            "it('sends admins to the admin dashboard when subscriptions are disabled', async () => {",
            "it.each([[false, '/dashboard'], [true, '/admin/dashboard']])('uses custom admin destination with backend mode %s when subscriptions are disabled', async (backendMode, destination) => {")
        text = once(text,
            "    authStore.isAdmin = true\n    appStore.cachedPublicSettings = { subscription_enabled: false }",
            "    authStore.isAdmin = true\n    appStore.backendModeEnabled = backendMode\n    appStore.cachedPublicSettings = { subscription_enabled: false }")
        return once(text,
            "    expect(next).toHaveBeenCalledWith('/admin/dashboard')",
            "    expect(next).toHaveBeenCalledOnce()\n    expect(next).toHaveBeenCalledWith(destination)\n    appStore.backendModeEnabled = false")
    if path == 'frontend/src/views/user/__tests__/ChannelStatusView.mode.spec.ts':
        text = once(text, "import { defineComponent, h } from 'vue'", "import { defineComponent, h, reactive, nextTick } from 'vue'")
        text = once(text, "const isV1 = vi.fn(() => false)", """const isV1 = vi.fn(() => false)
const isUnified = vi.fn(() => false)
const route = reactive({ query: {} as Record<string, string> })
vi.mock('vue-router', () => ({ useRoute: () => route }))
vi.mock('@/features/model-channel-status/ModelChannelStatusView.vue', () => ({
  default: defineComponent({ name: 'ModelChannelStatusView', setup: () => () => h('div', { 'data-testid': 'unified' }) }),
}))""")
        text = once(text, '  isChannelMonitorV1Mode: () => isV1(),', '  isChannelMonitorV1Mode: () => isV1(),\n  isUnifiedModelChannelStatusEnabled: () => isUnified(),')
        text = once(text, '    isV1.mockReset()', '    isV1.mockReset()\n    isUnified.mockReturnValue(false)\n    route.query = {}')
        text += '''

describe('custom unified channel status routing', () => {
  beforeEach(() => {
    isV1.mockReturnValue(false)
    isUnified.mockReturnValue(true)
    route.query = {}
  })

  it('renders unified status instead of either classic monitor when enabled', () => {
    const wrapper = mount(ChannelStatusView)
    expect(wrapper.find('[data-testid="unified"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="v1"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(false)
  })

  it.each([false, true])('keeps a classic override after its query is rewritten, V1=%s', async (v1) => {
    isV1.mockReturnValue(v1)
    route.query = { view: 'classic' }
    const wrapper = mount(ChannelStatusView)
    expect(wrapper.find('[data-testid="unified"]').exists()).toBe(false)
    expect(wrapper.find(`[data-testid="${v1 ? 'v1' : 'v2'}"]`).exists()).toBe(true)
    route.query = { status: 'healthy' }
    await nextTick()
    expect(wrapper.find('[data-testid="unified"]').exists()).toBe(false)
    expect(wrapper.find(`[data-testid="${v1 ? 'v1' : 'v2'}"]`).exists()).toBe(true)
  })
})
'''
        return text
    raise ValueError('unreviewed frontend test adaptation: '+path)
