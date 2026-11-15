import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ChannelsView from '@/views/admin/ChannelsView.vue'

const { list, create, update, getGroups } = vi.hoisted(() => ({
  list: vi.fn(), create: vi.fn(), update: vi.fn(), getGroups: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    channels: { list, create, update },
    groups: { getAll: getGroups },
    settings: { getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false }) }
  }
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn(), showError: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key === 'admin.channels.copySuffix' ? ' (copy)' : key })
}))

function fixture() {
  return {
    id: 1, name: 'Original', description: 'Description', status: 'active',
    billing_model_source: 'requested', restrict_models: true, features: 'feature note',
    features_config: { custom_option: { enabled: true }, codex_image_generation_bridge: { openai: true } },
    group_ids: [10], model_mapping: { openai: { alias: 'gpt-example' } },
    model_pricing: [{
      platform: 'openai', models: ['gpt-example'], hide_in_catalog: true, billing_mode: 'token',
      input_price: 0.000002, output_price: 0.000004, fast_multiplier: 2,
      reasoning_effort_multipliers: { high: 1.5 },
      intervals: [], time_pricing: { timezone: 'UTC', periods: [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: 2 }] }
    }],
    apply_pricing_to_account_stats: true,
    account_stats_pricing_rules: [{ name: 'stats', group_ids: [10], account_ids: [], pricing: [{
      platform: 'openai', models: ['gpt-example'], billing_mode: 'token', input_price: 0.000001,
      intervals: [], time_pricing: null
    }] }]
  }
}

function mountView() {
  return mount(ChannelsView, { global: { stubs: {
    AppLayout: defineComponent({ template: '<main><slot /></main>' }),
    TablePageLayout: defineComponent({ template: '<section><slot name="filters" /><slot name="table" /></section>' }),
    DataTable: defineComponent({
      props: ['data'], template: '<div><div v-for="row in data" :key="row.id" class="actions"><slot name="cell-actions" :row="row" /></div></div>'
    }),
    BaseDialog: defineComponent({ props: ['show'], template: '<section v-if="show"><slot /><slot name="footer" /></section>' }),
    PricingEntryCard: defineComponent({
      name: 'PricingEntryCard', props: { entry: { type: Object, required: true }, enableCatalogVisibility: Boolean }, emits: ['update'],
      template: '<button v-if="enableCatalogVisibility" type="button" class="catalog-switch" @click="$emit(\'update\', { ...entry, hide_in_catalog: !entry.hide_in_catalog })">{{ entry.hide_in_catalog }}</button>'
    }),
    Pagination: true, ConfirmDialog: true, EmptyState: true, Select: true, Icon: true, PlatformIcon: true, Toggle: true
  } } })
}

describe('ChannelsView catalog visibility and copy', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    create.mockResolvedValue({ id: 2 })
    update.mockResolvedValue({ id: 1 })
    getGroups.mockResolvedValue([
      { id: 10, name: 'Original group', platform: 'openai' },
      { id: 20, name: 'New group', platform: 'openai' }
    ])
  })

  it('loads and saves the visibility switch through edit', async () => {
    list.mockResolvedValue({ items: [fixture()], total: 1 })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('.actions button').trigger('click')
    await flushPromises()
    expect(wrapper.get('.catalog-switch').text()).toBe('true')
    await wrapper.get('.catalog-switch').trigger('click')
    await wrapper.get('#channel-form').trigger('submit')
    await flushPromises()
    expect(update).toHaveBeenCalledWith(1, expect.objectContaining({
      model_pricing: [expect.objectContaining({ hide_in_catalog: false, input_price: 0.000002 })]
    }))
    wrapper.unmount()
  })

  it('copies into an independent create form and requires new group associations', async () => {
    const original = fixture()
    const snapshot = JSON.stringify(original)
    list.mockResolvedValue({ items: [original], total: 1 })
    const wrapper = mountView()
    await flushPromises()
    const actions = wrapper.findAll('.actions button')
    expect(actions.map(button => button.text())).toEqual(['common.edit', 'common.copy', 'common.delete'])
    await actions[1].trigger('click')
    await flushPromises()
    expect(create).not.toHaveBeenCalled()
    expect(wrapper.get('input[placeholder="admin.channels.form.namePlaceholder"]').element).toHaveProperty('value', 'Original (copy)')
    await wrapper.get('#channel-form').trigger('submit')
    expect(create).not.toHaveBeenCalled()
    const groupLabel = wrapper.findAll('label').find(label => label.text().includes('New group'))!
    expect(groupLabel).toBeDefined()
    await groupLabel.get('input[type="checkbox"]').setValue(true)
    await wrapper.get('.catalog-switch').trigger('click')
    await wrapper.get('#channel-form').trigger('submit')
    await flushPromises()
    expect(update).not.toHaveBeenCalled()
    expect(create).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Original (copy)', group_ids: [20], features: 'feature note', restrict_models: true,
      model_mapping: { openai: { alias: 'gpt-example' } },
      features_config: expect.objectContaining({ custom_option: { enabled: true }, codex_image_generation_bridge: { openai: true } }),
      model_pricing: [expect.objectContaining({ hide_in_catalog: false, input_price: 0.000002, fast_multiplier: 2, reasoning_effort_multipliers: { high: 1.5 } })],
      account_stats_pricing_rules: [expect.objectContaining({ group_ids: [], name: 'stats' })]
    }))
    expect(JSON.stringify(original)).toBe(snapshot)
    wrapper.unmount()
  })
})
