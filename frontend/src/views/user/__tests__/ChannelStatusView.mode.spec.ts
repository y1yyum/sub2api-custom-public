import { describe, expect, it, vi, beforeEach } from 'vitest'
import { defineComponent, h, nextTick, reactive } from 'vue'
import { mount } from '@vue/test-utils'

const isV1 = vi.fn(() => false)
const isUnified = vi.fn(() => false)
const route = reactive<{ query: Record<string, string> }>({ query: {} })

vi.mock('@/utils/featureFlags', () => ({
  isChannelMonitorV1Mode: () => isV1(),
  isUnifiedModelChannelStatusEnabled: () => isUnified(),
}))

vi.mock('vue-router', () => ({
  useRoute: () => route,
}))

vi.mock('@/features/model-channel-status/ModelChannelStatusView.vue', () => ({
  default: defineComponent({ name: 'ModelChannelStatusView', setup: () => () => h('div', { 'data-testid': 'unified' }) }),
}))
vi.mock('../ChannelStatusV1View.vue', () => ({
  default: defineComponent({ name: 'ChannelStatusV1View', setup: () => () => h('div', { 'data-testid': 'v1' }) }),
}))
vi.mock('../ChannelStatusV2View.vue', () => ({
  default: defineComponent({ name: 'ChannelStatusV2View', setup: () => () => h('div', { 'data-testid': 'v2' }) }),
}))

import ChannelStatusView from '../ChannelStatusView.vue'

describe('ChannelStatusView mode switch', () => {
  beforeEach(() => {
    isV1.mockReset()
    isUnified.mockReset()
    isUnified.mockReturnValue(false)
    route.query = {}
  })

  it('renders V2 when not in v1 mode', () => {
    isV1.mockReturnValue(false)
    const wrapper = mount(ChannelStatusView)
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="v1"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="unified"]').exists()).toBe(false)
  })

  it('renders V1 when in v1 mode', () => {
    isV1.mockReturnValue(true)
    const wrapper = mount(ChannelStatusView)
    expect(wrapper.find('[data-testid="v1"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(false)
  })

  it('renders the unified page when the unified check passes', () => {
    isUnified.mockReturnValue(true)
    isV1.mockReturnValue(false)
    const wrapper = mount(ChannelStatusView)
    expect(wrapper.find('[data-testid="unified"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="v1"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(false)
  })

  it('falls back to the classic monitor with ?view=classic', () => {
    isUnified.mockReturnValue(true)
    isV1.mockReturnValue(false)
    route.query = { view: 'classic' }
    const wrapper = mount(ChannelStatusView)
    expect(wrapper.find('[data-testid="unified"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(true)
  })

  it('stays on the classic monitor after it rewrites its own query', async () => {
    isUnified.mockReturnValue(true)
    isV1.mockReturnValue(false)
    route.query = { view: 'classic' }
    const wrapper = mount(ChannelStatusView)
    route.query = { range: '7d', tab: 'matrix' }
    await nextTick()
    expect(wrapper.find('[data-testid="unified"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(true)
  })
})
